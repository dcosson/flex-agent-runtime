package anthropic

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

	"github.com/anthropics/flex-agent-runtime/internal/ai"
	"github.com/anthropics/flex-agent-runtime/internal/ai/sse"
)

func (p *Provider) Stream(ctx context.Context, model ai.Model, llmCtx ai.Context, opts ai.StreamOptions) *ai.EventStream {
	return p.streamWithThinking(ctx, model, llmCtx, opts, nil)
}

func (p *Provider) StreamSimple(ctx context.Context, model ai.Model, llmCtx ai.Context, opts ai.SimpleStreamOptions) *ai.EventStream {
	base := ai.BuildBaseOptions(model, &opts, p.apiKey)
	if base.MaxTokens == nil {
		maxTok := model.MaxTokens
		base.MaxTokens = &maxTok
	}
	if model.Reasoning {
		maxTokens, budget := ai.AdjustMaxTokensForThinking(*base.MaxTokens, model.MaxTokens, opts.Reasoning, opts.ThinkingBudgets)
		base.MaxTokens = &maxTokens
		thinking := &wireThinking{Type: "enabled", BudgetTokens: budget}
		return p.streamWithThinking(ctx, model, llmCtx, base, thinking)
	}
	return p.streamWithThinking(ctx, model, llmCtx, base, nil)
}

func (p *Provider) streamWithThinking(ctx context.Context, model ai.Model, llmCtx ai.Context, opts ai.StreamOptions, thinking *wireThinking) *ai.EventStream {
	es := ai.NewEventStream()
	go func() {
		defer es.Close()
		if err := p.runStream(ctx, model, llmCtx, opts, thinking, es); err != nil {
			sendErrorEvent(es, model, err)
		}
	}()
	return es
}

func (p *Provider) runStream(ctx context.Context, model ai.Model, llmCtx ai.Context, opts ai.StreamOptions, thinking *wireThinking, es *ai.EventStream) error {
	reqBody, err := buildRequest(model, llmCtx, opts, thinking)
	if err != nil {
		return err
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	applyHeaders(req, p, model, opts)

	resp, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return &ai.ProviderError{Code: ai.ErrUnknown, Message: "request canceled", Provider: "anthropic"}
		}
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return providerError(resp.StatusCode, decodeErrorMessage(body))
	}

	processor := newSSEProcessor(model, es)
	scanner := sse.NewScanner(resp.Body)
	for scanner.Next() {
		select {
		case <-ctx.Done():
			return &ai.ProviderError{Code: ai.ErrUnknown, Message: "request canceled", Provider: "anthropic"}
		default:
		}
		e := scanner.UnsafeEvent()
		if err := processor.process(e); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("sse scan: %w", err)
	}
	return processor.finish(nil)
}

func applyHeaders(req *http.Request, p *Provider, model ai.Model, opts ai.StreamOptions) {
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "text/event-stream")
	req.Header.Set("anthropic-version", p.version)
	key := strings.TrimSpace(opts.APIKey)
	if key == "" {
		key = p.apiKey
	}
	if key != "" {
		req.Header.Set("x-api-key", key)
	}
	for _, beta := range p.betaHeaders {
		if strings.TrimSpace(beta) != "" {
			req.Header.Add("anthropic-beta", beta)
		}
	}
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}
}

func sendErrorEvent(es *ai.EventStream, model ai.Model, err error) {
	perr := &ai.ProviderError{Code: ai.ErrUnknown, Message: err.Error(), Provider: "anthropic"}
	if x, ok := err.(*ai.ProviderError); ok {
		perr = x
	}
	now := ai.TimeToMillis(time.Now())
	es.Send(ai.AssistantMessageEvent{
		Type:   ai.EventError,
		Reason: ai.StopReasonError,
		Error: &ai.AssistantMessage{
			API:          model.API,
			Provider:     "anthropic",
			Model:        model.ID,
			StopReason:   ai.StopReasonError,
			ErrorMessage: perr.Message,
			Timestamp:    now,
		},
	})
}
