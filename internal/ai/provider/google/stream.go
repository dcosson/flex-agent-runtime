package google

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
)

func (p *Provider) Stream(ctx context.Context, model ai.Model, llmCtx ai.Context, opts ai.StreamOptions) *ai.EventStream {
	return p.streamInternal(ctx, model, llmCtx, opts, requestParams{})
}

func (p *Provider) StreamSimple(ctx context.Context, model ai.Model, llmCtx ai.Context, opts ai.SimpleStreamOptions) *ai.EventStream {
	base := ai.BuildBaseOptions(model, &opts, p.apiKey)
	if base.MaxTokens == nil {
		maxTok := model.MaxTokens
		base.MaxTokens = &maxTok
	}

	var params requestParams

	if model.Reasoning {
		maxTokens, thinkBudget := ai.AdjustMaxTokensForThinking(
			*base.MaxTokens, model.MaxTokens, opts.Reasoning, opts.ThinkingBudgets)
		base.MaxTokens = &maxTokens
		params.thinkingBudget = &thinkBudget
		params.thinkingLevel = mapThinkingLevel(opts.Reasoning)
	}

	return p.streamInternal(ctx, model, llmCtx, base, params)
}

// mapThinkingLevel converts an ai.ThinkingLevel to a Gemini thinkingLevel string.
// Gemini uses uppercase values: THINKING_LEVEL_NONE, THINKING_LEVEL_LOW, etc.
// xhigh is clamped to high since Gemini doesn't support xhigh.
func mapThinkingLevel(level ai.ThinkingLevel) string {
	switch ai.ClampReasoning(level) {
	case ai.ThinkingMinimal:
		return "THINKING_LEVEL_LOW" // Gemini has no "minimal"; map to low
	case ai.ThinkingLow:
		return "THINKING_LEVEL_LOW"
	case ai.ThinkingMedium:
		return "THINKING_LEVEL_MEDIUM"
	case ai.ThinkingHigh:
		return "THINKING_LEVEL_HIGH"
	default:
		return ""
	}
}

func (p *Provider) streamInternal(ctx context.Context, model ai.Model, llmCtx ai.Context, opts ai.StreamOptions, params requestParams) *ai.EventStream {
	es := ai.NewEventStream()
	go func() {
		defer es.Close()
		if err := p.runStream(ctx, model, llmCtx, opts, params, es); err != nil {
			sendErrorEvent(es, model, err)
		}
	}()
	return es
}

func (p *Provider) runStream(ctx context.Context, model ai.Model, llmCtx ai.Context, opts ai.StreamOptions, params requestParams, es *ai.EventStream) error {
	reqBody := buildRequest(model, llmCtx, opts, params)
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	// Gemini uses a model-specific URL: /v1beta/models/{model}:streamGenerateContent?alt=sse
	url := fmt.Sprintf("%s/%s/models/%s:streamGenerateContent?alt=sse", p.baseURL, p.version, model.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	applyHeaders(req, p, model, opts)

	resp, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return &ai.ProviderError{Code: ai.ErrUnknown, Message: "request canceled", Provider: "google"}
		}
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return providerError(resp.StatusCode, decodeErrorMessage(body))
	}

	return processStream(ctx, resp.Body, model, es)
}

func applyHeaders(req *http.Request, p *Provider, model ai.Model, opts ai.StreamOptions) {
	req.Header.Set("Content-Type", "application/json")
	key := strings.TrimSpace(opts.APIKey)
	if key == "" {
		key = p.apiKey
	}
	if key != "" {
		req.Header.Set("x-goog-api-key", key)
	}
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}
}

func sendErrorEvent(es *ai.EventStream, model ai.Model, err error) {
	perr := &ai.ProviderError{Code: ai.ErrUnknown, Message: err.Error(), Provider: "google"}
	if x, ok := err.(*ai.ProviderError); ok {
		perr = x
	}
	now := ai.TimeToMillis(time.Now())
	es.Send(ai.AssistantMessageEvent{
		Type:   ai.EventError,
		Reason: ai.StopReasonError,
		Error: &ai.AssistantMessage{
			API:          model.API,
			Provider:     "google",
			Model:        model.ID,
			StopReason:   ai.StopReasonError,
			ErrorMessage: perr.Message,
			Timestamp:    now,
		},
	})
}
