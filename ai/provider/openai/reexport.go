package openai

import (
	"github.com/dcosson/flex-agent-runtime/ai/provider"
	internal "github.com/dcosson/flex-agent-runtime/internal/ai/provider/openai"
)

// OpenAI supports chat/streaming and embeddings.
type (
	Config            = internal.Config
	ClientConfig      = internal.ClientConfig
	Client            = internal.Client
	Provider          = internal.Provider
	EmbeddingClient   = internal.EmbeddingClient
	EmbeddingProvider = internal.EmbeddingProvider
)

var (
	New                     = internal.New
	NewClient               = internal.NewClient
	Register                = internal.Register
	EndpointFromConfig      = internal.EndpointFromConfig
	NewEmbeddingClient      = internal.NewEmbeddingClient
	RegisterEmbeddingClient = internal.RegisterEmbeddingClient
	NewEmbedding            = internal.NewEmbedding
	RegisterEmbedding       = internal.RegisterEmbedding
)

func Capabilities() provider.CapabilitySet {
	return provider.CapabilitySet{Chat: true, Embeddings: true}
}
