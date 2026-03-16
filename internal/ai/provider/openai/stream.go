package openai

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

	"flex-agent-runtime/internal/ai"
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

	// Reasoning effort mapping for models that support it
	if model.Compat != nil && boolVal(model.Compat.SupportsReasoningEffort) {
		if model.Compat.ReasoningEffortMap != nil {
			if mapped, ok := model.Compat.ReasoningEffortMap[string(opts.Reasoning)]; ok {
				params.reasoningEffort = mapped
			}
		}
	} else if model.Reasoning {
		// Fallback: adjust max tokens + thinking budget for reasoning models
		// without reasoning_effort support
		maxTokens, _ := ai.AdjustMaxTokensForThinking(
			*base.MaxTokens, model.MaxTokens, opts.Reasoning, opts.ThinkingBudgets)
		base.MaxTokens = &maxTokens
	}

	return p.streamInternal(ctx, model, llmCtx, base, params)
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
	reqBody, err := buildRequest(model, llmCtx, opts, params)
	if err != nil {
		return err
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	applyHeaders(req, p, model, opts)

	resp, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return &ai.ProviderError{Code: ai.ErrUnknown, Message: "request canceled", Provider: "openai"}
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
	req.Header.Set("Accept", "text/event-stream")
	key := strings.TrimSpace(opts.APIKey)
	if key == "" {
		key = p.apiKey
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}
}

func sendErrorEvent(es *ai.EventStream, model ai.Model, err error) {
	perr := &ai.ProviderError{Code: ai.ErrUnknown, Message: err.Error(), Provider: "openai"}
	if x, ok := err.(*ai.ProviderError); ok {
		perr = x
	}
	now := ai.TimeToMillis(time.Now())
	es.Send(ai.AssistantMessageEvent{
		Type:   ai.EventError,
		Reason: ai.StopReasonError,
		Error: &ai.AssistantMessage{
			API:          model.API,
			Provider:     "openai",
			Model:        model.ID,
			StopReason:   ai.StopReasonError,
			ErrorMessage: perr.Message,
			Timestamp:    now,
		},
	})
}
