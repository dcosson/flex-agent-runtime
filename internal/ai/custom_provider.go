package ai

import "fmt"

// CustomProviderConfig is a convenience for registering a provider at runtime
// for an arbitrary endpoint. Unlike catalog providers, custom providers:
// - Accept any model name (no catalog validation)
// - Have unknown pricing by default (PricingKnown=false, Cost fields zero-valued)
// - Can accept API keys directly (not just via env vars)
type CustomProviderConfig struct {
	ProviderConfig

	// APIKey is a directly-provided API key. If set, takes precedence
	// over KeyEnvVars during resolution.
	APIKey string
}

// RegisterCustomProvider registers a custom provider from runtime config.
// It registers the ProviderConfig in the provider registry.
// Returns an error if Name, APIClientType, or BaseURL is empty.
//
// Note: API client type validation is NOT performed at registration time.
// This is intentional -- API clients may not be registered yet if
// RegisterCustomProvider is called during init(). Validation happens at
// Stream()/Embed() time, which already checks via GetAPIClient/GetEmbeddingAPIClient.
func RegisterCustomProvider(cfg CustomProviderConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("custom provider name is required")
	}
	if cfg.APIClientType == "" {
		return fmt.Errorf("custom provider %q: apiClientType is required", cfg.Name)
	}
	if cfg.BaseURL == "" {
		return fmt.Errorf("custom provider %q: baseURL is required", cfg.Name)
	}

	// Register the provider config first to maintain consistent lock ordering:
	// providerConfigMu -> directAPIKeysMu (same order as UnregisterProviderConfig
	// and ClearProviderConfigs).
	RegisterProviderConfig(cfg.ProviderConfig)

	// If a direct API key was provided, store it in the module-level map.
	// ResolveEndpoint checks this map as step 2 in key resolution.
	if cfg.APIKey != "" {
		directAPIKeysMu.Lock()
		directAPIKeys[cfg.Name] = cfg.APIKey
		directAPIKeysMu.Unlock()
	}

	return nil
}

// RegisterCustomModel registers a model for a custom provider.
// The model's Provider and API fields are set from the provider config.
// PricingKnown defaults to false unless explicitly set in opts.
func RegisterCustomModel(providerName string, modelID string, opts CustomModelOpts) error {
	cfg, err := GetProviderConfig(providerName)
	if err != nil {
		return fmt.Errorf("register custom model: %w", err)
	}
	m := Model{
		ID:            modelID,
		Name:          opts.Name,
		Provider:      providerName,
		API:           cfg.APIClientType,
		Reasoning:     opts.Reasoning,
		Input:         opts.Input,
		Cost:          opts.Cost,
		PricingKnown:  opts.PricingKnown,
		ContextWindow: opts.ContextWindow,
		MaxTokens:     opts.MaxTokens,
		Compat:        opts.Compat,
	}
	if m.Name == "" {
		m.Name = modelID
	}
	RegisterModel(m)
	return nil
}

// CustomModelOpts provides optional fields when registering a custom model.
type CustomModelOpts struct {
	Name          string
	Reasoning     bool
	Input         []string
	Cost          ModelCost
	PricingKnown  bool // false by default; set to true if pricing is known
	ContextWindow int
	MaxTokens     int
	Compat        *ModelCompat
}
