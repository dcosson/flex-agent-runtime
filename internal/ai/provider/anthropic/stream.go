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

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/ai/sse"
)

func (c *Client) Stream(ctx context.Context, endpoint ai.ProviderEndpoint, model ai.Model, llmCtx ai.Context, opts ai.StreamOptions) *ai.EventStream {
	return c.streamWithThinking(ctx, endpoint, model, llmCtx, opts, nil)
}

func (c *Client) StreamSimple(ctx context.Context, endpoint ai.ProviderEndpoint, model ai.Model, llmCtx ai.Context, opts ai.SimpleStreamOptions) *ai.EventStream {
	base := ai.BuildBaseOptions(model, &opts)
	if base.MaxTokens == nil {
		maxTok := model.MaxTokens
		base.MaxTokens = &maxTok
	}
	if model.Reasoning {
		maxTokens, budget := ai.AdjustMaxTokensForThinking(*base.MaxTokens, model.MaxTokens, opts.Reasoning, opts.ThinkingBudgets)
		base.MaxTokens = &maxTokens
		thinking := &wireThinking{Type: "enabled", BudgetTokens: budget}
		return c.streamWithThinking(ctx, endpoint, model, llmCtx, base, thinking)
	}
	return c.streamWithThinking(ctx, endpoint, model, llmCtx, base, nil)
}

func (c *Client) streamWithThinking(ctx context.Context, endpoint ai.ProviderEndpoint, model ai.Model, llmCtx ai.Context, opts ai.StreamOptions, thinking *wireThinking) *ai.EventStream {
	es := ai.NewEventStream()
	go func() {
		defer es.Close()
		if err := c.runStream(ctx, endpoint, model, llmCtx, opts, thinking, es); err != nil {
			sendErrorEvent(es, endpoint, model, err)
		}
	}()
	return es
}

func (c *Client) runStream(ctx context.Context, endpoint ai.ProviderEndpoint, model ai.Model, llmCtx ai.Context, opts ai.StreamOptions, thinking *wireThinking, es *ai.EventStream) error {
	reqBody, err := buildRequest(model, llmCtx, opts, thinking)
	if err != nil {
		return err
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(endpoint.BaseURL, "/") + "/v1/messages"
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

	processor := newSSEProcessor(model, es)
	scanner := sse.NewScanner(resp.Body)
	for scanner.Next() {
		select {
		case <-ctx.Done():
			return &ai.ProviderError{Code: ai.ErrUnknown, Message: "request canceled", Provider: endpoint.ProviderName}
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

func applyHeaders(req *http.Request, endpoint ai.ProviderEndpoint, model ai.Model) {
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "text/event-stream")

	// API version from provider-specific config
	version := defaultVersion
	if endpoint.ProviderSpecific != nil {
		if v, ok := endpoint.ProviderSpecific["apiVersion"]; ok && v != "" {
			version = v
		}
	}
	req.Header.Set("anthropic-version", version)

	if endpoint.APIKey != "" {
		req.Header.Set("x-api-key", endpoint.APIKey)
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
