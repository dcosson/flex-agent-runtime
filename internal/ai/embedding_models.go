package ai

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

//go:embed models/embedding_catalog.json
var embeddingCatalogJSON []byte

var (
	embeddingModelMu       sync.RWMutex
	embeddingModelRegistry = make(map[string]EmbeddingModel)
)

// embeddingCatalogFile is the on-disk format for the embedding catalog.
type embeddingCatalogFile struct {
	LastUpdated string           `json:"lastUpdated"`
	Models      []EmbeddingModel `json:"models"`
}

// loadEmbeddingCatalog parses embedding_catalog.json and registers embedding models.
// Called from the single init() in models.go after provider configs are registered.
func loadEmbeddingCatalog(providers map[string]ProviderConfig) {
	var catalog embeddingCatalogFile
	if err := json.Unmarshal(embeddingCatalogJSON, &catalog); err != nil {
		panic(fmt.Sprintf("failed to load embedding catalog: %v", err))
	}
	for _, m := range catalog.Models {
		provCfg, ok := providers[m.Provider]
		if !ok {
			panic(fmt.Sprintf("embedding catalog: provider %q not in providers section", m.Provider))
		}
		// Derive API from provider's EmbeddingAPIClientType if model doesn't specify.
		if m.API == "" && provCfg.EmbeddingAPIClientType != "" {
			m.API = provCfg.EmbeddingAPIClientType
		}
		m.PricingKnown = true
		RegisterEmbeddingModel(m)
	}
}

// RegisterEmbeddingModel registers an embedding model by ID.
func RegisterEmbeddingModel(m EmbeddingModel) {
	m = deepCopyEmbeddingModel(m)
	embeddingModelMu.Lock()
	defer embeddingModelMu.Unlock()
	embeddingModelRegistry[m.ID] = m
}

// GetEmbeddingModel returns a registered embedding model by ID.
func GetEmbeddingModel(id string) (EmbeddingModel, bool) {
	embeddingModelMu.RLock()
	defer embeddingModelMu.RUnlock()
	m, ok := embeddingModelRegistry[id]
	if !ok {
		return EmbeddingModel{}, false
	}
	return deepCopyEmbeddingModel(m), true
}

// ListEmbeddingModels returns all registered embedding models.
func ListEmbeddingModels() []EmbeddingModel {
	embeddingModelMu.RLock()
	defer embeddingModelMu.RUnlock()
	models := make([]EmbeddingModel, 0, len(embeddingModelRegistry))
	for _, m := range embeddingModelRegistry {
		models = append(models, deepCopyEmbeddingModel(m))
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

// ListEmbeddingModelsByProvider returns registered embedding models for provider.
func ListEmbeddingModelsByProvider(provider string) []EmbeddingModel {
	embeddingModelMu.RLock()
	defer embeddingModelMu.RUnlock()
	models := make([]EmbeddingModel, 0)
	for _, m := range embeddingModelRegistry {
		if m.Provider == provider {
			models = append(models, deepCopyEmbeddingModel(m))
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

// ClearEmbeddingModels removes all registered embedding models.
func ClearEmbeddingModels() {
	embeddingModelMu.Lock()
	defer embeddingModelMu.Unlock()
	embeddingModelRegistry = make(map[string]EmbeddingModel)
}

func deepCopyEmbeddingModel(m EmbeddingModel) EmbeddingModel {
	if m.Headers != nil {
		h := make(map[string][]string, len(m.Headers))
		for k, v := range m.Headers {
			vc := make([]string, len(v))
			copy(vc, v)
			h[k] = vc
		}
		m.Headers = h
	}
	return m
}
