package ai

import "context"

// APIClient is a protocol-level implementation for a specific API format.
// It knows HOW to talk to an API (request format, SSE parsing, response
// mapping) but not WHERE or with what credentials. Those come from
// ProviderEndpoint, passed per-call.
//
// Implementations are stateless and safe for concurrent use.
// Examples: openai-completions, anthropic-messages, google-genai.
type APIClient interface {
	// ClientType returns the API client type identifier.
	// Examples: "openai-completions", "anthropic-messages", "google-genai".
	ClientType() string

	// Stream starts a streaming LLM call.
	// The endpoint provides base URL and resolved API key.
	// The model provides model-specific config (ID, compat flags, etc.).
	Stream(ctx context.Context, endpoint ProviderEndpoint, model Model,
		llmCtx Context, opts StreamOptions) *EventStream

	// StreamSimple is the high-level API that maps ThinkingLevel to
	// provider-specific params.
	StreamSimple(ctx context.Context, endpoint ProviderEndpoint, model Model,
		llmCtx Context, opts SimpleStreamOptions) *EventStream
}
