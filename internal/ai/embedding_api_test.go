package ai

import (
	"context"
	"errors"
	"testing"
)

func TestEmbedValidation(t *testing.T) {
	withIsolatedEmbeddingClients(t)
	withIsolatedProviderConfigs(t)
	withIsolatedEmbeddingModels(t)

	if _, err := Embed(context.Background(), "m", EmbeddingRequest{}); err == nil {
		t.Fatal("expected empty texts validation error")
	}

	if _, err := Embed(context.Background(), "missing", EmbeddingRequest{Texts: []string{"x"}}); err == nil {
		t.Fatal("expected unknown model error")
	}

	m := EmbeddingModel{ID: "m", API: "mock", Provider: "mock", SupportsDimCtrl: false, MaxDims: 1024, MinDims: 64}
	RegisterEmbeddingModel(m)
	registerMockEmbeddingProvider(t, "mock", "mock", nil)

	if _, err := Embed(context.Background(), "m", EmbeddingRequest{Texts: []string{"x"}, Dimensions: 128}); err == nil {
		t.Fatal("expected unsupported dimension control error")
	}

	ClearEmbeddingModels()
	RegisterEmbeddingModel(EmbeddingModel{ID: "m2", API: "mock", Provider: "mock", SupportsDimCtrl: true, MinDims: 64, MaxDims: 256})

	if _, err := Embed(context.Background(), "m2", EmbeddingRequest{Texts: []string{"x"}, Dimensions: 32}); err == nil {
		t.Fatal("expected min dims validation error")
	}
	if _, err := Embed(context.Background(), "m2", EmbeddingRequest{Texts: []string{"x"}, Dimensions: 512}); err == nil {
		t.Fatal("expected max dims validation error")
	}
}

func TestEmbedProviderLookupError(t *testing.T) {
	withIsolatedEmbeddingClients(t)
	withIsolatedProviderConfigs(t)
	withIsolatedEmbeddingModels(t)

	RegisterEmbeddingModel(EmbeddingModel{ID: "m", API: "missing", Provider: "x"})
	_, err := Embed(context.Background(), "m", EmbeddingRequest{Texts: []string{"x"}})
	if err == nil {
		t.Fatal("expected provider lookup error")
	}
}

func TestEmbedCalculatesCostAndPreservesProviderError(t *testing.T) {
	withIsolatedEmbeddingClients(t)
	withIsolatedProviderConfigs(t)
	withIsolatedEmbeddingModels(t)

	RegisterEmbeddingModel(EmbeddingModel{ID: "m", API: "mock", Provider: "mock", Cost: EmbeddingCost{PerMTok: 0.5}})
	registerMockEmbeddingProvider(t, "mock", "mock", func(ctx context.Context, _ ProviderEndpoint, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
		return &EmbeddingResponse{
			Embeddings: []Embedding{{Index: 0, Values: []float32{1, 2}}},
			Usage:      EmbeddingUsage{Tokens: 2000},
		}, nil
	})

	resp, err := Embed(context.Background(), "m", EmbeddingRequest{Texts: []string{"hello"}})
	if err != nil {
		t.Fatalf("Embed err: %v", err)
	}
	if resp.Usage.Cost <= 0 {
		t.Fatalf("expected non-zero computed cost")
	}
	if resp.Model != "m" {
		t.Fatalf("model=%q want=m", resp.Model)
	}

	pErr := &ProviderError{Code: ErrRateLimit, Provider: "mock", Message: "retry"}
	ClearEmbeddingAPIClients()
	registerMockEmbeddingProvider(t, "mock", "mock", func(context.Context, ProviderEndpoint, EmbeddingModel, EmbeddingRequest) (*EmbeddingResponse, error) {
		return nil, pErr
	})
	_, err = Embed(context.Background(), "m", EmbeddingRequest{Texts: []string{"hello"}})
	if err == nil {
		t.Fatal("expected provider error")
	}
	if !errors.Is(err, pErr) {
		t.Fatalf("provider error type not preserved: %v", err)
	}
}

func TestEmbedReturnsProviderResponse(t *testing.T) {
	withIsolatedEmbeddingClients(t)
	withIsolatedProviderConfigs(t)
	withIsolatedEmbeddingModels(t)

	RegisterEmbeddingModel(EmbeddingModel{ID: "m", API: "mock", Provider: "mock"})
	registerMockEmbeddingProvider(t, "mock", "mock", func(ctx context.Context, _ ProviderEndpoint, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
		return &EmbeddingResponse{
			Model:      model.ID,
			Embeddings: []Embedding{{Index: 0, Values: []float32{3.14}}},
			Usage:      EmbeddingUsage{Tokens: 5, Cost: 1.23},
		}, nil
	})

	resp, err := Embed(context.Background(), "m", EmbeddingRequest{Texts: []string{"x"}})
	if err != nil {
		t.Fatalf("Embed err: %v", err)
	}
	if len(resp.Embeddings) != 1 {
		t.Fatalf("unexpected embedding count: %d", len(resp.Embeddings))
	}
	if resp.Usage.Cost != 1.23 {
		t.Fatalf("expected cost passthrough, got %f", resp.Usage.Cost)
	}
}

func withIsolatedProviderConfigs(t *testing.T) {
	t.Helper()
	ClearProviderConfigs()
	t.Cleanup(ClearProviderConfigs)
}
