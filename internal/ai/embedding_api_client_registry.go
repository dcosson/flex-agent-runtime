package ai

import (
	"fmt"
	"sync"
)

var (
	embeddingAPIClientMu       sync.RWMutex
	embeddingAPIClientRegistry = make(map[string]EmbeddingAPIClient)
)

// RegisterEmbeddingAPIClient registers an embedding API client type.
// Typically called once per embedding protocol implementation at init time.
func RegisterEmbeddingAPIClient(c EmbeddingAPIClient) {
	embeddingAPIClientMu.Lock()
	defer embeddingAPIClientMu.Unlock()
	embeddingAPIClientRegistry[c.ClientType()] = c
}

// GetEmbeddingAPIClient returns the embedding API client for the given type.
func GetEmbeddingAPIClient(clientType string) (EmbeddingAPIClient, error) {
	embeddingAPIClientMu.RLock()
	defer embeddingAPIClientMu.RUnlock()
	c, ok := embeddingAPIClientRegistry[clientType]
	if !ok {
		return nil, fmt.Errorf("no embedding API client registered for type: %s", clientType)
	}
	return c, nil
}

// ClearEmbeddingAPIClients removes all registered embedding API clients.
// For testing only. Do not call in production code.
func ClearEmbeddingAPIClients() {
	embeddingAPIClientMu.Lock()
	defer embeddingAPIClientMu.Unlock()
	embeddingAPIClientRegistry = make(map[string]EmbeddingAPIClient)
}
