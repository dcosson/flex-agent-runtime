package google

import (
	"github.com/dcosson/flex-agent-runtime/ai/provider"
	internal "github.com/dcosson/flex-agent-runtime/internal/ai/provider/google"
)

// Google supports chat/streaming and embeddings.
type (
	Config            = internal.Config
	ClientConfig      = internal.ClientConfig
	Client            = internal.Client
	Provider          = internal.Provider
	EmbeddingProvider = internal.EmbeddingProvider
)

var (
	New                = internal.New
	NewClient          = internal.NewClient
	Register           = internal.Register
	EndpointFromConfig = internal.EndpointFromConfig
	NewEmbedding       = internal.NewEmbedding
	RegisterEmbedding  = internal.RegisterEmbedding
)

func Capabilities() provider.CapabilitySet {
	return provider.CapabilitySet{Chat: true, Embeddings: true}
}
