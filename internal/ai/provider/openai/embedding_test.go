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

func testEmbeddingEndpoint(baseURL, apiKey string) ai.ProviderEndpoint {
	return ai.ProviderEndpoint{ProviderName: "openai", BaseURL: baseURL, APIKey: apiKey}
}

func TestEmbeddingClient_ClientType(t *testing.T) {
	c := NewEmbeddingClient(ClientConfig{})
	if c.ClientType() != "openai-embeddings" {
		t.Fatalf("clientType=%q", c.ClientType())
	}
}

func TestEmbeddingClient_RegisterEmbeddingClient(t *testing.T) {
	ai.ClearEmbeddingAPIClients()
	t.Cleanup(ai.ClearEmbeddingAPIClients)

	c := RegisterEmbeddingClient(ClientConfig{})
	got, err := ai.GetEmbeddingAPIClient("openai-embeddings")
	if err != nil {
		t.Fatalf("GetEmbeddingAPIClient err: %v", err)
	}
	if got.ClientType() != c.ClientType() {
		t.Fatalf("registered client mismatch")
	}
}

func TestEmbeddingClient_EmbedSingleSuccess(t *testing.T) {
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

	c := NewEmbeddingClient(ClientConfig{})
	ep := testEmbeddingEndpoint(srv.URL, "secret")
	model := ai.EmbeddingModel{ID: "text-embedding-3-small", MaxBatchSize: 100}
	resp, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{
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

func TestEmbeddingClient_ProviderBaseURL(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_ = json.NewEncoder(w).Encode(embeddingResponseWire{Data: []embeddingVectorWire{{Index: 0, Embedding: []float32{1}}}})
	}))
	defer srv.Close()

	c := NewEmbeddingClient(ClientConfig{})
	ep := testEmbeddingEndpoint(srv.URL, "")
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}
	if _, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{Texts: []string{"x"}}); err != nil {
		t.Fatalf("Embed err: %v", err)
	}
	if !hit {
		t.Fatal("expected provider baseURL to be used")
	}
}

func TestEmbeddingClient_HTTPErrorClassification(t *testing.T) {
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

			c := NewEmbeddingClient(ClientConfig{})
			ep := testEmbeddingEndpoint(srv.URL, "")
			_, err := c.Embed(context.Background(), ep, ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}, ai.EmbeddingRequest{Texts: []string{"x"}})
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

func TestEmbeddingClient_BatchSplitAndProgress(t *testing.T) {
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
	c := NewEmbeddingClient(ClientConfig{})
	ep := testEmbeddingEndpoint(srv.URL, "")
	resp, err := c.Embed(context.Background(), ep, ai.EmbeddingModel{ID: "m", MaxBatchSize: 2}, ai.EmbeddingRequest{
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

func TestEmbeddingClient_HeaderPropagation(t *testing.T) {
	var seenHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeaders = r.Header.Clone()
		_ = json.NewEncoder(w).Encode(embeddingResponseWire{
			Data: []embeddingVectorWire{{Index: 0, Embedding: []float32{1}}},
		})
	}))
	defer srv.Close()

	c := NewEmbeddingClient(ClientConfig{})
	ep := ai.ProviderEndpoint{
		ProviderName: "openai",
		BaseURL:      srv.URL,
		APIKey:       "test-key",
		Headers: map[string][]string{
			"X-Provider-Header": {"pval1", "pval2"},
		},
	}
	model := ai.EmbeddingModel{
		ID:           "m",
		MaxBatchSize: 10,
		Headers: map[string][]string{
			"X-Model-Header": {"mval"},
		},
	}
	_, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{Texts: []string{"x"}})
	if err != nil {
		t.Fatalf("Embed err: %v", err)
	}
	// Provider headers
	if vals := seenHeaders.Values("X-Provider-Header"); len(vals) != 2 || vals[0] != "pval1" || vals[1] != "pval2" {
		t.Fatalf("provider headers: %v", vals)
	}
	// Model headers
	if vals := seenHeaders.Values("X-Model-Header"); len(vals) != 1 || vals[0] != "mval" {
		t.Fatalf("model headers: %v", vals)
	}
	// Auth header still present
	if seenHeaders.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("auth header: %q", seenHeaders.Get("Authorization"))
	}
}
