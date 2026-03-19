package ai

import (
	"context"
	"fmt"
	"time"
)

// Stream starts a streaming LLM call. Resolution order:
//  1. New path: model.Provider → ProviderConfig → APIClient + ResolveEndpoint
//  2. Legacy fallback: model.API → old Provider registry (until callers migrate)
func Stream(ctx context.Context, model Model, llmCtx Context, opts StreamOptions) *EventStream {
	// Try new two-step resolution path first.
	if cfg, err := GetProviderConfig(model.Provider); err == nil {
		if client, err := GetAPIClient(cfg.APIClientType); err == nil {
			endpoint := ResolveEndpoint(cfg, opts)
			return client.Stream(ctx, endpoint, model, llmCtx, opts)
		}
	}
	// Legacy fallback: use old Provider registry.
	p, err := GetProvider(model.API)
	if err != nil {
		return errorStream(fmt.Errorf("no provider config for %q and no legacy provider for API %q", model.Provider, model.API))
	}
	return p.Stream(ctx, model, llmCtx, opts)
}

// StreamSimple starts a streaming LLM call with simplified options.
func StreamSimple(ctx context.Context, model Model, llmCtx Context, opts SimpleStreamOptions) *EventStream {
	// Try new two-step resolution path first.
	if cfg, err := GetProviderConfig(model.Provider); err == nil {
		if client, err := GetAPIClient(cfg.APIClientType); err == nil {
			endpoint := ResolveEndpoint(cfg, StreamOptions{})
			return client.StreamSimple(ctx, endpoint, model, llmCtx, opts)
		}
	}
	// Legacy fallback: use old Provider registry.
	p, err := GetProvider(model.API)
	if err != nil {
		return errorStream(fmt.Errorf("no provider config for %q and no legacy provider for API %q", model.Provider, model.API))
	}
	return p.StreamSimple(ctx, model, llmCtx, opts)
}

// Complete makes a blocking LLM call.
func Complete(ctx context.Context, model Model, llmCtx Context, opts StreamOptions) (AssistantMessage, error) {
	return Stream(ctx, model, llmCtx, opts).Drain()
}

// CompleteSimple makes a blocking LLM call with simplified options.
func CompleteSimple(ctx context.Context, model Model, llmCtx Context, opts SimpleStreamOptions) (AssistantMessage, error) {
	return StreamSimple(ctx, model, llmCtx, opts).Drain()
}

func errorStream(err error) *EventStream {
	es := NewEventStream()
	go func() {
		defer es.Close()
		es.Send(AssistantMessageEvent{
			Type:   EventError,
			Reason: StopReasonError,
			Error: &AssistantMessage{
				StopReason:   StopReasonError,
				ErrorMessage: err.Error(),
				Timestamp:    TimeToMillis(time.Now()),
			},
		})
	}()
	return es
}
