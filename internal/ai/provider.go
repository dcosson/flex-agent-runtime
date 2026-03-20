package ai

// ProviderConfig is the declarative configuration for a named service endpoint.
// It specifies which API client type to use, where to send requests,
// and how to authenticate. Stored in the provider registry.
type ProviderConfig struct {
	// Name is the unique provider identifier.
	// Examples: "openai", "anthropic", "openrouter".
	Name string `json:"name"`

	// APIClientType is the protocol to use for chat completions with this provider.
	// Must match a registered APIClient's ClientType() return value.
	// Examples: "openai-completions", "anthropic-messages", "google-genai".
	// Note: embedding models use a separate client type -- see EmbeddingAPIClientType.
	APIClientType string `json:"apiClientType"`

	// EmbeddingAPIClientType is the protocol to use for embeddings with this provider.
	// Must match a registered EmbeddingAPIClient's ClientType() return value.
	// Optional: only needed if this provider offers embedding models.
	EmbeddingAPIClientType string `json:"embeddingApiClientType,omitempty"`

	// BaseURL is the base URL for the API endpoint.
	// Examples: "https://api.openai.com/v1", "https://openrouter.ai/api/v1".
	BaseURL string `json:"baseUrl"`

	// KeyEnvVars is the ordered list of environment variable names to check
	// for the API key. The first non-empty value wins.
	KeyEnvVars []string `json:"keyEnvVars,omitempty"`

	// Headers are extra HTTP headers sent with every request to this provider.
	// Multi-valued headers use multiple values in the slice.
	Headers map[string][]string `json:"headers,omitempty"`

	// ProviderSpecific holds provider-level config that API client
	// implementations may need. Examples: Anthropic API version,
	// Google API version path segment.
	ProviderSpecific map[string]string `json:"providerSpecific,omitempty"`
}

// ProviderEndpoint is the resolved endpoint info passed to APIClient methods.
// It is computed from ProviderConfig at call time, with API key resolved
// from environment variables or StreamOptions overrides.
type ProviderEndpoint struct {
	// ProviderName is the provider identifier (for error messages, telemetry).
	ProviderName string

	// BaseURL is the resolved base URL.
	BaseURL string

	// APIKey is the resolved API key (from env var or StreamOptions override).
	// May be empty if the provider doesn't require authentication (e.g., local).
	APIKey string

	// Headers are merged provider-level + call-level headers.
	Headers map[string][]string

	// ProviderSpecific passes through from ProviderConfig.
	ProviderSpecific map[string]string
}
