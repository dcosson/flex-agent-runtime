package google

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"h2-agent-runtime/internal/ai"
)

func TestEmbeddingProvider_APIAndRegister(t *testing.T) {
	ai.ClearEmbeddingProviders()
	t.Cleanup(ai.ClearEmbeddingProviders)

	p := RegisterEmbedding(Config{APIKey: "k"}, "src")
	if p.API() != "google-embeddings" {
		t.Fatalf("api=%q", p.API())
	}
	got, err := ai.GetEmbeddingProvider("google-embeddings")
	if err != nil {
		t.Fatalf("GetEmbeddingProvider err: %v", err)
	}
	if got.API() != p.API() {
		t.Fatalf("registered provider mismatch")
	}
}

func TestEmbeddingProvider_EmbedSingleSuccess(t *testing.T) {
	var seenReq batchEmbedContentsRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-embedding-001:batchEmbedContents" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		if r.URL.Query().Get("key") != "secret" {
			t.Fatalf("key=%q", r.URL.Query().Get("key"))
		}
		if err := json.NewDecoder(r.Body).Decode(&seenReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(batchEmbedContentsResponse{
			Embeddings: []contentEmbedding{
				{Values: []float32{0.1, 0.2}},
				{Values: []float32{0.3, 0.4}},
			},
		})
	}))
	defer srv.Close()

	p := NewEmbedding(Config{BaseURL: srv.URL, APIKey: "secret"})
	model := ai.EmbeddingModel{ID: "gemini-embedding-001", BaseURL: srv.URL, MaxBatchSize: 100}
	resp, err := p.Embed(context.Background(), model, ai.EmbeddingRequest{
		Texts:      []string{"hello", "world"},
		Dimensions: 256,
		TaskType:   ai.EmbeddingTaskQuery,
	})
	if err != nil {
		t.Fatalf("Embed err: %v", err)
	}

	// Verify request structure
	if len(seenReq.Requests) != 2 {
		t.Fatalf("requests=%d", len(seenReq.Requests))
	}
	if seenReq.Requests[0].Model != "models/gemini-embedding-001" {
		t.Fatalf("model=%q", seenReq.Requests[0].Model)
	}
	if seenReq.Requests[0].TaskType != "RETRIEVAL_QUERY" {
		t.Fatalf("taskType=%q", seenReq.Requests[0].TaskType)
	}
	if seenReq.Requests[0].OutputDimensionality == nil || *seenReq.Requests[0].OutputDimensionality != 256 {
		t.Fatalf("outputDimensionality not propagated")
	}
	if len(seenReq.Requests[0].Content.Parts) != 1 || seenReq.Requests[0].Content.Parts[0].Text != "hello" {
		t.Fatalf("content mismatch: %+v", seenReq.Requests[0].Content)
	}

	// Verify response
	if len(resp.Embeddings) != 2 {
		t.Fatalf("embeddings=%d", len(resp.Embeddings))
	}
	if resp.Embeddings[0].Index != 0 || resp.Embeddings[1].Index != 1 {
		t.Fatalf("indices: %d, %d", resp.Embeddings[0].Index, resp.Embeddings[1].Index)
	}
	if !reflect.DeepEqual(resp.Embeddings[0].Values, []float32{0.1, 0.2}) {
		t.Fatalf("values[0] mismatch: %v", resp.Embeddings[0].Values)
	}
	if resp.Model != "gemini-embedding-001" {
		t.Fatalf("model=%q", resp.Model)
	}
	// Usage estimated from input text: "hello"=(5+3)/4=2 + "world"=(5+3)/4=2 = 4 tokens
	if resp.Usage.Tokens != 4 {
		t.Fatalf("estimated tokens=%d want=4", resp.Usage.Tokens)
	}
}

func TestEmbeddingProvider_TaskTypeMapping(t *testing.T) {
	tests := []struct {
		taskType ai.EmbeddingTaskType
		wantWire string
	}{
		{ai.EmbeddingTaskQuery, "RETRIEVAL_QUERY"},
		{ai.EmbeddingTaskDocument, "RETRIEVAL_DOCUMENT"},
		{ai.EmbeddingTaskClassification, "CLASSIFICATION"},
		{ai.EmbeddingTaskClustering, "CLUSTERING"},
		{ai.EmbeddingTaskSimilarity, "SEMANTIC_SIMILARITY"},
		{ai.EmbeddingTaskUnspecified, ""},
	}
	for _, tc := range tests {
		t.Run(string(tc.taskType), func(t *testing.T) {
			var seenReq batchEmbedContentsRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&seenReq)
				_ = json.NewEncoder(w).Encode(batchEmbedContentsResponse{
					Embeddings: []contentEmbedding{{Values: []float32{1}}},
				})
			}))
			defer srv.Close()

			p := NewEmbedding(Config{BaseURL: srv.URL})
			model := ai.EmbeddingModel{ID: "m", BaseURL: srv.URL, MaxBatchSize: 10}
			_, err := p.Embed(context.Background(), model, ai.EmbeddingRequest{
				Texts:    []string{"x"},
				TaskType: tc.taskType,
			})
			if err != nil {
				t.Fatalf("Embed err: %v", err)
			}
			if seenReq.Requests[0].TaskType != tc.wantWire {
				t.Fatalf("taskType=%q want=%q", seenReq.Requests[0].TaskType, tc.wantWire)
			}
		})
	}
}

func TestEmbeddingProvider_ModelBaseURLOverride(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_ = json.NewEncoder(w).Encode(batchEmbedContentsResponse{
			Embeddings: []contentEmbedding{{Values: []float32{1}}},
		})
	}))
	defer srv.Close()

	p := NewEmbedding(Config{BaseURL: "http://127.0.0.1:1"}) // invalid unless model override is used
	model := ai.EmbeddingModel{ID: "m", BaseURL: srv.URL, MaxBatchSize: 10}
	if _, err := p.Embed(context.Background(), model, ai.EmbeddingRequest{Texts: []string{"x"}}); err != nil {
		t.Fatalf("Embed err: %v", err)
	}
	if !hit {
		t.Fatal("expected model baseURL override to be used")
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
		{name: "forbidden", status: 403, code: ai.ErrAuth, msg: "forbidden"},
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
			_, err := p.Embed(context.Background(), ai.EmbeddingModel{ID: "m", BaseURL: srv.URL, MaxBatchSize: 10}, ai.EmbeddingRequest{Texts: []string{"x"}})
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
		var req batchEmbedContentsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode req: %v", err)
		}
		call := int(calls.Add(1))
		embeddings := make([]contentEmbedding, len(req.Requests))
		for i := range req.Requests {
			embeddings[i] = contentEmbedding{Values: []float32{float32(call), float32(i)}}
		}
		_ = json.NewEncoder(w).Encode(batchEmbedContentsResponse{Embeddings: embeddings})
	}))
	defer srv.Close()

	progress := make([][2]int, 0)
	p := NewEmbedding(Config{BaseURL: srv.URL})
	resp, err := p.Embed(context.Background(), ai.EmbeddingModel{ID: "m", BaseURL: srv.URL, MaxBatchSize: 2}, ai.EmbeddingRequest{
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

func TestEmbeddingProvider_NoDimensionsWhenZero(t *testing.T) {
	var seenReq batchEmbedContentsRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seenReq)
		_ = json.NewEncoder(w).Encode(batchEmbedContentsResponse{
			Embeddings: []contentEmbedding{{Values: []float32{1}}},
		})
	}))
	defer srv.Close()

	p := NewEmbedding(Config{BaseURL: srv.URL})
	model := ai.EmbeddingModel{ID: "m", BaseURL: srv.URL, MaxBatchSize: 10}
	_, err := p.Embed(context.Background(), model, ai.EmbeddingRequest{Texts: []string{"x"}})
	if err != nil {
		t.Fatalf("Embed err: %v", err)
	}
	if seenReq.Requests[0].OutputDimensionality != nil {
		t.Fatal("expected nil outputDimensionality when Dimensions=0")
	}
}
