package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
)

func TestEmbeddingProvider_APIAndRegister(t *testing.T) {
	ai.ClearEmbeddingProviders()
	t.Cleanup(ai.ClearEmbeddingProviders)

	p := RegisterEmbedding(Config{APIKey: "k"}, "src")
	if p.API() != "openai-embeddings" {
		t.Fatalf("api=%q", p.API())
	}
	got, err := ai.GetEmbeddingProvider("openai-embeddings")
	if err != nil {
		t.Fatalf("GetEmbeddingProvider err: %v", err)
	}
	if got.API() != p.API() {
		t.Fatalf("registered provider mismatch")
	}
}

func TestEmbeddingProvider_EmbedSingleSuccess(t *testing.T) {
	var seenAuth string
	var seenReq embeddingRequestWire
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		seenAuth = r.Header.Get("authorization")
		if err := json.NewDecoder(r.Body).Decode(&seenReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(embeddingResponseWire{
			Data: []embeddingVectorWire{
				{Index: 0, Embedding: []float32{0.1, 0.2}},
				{Index: 1, Embedding: []float32{0.3, 0.4}},
			},
			Model: "text-embedding-3-small",
			Usage: embeddingUsageWire{TotalTokens: 12},
		})
	}))
	defer srv.Close()

	p := NewEmbedding(Config{APIKey: "secret", BaseURL: srv.URL})
	model := ai.EmbeddingModel{ID: "text-embedding-3-small", MaxBatchSize: 100}
	resp, err := p.Embed(context.Background(), model, ai.EmbeddingRequest{
		Texts:      []string{"hello", "world"},
		Dimensions: 256,
		Encoding:   ai.EmbeddingEncodingBase64,
		TaskType:   ai.EmbeddingTaskQuery, // ignored by OpenAI adapter
	})
	if err != nil {
		t.Fatalf("Embed err: %v", err)
	}
	if seenAuth != "Bearer secret" {
		t.Fatalf("authorization=%q", seenAuth)
	}
	if seenReq.Model != "text-embedding-3-small" {
		t.Fatalf("model=%q", seenReq.Model)
	}
	if seenReq.Dimensions == nil || *seenReq.Dimensions != 256 {
		t.Fatalf("dimensions not propagated")
	}
	if seenReq.EncodingFormat == nil || *seenReq.EncodingFormat != "base64" {
		t.Fatalf("encoding_format not propagated")
	}
	if !reflect.DeepEqual(seenReq.Input, []string{"hello", "world"}) {
		t.Fatalf("input mismatch: %v", seenReq.Input)
	}
	if resp.Usage.Tokens != 12 {
		t.Fatalf("tokens=%d", resp.Usage.Tokens)
	}
	if len(resp.Embeddings) != 2 || resp.Embeddings[1].Index != 1 {
		t.Fatalf("embeddings mismatch: %+v", resp.Embeddings)
	}
}

func TestEmbeddingProvider_ProviderBaseURL(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_ = json.NewEncoder(w).Encode(embeddingResponseWire{Data: []embeddingVectorWire{{Index: 0, Embedding: []float32{1}}}})
	}))
	defer srv.Close()

	p := NewEmbedding(Config{BaseURL: srv.URL})
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}
	if _, err := p.Embed(context.Background(), model, ai.EmbeddingRequest{Texts: []string{"x"}}); err != nil {
		t.Fatalf("Embed err: %v", err)
	}
	if !hit {
		t.Fatal("expected provider baseURL to be used")
	}
}

func TestEmbeddingProvider_HTTPErrorClassification(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   ai.ProviderErrorCode
		msg    string
	}{
		{name: "rate-limit", status: 429, code: ai.ErrRateLimit, msg: "too many"},
		{name: "auth", status: 401, code: ai.ErrAuth, msg: "bad auth"},
		{name: "server", status: 500, code: ai.ErrServerError, msg: "boom"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": tc.msg}})
			}))
			defer srv.Close()

			p := NewEmbedding(Config{BaseURL: srv.URL})
			_, err := p.Embed(context.Background(), ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}, ai.EmbeddingRequest{Texts: []string{"x"}})
			if err == nil {
				t.Fatal("expected error")
			}
			var pErr *ai.ProviderError
			if !errors.As(err, &pErr) {
				t.Fatalf("expected ProviderError, got %T", err)
			}
			if pErr.Code != tc.code {
				t.Fatalf("code=%q want=%q", pErr.Code, tc.code)
			}
		})
	}
}

func TestEmbeddingProvider_BatchSplitAndProgress(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embeddingRequestWire
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode req: %v", err)
		}
		call := int(calls.Add(1))
		vectors := make([]embeddingVectorWire, len(req.Input))
		for i := range req.Input {
			vectors[i] = embeddingVectorWire{Index: i, Embedding: []float32{float32(call), float32(i)}}
		}
		_ = json.NewEncoder(w).Encode(embeddingResponseWire{Data: vectors, Usage: embeddingUsageWire{TotalTokens: len(req.Input)}})
	}))
	defer srv.Close()

	progress := make([][2]int, 0)
	p := NewEmbedding(Config{BaseURL: srv.URL})
	resp, err := p.Embed(context.Background(), ai.EmbeddingModel{ID: "m", MaxBatchSize: 2}, ai.EmbeddingRequest{
		Texts: []string{"a", "b", "c", "d", "e"},
		OnProgress: func(done, total int) {
			progress = append(progress, [2]int{done, total})
		},
	})
	if err != nil {
		t.Fatalf("Embed err: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls=%d want=3", calls.Load())
	}
	if len(resp.Embeddings) != 5 {
		t.Fatalf("embeddings=%d", len(resp.Embeddings))
	}
	for i := 0; i < 5; i++ {
		if resp.Embeddings[i].Index != i {
			t.Fatalf("index[%d]=%d", i, resp.Embeddings[i].Index)
		}
	}
	wantProgress := [][2]int{{2, 5}, {4, 5}, {5, 5}}
	if !reflect.DeepEqual(progress, wantProgress) {
		t.Fatalf("progress=%v want=%v", progress, wantProgress)
	}
}
