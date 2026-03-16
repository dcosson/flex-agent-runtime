package provider

// CapabilitySet declares which high-level APIs a provider package exposes.
type CapabilitySet struct {
	Chat       bool
	Embeddings bool
}
