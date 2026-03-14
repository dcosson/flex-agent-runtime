package ai

import (
	"fmt"
	"sync"
)

var (
	embeddingProviderMu       sync.RWMutex
	embeddingProviderRegistry = make(map[string]registeredEmbeddingProvider)
)

type registeredEmbeddingProvider struct {
	provider EmbeddingProvider
	sourceID string
}

// RegisterEmbeddingProvider registers an embedding provider for its API identifier.
func RegisterEmbeddingProvider(p EmbeddingProvider, sourceID string) {
	embeddingProviderMu.Lock()
	defer embeddingProviderMu.Unlock()
	embeddingProviderRegistry[p.API()] = registeredEmbeddingProvider{provider: p, sourceID: sourceID}
}

// GetEmbeddingProvider returns the embedding provider for the given API.
func GetEmbeddingProvider(api string) (EmbeddingProvider, error) {
	embeddingProviderMu.RLock()
	defer embeddingProviderMu.RUnlock()
	rp, ok := embeddingProviderRegistry[api]
	if !ok {
		return nil, fmt.Errorf("no embedding provider registered for API: %s", api)
	}
	return rp.provider, nil
}

// UnregisterEmbeddingProviders removes all providers registered with sourceID.
func UnregisterEmbeddingProviders(sourceID string) {
	embeddingProviderMu.Lock()
	defer embeddingProviderMu.Unlock()
	for api, rp := range embeddingProviderRegistry {
		if rp.sourceID == sourceID {
			delete(embeddingProviderRegistry, api)
		}
	}
}

// ClearEmbeddingProviders removes all registered embedding providers.
func ClearEmbeddingProviders() {
	embeddingProviderMu.Lock()
	defer embeddingProviderMu.Unlock()
	embeddingProviderRegistry = make(map[string]registeredEmbeddingProvider)
}
