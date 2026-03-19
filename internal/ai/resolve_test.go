package ai

import (
	"os"
	"testing"
)

func TestResolveEndpoint(t *testing.T) {
	ClearProviderConfigs()
	t.Cleanup(ClearProviderConfigs)

	cfg := ProviderConfig{
		Name:    "test-provider",
		BaseURL: "https://api.example.com",
		Headers: map[string][]string{"X-Provider": {"pval"}},
		ProviderSpecific: map[string]string{
			"org": "test-org",
		},
	}

	t.Run("opts_apikey_takes_precedence", func(t *testing.T) {
		ep := ResolveEndpoint(cfg, StreamOptions{APIKey: "opts-key"})
		if ep.APIKey != "opts-key" {
			t.Fatalf("apiKey=%q want=opts-key", ep.APIKey)
		}
	})

	t.Run("direct_apikey_takes_precedence_over_env", func(t *testing.T) {
		directAPIKeysMu.Lock()
		directAPIKeys["test-provider"] = "direct-key"
		directAPIKeysMu.Unlock()
		defer func() {
			directAPIKeysMu.Lock()
			delete(directAPIKeys, "test-provider")
			directAPIKeysMu.Unlock()
		}()

		ep := ResolveEndpoint(cfg, StreamOptions{})
		if ep.APIKey != "direct-key" {
			t.Fatalf("apiKey=%q want=direct-key", ep.APIKey)
		}
	})

	t.Run("basic_fields", func(t *testing.T) {
		ep := ResolveEndpoint(cfg, StreamOptions{})
		if ep.ProviderName != "test-provider" {
			t.Fatalf("providerName=%q", ep.ProviderName)
		}
		if ep.BaseURL != "https://api.example.com" {
			t.Fatalf("baseURL=%q", ep.BaseURL)
		}
		if ep.ProviderSpecific["org"] != "test-org" {
			t.Fatalf("providerSpecific=%v", ep.ProviderSpecific)
		}
	})

	t.Run("header_merge", func(t *testing.T) {
		ep := ResolveEndpoint(cfg, StreamOptions{
			Headers: map[string][]string{"X-Call": {"cval"}},
		})
		if len(ep.Headers["X-Provider"]) != 1 || ep.Headers["X-Provider"][0] != "pval" {
			t.Fatalf("provider header missing: %v", ep.Headers)
		}
		if len(ep.Headers["X-Call"]) != 1 || ep.Headers["X-Call"][0] != "cval" {
			t.Fatalf("call header missing: %v", ep.Headers)
		}
	})

	t.Run("header_merge_same_key", func(t *testing.T) {
		cfgWithHeader := ProviderConfig{
			Name:    "hp",
			Headers: map[string][]string{"X-Beta": {"beta1"}},
		}
		ep := ResolveEndpoint(cfgWithHeader, StreamOptions{
			Headers: map[string][]string{"X-Beta": {"beta2"}},
		})
		if len(ep.Headers["X-Beta"]) != 2 {
			t.Fatalf("expected 2 X-Beta values, got %v", ep.Headers["X-Beta"])
		}
	})
}

func TestResolveEndpointEnvVars(t *testing.T) {
	ClearProviderConfigs()
	t.Cleanup(ClearProviderConfigs)

	const envVar = "TEST_RESOLVE_API_KEY_12345"
	os.Setenv(envVar, "env-key-value")
	t.Cleanup(func() { os.Unsetenv(envVar) })

	cfg := ProviderConfig{
		Name:       "env-test",
		KeyEnvVars: []string{"NONEXISTENT_VAR_XYZ", envVar},
	}

	ep := ResolveEndpoint(cfg, StreamOptions{})
	if ep.APIKey != "env-key-value" {
		t.Fatalf("apiKey=%q want=env-key-value", ep.APIKey)
	}
}

func TestResolveEndpointEmptyKey(t *testing.T) {
	cfg := ProviderConfig{
		Name:       "no-key",
		KeyEnvVars: []string{"NONEXISTENT_VAR_ABC"},
	}
	ep := ResolveEndpoint(cfg, StreamOptions{})
	if ep.APIKey != "" {
		t.Fatalf("expected empty apiKey, got %q", ep.APIKey)
	}
}

// TestMultipleProvidersPerAPIClient verifies that multiple ProviderConfigs can
// share the same APIClient type (the core value proposition of the separation).
func TestMultipleProvidersPerAPIClient(t *testing.T) {
	ClearAPIClients()
	ClearProviderConfigs()
	t.Cleanup(func() {
		ClearAPIClients()
		ClearProviderConfigs()
	})

	// Register a single API client type.
	RegisterAPIClient(&mockAPIClient{clientType: "openai-completions"})

	// Register two different providers that share the same API client type.
	RegisterProviderConfig(ProviderConfig{
		Name:          "openai-direct",
		APIClientType: "openai-completions",
		BaseURL:       "https://api.openai.com/v1",
		KeyEnvVars:    []string{"OPENAI_API_KEY"},
	})
	RegisterProviderConfig(ProviderConfig{
		Name:          "openrouter",
		APIClientType: "openai-completions",
		BaseURL:       "https://openrouter.ai/api/v1",
		KeyEnvVars:    []string{"OPENROUTER_API_KEY"},
	})

	// Both providers resolve to the same API client.
	cfg1, err := GetProviderConfig("openai-direct")
	if err != nil {
		t.Fatalf("GetProviderConfig openai-direct: %v", err)
	}
	cfg2, err := GetProviderConfig("openrouter")
	if err != nil {
		t.Fatalf("GetProviderConfig openrouter: %v", err)
	}

	client1, err := GetAPIClient(cfg1.APIClientType)
	if err != nil {
		t.Fatalf("GetAPIClient for openai-direct: %v", err)
	}
	client2, err := GetAPIClient(cfg2.APIClientType)
	if err != nil {
		t.Fatalf("GetAPIClient for openrouter: %v", err)
	}

	// Same client instance (same protocol).
	if client1.ClientType() != client2.ClientType() {
		t.Fatalf("expected same client type, got %q and %q", client1.ClientType(), client2.ClientType())
	}

	// But different endpoints.
	ep1 := ResolveEndpoint(cfg1, StreamOptions{})
	ep2 := ResolveEndpoint(cfg2, StreamOptions{})
	if ep1.BaseURL == ep2.BaseURL {
		t.Fatalf("expected different base URLs, both got %q", ep1.BaseURL)
	}
	if ep1.ProviderName == ep2.ProviderName {
		t.Fatalf("expected different provider names, both got %q", ep1.ProviderName)
	}
}

// TestMultiValuedHeaders verifies that headers with multiple values
// (e.g., anthropic-beta) are correctly preserved through resolution.
func TestMultiValuedHeaders(t *testing.T) {
	cfg := ProviderConfig{
		Name: "multi-header-test",
		Headers: map[string][]string{
			"anthropic-beta": {"prompt-caching-2024-07-31", "max-tokens-3-5-sonnet-2024-07-15"},
			"X-Single":       {"one"},
		},
	}

	t.Run("provider_multi_valued_preserved", func(t *testing.T) {
		ep := ResolveEndpoint(cfg, StreamOptions{})
		betas := ep.Headers["anthropic-beta"]
		if len(betas) != 2 {
			t.Fatalf("expected 2 beta values, got %d: %v", len(betas), betas)
		}
		if betas[0] != "prompt-caching-2024-07-31" || betas[1] != "max-tokens-3-5-sonnet-2024-07-15" {
			t.Fatalf("beta values mismatch: %v", betas)
		}
	})

	t.Run("call_level_appended_not_replaced", func(t *testing.T) {
		ep := ResolveEndpoint(cfg, StreamOptions{
			Headers: map[string][]string{
				"anthropic-beta": {"interleaved-thinking-2025-05-14"},
			},
		})
		betas := ep.Headers["anthropic-beta"]
		if len(betas) != 3 {
			t.Fatalf("expected 3 beta values after merge, got %d: %v", len(betas), betas)
		}
	})

	t.Run("single_valued_also_works", func(t *testing.T) {
		ep := ResolveEndpoint(cfg, StreamOptions{})
		singles := ep.Headers["X-Single"]
		if len(singles) != 1 || singles[0] != "one" {
			t.Fatalf("X-Single: %v", singles)
		}
	})
}
