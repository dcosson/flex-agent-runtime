package cohere

import (
	"github.com/dcosson/flex-agent-runtime/ai/provider"
	internal "github.com/dcosson/flex-agent-runtime/internal/ai/provider/cohere"
)

// Cohere supports embeddings only.
type (
	Config            = internal.Config
	ClientConfig      = internal.ClientConfig
	EmbeddingClient   = internal.EmbeddingClient
	EmbeddingProvider = internal.EmbeddingProvider
)

var (
	NewEmbeddingClient      = internal.NewEmbeddingClient
	RegisterEmbeddingClient = internal.RegisterEmbeddingClient
	NewEmbedding            = internal.NewEmbedding
	RegisterEmbedding       = internal.RegisterEmbedding
)

func Capabilities() provider.CapabilitySet {
	return provider.CapabilitySet{Chat: false, Embeddings: true}
}
