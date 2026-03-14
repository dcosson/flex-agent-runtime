package ai

import "context"

// EmbeddingProvider generates embedding vectors for input text batches.
type EmbeddingProvider interface {
	API() string
	Embed(ctx context.Context, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error)
}
