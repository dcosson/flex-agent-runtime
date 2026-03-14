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

func init() {
	var models []EmbeddingModel
	if err := json.Unmarshal(embeddingCatalogJSON, &models); err != nil {
		panic(fmt.Sprintf("failed to load embedding catalog: %v", err))
	}
	for _, m := range models {
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
		h := make(map[string]string, len(m.Headers))
		for k, v := range m.Headers {
			h[k] = v
		}
		m.Headers = h
	}
	return m
}
