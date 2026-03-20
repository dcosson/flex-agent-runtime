package ai

import (
	"testing"
)

func TestCustomProviderRegistration(t *testing.T) {
	ClearProviderConfigs()
	ClearModels()
	t.Cleanup(ClearProviderConfigs)
	t.Cleanup(ClearModels)

	err := RegisterCustomProvider(CustomProviderConfig{
		ProviderConfig: ProviderConfig{
			Name:          "my-provider",
			APIClientType: "openai-completions",
			BaseURL:       "https://my-llm.example.com/v1",
			KeyEnvVars:    []string{"MY_API_KEY"},
		},
		APIKey: "sk-direct-key",
	})
	if err != nil {
		t.Fatalf("RegisterCustomProvider err: %v", err)
	}

	cfg, err := GetProviderConfig("my-provider")
	if err != nil {
		t.Fatalf("GetProviderConfig err: %v", err)
	}
	if cfg.BaseURL != "https://my-llm.example.com/v1" {
		t.Fatalf("baseURL=%q", cfg.BaseURL)
	}
	if cfg.APIClientType != "openai-completions" {
		t.Fatalf("apiClientType=%q", cfg.APIClientType)
	}

	// Verify direct API key was stored
	directAPIKeysMu.RLock()
	key := directAPIKeys["my-provider"]
	directAPIKeysMu.RUnlock()
	if key != "sk-direct-key" {
		t.Fatalf("directAPIKey=%q want=sk-direct-key", key)
	}
}

func TestCustomProviderValidation(t *testing.T) {
	ClearProviderConfigs()
	t.Cleanup(ClearProviderConfigs)

	tests := []struct {
		name string
		cfg  CustomProviderConfig
	}{
		{
			name: "missing_name",
			cfg: CustomProviderConfig{
				ProviderConfig: ProviderConfig{APIClientType: "t", BaseURL: "u"},
			},
		},
		{
			name: "missing_api_client_type",
			cfg: CustomProviderConfig{
				ProviderConfig: ProviderConfig{Name: "p", BaseURL: "u"},
			},
		},
		{
			name: "missing_base_url",
			cfg: CustomProviderConfig{
				ProviderConfig: ProviderConfig{Name: "p", APIClientType: "t"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := RegisterCustomProvider(tc.cfg); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCustomProviderOverwritesCatalogProvider(t *testing.T) {
	ClearProviderConfigs()
	ClearModels()
	t.Cleanup(ClearProviderConfigs)
	t.Cleanup(ClearModels)

	// Register "original" provider
	RegisterProviderConfig(ProviderConfig{
		Name:          "openai",
		APIClientType: "openai-completions",
		BaseURL:       "https://api.openai.com/v1",
	})

	// Overwrite with custom
	err := RegisterCustomProvider(CustomProviderConfig{
		ProviderConfig: ProviderConfig{
			Name:          "openai",
			APIClientType: "openai-completions",
			BaseURL:       "https://my-proxy.example.com/v1",
		},
		APIKey: "proxy-key",
	})
	if err != nil {
		t.Fatalf("RegisterCustomProvider err: %v", err)
	}

	cfg, _ := GetProviderConfig("openai")
	if cfg.BaseURL != "https://my-proxy.example.com/v1" {
		t.Fatalf("baseURL not overwritten: %q", cfg.BaseURL)
	}
}

func TestCustomModelRegistration(t *testing.T) {
	ClearProviderConfigs()
	ClearModels()
	t.Cleanup(ClearProviderConfigs)
	t.Cleanup(ClearModels)

	_ = RegisterCustomProvider(CustomProviderConfig{
		ProviderConfig: ProviderConfig{
			Name:          "my-provider",
			APIClientType: "openai-completions",
			BaseURL:       "https://example.com",
		},
	})

	err := RegisterCustomModel("my-provider", "my-model", CustomModelOpts{
		ContextWindow: 128000,
		MaxTokens:     4096,
	})
	if err != nil {
		t.Fatalf("RegisterCustomModel err: %v", err)
	}

	m, err := GetModel("my-provider", "my-model")
	if err != nil {
		t.Fatalf("GetModel err: %v", err)
	}
	if m.Provider != "my-provider" {
		t.Fatalf("provider=%q", m.Provider)
	}
	if m.API != "openai-completions" {
		t.Fatalf("api=%q", m.API)
	}
	if m.ContextWindow != 128000 {
		t.Fatalf("contextWindow=%d", m.ContextWindow)
	}
	if m.Name != "my-model" {
		t.Fatalf("name=%q want=my-model (should default to ID)", m.Name)
	}
}

func TestCustomModelPricingKnown(t *testing.T) {
	ClearProviderConfigs()
	ClearModels()
	t.Cleanup(ClearProviderConfigs)
	t.Cleanup(ClearModels)

	_ = RegisterCustomProvider(CustomProviderConfig{
		ProviderConfig: ProviderConfig{
			Name:          "p",
			APIClientType: "t",
			BaseURL:       "u",
		},
	})

	// Default: PricingKnown = false
	_ = RegisterCustomModel("p", "m1", CustomModelOpts{})
	m1, _ := GetModel("p", "m1")
	if m1.PricingKnown {
		t.Fatal("expected PricingKnown=false by default for custom model")
	}

	// Explicit: PricingKnown = true
	_ = RegisterCustomModel("p", "m2", CustomModelOpts{
		PricingKnown: true,
		Cost:         ModelCost{Input: 0.5, Output: 1.0},
	})
	m2, _ := GetModel("p", "m2")
	if !m2.PricingKnown {
		t.Fatal("expected PricingKnown=true when explicitly set")
	}
	if m2.Cost.Input != 0.5 {
		t.Fatalf("cost.input=%f", m2.Cost.Input)
	}
}

func TestCustomModelMissingProvider(t *testing.T) {
	ClearProviderConfigs()
	ClearModels()
	t.Cleanup(ClearProviderConfigs)
	t.Cleanup(ClearModels)

	err := RegisterCustomModel("nonexistent", "m1", CustomModelOpts{})
	if err == nil {
		t.Fatal("expected error for missing provider")
	}
}

func TestCalculateCostPricingKnown(t *testing.T) {
	// Verify that CalculateCost still works regardless of PricingKnown flag.
	// PricingKnown is informational — cost calculation is always attempted.
	usage := &Usage{Input: 1000, Output: 500}
	model := Model{
		PricingKnown: false,
		Cost:         ModelCost{Input: 10.0, Output: 20.0},
	}
	CalculateCost(model, usage)
	if usage.Cost.Total == 0 {
		t.Fatal("expected non-zero cost even with PricingKnown=false")
	}

	usage2 := &Usage{Input: 1000, Output: 500}
	model2 := Model{
		PricingKnown: true,
		Cost:         ModelCost{Input: 10.0, Output: 20.0},
	}
	CalculateCost(model2, usage2)
	if usage2.Cost.Total != usage.Cost.Total {
		t.Fatalf("cost should be identical regardless of PricingKnown: %f vs %f",
			usage.Cost.Total, usage2.Cost.Total)
	}
}
