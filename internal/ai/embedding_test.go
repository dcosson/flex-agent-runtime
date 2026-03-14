package ai

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

type mockEmbeddingProvider struct {
	api string
	fn  func(ctx context.Context, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error)
}

func (m *mockEmbeddingProvider) API() string { return m.api }

func (m *mockEmbeddingProvider) Embed(ctx context.Context, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
	if m.fn != nil {
		return m.fn(ctx, model, req)
	}
	out := make([]Embedding, len(req.Texts))
	for i := range req.Texts {
		out[i] = Embedding{Index: i, Values: []float32{float32(i + 1)}}
	}
	return &EmbeddingResponse{Embeddings: out, Usage: EmbeddingUsage{Tokens: len(req.Texts)}}, nil
}

func TestEmbeddingConstants(t *testing.T) {
	tasks := []EmbeddingTaskType{
		EmbeddingTaskQuery,
		EmbeddingTaskDocument,
		EmbeddingTaskClassification,
		EmbeddingTaskClustering,
		EmbeddingTaskSimilarity,
		EmbeddingTaskUnspecified,
	}
	if len(tasks) != 6 {
		t.Fatalf("unexpected task type count: %d", len(tasks))
	}
	encs := []EmbeddingEncoding{
		EmbeddingEncodingFloat,
		EmbeddingEncodingBase64,
		EmbeddingEncodingInt8,
		EmbeddingEncodingUint8,
		EmbeddingEncodingBinary,
		EmbeddingEncodingUBinary,
	}
	if len(encs) != 6 {
		t.Fatalf("unexpected encoding count: %d", len(encs))
	}
}

func TestEmbeddingProviderRegistryBasic(t *testing.T) {
	withIsolatedEmbeddingProviders(t)
	p := &mockEmbeddingProvider{api: "openai-embeddings"}
	RegisterEmbeddingProvider(p, "src")
	got, err := GetEmbeddingProvider("openai-embeddings")
	if err != nil {
		t.Fatalf("GetEmbeddingProvider err: %v", err)
	}
	if got.API() != p.api {
		t.Fatalf("unexpected provider api: %q", got.API())
	}
	UnregisterEmbeddingProviders("src")
	if _, err := GetEmbeddingProvider("openai-embeddings"); err == nil {
		t.Fatal("expected missing embedding provider after unregister")
	}
}

func TestEmbeddingProviderRegistryConcurrent(t *testing.T) {
	withIsolatedEmbeddingProviders(t)
	const goroutines = 20
	const ops = 500
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				api := fmt.Sprintf("api-%d", j%7)
				switch j % 4 {
				case 0:
					RegisterEmbeddingProvider(&mockEmbeddingProvider{api: api}, "src")
				case 1:
					_, _ = GetEmbeddingProvider(api)
				case 2:
					UnregisterEmbeddingProviders("src")
				case 3:
					ClearEmbeddingProviders()
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestGetEmbeddingProviderError(t *testing.T) {
	withIsolatedEmbeddingProviders(t)
	_, err := GetEmbeddingProvider("missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got == "" {
		t.Fatal("expected non-empty error message")
	}
}

func withIsolatedEmbeddingProviders(t *testing.T) {
	t.Helper()
	ClearEmbeddingProviders()
	t.Cleanup(ClearEmbeddingProviders)
}

func withIsolatedEmbeddingModels(t *testing.T) {
	t.Helper()
	orig := ListEmbeddingModels()
	ClearEmbeddingModels()
	t.Cleanup(func() {
		ClearEmbeddingModels()
		for _, m := range orig {
			RegisterEmbeddingModel(m)
		}
	})
}
