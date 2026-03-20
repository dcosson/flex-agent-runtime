package anthropic

import (
	"github.com/dcosson/flex-agent-runtime/ai/provider"
	internal "github.com/dcosson/flex-agent-runtime/internal/ai/provider/anthropic"
)

// Anthropic supports chat/streaming only.
type (
	Config       = internal.Config
	ClientConfig = internal.ClientConfig
	Client       = internal.Client
)

var (
	NewClient          = internal.NewClient
	Register           = internal.Register
	EndpointFromConfig = internal.EndpointFromConfig
)

func Capabilities() provider.CapabilitySet {
	return provider.CapabilitySet{Chat: true, Embeddings: false}
}
