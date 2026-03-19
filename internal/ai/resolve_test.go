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
