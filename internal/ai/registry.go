package ai

import (
	"fmt"
	"sync"
)

var (
	providerMu       sync.RWMutex
	providerRegistry = make(map[string]registeredProvider)
)

type registeredProvider struct {
	provider Provider
	sourceID string
}

// RegisterProvider registers a provider for its API identifier.
func RegisterProvider(p Provider, sourceID string) {
	providerMu.Lock()
	defer providerMu.Unlock()
	providerRegistry[p.API()] = registeredProvider{provider: p, sourceID: sourceID}
}

// GetProvider returns the provider for the given API.
func GetProvider(api string) (Provider, error) {
	providerMu.RLock()
	defer providerMu.RUnlock()
	rp, ok := providerRegistry[api]
	if !ok {
		return nil, fmt.Errorf("no provider registered for API: %s", api)
	}
	return rp.provider, nil
}

// GetProviders returns all registered providers.
func GetProviders() []Provider {
	providerMu.RLock()
	defer providerMu.RUnlock()
	providers := make([]Provider, 0, len(providerRegistry))
	for _, rp := range providerRegistry {
		providers = append(providers, rp.provider)
	}
	return providers
}

// UnregisterProviders removes all providers registered with sourceID.
func UnregisterProviders(sourceID string) {
	providerMu.Lock()
	defer providerMu.Unlock()
	for api, rp := range providerRegistry {
		if rp.sourceID == sourceID {
			delete(providerRegistry, api)
		}
	}
}

// ClearProviders removes all registered providers.
func ClearProviders() {
	providerMu.Lock()
	defer providerMu.Unlock()
	providerRegistry = make(map[string]registeredProvider)
}
