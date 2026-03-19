package ai

import (
	"fmt"
	"sync"
	"testing"
)

func TestProviderConfigRegistryBasic(t *testing.T) {
	ClearProviderConfigs()
	t.Cleanup(ClearProviderConfigs)

	cfg := ProviderConfig{
		Name:          "openai",
		APIClientType: "openai-completions",
		BaseURL:       "https://api.openai.com/v1",
		KeyEnvVars:    []string{"OPENAI_API_KEY"},
		Headers:       map[string][]string{"X-Custom": {"val"}},
	}
	RegisterProviderConfig(cfg)

	got, err := GetProviderConfig("openai")
	if err != nil {
		t.Fatalf("GetProviderConfig err: %v", err)
	}
	if got.Name != "openai" {
		t.Fatalf("name=%q", got.Name)
	}
	if got.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("baseURL=%q", got.BaseURL)
	}

	names := ListProviderConfigs()
	if len(names) != 1 || names[0] != "openai" {
		t.Fatalf("names=%v", names)
	}

	UnregisterProviderConfig("openai")
	if _, err := GetProviderConfig("openai"); err == nil {
		t.Fatal("expected error after unregister")
	}
}

func TestProviderConfigRegistryDeepCopy(t *testing.T) {
	ClearProviderConfigs()
	t.Cleanup(ClearProviderConfigs)

	RegisterProviderConfig(ProviderConfig{
		Name:             "test",
		APIClientType:    "test-api",
		BaseURL:          "https://example.com",
		KeyEnvVars:       []string{"KEY1"},
		Headers:          map[string][]string{"X-Test": {"orig"}},
		ProviderSpecific: map[string]string{"k": "v"},
	})

	got, err := GetProviderConfig("test")
	if err != nil {
		t.Fatalf("GetProviderConfig err: %v", err)
	}

	// Mutate the returned copy
	got.Headers["X-Test"] = []string{"mutated"}
	got.Headers["X-New"] = []string{"injected"}
	got.KeyEnvVars[0] = "MUTATED"
	got.ProviderSpecific["k"] = "mutated"
	got.ProviderSpecific["new"] = "injected"

	// Verify registry is unaffected
	got2, _ := GetProviderConfig("test")
	if len(got2.Headers["X-Test"]) != 1 || got2.Headers["X-Test"][0] != "orig" {
		t.Fatalf("header mutated: %v", got2.Headers["X-Test"])
	}
	if _, ok := got2.Headers["X-New"]; ok {
		t.Fatal("header injected")
	}
	if got2.KeyEnvVars[0] != "KEY1" {
		t.Fatalf("keyEnvVars mutated: %v", got2.KeyEnvVars)
	}
	if got2.ProviderSpecific["k"] != "v" {
		t.Fatalf("providerSpecific mutated: %v", got2.ProviderSpecific)
	}
	if _, ok := got2.ProviderSpecific["new"]; ok {
		t.Fatal("providerSpecific injected")
	}
}

func TestProviderConfigRegistryConcurrent(t *testing.T) {
	ClearProviderConfigs()
	t.Cleanup(ClearProviderConfigs)

	const goroutines = 20
	const ops = 500
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				name := fmt.Sprintf("p-%d", j%10)
				switch j % 5 {
				case 0:
					RegisterProviderConfig(ProviderConfig{Name: name, APIClientType: "t", BaseURL: "u"})
				case 1:
					_, _ = GetProviderConfig(name)
				case 2:
					_ = ListProviderConfigs()
				case 3:
					UnregisterProviderConfig(name)
				case 4:
					ClearProviderConfigs()
				}
			}
		}(i)
	}
	wg.Wait()
}
