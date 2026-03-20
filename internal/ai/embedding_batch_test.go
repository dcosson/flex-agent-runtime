package ai

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestBatchEmbedNoSplit(t *testing.T) {
	calls := 0
	model := EmbeddingModel{ID: "m", MaxBatchSize: 10}
	ep := ProviderEndpoint{ProviderName: "test"}
	resp, err := BatchEmbed(context.Background(), func(ctx context.Context, _ ProviderEndpoint, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
		calls++
		return &EmbeddingResponse{
			Embeddings: []Embedding{{Index: 0, Values: []float32{1}}},
			Usage:      EmbeddingUsage{Tokens: 7, Cost: 0.1},
		}, nil
	}, ep, model, EmbeddingRequest{Texts: []string{"a"}})
	if err != nil {
		t.Fatalf("BatchEmbed err: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d want=1", calls)
	}
	if resp.Usage.Tokens != 7 {
		t.Fatalf("tokens=%d want=7", resp.Usage.Tokens)
	}
}

func TestBatchEmbedSplitOrderProgress(t *testing.T) {
	model := EmbeddingModel{ID: "m", MaxBatchSize: 2}
	ep := ProviderEndpoint{ProviderName: "test"}
	texts := []string{"t0", "t1", "t2", "t3", "t4"}
	progress := make([][2]int, 0)

	resp, err := BatchEmbed(context.Background(), func(ctx context.Context, _ ProviderEndpoint, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
		emb := make([]Embedding, len(req.Texts))
		for i := range req.Texts {
			emb[i] = Embedding{Index: i, Values: []float32{float32(len(req.Texts)), float32(i)}}
		}
		return &EmbeddingResponse{Embeddings: emb, Usage: EmbeddingUsage{Tokens: len(req.Texts)}}, nil
	}, ep, model, EmbeddingRequest{Texts: texts, OnProgress: func(completed, total int) {
		progress = append(progress, [2]int{completed, total})
	}})
	if err != nil {
		t.Fatalf("BatchEmbed err: %v", err)
	}
	if len(resp.Embeddings) != len(texts) {
		t.Fatalf("embeddings=%d want=%d", len(resp.Embeddings), len(texts))
	}
	for i := range texts {
		if resp.Embeddings[i].Index != i {
			t.Fatalf("index[%d]=%d", i, resp.Embeddings[i].Index)
		}
	}
	if resp.Usage.Tokens != len(texts) {
		t.Fatalf("tokens=%d want=%d", resp.Usage.Tokens, len(texts))
	}
	wantProgress := [][2]int{{2, 5}, {4, 5}, {5, 5}}
	if !reflect.DeepEqual(progress, wantProgress) {
		t.Fatalf("progress=%v want=%v", progress, wantProgress)
	}
}

func TestBatchEmbedPartialResultsOnError(t *testing.T) {
	baseErr := &ProviderError{Code: ErrRateLimit, Provider: "mock", Message: "slow down"}
	model := EmbeddingModel{ID: "m", MaxBatchSize: 2}
	ep := ProviderEndpoint{ProviderName: "test"}
	resp, err := BatchEmbed(context.Background(), func(ctx context.Context, _ ProviderEndpoint, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
		if len(req.Texts) == 1 {
			return nil, baseErr
		}
		return &EmbeddingResponse{
			Embeddings: []Embedding{{Index: 0, Values: []float32{1}}, {Index: 1, Values: []float32{2}}},
			Usage:      EmbeddingUsage{Tokens: 2},
		}, nil
	}, ep, model, EmbeddingRequest{Texts: []string{"a", "b", "c"}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, baseErr) {
		t.Fatalf("expected wrapped provider error, got %v", err)
	}
	if resp == nil {
		t.Fatal("expected partial response")
	}
	if len(resp.Embeddings) != 2 {
		t.Fatalf("partial embeddings=%d want=2", len(resp.Embeddings))
	}
}
