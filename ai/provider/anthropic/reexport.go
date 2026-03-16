package anthropic

import (
	"flex-agent-runtime/ai/provider"
	internal "flex-agent-runtime/internal/ai/provider/anthropic"
)

// Anthropic supports chat/streaming only.
type (
	Config   = internal.Config
	Provider = internal.Provider
)

var (
	New      = internal.New
	Register = internal.Register
)

func Capabilities() provider.CapabilitySet {
	return provider.CapabilitySet{Chat: true, Embeddings: false}
}
