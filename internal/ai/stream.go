package ai

import (
	"context"
	"fmt"
	"time"
)

// Stream starts a streaming LLM call.
// Resolution: model.Provider → ProviderConfig → APIClient + ResolveEndpoint
func Stream(ctx context.Context, model Model, llmCtx Context, opts StreamOptions) *EventStream {
	cfg, err := GetProviderConfig(model.Provider)
	if err != nil {
		return errorStream(fmt.Errorf("no provider config for %q: %w", model.Provider, err))
	}
	client, err := GetAPIClient(cfg.APIClientType)
	if err != nil {
		return errorStream(fmt.Errorf("no API client for type %q (provider %q): %w", cfg.APIClientType, model.Provider, err))
	}
	endpoint := ResolveEndpoint(cfg, opts)
	return client.Stream(ctx, endpoint, model, llmCtx, opts)
}

// StreamSimple starts a streaming LLM call with simplified options.
func StreamSimple(ctx context.Context, model Model, llmCtx Context, opts SimpleStreamOptions) *EventStream {
	cfg, err := GetProviderConfig(model.Provider)
	if err != nil {
		return errorStream(fmt.Errorf("no provider config for %q: %w", model.Provider, err))
	}
	client, err := GetAPIClient(cfg.APIClientType)
	if err != nil {
		return errorStream(fmt.Errorf("no API client for type %q (provider %q): %w", cfg.APIClientType, model.Provider, err))
	}
	endpoint := ResolveEndpoint(cfg, opts.StreamOptions)
	return client.StreamSimple(ctx, endpoint, model, llmCtx, opts)
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
