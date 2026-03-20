package ai

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"pgregory.net/rapid"
)

func TestModelRegistryMutationIsolation(t *testing.T) {
	ClearModels()
	RegisterModel(Model{
		ID:       "test-model",
		Provider: "test",
		API:      "openai-completions",
		Headers:  map[string][]string{"X-Key": {"original"}},
		Input:    []string{"text"},
		Compat:   &ModelCompat{ReasoningEffortMap: map[string]string{"high": "h"}},
	})

	m, err := GetModel("test", "test-model")
	if err != nil {
		t.Fatalf("GetModel err: %v", err)
	}
	m.Headers["X-Key"] = []string{"mutated"}
	m.Headers["X-New"] = []string{"injected"}
	m.Input[0] = "corrupted"
	m.Compat.ReasoningEffortMap["high"] = "corrupted"

	m2, err := GetModel("test", "test-model")
	if err != nil {
		t.Fatalf("GetModel err: %v", err)
	}
	if len(m2.Headers["X-Key"]) != 1 || m2.Headers["X-Key"][0] != "original" {
		t.Fatalf("headers mutated in registry")
	}
	if _, ok := m2.Headers["X-New"]; ok {
		t.Fatalf("unexpected injected header")
	}
	if m2.Input[0] != "text" {
		t.Fatalf("input mutated in registry")
	}
	if m2.Compat.ReasoningEffortMap["high"] != "h" {
		t.Fatalf("compat map mutated")
	}
}

// P3: CalculateCost consistency with property testing.
func TestCostConsistencyRapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		usage := &Usage{
			Input:      rapid.IntRange(0, 1_000_000).Draw(t, "input"),
			Output:     rapid.IntRange(0, 100_000).Draw(t, "output"),
			CacheRead:  rapid.IntRange(0, 1_000_000).Draw(t, "cacheRead"),
			CacheWrite: rapid.IntRange(0, 1_000_000).Draw(t, "cacheWrite"),
		}
		model := Model{Cost: ModelCost{
			Input:      rapid.Float64Range(0, 100).Draw(t, "inputCost"),
			Output:     rapid.Float64Range(0, 100).Draw(t, "outputCost"),
			CacheRead:  rapid.Float64Range(0, 100).Draw(t, "cacheReadCost"),
			CacheWrite: rapid.Float64Range(0, 100).Draw(t, "cacheWriteCost"),
		}}

		CalculateCost(model, usage)
		expected := usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
		if diff := usage.Cost.Total - expected; diff > 1e-9 || diff < -1e-9 {
			t.Fatalf("cost mismatch: got=%f expected=%f", usage.Cost.Total, expected)
		}
	})
}

func TestModelUtilities(t *testing.T) {
	a := &Model{ID: "m", Provider: "p", API: "openai-completions"}
	b := &Model{ID: "m", Provider: "p", API: "anthropic-messages"}
	if !ModelsEqual(a, b) {
		t.Fatalf("expected equal by ID/provider")
	}
	if !SupportsXHigh(Model{ID: "my-opus-4-6", API: "anthropic-messages"}) {
		t.Fatalf("expected xhigh support")
	}
	if SupportsXHigh(Model{ID: "sonnet", API: "anthropic-messages"}) {
		t.Fatalf("unexpected xhigh support")
	}
}

func TestOptionsHelpers(t *testing.T) {
	model := Model{MaxTokens: 50000}
	s := BuildBaseOptions(model, nil)
	if s.MaxTokens == nil || *s.MaxTokens != 32000 {
		t.Fatalf("default max tokens mismatch")
	}

	if ClampReasoning(ThinkingXHigh) != ThinkingHigh {
		t.Fatalf("xhigh should clamp to high")
	}
	if ClampReasoning("") != ThinkingMedium {
		t.Fatalf("empty reasoning should default to medium")
	}
	if ClampReasoning(ThinkingLevel("unexpected")) != ThinkingMedium {
		t.Fatalf("unknown reasoning should default to medium")
	}
	max, budget := AdjustMaxTokensForThinking(2000, 3000, ThinkingHigh, nil)
	if max != 3000 || budget != 1976 {
		t.Fatalf("unexpected adjust result: max=%d budget=%d", max, budget)
	}
	max, budget = AdjustMaxTokensForThinking(2000, 3000, "", nil)
	if budget < 1024 {
		t.Fatalf("default reasoning budget should be >= 1024, got %d (max=%d)", budget, max)
	}

	custom := 500
	_, budget = AdjustMaxTokensForThinking(1000, 5000, ThinkingLow, &ThinkingBudgets{Low: &custom})
	if budget != 500 {
		t.Fatalf("expected custom budget")
	}
}

func TestModelProvidersAndGets(t *testing.T) {
	ClearModels()
	RegisterModel(Model{ID: "a", Provider: "p1"})
	RegisterModel(Model{ID: "b", Provider: "p2"})
	ps := GetModelProviders()
	if len(ps) != 2 || ps[0] != "p1" || ps[1] != "p2" {
		t.Fatalf("unexpected providers: %+v", ps)
	}
	if _, err := GetModel("missing", "x"); err == nil {
		t.Fatalf("expected error for missing provider")
	}
	if len(GetModels("missing")) != 0 {
		t.Fatalf("expected empty models slice")
	}
}

func TestModelRegistryConcurrent(t *testing.T) {
	ClearModels()
	const goroutines = 50
	const ops = 1000
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				provider := fmt.Sprintf("p-%d", id%5)
				modelID := fmt.Sprintf("m-%d", j%10)
				RegisterModel(Model{ID: modelID, Provider: provider})
				_, _ = GetModel(provider, modelID)
				_ = GetModels(provider)
				_ = GetModelProviders()
			}
		}(i)
	}
	wg.Wait()
}

// TestCatalogNewFormat verifies that catalog.json has the correct new format:
// provider configs under "providers", models under "models", and that model.API
// can be derived from ProviderConfig.APIClientType at load time.
func TestCatalogNewFormat(t *testing.T) {
	// Parse the embedded catalog JSON directly (independent of init() state
	// which may be affected by other tests that clear registries).
	var catalog catalogFile
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		t.Fatalf("unmarshal catalog: %v", err)
	}

	// Verify lastUpdated is set.
	if catalog.LastUpdated == "" {
		t.Fatal("lastUpdated is empty")
	}

	// Verify provider configs are present.
	for _, name := range []string{"anthropic", "google", "openai", "openrouter", "cohere"} {
		cfg, ok := catalog.Providers[name]
		if !ok {
			t.Fatalf("missing provider %q", name)
		}
		if cfg.BaseURL == "" {
			t.Fatalf("provider %q has empty BaseURL", name)
		}
	}

	// Verify chat models exist and API can be derived from provider.
	tests := []struct {
		provider string
		modelID  string
		wantAPI  string
	}{
		{"anthropic", "claude-sonnet-4-20250514", "anthropic-messages"},
		{"openai", "gpt-4o", "openai-completions"},
		{"google", "gemini-2.5-pro", "google-genai"},
	}
	for _, tc := range tests {
		provModels, ok := catalog.Models[tc.provider]
		if !ok {
			t.Fatalf("missing provider %q in models", tc.provider)
		}
		m, ok := provModels[tc.modelID]
		if !ok {
			t.Fatalf("missing model %q in provider %q", tc.modelID, tc.provider)
		}
		// API should be empty in JSON (derived at load time).
		if m.API != "" {
			t.Fatalf("model %s/%s: API=%q in JSON, should be empty (derived from provider)", tc.provider, tc.modelID, m.API)
		}
		// Verify the provider config has the expected APIClientType.
		provCfg := catalog.Providers[tc.provider]
		if provCfg.APIClientType != tc.wantAPI {
			t.Fatalf("provider %q: APIClientType=%q want=%q", tc.provider, provCfg.APIClientType, tc.wantAPI)
		}
		// Model name should be set.
		if m.Name == "" {
			t.Fatalf("model %s/%s: empty name", tc.provider, tc.modelID)
		}
	}

	// Verify embedding catalog also parses correctly.
	var embCatalog embeddingCatalogFile
	if err := json.Unmarshal(embeddingCatalogJSON, &embCatalog); err != nil {
		t.Fatalf("unmarshal embedding catalog: %v", err)
	}
	if embCatalog.LastUpdated == "" {
		t.Fatal("embedding catalog lastUpdated is empty")
	}

	embTests := []struct {
		id         string
		provider   string
		wantAPI    string
		wantInJSON bool // true if api should be explicit in JSON, false if derived
	}{
		// These providers have embeddingApiClientType — api is omitted from JSON and derived.
		{"text-embedding-3-small", "openai", "openai-embeddings", false},
		{"gemini-embedding-001", "google", "google-embeddings", false},
		{"embed-v4.0", "cohere", "cohere-embeddings", false},
		// openrouter has no embeddingApiClientType — api must be explicit in JSON.
		{"openai/text-embedding-3-small", "openrouter", "openai-embeddings", true},
	}
	for _, tc := range embTests {
		found := false
		for _, m := range embCatalog.Models {
			if m.ID == tc.id {
				found = true
				if m.Provider != tc.provider {
					t.Fatalf("embedding %q: Provider=%q want=%q", tc.id, m.Provider, tc.provider)
				}
				if tc.wantInJSON {
					// API should be explicitly set in JSON.
					if m.API != tc.wantAPI {
						t.Fatalf("embedding %q: API=%q want=%q (expected explicit)", tc.id, m.API, tc.wantAPI)
					}
				} else {
					// API should be empty in JSON (derived at load time from provider config).
					if m.API != "" {
						t.Fatalf("embedding %q: API=%q in JSON, should be empty (derived from provider)", tc.id, m.API)
					}
				}
				break
			}
		}
		if !found {
			t.Fatalf("embedding model %q not found in catalog", tc.id)
		}
	}

	// Verify the derivation path works end-to-end: simulate loadEmbeddingCatalog.
	// After loading, models with omitted api should have it filled from provider config.
	derivedModels := make(map[string]EmbeddingModel)
	for _, m := range embCatalog.Models {
		provCfg, ok := catalog.Providers[m.Provider]
		if !ok {
			t.Fatalf("embedding %q: provider %q not in providers", m.ID, m.Provider)
		}
		if m.API == "" && provCfg.EmbeddingAPIClientType != "" {
			m.API = provCfg.EmbeddingAPIClientType
		}
		derivedModels[m.ID] = m
	}
	for _, tc := range embTests {
		m := derivedModels[tc.id]
		if m.API != tc.wantAPI {
			t.Fatalf("embedding %q after derivation: API=%q want=%q", tc.id, m.API, tc.wantAPI)
		}
	}

	// Verify cohere is embedding-only (no APIClientType).
	cohereCfg := catalog.Providers["cohere"]
	if cohereCfg.APIClientType != "" {
		t.Fatalf("cohere should be embedding-only, has APIClientType=%q", cohereCfg.APIClientType)
	}
	if cohereCfg.EmbeddingAPIClientType != "cohere-embeddings" {
		t.Fatalf("cohere EmbeddingAPIClientType=%q", cohereCfg.EmbeddingAPIClientType)
	}

	// Verify plan-specified headers are present.
	anthrCfg := catalog.Providers["anthropic"]
	if betas := anthrCfg.Headers["anthropic-beta"]; len(betas) == 0 {
		t.Fatal("anthropic missing anthropic-beta headers")
	}
	orCfg := catalog.Providers["openrouter"]
	if refs := orCfg.Headers["HTTP-Referer"]; len(refs) == 0 {
		t.Fatal("openrouter missing HTTP-Referer header")
	}
	if titles := orCfg.Headers["X-Title"]; len(titles) == 0 {
		t.Fatal("openrouter missing X-Title header")
	}
}

// TestResolutionDeterminism verifies that ResolveEndpoint produces the same
// ProviderEndpoint given the same inputs, across many random configurations.
func TestResolutionDeterminism(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cfg := ProviderConfig{
			Name:    rapid.StringMatching(`[a-z]{3,10}`).Draw(t, "name"),
			BaseURL: rapid.StringMatching(`https://[a-z]+\.example\.com`).Draw(t, "baseURL"),
			Headers: map[string][]string{
				rapid.StringMatching(`X-[A-Z][a-z]+`).Draw(t, "hdrKey"): {
					rapid.StringMatching(`[a-z]+`).Draw(t, "hdrVal"),
				},
			},
			ProviderSpecific: map[string]string{
				"key": rapid.StringMatching(`[a-z]+`).Draw(t, "psVal"),
			},
		}
		opts := StreamOptions{
			APIKey: rapid.StringMatching(`[a-z]{0,10}`).Draw(t, "apiKey"),
		}

		ep1 := ResolveEndpoint(cfg, opts)
		ep2 := ResolveEndpoint(cfg, opts)

		if ep1.ProviderName != ep2.ProviderName {
			t.Fatalf("ProviderName differs: %q vs %q", ep1.ProviderName, ep2.ProviderName)
		}
		if ep1.BaseURL != ep2.BaseURL {
			t.Fatalf("BaseURL differs: %q vs %q", ep1.BaseURL, ep2.BaseURL)
		}
		if ep1.APIKey != ep2.APIKey {
			t.Fatalf("APIKey differs: %q vs %q", ep1.APIKey, ep2.APIKey)
		}
		if len(ep1.Headers) != len(ep2.Headers) {
			t.Fatalf("Headers len differs: %d vs %d", len(ep1.Headers), len(ep2.Headers))
		}
		if len(ep1.ProviderSpecific) != len(ep2.ProviderSpecific) {
			t.Fatalf("ProviderSpecific len differs")
		}
	})
}

// TestRegistryIsolation verifies that registering/unregistering in one registry
// does not affect other registries.
func TestRegistryIsolation(t *testing.T) {
	ClearAPIClients()
	ClearEmbeddingAPIClients()
	ClearProviderConfigs()
	t.Cleanup(func() {
		ClearAPIClients()
		ClearEmbeddingAPIClients()
		ClearProviderConfigs()
	})

	// Register items in each registry.
	RegisterAPIClient(&mockAPIClient{clientType: "iso-chat"})
	RegisterEmbeddingAPIClient(&mockEmbeddingClient{clientType: "iso-embed", fn: nil})
	RegisterProviderConfig(ProviderConfig{Name: "iso-provider", APIClientType: "iso-chat"})

	// Clearing API clients should not affect embedding or provider registries.
	ClearAPIClients()
	if _, err := GetEmbeddingAPIClient("iso-embed"); err != nil {
		t.Fatalf("embedding client lost after ClearAPIClients: %v", err)
	}
	if _, err := GetProviderConfig("iso-provider"); err != nil {
		t.Fatalf("provider config lost after ClearAPIClients: %v", err)
	}

	// Re-register, then clear embedding clients.
	RegisterAPIClient(&mockAPIClient{clientType: "iso-chat"})
	ClearEmbeddingAPIClients()
	if _, err := GetAPIClient("iso-chat"); err != nil {
		t.Fatalf("API client lost after ClearEmbeddingAPIClients: %v", err)
	}
	if _, err := GetProviderConfig("iso-provider"); err != nil {
		t.Fatalf("provider config lost after ClearEmbeddingAPIClients: %v", err)
	}

	// Re-register, then clear provider configs.
	RegisterEmbeddingAPIClient(&mockEmbeddingClient{clientType: "iso-embed", fn: nil})
	ClearProviderConfigs()
	if _, err := GetAPIClient("iso-chat"); err != nil {
		t.Fatalf("API client lost after ClearProviderConfigs: %v", err)
	}
	if _, err := GetEmbeddingAPIClient("iso-embed"); err != nil {
		t.Fatalf("embedding client lost after ClearProviderConfigs: %v", err)
	}
}

// TestDeepCopyIntegrity uses property testing to verify that mutating returned
// ProviderConfig values does not affect registry state.
func TestDeepCopyIntegrity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ClearProviderConfigs()

		name := rapid.StringMatching(`[a-z]{3,8}`).Draw(t, "name")
		hdrKey := rapid.StringMatching(`X-[A-Z][a-z]+`).Draw(t, "hdrKey")
		hdrVal := rapid.StringMatching(`[a-z]+`).Draw(t, "hdrVal")

		RegisterProviderConfig(ProviderConfig{
			Name:       name,
			BaseURL:    "https://example.com",
			KeyEnvVars: []string{"KEY1"},
			Headers:    map[string][]string{hdrKey: {hdrVal}},
			ProviderSpecific: map[string]string{
				"k": "original",
			},
		})

		// Get and mutate.
		got, err := GetProviderConfig(name)
		if err != nil {
			t.Fatalf("GetProviderConfig err: %v", err)
		}
		got.Headers[hdrKey] = []string{"mutated"}
		got.Headers["X-Injected"] = []string{"bad"}
		got.KeyEnvVars[0] = "MUTATED"
		got.ProviderSpecific["k"] = "mutated"
		got.ProviderSpecific["injected"] = "bad"

		// Verify registry is untouched.
		got2, _ := GetProviderConfig(name)
		if got2.Headers[hdrKey][0] != hdrVal {
			t.Fatalf("header mutated in registry: %v", got2.Headers[hdrKey])
		}
		if _, ok := got2.Headers["X-Injected"]; ok {
			t.Fatal("header injected into registry")
		}
		if got2.KeyEnvVars[0] != "KEY1" {
			t.Fatalf("keyEnvVars mutated: %v", got2.KeyEnvVars)
		}
		if got2.ProviderSpecific["k"] != "original" {
			t.Fatalf("providerSpecific mutated: %v", got2.ProviderSpecific)
		}
		if _, ok := got2.ProviderSpecific["injected"]; ok {
			t.Fatal("providerSpecific injected")
		}
	})
}
