package ai

import (
	"context"
	"time"
)

// Stream starts a streaming LLM call using the provider registered for model.API.
func Stream(ctx context.Context, model Model, llmCtx Context, opts StreamOptions) *EventStream {
	p, err := GetProvider(model.API)
	if err != nil {
		return errorStream(err)
	}
	return p.Stream(ctx, model, llmCtx, opts)
}

// StreamSimple starts a streaming LLM call with simplified options.
func StreamSimple(ctx context.Context, model Model, llmCtx Context, opts SimpleStreamOptions) *EventStream {
	p, err := GetProvider(model.API)
	if err != nil {
		return errorStream(err)
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
