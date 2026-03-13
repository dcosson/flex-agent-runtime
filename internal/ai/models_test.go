package ai

import (
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
		Headers:  map[string]string{"X-Key": "original"},
		Input:    []string{"text"},
		Compat:   &ModelCompat{ReasoningEffortMap: map[string]string{"high": "h"}},
	})

	m, err := GetModel("test", "test-model")
	if err != nil {
		t.Fatalf("GetModel err: %v", err)
	}
	m.Headers["X-Key"] = "mutated"
	m.Headers["X-New"] = "injected"
	m.Input[0] = "corrupted"
	m.Compat.ReasoningEffortMap["high"] = "corrupted"

	m2, err := GetModel("test", "test-model")
	if err != nil {
		t.Fatalf("GetModel err: %v", err)
	}
	if m2.Headers["X-Key"] != "original" {
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
	s := BuildBaseOptions(model, nil, "k")
	if s.MaxTokens == nil || *s.MaxTokens != 32000 {
		t.Fatalf("default max tokens mismatch")
	}
	if s.APIKey != "k" {
		t.Fatalf("apikey mismatch")
	}

	if ClampReasoning(ThinkingXHigh) != ThinkingHigh {
		t.Fatalf("xhigh should clamp to high")
	}
	max, budget := AdjustMaxTokensForThinking(2000, 3000, ThinkingHigh, nil)
	if max != 3000 || budget != 1976 {
		t.Fatalf("unexpected adjust result: max=%d budget=%d", max, budget)
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
