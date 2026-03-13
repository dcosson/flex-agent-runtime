package ai

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed models/catalog.json
var catalogJSON []byte

var (
	modelMu       sync.RWMutex
	modelRegistry = make(map[string]map[string]Model) // provider -> modelID -> Model
)

// Model defines a provider model configuration.
type Model struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	API           string            `json:"api"`
	Provider      string            `json:"provider"`
	BaseURL       string            `json:"baseUrl"`
	Reasoning     bool              `json:"reasoning"`
	Input         []string          `json:"input"`
	Cost          ModelCost         `json:"cost"`
	ContextWindow int               `json:"contextWindow"`
	MaxTokens     int               `json:"maxTokens"`
	Headers       map[string]string `json:"headers,omitempty"`
	Compat        *ModelCompat      `json:"compat,omitempty"`
}

type ModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

type ModelCompat struct {
	SupportsStore                    *bool             `json:"supportsStore,omitempty"`
	SupportsDeveloperRole            *bool             `json:"supportsDeveloperRole,omitempty"`
	SupportsReasoningEffort          *bool             `json:"supportsReasoningEffort,omitempty"`
	ReasoningEffortMap               map[string]string `json:"reasoningEffortMap,omitempty"`
	SupportsUsageInStreaming         *bool             `json:"supportsUsageInStreaming,omitempty"`
	MaxTokensField                   string            `json:"maxTokensField,omitempty"`
	RequiresToolResultName           *bool             `json:"requiresToolResultName,omitempty"`
	RequiresAssistantAfterToolResult *bool             `json:"requiresAssistantAfterToolResult,omitempty"`
	RequiresThinkingAsText           *bool             `json:"requiresThinkingAsText,omitempty"`
	RequiresMistralToolIDs           *bool             `json:"requiresMistralToolIds,omitempty"`
	ThinkingFormat                   string            `json:"thinkingFormat,omitempty"`
	SupportsStrictMode               *bool             `json:"supportsStrictMode,omitempty"`
}

func init() {
	var catalog map[string]map[string]Model
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		panic(fmt.Sprintf("failed to load model catalog: %v", err))
	}
	for provider, models := range catalog {
		for id, m := range models {
			m.ID = id
			m.Provider = provider
			RegisterModel(m)
		}
	}
}

// RegisterModel registers a model under its provider.
func RegisterModel(m Model) {
	m = deepCopyModel(m)
	modelMu.Lock()
	defer modelMu.Unlock()
	providerModels, ok := modelRegistry[m.Provider]
	if !ok {
		providerModels = make(map[string]Model)
		modelRegistry[m.Provider] = providerModels
	}
	providerModels[m.ID] = m
}

// GetModel returns a deep copy of a registered model.
func GetModel(provider, modelID string) (Model, error) {
	modelMu.RLock()
	defer modelMu.RUnlock()
	providerModels, ok := modelRegistry[provider]
	if !ok {
		return Model{}, fmt.Errorf("no models registered for provider: %s", provider)
	}
	m, ok := providerModels[modelID]
	if !ok {
		return Model{}, fmt.Errorf("model %q not found for provider %q", modelID, provider)
	}
	return deepCopyModel(m), nil
}

// GetModels returns deep copies of all models for a provider.
func GetModels(provider string) []Model {
	modelMu.RLock()
	defer modelMu.RUnlock()
	providerModels := modelRegistry[provider]
	models := make([]Model, 0, len(providerModels))
	for _, m := range providerModels {
		models = append(models, deepCopyModel(m))
	}
	return models
}

// GetModelProviders returns all providers that have models.
func GetModelProviders() []string {
	modelMu.RLock()
	defer modelMu.RUnlock()
	providers := make([]string, 0, len(modelRegistry))
	for p := range modelRegistry {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	return providers
}

// ClearModels removes all registered models.
func ClearModels() {
	modelMu.Lock()
	defer modelMu.Unlock()
	modelRegistry = make(map[string]map[string]Model)
}

func deepCopyModel(m Model) Model {
	if m.Headers != nil {
		h := make(map[string]string, len(m.Headers))
		for k, v := range m.Headers {
			h[k] = v
		}
		m.Headers = h
	}
	if m.Input != nil {
		inp := make([]string, len(m.Input))
		copy(inp, m.Input)
		m.Input = inp
	}
	if m.Compat != nil {
		c := *m.Compat
		if c.ReasoningEffortMap != nil {
			rem := make(map[string]string, len(c.ReasoningEffortMap))
			for k, v := range c.ReasoningEffortMap {
				rem[k] = v
			}
			c.ReasoningEffortMap = rem
		}
		m.Compat = &c
	}
	return m
}

// CalculateCost computes usage cost in dollars using $/million token pricing.
func CalculateCost(model Model, usage *Usage) {
	usage.Cost.Input = (model.Cost.Input / 1_000_000) * float64(usage.Input)
	usage.Cost.Output = (model.Cost.Output / 1_000_000) * float64(usage.Output)
	usage.Cost.CacheRead = (model.Cost.CacheRead / 1_000_000) * float64(usage.CacheRead)
	usage.Cost.CacheWrite = (model.Cost.CacheWrite / 1_000_000) * float64(usage.CacheWrite)
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
}

// ModelsEqual checks equality by ID and Provider.
func ModelsEqual(a, b *Model) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID && a.Provider == b.Provider
}

// SupportsXHigh reports whether model supports xhigh reasoning.
func SupportsXHigh(model Model) bool {
	switch model.API {
	case "anthropic-messages":
		return strings.Contains(model.ID, "opus-4-6") || strings.Contains(model.ID, "opus-4.6")
	case "openai-completions":
		return strings.Contains(model.ID, "gpt-5.2") || strings.Contains(model.ID, "gpt-5.3")
	default:
		return false
	}
}
