package openai

import (
	"github.com/anthropics/flex-agent-runtime/ai/provider"
	internal "github.com/anthropics/flex-agent-runtime/internal/ai/provider/openai"
)

// OpenAI supports chat/streaming and embeddings.
type (
	Config            = internal.Config
	Provider          = internal.Provider
	EmbeddingProvider = internal.EmbeddingProvider
)

var (
	New               = internal.New
	Register          = internal.Register
	NewEmbedding      = internal.NewEmbedding
	RegisterEmbedding = internal.RegisterEmbedding
)

func Capabilities() provider.CapabilitySet {
	return provider.CapabilitySet{Chat: true, Embeddings: true}
}
