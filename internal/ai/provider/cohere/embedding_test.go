package cohere

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
	return ai.ProviderEndpoint{ProviderName: "cohere", BaseURL: baseURL, APIKey: apiKey}
}

func TestEmbeddingClient_ClientType(t *testing.T) {
	c := NewEmbeddingClient(ClientConfig{})
	if c.ClientType() != "cohere-embeddings" {
		t.Fatalf("clientType=%q", c.ClientType())
	}
}

func TestEmbeddingClient_RegisterEmbeddingClient(t *testing.T) {
	ai.ClearEmbeddingAPIClients()
	t.Cleanup(ai.ClearEmbeddingAPIClients)

	c := RegisterEmbeddingClient(ClientConfig{})
	got, err := ai.GetEmbeddingAPIClient("cohere-embeddings")
	if err != nil {
		t.Fatalf("GetEmbeddingAPIClient err: %v", err)
	}
	if got.ClientType() != c.ClientType() {
		t.Fatalf("registered client mismatch")
	}
}

func TestEmbeddingClient_EmbedSingleSuccess(t *testing.T) {
	var seenAuth string
	var seenReq embedRequestWire
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		seenAuth = r.Header.Get("authorization")
		if err := json.NewDecoder(r.Body).Decode(&seenReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(embedResponseWire{
			Embeddings: embeddingsContainer{
				Float: [][]float32{{0.1, 0.2}, {0.3, 0.4}},
			},
			Meta: embedMeta{BilledUnits: billedUnits{InputTokens: 15}},
		})
	}))
	defer srv.Close()

	c := NewEmbeddingClient(ClientConfig{})
	ep := testEmbeddingEndpoint(srv.URL, "secret")
	model := ai.EmbeddingModel{ID: "embed-v3.5", MaxBatchSize: 96}
	resp, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{
		Texts:    []string{"hello", "world"},
		TaskType: ai.EmbeddingTaskQuery,
	})
	if err != nil {
		t.Fatalf("Embed err: %v", err)
	}
	if seenAuth != "Bearer secret" {
		t.Fatalf("authorization=%q", seenAuth)
	}
	if seenReq.Model != "embed-v3.5" {
		t.Fatalf("model=%q", seenReq.Model)
	}
	if seenReq.InputType != "search_query" {
		t.Fatalf("input_type=%q", seenReq.InputType)
	}
	if !reflect.DeepEqual(seenReq.Texts, []string{"hello", "world"}) {
		t.Fatalf("texts mismatch: %v", seenReq.Texts)
	}
	if resp.Usage.Tokens != 15 {
		t.Fatalf("tokens=%d", resp.Usage.Tokens)
	}
	if len(resp.Embeddings) != 2 || resp.Embeddings[1].Index != 1 {
		t.Fatalf("embeddings mismatch: %+v", resp.Embeddings)
	}
}

func TestEmbeddingClient_InputTypeMapping(t *testing.T) {
	tests := []struct {
		taskType  ai.EmbeddingTaskType
		wantInput string
	}{
		{ai.EmbeddingTaskQuery, "search_query"},
		{ai.EmbeddingTaskDocument, "search_document"},
		{ai.EmbeddingTaskClassification, "classification"},
		{ai.EmbeddingTaskClustering, "clustering"},
		{ai.EmbeddingTaskSimilarity, "search_document"},
		{ai.EmbeddingTaskUnspecified, "search_document"},
	}
	for _, tc := range tests {
		t.Run(string(tc.taskType), func(t *testing.T) {
			var seenReq embedRequestWire
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&seenReq)
				_ = json.NewEncoder(w).Encode(embedResponseWire{
					Embeddings: embeddingsContainer{Float: [][]float32{{1}}},
				})
			}))
			defer srv.Close()

			c := NewEmbeddingClient(ClientConfig{})
			ep := testEmbeddingEndpoint(srv.URL, "")
			model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}
			_, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{
				Texts:    []string{"x"},
				TaskType: tc.taskType,
			})
			if err != nil {
				t.Fatalf("Embed err: %v", err)
			}
			if seenReq.InputType != tc.wantInput {
				t.Fatalf("input_type=%q want=%q", seenReq.InputType, tc.wantInput)
			}
		})
	}
}

func TestEmbeddingClient_QuantizedEncoding(t *testing.T) {
	tests := []struct {
		name     string
		encoding ai.EmbeddingEncoding
		resp     embedResponseWire
		wantRaw  any
	}{
		{
			name:     "int8",
			encoding: ai.EmbeddingEncodingInt8,
			resp: embedResponseWire{
				Embeddings: embeddingsContainer{Int8: [][]int8{{1, -2, 3}}},
			},
			wantRaw: []int8{1, -2, 3},
		},
		{
			name:     "uint8",
			encoding: ai.EmbeddingEncodingUint8,
			resp: embedResponseWire{
				Embeddings: embeddingsContainer{Uint8: [][]uint8{{10, 20, 30}}},
			},
			wantRaw: []uint8{10, 20, 30},
		},
		{
			name:     "binary",
			encoding: ai.EmbeddingEncodingBinary,
			resp: embedResponseWire{
				Embeddings: embeddingsContainer{Binary: [][]int8{{-1, 0, 1}}},
			},
			wantRaw: []int8{-1, 0, 1},
		},
		{
			name:     "ubinary",
			encoding: ai.EmbeddingEncodingUBinary,
			resp: embedResponseWire{
				Embeddings: embeddingsContainer{Ubinary: [][]uint8{{0, 1, 255}}},
			},
			wantRaw: []uint8{0, 1, 255},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req embedRequestWire
				_ = json.NewDecoder(r.Body).Decode(&req)
				if len(req.EmbeddingTypes) != 1 || req.EmbeddingTypes[0] != string(tc.encoding) {
					t.Fatalf("embedding_types=%v want=[%q]", req.EmbeddingTypes, tc.encoding)
				}
				_ = json.NewEncoder(w).Encode(tc.resp)
			}))
			defer srv.Close()

			c := NewEmbeddingClient(ClientConfig{})
			ep := testEmbeddingEndpoint(srv.URL, "")
			model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}
			resp, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{
				Texts:    []string{"x"},
				Encoding: tc.encoding,
			})
			if err != nil {
				t.Fatalf("Embed err: %v", err)
			}
			if len(resp.Embeddings) != 1 {
				t.Fatalf("embeddings=%d", len(resp.Embeddings))
			}
			if resp.Embeddings[0].Raw == nil {
				t.Fatal("expected Raw to be set for quantized encoding")
			}
			if !reflect.DeepEqual(resp.Embeddings[0].Raw, tc.wantRaw) {
				t.Fatalf("raw=%v want=%v", resp.Embeddings[0].Raw, tc.wantRaw)
			}
		})
	}
}

func TestEmbeddingClient_DimensionsAndDefaultEncoding(t *testing.T) {
	var seenReq embedRequestWire
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seenReq)
		_ = json.NewEncoder(w).Encode(embedResponseWire{
			Embeddings: embeddingsContainer{Float: [][]float32{{1}}},
		})
	}))
	defer srv.Close()

	c := NewEmbeddingClient(ClientConfig{})
	ep := testEmbeddingEndpoint(srv.URL, "")
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}
	_, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{
		Texts:      []string{"x"},
		Dimensions: 512,
	})
	if err != nil {
		t.Fatalf("Embed err: %v", err)
	}
	if seenReq.OutputDimension == nil || *seenReq.OutputDimension != 512 {
		t.Fatal("output_dimension not propagated")
	}
	if seenReq.EmbeddingTypes != nil {
		t.Fatalf("expected nil embedding_types for float, got %v", seenReq.EmbeddingTypes)
	}
}

func TestEmbeddingClient_ProviderBaseURL(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_ = json.NewEncoder(w).Encode(embedResponseWire{
			Embeddings: embeddingsContainer{Float: [][]float32{{1}}},
		})
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
		{name: "forbidden", status: 403, code: ai.ErrAuth, msg: "forbidden"},
		{name: "server", status: 500, code: ai.ErrServerError, msg: "boom"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(cohereErrorResponse{Message: tc.msg})
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
		var req embedRequestWire
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode req: %v", err)
		}
		call := int(calls.Add(1))
		vecs := make([][]float32, len(req.Texts))
		for i := range req.Texts {
			vecs[i] = []float32{float32(call), float32(i)}
		}
		_ = json.NewEncoder(w).Encode(embedResponseWire{
			Embeddings: embeddingsContainer{Float: vecs},
			Meta:       embedMeta{BilledUnits: billedUnits{InputTokens: len(req.Texts)}},
		})
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
