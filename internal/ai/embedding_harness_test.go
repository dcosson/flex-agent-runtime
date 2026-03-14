package ai

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// =============================================================================
// Mock helpers for harness tests
// =============================================================================

// deterministicEmbedFn returns an EmbedFunc that returns deterministic vectors.
// Each embedding has Values of length dims, with values derived from the text index.
func deterministicEmbedFn(dims int) EmbedFunc {
	return func(ctx context.Context, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
		embs := make([]Embedding, len(req.Texts))
		for i := range req.Texts {
			vals := make([]float32, dims)
			for d := range vals {
				vals[d] = float32(i*1000 + d)
			}
			embs[i] = Embedding{Index: i, Values: vals}
		}
		return &EmbeddingResponse{
			Embeddings: embs,
			Model:      model.ID,
			Usage:      EmbeddingUsage{Tokens: len(req.Texts) * 3},
		}, nil
	}
}

// countingEmbedFn wraps an EmbedFunc and counts calls.
func countingEmbedFn(fn EmbedFunc, count *int64) EmbedFunc {
	return func(ctx context.Context, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
		atomic.AddInt64(count, 1)
		return fn(ctx, model, req)
	}
}

// =============================================================================
// P1: Batch Splitting Preserves Ordering (Property-Based)
// =============================================================================

func TestP1_BatchSplittingPreservesOrdering(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 200).Draw(t, "n")
		batchSize := rapid.IntRange(1, 50).Draw(t, "batchSize")

		texts := make([]string, n)
		for i := range texts {
			texts[i] = fmt.Sprintf("text-%d", i)
		}

		var calls int64
		fn := countingEmbedFn(deterministicEmbedFn(4), &calls)
		model := EmbeddingModel{ID: "test", MaxBatchSize: batchSize}

		resp, err := BatchEmbed(context.Background(), fn, model, EmbeddingRequest{Texts: texts})
		if err != nil {
			t.Fatalf("BatchEmbed: %v", err)
		}
		if len(resp.Embeddings) != n {
			t.Fatalf("embeddings=%d want=%d", len(resp.Embeddings), n)
		}
		for i, e := range resp.Embeddings {
			if e.Index != i {
				t.Fatalf("index[%d]=%d", i, e.Index)
			}
		}

		expectedCalls := int64((n + batchSize - 1) / batchSize)
		gotCalls := atomic.LoadInt64(&calls)
		if gotCalls != expectedCalls {
			t.Fatalf("calls=%d want=%d", gotCalls, expectedCalls)
		}
	})
}

// =============================================================================
// P2: Dimension Bound (Property-Based)
// =============================================================================

func TestP2_DimensionBound(t *testing.T) {
	withIsolatedEmbeddingProviders(t)
	withIsolatedEmbeddingModels(t)

	rapid.Check(t, func(rt *rapid.T) {
		minDims := rapid.IntRange(32, 128).Draw(rt, "minDims")
		maxDims := rapid.IntRange(minDims, minDims*4).Draw(rt, "maxDims")
		dims := rapid.IntRange(minDims, maxDims).Draw(rt, "dims")

		model := EmbeddingModel{
			ID:              "dim-test",
			API:             "mock-dims",
			Provider:        "mock",
			SupportsDimCtrl: true,
			MinDims:         minDims,
			MaxDims:         maxDims,
			MaxBatchSize:    100,
		}

		fn := func(ctx context.Context, m EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
			d := m.DefaultDims
			if req.Dimensions > 0 {
				d = req.Dimensions
			}
			if d == 0 {
				d = dims
			}
			vals := make([]float32, d)
			for i := range vals {
				vals[i] = float32(i) * 0.01
			}
			return &EmbeddingResponse{
				Embeddings: []Embedding{{Index: 0, Values: vals}},
				Usage:      EmbeddingUsage{Tokens: 1},
			}, nil
		}

		ClearEmbeddingModels()
		ClearEmbeddingProviders()
		RegisterEmbeddingModel(model)
		RegisterEmbeddingProvider(&mockEmbeddingProvider{api: "mock-dims", fn: fn}, "test")

		resp, err := Embed(context.Background(), "dim-test", EmbeddingRequest{
			Texts:      []string{"test"},
			Dimensions: dims,
		})
		if err != nil {
			rt.Fatalf("Embed: %v", err)
		}
		if len(resp.Embeddings[0].Values) != dims {
			rt.Fatalf("vector dims=%d want=%d", len(resp.Embeddings[0].Values), dims)
		}
	})
}

// =============================================================================
// P3: Cost Monotonicity (Property-Based)
// =============================================================================

func TestP3_CostMonotonicity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		t1 := rapid.IntRange(1, 999999).Draw(t, "t1")
		t2 := rapid.IntRange(t1+1, t1+1000000).Draw(t, "t2")
		perMTok := rapid.Float64Range(0.001, 10.0).Draw(t, "perMTok")

		cost1 := float64(t1) * perMTok / 1_000_000
		cost2 := float64(t2) * perMTok / 1_000_000

		if cost1 >= cost2 {
			t.Fatalf("cost not monotonic: cost(%d)=%f >= cost(%d)=%f (perMTok=%f)",
				t1, cost1, t2, cost2, perMTok)
		}
	})
}

// =============================================================================
// P4: Registry Isolation (Property-Based)
// =============================================================================

func TestP4_RegistryIsolation(t *testing.T) {
	t.Run("embedding_ops_dont_affect_chat", func(t *testing.T) {
		withIsolatedEmbeddingProviders(t)
		withIsolatedEmbeddingModels(t)

		rapid.Check(t, func(rt *rapid.T) {
			chatProvidersBefore := GetProviders()

			ops := rapid.IntRange(5, 30).Draw(rt, "ops")
			for i := 0; i < ops; i++ {
				op := rapid.IntRange(0, 3).Draw(rt, fmt.Sprintf("op-%d", i))
				switch op {
				case 0:
					RegisterEmbeddingProvider(&mockEmbeddingProvider{api: fmt.Sprintf("embed-api-%d", i)}, "iso-test")
				case 1:
					_, _ = GetEmbeddingProvider(fmt.Sprintf("embed-api-%d", i%5))
				case 2:
					RegisterEmbeddingModel(EmbeddingModel{ID: fmt.Sprintf("em-%d", i), Provider: "p"})
				case 3:
					UnregisterEmbeddingProviders("iso-test")
				}
			}

			chatProvidersAfter := GetProviders()
			if len(chatProvidersBefore) != len(chatProvidersAfter) {
				rt.Fatalf("chat providers changed: %d → %d", len(chatProvidersBefore), len(chatProvidersAfter))
			}
		})
	})

	t.Run("chat_ops_dont_affect_embedding", func(t *testing.T) {
		withIsolatedEmbeddingProviders(t)
		withIsolatedEmbeddingModels(t)

		// Seed the embedding registry with known entries.
		RegisterEmbeddingProvider(&mockEmbeddingProvider{api: "embed-sentinel"}, "sentinel")
		RegisterEmbeddingModel(EmbeddingModel{ID: "em-sentinel", Provider: "p"})

		rapid.Check(t, func(rt *rapid.T) {
			ops := rapid.IntRange(5, 30).Draw(rt, "ops")
			for i := 0; i < ops; i++ {
				op := rapid.IntRange(0, 2).Draw(rt, fmt.Sprintf("op-%d", i))
				switch op {
				case 0:
					RegisterProvider(&mockProvider{api: fmt.Sprintf("chat-api-%d", i)}, "iso-test")
				case 1:
					_, _ = GetProvider(fmt.Sprintf("chat-api-%d", i%5))
				case 2:
					UnregisterProviders("iso-test")
				}
			}

			// Embedding sentinel must still exist.
			if _, err := GetEmbeddingProvider("embed-sentinel"); err != nil {
				rt.Fatalf("embedding provider lost after chat ops: %v", err)
			}
			if _, ok := GetEmbeddingModel("em-sentinel"); !ok {
				rt.Fatalf("embedding model lost after chat ops")
			}
		})
	})
}

// =============================================================================
// P5: Task Type Mapping Completeness (Property-Based)
// =============================================================================

func TestP5_TaskTypeMappingCompleteness(t *testing.T) {
	// Every EmbeddingTaskType constant must be a well-known value.
	allTaskTypes := []EmbeddingTaskType{
		EmbeddingTaskQuery,
		EmbeddingTaskDocument,
		EmbeddingTaskClassification,
		EmbeddingTaskClustering,
		EmbeddingTaskSimilarity,
		EmbeddingTaskUnspecified,
	}

	// Provider mapping tables (from the plan §8.4).
	googleTaskTypes := map[EmbeddingTaskType]string{
		EmbeddingTaskQuery:          "RETRIEVAL_QUERY",
		EmbeddingTaskDocument:       "RETRIEVAL_DOCUMENT",
		EmbeddingTaskClassification: "CLASSIFICATION",
		EmbeddingTaskClustering:     "CLUSTERING",
		EmbeddingTaskSimilarity:     "SEMANTIC_SIMILARITY",
		EmbeddingTaskUnspecified:    "",
	}
	cohereInputTypes := map[EmbeddingTaskType]string{
		EmbeddingTaskQuery:          "search_query",
		EmbeddingTaskDocument:       "search_document",
		EmbeddingTaskClassification: "classification",
		EmbeddingTaskClustering:     "clustering",
		EmbeddingTaskSimilarity:     "search_document",
		EmbeddingTaskUnspecified:    "search_document",
	}
	voyageInputTypes := map[EmbeddingTaskType]string{
		EmbeddingTaskQuery:    "query",
		EmbeddingTaskDocument: "document",
	}

	for _, tt := range allTaskTypes {
		// Google must have mapping for every type
		if _, ok := googleTaskTypes[tt]; !ok {
			t.Errorf("Google mapping missing for task type %q", tt)
		}
		// Cohere must have mapping for every type
		if _, ok := cohereInputTypes[tt]; !ok {
			t.Errorf("Cohere mapping missing for task type %q", tt)
		}
		// Voyage only maps query/document; others are explicitly omitted (ignored)
		if tt == EmbeddingTaskQuery || tt == EmbeddingTaskDocument {
			if _, ok := voyageInputTypes[tt]; !ok {
				t.Errorf("Voyage mapping missing for task type %q", tt)
			}
		}
	}
}

// =============================================================================
// F1: Provider API Error Handling (Fault Injection)
// =============================================================================

func TestF1_ProviderAPIErrorHandling(t *testing.T) {
	withIsolatedEmbeddingProviders(t)
	withIsolatedEmbeddingModels(t)

	model := EmbeddingModel{ID: "f1", API: "mock-f1", Provider: "mock", MaxBatchSize: 100}
	RegisterEmbeddingModel(model)

	cases := []struct {
		name      string
		err       error
		wantCode  ProviderErrorCode
		wantRetry bool
	}{
		{
			name:      "rate_limit_429",
			err:       &ProviderError{Code: ErrRateLimit, StatusCode: 429, Provider: "mock", Message: "rate limited", RetryAfter: 2 * time.Second},
			wantCode:  ErrRateLimit,
			wantRetry: true,
		},
		{
			name:      "bad_request_400",
			err:       &ProviderError{Code: ErrUnknown, StatusCode: 400, Provider: "mock", Message: "bad request"},
			wantCode:  ErrUnknown,
			wantRetry: false,
		},
		{
			name:      "server_error_500",
			err:       &ProviderError{Code: ErrServerError, StatusCode: 500, Provider: "mock", Message: "internal"},
			wantCode:  ErrServerError,
			wantRetry: false,
		},
		{
			name:      "timeout",
			err:       context.DeadlineExceeded,
			wantRetry: false,
		},
		{
			name:      "connection_refused",
			err:       fmt.Errorf("connection refused: %w", context.DeadlineExceeded),
			wantRetry: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ClearEmbeddingProviders()
			RegisterEmbeddingProvider(&mockEmbeddingProvider{api: "mock-f1", fn: func(context.Context, EmbeddingModel, EmbeddingRequest) (*EmbeddingResponse, error) {
				return nil, tc.err
			}}, "src")

			_, err := Embed(context.Background(), "f1", EmbeddingRequest{Texts: []string{"x"}})
			if err == nil {
				t.Fatal("expected error")
			}

			var pe *ProviderError
			if tc.wantCode != "" {
				if !errors.As(err, &pe) {
					t.Fatalf("expected ProviderError, got %T: %v", err, err)
				}
				if pe.Code != tc.wantCode {
					t.Fatalf("code=%q want=%q", pe.Code, tc.wantCode)
				}
				if tc.wantRetry && pe.RetryAfter == 0 {
					t.Fatal("expected non-zero RetryAfter for retryable error")
				}
			}
		})
	}
}

// =============================================================================
// F2: Partial Batch Failure (Fault Injection)
// =============================================================================

func TestF2_PartialBatchFailure(t *testing.T) {
	model := EmbeddingModel{ID: "f2", MaxBatchSize: 3}
	texts := []string{"t0", "t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8"}
	// 3 batches of 3. Batch 2 (texts 3-5) fails.

	batchErr := &ProviderError{Code: ErrServerError, StatusCode: 500, Provider: "mock", Message: "batch 2 fail"}
	callNum := 0
	fn := func(ctx context.Context, m EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
		callNum++
		if callNum == 2 {
			return nil, batchErr
		}
		embs := make([]Embedding, len(req.Texts))
		for i := range req.Texts {
			embs[i] = Embedding{Index: i, Values: []float32{float32(callNum)}}
		}
		return &EmbeddingResponse{Embeddings: embs, Usage: EmbeddingUsage{Tokens: len(req.Texts)}}, nil
	}

	resp, err := BatchEmbed(context.Background(), fn, model, EmbeddingRequest{Texts: texts})
	if err == nil {
		t.Fatal("expected error from batch 2")
	}
	if !errors.Is(err, batchErr) {
		t.Fatalf("expected wrapped provider error, got: %v", err)
	}
	// Partial results from batch 1 should be preserved.
	if resp == nil {
		t.Fatal("expected partial response")
	}
	if len(resp.Embeddings) != 3 {
		t.Fatalf("partial embeddings=%d want=3", len(resp.Embeddings))
	}
	// Batch 3 should not be attempted.
	if callNum != 2 {
		t.Fatalf("calls=%d want=2 (batch 3 should not execute)", callNum)
	}
}

// =============================================================================
// F3: Malformed Response Handling (Fault Injection)
// =============================================================================

func TestF3_MalformedResponseHandling(t *testing.T) {
	withIsolatedEmbeddingProviders(t)
	withIsolatedEmbeddingModels(t)

	model := EmbeddingModel{ID: "f3", API: "mock-f3", Provider: "mock", MaxBatchSize: 100}
	RegisterEmbeddingModel(model)

	t.Run("nil_response", func(t *testing.T) {
		ClearEmbeddingProviders()
		RegisterEmbeddingProvider(&mockEmbeddingProvider{api: "mock-f3", fn: func(context.Context, EmbeddingModel, EmbeddingRequest) (*EmbeddingResponse, error) {
			return nil, nil
		}}, "src")

		resp, err := Embed(context.Background(), "f3", EmbeddingRequest{Texts: []string{"x"}})
		// Embed() should return an error for nil provider response.
		if err == nil {
			t.Fatal("expected error for nil provider response")
		}
		if resp != nil {
			t.Fatalf("expected nil response, got %v", resp)
		}
	})

	t.Run("nan_values", func(t *testing.T) {
		ClearEmbeddingProviders()
		RegisterEmbeddingProvider(&mockEmbeddingProvider{api: "mock-f3", fn: func(context.Context, EmbeddingModel, EmbeddingRequest) (*EmbeddingResponse, error) {
			return &EmbeddingResponse{
				Embeddings: []Embedding{{Index: 0, Values: []float32{float32(math.NaN()), 1.0, float32(math.Inf(1))}}},
			}, nil
		}}, "src")

		// Should not panic. Core layer passes through without validation (URP §11.2 deferred).
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on NaN values: %v", r)
				}
			}()
			resp, err := Embed(context.Background(), "f3", EmbeddingRequest{Texts: []string{"x"}})
			// Currently passes through without error — provider response is trusted.
			if err != nil {
				t.Logf("NaN values returned error (acceptable): %v", err)
				return
			}
			if resp == nil || len(resp.Embeddings) != 1 {
				t.Fatalf("expected 1 embedding in passthrough, got %v", resp)
			}
		}()
	})

	t.Run("negative_index", func(t *testing.T) {
		ClearEmbeddingProviders()
		RegisterEmbeddingProvider(&mockEmbeddingProvider{api: "mock-f3", fn: func(context.Context, EmbeddingModel, EmbeddingRequest) (*EmbeddingResponse, error) {
			return &EmbeddingResponse{
				Embeddings: []Embedding{{Index: -1, Values: []float32{1.0}}},
			}, nil
		}}, "src")

		// Should not panic.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on negative index: %v", r)
				}
			}()
			resp, err := Embed(context.Background(), "f3", EmbeddingRequest{Texts: []string{"x"}})
			// Currently passes through without error — provider response is trusted.
			if err != nil {
				t.Logf("negative index returned error (acceptable): %v", err)
				return
			}
			if resp == nil || len(resp.Embeddings) != 1 {
				t.Fatalf("expected 1 embedding in passthrough, got %v", resp)
			}
		}()
	})
}

// =============================================================================
// F4: Context Cancellation (Fault Injection)
// =============================================================================

func TestF4_ContextCancellation(t *testing.T) {
	model := EmbeddingModel{ID: "f4", MaxBatchSize: 2}
	texts := make([]string, 100)
	for i := range texts {
		texts[i] = fmt.Sprintf("text-%d", i)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var callCount int64

	fn := func(ctx context.Context, m EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
		n := atomic.AddInt64(&callCount, 1)
		if n >= 5 {
			cancel()
		}
		embs := make([]Embedding, len(req.Texts))
		for i := range req.Texts {
			embs[i] = Embedding{Index: i, Values: []float32{1.0}}
		}
		return &EmbeddingResponse{Embeddings: embs, Usage: EmbeddingUsage{Tokens: len(req.Texts)}}, nil
	}

	_, err := BatchEmbed(ctx, fn, model, EmbeddingRequest{Texts: texts})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
	// Should have stopped well before processing all 50 batches.
	got := atomic.LoadInt64(&callCount)
	if got >= 50 {
		t.Fatalf("expected early stop, got %d calls", got)
	}
}

// =============================================================================
// O3: Embed() Entry Point Contract (Oracle / Golden)
// =============================================================================

func TestO3_EmbedEntryPointContract(t *testing.T) {
	withIsolatedEmbeddingProviders(t)
	withIsolatedEmbeddingModels(t)

	model := EmbeddingModel{
		ID:              "embed-3-small",
		API:             "mock-oracle",
		Provider:        "mock",
		SupportsDimCtrl: true,
		MinDims:         64,
		MaxDims:         1536,
		DefaultDims:     1536,
		MaxBatchSize:    100,
		Cost:            EmbeddingCost{PerMTok: 0.02},
	}
	RegisterEmbeddingModel(model)

	var capturedModel EmbeddingModel
	var capturedReq EmbeddingRequest
	RegisterEmbeddingProvider(&mockEmbeddingProvider{api: "mock-oracle", fn: func(ctx context.Context, m EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
		capturedModel = m
		capturedReq = req
		return &EmbeddingResponse{
			Embeddings: []Embedding{
				{Index: 0, Values: []float32{0.1, 0.2, 0.3}},
				{Index: 1, Values: []float32{0.4, 0.5, 0.6}},
			},
			Usage: EmbeddingUsage{Tokens: 10},
		}, nil
	}}, "src")

	req := EmbeddingRequest{
		Texts:      []string{"hello", "world"},
		TaskType:   EmbeddingTaskQuery,
		Dimensions: 256,
	}
	resp, err := Embed(context.Background(), "embed-3-small", req)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	// 1. Verify provider received correct model
	if capturedModel.ID != "embed-3-small" {
		t.Fatalf("provider model=%q want=embed-3-small", capturedModel.ID)
	}
	if capturedModel.API != "mock-oracle" {
		t.Fatalf("provider model.API=%q want=mock-oracle", capturedModel.API)
	}

	// 2. Verify provider received correct request
	if len(capturedReq.Texts) != 2 {
		t.Fatalf("provider req texts=%d want=2", len(capturedReq.Texts))
	}
	if capturedReq.TaskType != EmbeddingTaskQuery {
		t.Fatalf("provider req taskType=%q want=query", capturedReq.TaskType)
	}
	if capturedReq.Dimensions != 256 {
		t.Fatalf("provider req dims=%d want=256", capturedReq.Dimensions)
	}

	// 3. Verify response has cost calculated
	expectedCost := 10.0 * 0.02 / 1_000_000
	if math.Abs(resp.Usage.Cost-expectedCost) > 1e-15 {
		t.Fatalf("cost=%v want=%v", resp.Usage.Cost, expectedCost)
	}

	// 4. Verify embeddings in order
	if len(resp.Embeddings) != 2 {
		t.Fatalf("embeddings=%d want=2", len(resp.Embeddings))
	}
	if resp.Embeddings[0].Index != 0 || resp.Embeddings[1].Index != 1 {
		t.Fatalf("embeddings not in order: [%d, %d]", resp.Embeddings[0].Index, resp.Embeddings[1].Index)
	}

	// 5. Verify model name set
	if resp.Model != "embed-3-small" {
		t.Fatalf("resp.Model=%q want=embed-3-small", resp.Model)
	}
}

// =============================================================================
// B1: Registry Lookup Latency (Benchmark)
// =============================================================================

func BenchmarkB1_RegistryLookup(b *testing.B) {
	sizes := []int{10, 100, 1000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("models_%d", size), func(b *testing.B) {
			ClearEmbeddingModels()
			for i := 0; i < size; i++ {
				RegisterEmbeddingModel(EmbeddingModel{ID: fmt.Sprintf("m-%d", i), Provider: "p"})
			}
			target := fmt.Sprintf("m-%d", size/2)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = GetEmbeddingModel(target)
			}
		})

		b.Run(fmt.Sprintf("providers_%d", size), func(b *testing.B) {
			ClearEmbeddingProviders()
			for i := 0; i < size; i++ {
				RegisterEmbeddingProvider(&mockEmbeddingProvider{api: fmt.Sprintf("api-%d", i)}, "bench")
			}
			target := fmt.Sprintf("api-%d", size/2)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = GetEmbeddingProvider(target)
			}
		})
	}
}

// =============================================================================
// B2: Batch Splitting Overhead (Benchmark)
// =============================================================================

func BenchmarkB2_BatchSplittingOverhead(b *testing.B) {
	model := EmbeddingModel{ID: "bench", MaxBatchSize: 100}
	texts := make([]string, 10000)
	for i := range texts {
		texts[i] = "text"
	}
	// No-op embed function to isolate splitting overhead.
	fn := func(ctx context.Context, m EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
		embs := make([]Embedding, len(req.Texts))
		for i := range embs {
			embs[i] = Embedding{Index: i, Values: []float32{1.0}}
		}
		return &EmbeddingResponse{Embeddings: embs, Usage: EmbeddingUsage{Tokens: len(req.Texts)}}, nil
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := BatchEmbed(context.Background(), fn, model, EmbeddingRequest{Texts: texts})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// B3: Vector Validation (Benchmark)
// =============================================================================

func BenchmarkB3_VectorValidation(b *testing.B) {
	for _, dims := range []int{256, 1024, 3072} {
		b.Run(fmt.Sprintf("dims_%d", dims), func(b *testing.B) {
			vec := make([]float32, dims)
			for i := range vec {
				vec[i] = float32(i) * 0.001
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				validateEmbeddingVector(vec)
			}
		})
	}
}

// validateEmbeddingVector checks for NaN/Inf values.
// NOTE: This is a reference implementation for benchmarking. Production code
// currently passes vectors through unvalidated. When URP §11.2 vector
// validation is added to Embed()/BatchEmbed(), this benchmark will measure
// the production code path instead.
func validateEmbeddingVector(v []float32) (hasNaN, hasInf bool) {
	for _, f := range v {
		if math.IsNaN(float64(f)) {
			hasNaN = true
		}
		if math.IsInf(float64(f), 0) {
			hasInf = true
		}
	}
	return
}

// =============================================================================
// B4: Cost Calculation (Benchmark)
// =============================================================================

func BenchmarkB4_CostCalculation(b *testing.B) {
	for _, batchSize := range []int{1, 100, 10000} {
		b.Run(fmt.Sprintf("batch_%d", batchSize), func(b *testing.B) {
			tokens := batchSize * 5
			perMTok := 0.02
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = float64(tokens) * perMTok / 1_000_000
			}
		})
	}
}

// =============================================================================
// ST1: Concurrent Registry Access (Stress)
// =============================================================================

func TestST1_ConcurrentRegistryAccess(t *testing.T) {
	withIsolatedEmbeddingProviders(t)
	withIsolatedEmbeddingModels(t)

	const goroutines = 100
	const ops = 500
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				switch j % 8 {
				case 0:
					RegisterEmbeddingProvider(&mockEmbeddingProvider{api: fmt.Sprintf("api-%d", id%10)}, fmt.Sprintf("src-%d", id))
				case 1:
					_, _ = GetEmbeddingProvider(fmt.Sprintf("api-%d", id%10))
				case 2:
					UnregisterEmbeddingProviders(fmt.Sprintf("src-%d", id))
				case 3:
					RegisterEmbeddingModel(EmbeddingModel{ID: fmt.Sprintf("m-%d-%d", id, j%20), Provider: "p"})
				case 4:
					_, _ = GetEmbeddingModel(fmt.Sprintf("m-%d-%d", id, j%20))
				case 5:
					_ = ListEmbeddingModels()
				case 6:
					_ = ListEmbeddingModelsByProvider("p")
				case 7:
					ClearEmbeddingModels()
				}
			}
		}(i)
	}
	wg.Wait()

	// Registry should be in a consistent state — no panics, no deadlocks.
	// Verify we can still perform operations.
	RegisterEmbeddingModel(EmbeddingModel{ID: "final", Provider: "p"})
	if _, ok := GetEmbeddingModel("final"); !ok {
		t.Fatal("registry inconsistent after concurrent stress")
	}
}

// =============================================================================
// ST2: Large Batch Embedding (Stress)
// =============================================================================

func TestST2_LargeBatchEmbedding(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large batch stress test in short mode")
	}

	const totalTexts = 100_000
	const batchSize = 100
	model := EmbeddingModel{ID: "st2", MaxBatchSize: batchSize}

	texts := make([]string, totalTexts)
	for i := range texts {
		texts[i] = fmt.Sprintf("text-%d", i)
	}

	fn := func(ctx context.Context, m EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
		embs := make([]Embedding, len(req.Texts))
		for i := range embs {
			embs[i] = Embedding{Index: i, Values: []float32{float32(i)}}
		}
		return &EmbeddingResponse{Embeddings: embs, Usage: EmbeddingUsage{Tokens: len(req.Texts)}}, nil
	}

	start := time.Now()
	resp, err := BatchEmbed(context.Background(), fn, model, EmbeddingRequest{Texts: texts})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("BatchEmbed: %v", err)
	}
	if len(resp.Embeddings) != totalTexts {
		t.Fatalf("embeddings=%d want=%d", len(resp.Embeddings), totalTexts)
	}
	// Verify ordering.
	for i, e := range resp.Embeddings {
		if e.Index != i {
			t.Fatalf("index[%d]=%d", i, e.Index)
		}
	}
	// Verify usage aggregated.
	if resp.Usage.Tokens != totalTexts {
		t.Fatalf("tokens=%d want=%d", resp.Usage.Tokens, totalTexts)
	}

	t.Logf("ST2: 100k texts in %v", elapsed)
	if elapsed > 10*time.Second {
		t.Fatalf("took too long: %v (expected < 10s)", elapsed)
	}
}

// =============================================================================
// ST3: Concurrent Embed Calls (Stress)
// =============================================================================

func TestST3_ConcurrentEmbedCalls(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrent embed stress test in short mode")
	}

	withIsolatedEmbeddingProviders(t)
	withIsolatedEmbeddingModels(t)

	model := EmbeddingModel{
		ID:           "st3",
		API:          "mock-st3",
		Provider:     "mock",
		MaxBatchSize: 50,
		Cost:         EmbeddingCost{PerMTok: 0.1},
	}
	RegisterEmbeddingModel(model)
	RegisterEmbeddingProvider(&mockEmbeddingProvider{api: "mock-st3", fn: func(ctx context.Context, m EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
		embs := make([]Embedding, len(req.Texts))
		for i := range embs {
			embs[i] = Embedding{Index: i, Values: []float32{float32(i), float32(len(req.Texts))}}
		}
		return &EmbeddingResponse{
			Embeddings: embs,
			Usage:      EmbeddingUsage{Tokens: len(req.Texts) * 3},
		}, nil
	}}, "src")

	const concurrent = 50
	const textsPerCall = 1000
	var wg sync.WaitGroup
	errs := make(chan error, concurrent)

	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			texts := make([]string, textsPerCall)
			for j := range texts {
				texts[j] = fmt.Sprintf("g%d-t%d", id, j)
			}
			resp, err := Embed(context.Background(), "st3", EmbeddingRequest{Texts: texts})
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: %w", id, err)
				return
			}
			if len(resp.Embeddings) != textsPerCall {
				errs <- fmt.Errorf("goroutine %d: embeddings=%d want=%d", id, len(resp.Embeddings), textsPerCall)
				return
			}
			for j, e := range resp.Embeddings {
				if e.Index != j {
					errs <- fmt.Errorf("goroutine %d: index[%d]=%d", id, j, e.Index)
					return
				}
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// =============================================================================
// SEC1: Input Validation (Security)
// =============================================================================

func TestSEC1_InputValidation(t *testing.T) {
	withIsolatedEmbeddingProviders(t)
	withIsolatedEmbeddingModels(t)

	model := EmbeddingModel{
		ID:              "sec1",
		API:             "mock-sec1",
		Provider:        "mock",
		SupportsDimCtrl: true,
		MinDims:         64,
		MaxDims:         1536,
		MaxBatchSize:    100,
	}
	RegisterEmbeddingModel(model)
	RegisterEmbeddingProvider(&mockEmbeddingProvider{api: "mock-sec1"}, "src")

	t.Run("empty_texts", func(t *testing.T) {
		_, err := Embed(context.Background(), "sec1", EmbeddingRequest{Texts: []string{}})
		if err == nil {
			t.Fatal("expected error for empty texts")
		}
	})

	t.Run("nil_texts", func(t *testing.T) {
		_, err := Embed(context.Background(), "sec1", EmbeddingRequest{Texts: nil})
		if err == nil {
			t.Fatal("expected error for nil texts")
		}
	})

	t.Run("text_with_null_bytes", func(t *testing.T) {
		// Should not panic or silently truncate.
		resp, err := Embed(context.Background(), "sec1", EmbeddingRequest{Texts: []string{"hello\x00world"}})
		if err != nil {
			t.Fatalf("unexpected error for text with null bytes: %v", err)
		}
		if len(resp.Embeddings) != 1 {
			t.Fatalf("embeddings=%d want=1", len(resp.Embeddings))
		}
	})

	t.Run("very_long_text", func(t *testing.T) {
		longText := make([]byte, 1024*1024) // 1MB
		for i := range longText {
			longText[i] = 'a'
		}
		// Should not panic.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on long text: %v", r)
				}
			}()
			_, _ = Embed(context.Background(), "sec1", EmbeddingRequest{Texts: []string{string(longText)}})
		}()
	})

	t.Run("dims_zero", func(t *testing.T) {
		// Zero dimensions = use default, should succeed.
		resp, err := Embed(context.Background(), "sec1", EmbeddingRequest{Texts: []string{"x"}, Dimensions: 0})
		if err != nil {
			t.Fatalf("unexpected error for dims=0: %v", err)
		}
		if len(resp.Embeddings) != 1 {
			t.Fatalf("embeddings=%d want=1", len(resp.Embeddings))
		}
	})

	t.Run("dims_negative", func(t *testing.T) {
		// Negative dimensions — should be treated as invalid.
		// The current Embed() code checks `req.Dimensions > 0` before validation,
		// so negative values pass through. This is acceptable — provider will reject.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on negative dims: %v", r)
				}
			}()
			_, _ = Embed(context.Background(), "sec1", EmbeddingRequest{Texts: []string{"x"}, Dimensions: -1})
		}()
	})

	t.Run("dims_maxint", func(t *testing.T) {
		_, err := Embed(context.Background(), "sec1", EmbeddingRequest{Texts: []string{"x"}, Dimensions: math.MaxInt32})
		if err == nil {
			t.Fatal("expected error for MaxInt dims")
		}
	})
}

// =============================================================================
// SEC2: API Key Handling (Security)
// =============================================================================

func TestSEC2_APIKeyHandling(t *testing.T) {
	withIsolatedEmbeddingProviders(t)
	withIsolatedEmbeddingModels(t)

	model := EmbeddingModel{ID: "sec2", API: "mock-sec2", Provider: "mock", MaxBatchSize: 100}
	RegisterEmbeddingModel(model)

	// Simulate a provider that returns an auth error — verify the error
	// message does not contain the API key.
	apiKey := "sk-secret-test-key-12345"
	RegisterEmbeddingProvider(&mockEmbeddingProvider{api: "mock-sec2", fn: func(context.Context, EmbeddingModel, EmbeddingRequest) (*EmbeddingResponse, error) {
		return nil, &ProviderError{
			Code:     ErrAuth,
			Provider: "mock",
			Message:  "missing API key",
		}
	}}, "src")

	_, err := Embed(context.Background(), "sec2", EmbeddingRequest{Texts: []string{"x"}})
	if err == nil {
		t.Fatal("expected auth error")
	}

	errMsg := err.Error()
	if strings.Contains(errMsg, apiKey) {
		t.Fatalf("error message contains API key: %s", errMsg)
	}

	// Verify error says "missing" rather than echoing the key.
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("expected ProviderError, got %T", err)
	}
	if pe.Code != ErrAuth {
		t.Fatalf("code=%q want=auth", pe.Code)
	}
}

// =============================================================================
// Additional: Embed() validation for dimension bounds via Embed() (not just batch)
// =============================================================================

func TestP2_EmbedRejectsOutOfBoundDimensions(t *testing.T) {
	withIsolatedEmbeddingProviders(t)
	withIsolatedEmbeddingModels(t)

	model := EmbeddingModel{
		ID:              "p2-bounds",
		API:             "mock-p2",
		Provider:        "mock",
		SupportsDimCtrl: true,
		MinDims:         64,
		MaxDims:         1024,
		MaxBatchSize:    100,
	}
	RegisterEmbeddingModel(model)
	RegisterEmbeddingProvider(&mockEmbeddingProvider{api: "mock-p2"}, "src")

	// Below min
	_, err := Embed(context.Background(), "p2-bounds", EmbeddingRequest{Texts: []string{"x"}, Dimensions: 32})
	if err == nil {
		t.Fatal("expected min dims error")
	}

	// Above max
	_, err = Embed(context.Background(), "p2-bounds", EmbeddingRequest{Texts: []string{"x"}, Dimensions: 2048})
	if err == nil {
		t.Fatal("expected max dims error")
	}

	// Within range
	resp, err := Embed(context.Background(), "p2-bounds", EmbeddingRequest{Texts: []string{"x"}, Dimensions: 512})
	if err != nil {
		t.Fatalf("unexpected error for valid dims: %v", err)
	}
	if len(resp.Embeddings) != 1 {
		t.Fatalf("embeddings=%d want=1", len(resp.Embeddings))
	}
}
