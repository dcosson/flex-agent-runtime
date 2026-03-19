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

func (c *Client) Stream(ctx context.Context, endpoint ai.ProviderEndpoint, model ai.Model, llmCtx ai.Context, opts ai.StreamOptions) *ai.EventStream {
	return c.streamInternal(ctx, endpoint, model, llmCtx, opts, requestParams{})
}

func (c *Client) StreamSimple(ctx context.Context, endpoint ai.ProviderEndpoint, model ai.Model, llmCtx ai.Context, opts ai.SimpleStreamOptions) *ai.EventStream {
	base := ai.BuildBaseOptions(model, &opts)
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

	return c.streamInternal(ctx, endpoint, model, llmCtx, base, params)
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

func (c *Client) streamInternal(ctx context.Context, endpoint ai.ProviderEndpoint, model ai.Model, llmCtx ai.Context, opts ai.StreamOptions, params requestParams) *ai.EventStream {
	es := ai.NewEventStream()
	go func() {
		defer es.Close()
		if err := c.runStream(ctx, endpoint, model, llmCtx, opts, params, es); err != nil {
			sendErrorEvent(es, endpoint, model, err)
		}
	}()
	return es
}

func (c *Client) runStream(ctx context.Context, endpoint ai.ProviderEndpoint, model ai.Model, llmCtx ai.Context, opts ai.StreamOptions, params requestParams, es *ai.EventStream) error {
	reqBody := buildRequest(model, llmCtx, opts, params)
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	// API version from provider-specific config
	version := defaultVersion
	if v, ok := endpoint.ProviderSpecific["apiVersion"]; ok && v != "" {
		version = v
	}

	// Gemini uses a model-specific URL: /v1beta/models/{model}:streamGenerateContent?alt=sse
	url := fmt.Sprintf("%s/%s/models/%s:streamGenerateContent?alt=sse",
		strings.TrimRight(endpoint.BaseURL, "/"), version, model.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	applyHeaders(req, endpoint, model)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return &ai.ProviderError{Code: ai.ErrUnknown, Message: "request canceled", Provider: endpoint.ProviderName}
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

func applyHeaders(req *http.Request, endpoint ai.ProviderEndpoint, model ai.Model) {
	req.Header.Set("Content-Type", "application/json")
	if endpoint.APIKey != "" {
		req.Header.Set("x-goog-api-key", endpoint.APIKey)
	}
	// Provider-level + call-level headers (already merged in endpoint)
	for k, vs := range endpoint.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	// Model-level headers
	for k, vs := range model.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
}

func sendErrorEvent(es *ai.EventStream, endpoint ai.ProviderEndpoint, model ai.Model, err error) {
	perr := &ai.ProviderError{Code: ai.ErrUnknown, Message: err.Error(), Provider: endpoint.ProviderName}
	if x, ok := err.(*ai.ProviderError); ok {
		perr = x
	}
	now := ai.TimeToMillis(time.Now())
	es.Send(ai.AssistantMessageEvent{
		Type:   ai.EventError,
		Reason: ai.StopReasonError,
		Error: &ai.AssistantMessage{
			API:          model.API,
			Provider:     endpoint.ProviderName,
			Model:        model.ID,
			StopReason:   ai.StopReasonError,
			ErrorMessage: perr.Message,
			Timestamp:    now,
		},
	})
}
