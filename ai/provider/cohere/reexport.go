package cohere

import (
	"github.com/dcosson/flex-agent-runtime/ai/provider"
	internal "github.com/dcosson/flex-agent-runtime/internal/ai/provider/cohere"
)

// Cohere supports embeddings only.
type (
	ClientConfig    = internal.ClientConfig
	EmbeddingClient = internal.EmbeddingClient
)

var (
	NewEmbeddingClient      = internal.NewEmbeddingClient
	RegisterEmbeddingClient = internal.RegisterEmbeddingClient
)

func Capabilities() provider.CapabilitySet {
	return provider.CapabilitySet{Chat: false, Embeddings: true}
}
