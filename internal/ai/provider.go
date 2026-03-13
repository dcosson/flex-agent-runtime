package ai

import "context"

// Provider is the interface each LLM backend implements.
type Provider interface {
	// API returns the API identifier (e.g., "anthropic-messages").
	API() string

	// Stream starts a streaming LLM call with provider-specific options.
	Stream(ctx context.Context, model Model, llmCtx Context, opts StreamOptions) *EventStream

	// StreamSimple is the high-level API that maps ThinkingLevel to provider-specific params.
	StreamSimple(ctx context.Context, model Model, llmCtx Context, opts SimpleStreamOptions) *EventStream
}
