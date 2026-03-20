package ai

import (
	"fmt"
	"sync"
)

var (
	apiClientMu       sync.RWMutex
	apiClientRegistry = make(map[string]APIClient)
)

// RegisterAPIClient registers an API client type.
// Typically called once per protocol implementation at init time.
func RegisterAPIClient(c APIClient) {
	apiClientMu.Lock()
	defer apiClientMu.Unlock()
	apiClientRegistry[c.ClientType()] = c
}

// GetAPIClient returns the API client for the given type.
func GetAPIClient(clientType string) (APIClient, error) {
	apiClientMu.RLock()
	defer apiClientMu.RUnlock()
	c, ok := apiClientRegistry[clientType]
	if !ok {
		return nil, fmt.Errorf("no API client registered for type: %s", clientType)
	}
	return c, nil
}

// ClearAPIClients removes all registered API clients.
// For testing only. Do not call in production code.
func ClearAPIClients() {
	apiClientMu.Lock()
	defer apiClientMu.Unlock()
	apiClientRegistry = make(map[string]APIClient)
}
