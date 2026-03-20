package ai

import (
	"fmt"
	"sort"
	"sync"
)

var (
	providerConfigMu       sync.RWMutex
	providerConfigRegistry = make(map[string]ProviderConfig)
)

// directAPIKeys stores API keys supplied directly via RegisterCustomProvider,
// keyed by provider name. Checked by ResolveEndpoint as step 2 in key resolution.
// Not part of ProviderConfig to keep that struct fully JSON-serializable.
var (
	directAPIKeysMu sync.RWMutex
	directAPIKeys   = make(map[string]string)
)

// RegisterProviderConfig registers a named provider configuration.
func RegisterProviderConfig(cfg ProviderConfig) {
	cfg = deepCopyProviderConfig(cfg)
	providerConfigMu.Lock()
	defer providerConfigMu.Unlock()
	providerConfigRegistry[cfg.Name] = cfg
}

// GetProviderConfig returns the provider configuration for the given name.
func GetProviderConfig(name string) (ProviderConfig, error) {
	providerConfigMu.RLock()
	defer providerConfigMu.RUnlock()
	cfg, ok := providerConfigRegistry[name]
	if !ok {
		return ProviderConfig{}, fmt.Errorf("no provider registered: %s", name)
	}
	return deepCopyProviderConfig(cfg), nil
}

// ListProviderConfigs returns all registered provider names.
func ListProviderConfigs() []string {
	providerConfigMu.RLock()
	defer providerConfigMu.RUnlock()
	names := make([]string, 0, len(providerConfigRegistry))
	for name := range providerConfigRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// UnregisterProviderConfig removes a provider by name.
// Also removes any direct API key associated with this provider.
func UnregisterProviderConfig(name string) {
	providerConfigMu.Lock()
	defer providerConfigMu.Unlock()
	delete(providerConfigRegistry, name)
	directAPIKeysMu.Lock()
	defer directAPIKeysMu.Unlock()
	delete(directAPIKeys, name)
}

// ClearProviderConfigs removes all provider configurations and direct API keys.
// For testing only. Do not call in production code.
// Tests should use t.Cleanup(ai.ClearProviderConfigs) for full cleanup,
// or defer ai.UnregisterProviderConfig(name) for individual cleanup.
func ClearProviderConfigs() {
	providerConfigMu.Lock()
	defer providerConfigMu.Unlock()
	providerConfigRegistry = make(map[string]ProviderConfig)
	directAPIKeysMu.Lock()
	defer directAPIKeysMu.Unlock()
	directAPIKeys = make(map[string]string)
}

func deepCopyProviderConfig(cfg ProviderConfig) ProviderConfig {
	if cfg.KeyEnvVars != nil {
		kv := make([]string, len(cfg.KeyEnvVars))
		copy(kv, cfg.KeyEnvVars)
		cfg.KeyEnvVars = kv
	}
	if cfg.Headers != nil {
		h := make(map[string][]string, len(cfg.Headers))
		for k, v := range cfg.Headers {
			vc := make([]string, len(v))
			copy(vc, v)
			h[k] = vc
		}
		cfg.Headers = h
	}
	if cfg.ProviderSpecific != nil {
		ps := make(map[string]string, len(cfg.ProviderSpecific))
		for k, v := range cfg.ProviderSpecific {
			ps[k] = v
		}
		cfg.ProviderSpecific = ps
	}
	return cfg
}
