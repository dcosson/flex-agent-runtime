package ai

import (
	"os"
	"strings"
)

// ResolveEmbeddingEndpoint enriches an existing ProviderEndpoint with
// API key resolution when the endpoint's APIKey is empty. Uses the same
// resolution order as ResolveEndpoint: directAPIKeys → env vars.
// This allows embedding clients to receive a pre-built endpoint from
// callers (e.g., with credentials from an external secret store) while
// still falling back to env vars when no explicit key is provided.
func ResolveEmbeddingEndpoint(endpoint ProviderEndpoint) ProviderEndpoint {
	if endpoint.APIKey != "" {
		return endpoint
	}
	cfg, err := GetProviderConfig(endpoint.ProviderName)
	if err != nil {
		return endpoint
	}
	directAPIKeysMu.RLock()
	apiKey := directAPIKeys[cfg.Name]
	directAPIKeysMu.RUnlock()
	if apiKey == "" {
		for _, envVar := range cfg.KeyEnvVars {
			if v := os.Getenv(envVar); v != "" {
				apiKey = v
				break
			}
		}
	}
	if apiKey != "" {
		endpoint.APIKey = apiKey
	}
	if endpoint.BaseURL == "" && cfg.BaseURL != "" {
		endpoint.BaseURL = cfg.BaseURL
	}
	return endpoint
}

// ResolveEndpoint creates a ProviderEndpoint from a ProviderConfig and
// call-level options. API key resolution order:
//  1. opts.APIKey (explicit per-call override)
//  2. directAPIKeys[cfg.Name] (set by RegisterCustomProvider)
//  3. First non-empty env var from cfg.KeyEnvVars
//  4. Empty string (provider may not require auth)
func ResolveEndpoint(cfg ProviderConfig, opts StreamOptions) ProviderEndpoint {
	apiKey := strings.TrimSpace(opts.APIKey)
	if apiKey == "" {
		directAPIKeysMu.RLock()
		apiKey = directAPIKeys[cfg.Name]
		directAPIKeysMu.RUnlock()
	}
	if apiKey == "" {
		for _, envVar := range cfg.KeyEnvVars {
			if v := os.Getenv(envVar); v != "" {
				apiKey = v
				break
			}
		}
	}

	// Merge headers: provider-level, then call-level opts.
	// Uses Add semantics: multiple values for the same key are preserved.
	headers := make(map[string][]string)
	for k, vs := range cfg.Headers {
		headers[k] = append(headers[k], vs...)
	}
	for k, vs := range opts.Headers {
		headers[k] = append(headers[k], vs...)
	}

	return ProviderEndpoint{
		ProviderName:     cfg.Name,
		BaseURL:          cfg.BaseURL,
		APIKey:           apiKey,
		Headers:          headers,
		ProviderSpecific: cfg.ProviderSpecific,
	}
}
