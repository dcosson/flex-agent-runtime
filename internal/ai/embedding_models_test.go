package ai

import (
	"sync"
	"testing"
)

func TestEmbeddingCatalogLoaded(t *testing.T) {
	for _, id := range []string{
		"text-embedding-3-small",
		"text-embedding-3-large",
		"gemini-embedding-001",
		"embed-v4.0",
	} {
		m, ok := GetEmbeddingModel(id)
		if !ok {
			t.Fatalf("catalog model %q missing", id)
		}
		if m.ID != id {
			t.Fatalf("model id mismatch: got=%q want=%q", m.ID, id)
		}
		if m.MaxBatchSize <= 0 {
			t.Fatalf("max batch size must be > 0 for %s", id)
		}
	}
}

func TestEmbeddingModelRegistryMutationIsolation(t *testing.T) {
	withIsolatedEmbeddingModels(t)
	RegisterEmbeddingModel(EmbeddingModel{
		ID:       "m1",
		Provider: "openai",
		API:      "openai-embeddings",
		Headers:  map[string][]string{"X-Test": {"orig"}},
	})
	m, ok := GetEmbeddingModel("m1")
	if !ok {
		t.Fatal("missing registered model")
	}
	m.Headers["X-Test"] = []string{"mutated"}
	m.Headers["X-New"] = []string{"injected"}
	m2, ok := GetEmbeddingModel("m1")
	if !ok {
		t.Fatal("missing registered model")
	}
	if len(m2.Headers["X-Test"]) != 1 || m2.Headers["X-Test"][0] != "orig" {
		t.Fatalf("header mutated in registry: %v", m2.Headers["X-Test"])
	}
	if _, ok := m2.Headers["X-New"]; ok {
		t.Fatalf("unexpected injected header")
	}
}

func TestEmbeddingModelListByProvider(t *testing.T) {
	withIsolatedEmbeddingModels(t)
	RegisterEmbeddingModel(EmbeddingModel{ID: "a", Provider: "p1"})
	RegisterEmbeddingModel(EmbeddingModel{ID: "b", Provider: "p2"})
	RegisterEmbeddingModel(EmbeddingModel{ID: "c", Provider: "p1"})

	all := ListEmbeddingModels()
	if len(all) != 3 {
		t.Fatalf("expected 3 models, got %d", len(all))
	}
	byP1 := ListEmbeddingModelsByProvider("p1")
	if len(byP1) != 2 {
		t.Fatalf("expected 2 p1 models, got %d", len(byP1))
	}
	if byP1[0].ID != "a" || byP1[1].ID != "c" {
		t.Fatalf("expected sorted ids [a c], got [%s %s]", byP1[0].ID, byP1[1].ID)
	}
}

func TestEmbeddingModelRegistryConcurrent(t *testing.T) {
	withIsolatedEmbeddingModels(t)
	const goroutines = 20
	const ops = 400
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				RegisterEmbeddingModel(EmbeddingModel{ID: string(rune('a' + rune(j%20))), Provider: "p", API: "x"})
				_, _ = GetEmbeddingModel("a")
				_ = ListEmbeddingModels()
				_ = ListEmbeddingModelsByProvider("p")
			}
		}(i)
	}
	wg.Wait()
}
