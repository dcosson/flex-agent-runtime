package ai

import "context"

// EmbeddingAPIClient is a protocol-level implementation for embedding APIs.
// Like APIClient, it is stateless — base URL and API key come from
// ProviderEndpoint, passed per-call.
type EmbeddingAPIClient interface {
	// ClientType returns the embedding API client type identifier.
	// Examples: "openai-embeddings", "google-embeddings", "cohere-embeddings".
	ClientType() string

	// Embed generates embeddings for the given texts.
	Embed(ctx context.Context, endpoint ProviderEndpoint, model EmbeddingModel,
		req EmbeddingRequest) (*EmbeddingResponse, error)
}
