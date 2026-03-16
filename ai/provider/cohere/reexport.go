package cohere

import (
	"flex-agent-runtime/ai/provider"
	internal "flex-agent-runtime/internal/ai/provider/cohere"
)

// Cohere supports embeddings only.
type (
	Config            = internal.Config
	EmbeddingProvider = internal.EmbeddingProvider
)

var (
	NewEmbedding      = internal.NewEmbedding
	RegisterEmbedding = internal.RegisterEmbedding
)

func Capabilities() provider.CapabilitySet {
	return provider.CapabilitySet{Chat: false, Embeddings: true}
}
