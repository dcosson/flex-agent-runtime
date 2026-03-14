# 01-ai-core Addendum 01 Test Harness: Embedding API

**Parent:** [01-ai-core.add01](./01-ai-core.add01.md)

---

## P: Property-Based Tests

### P1. Batch Splitting Preserves Ordering

**Invariant:** For any input of N texts and a model with max batch size B, the provider adapter makes `ceil(N/B)` internal calls and returns exactly N embeddings with indices [0, N).

```
Property: forall texts []string, batchSize int (1..100):
    resp := adapter.Embed(model{maxBatch: batchSize}, EmbeddingRequest{Texts: texts})
    assert len(resp.Embeddings) == len(texts)
    for i, e := range resp.Embeddings:
        assert e.Index == i
```

Use a mock HTTP transport that records call count and returns deterministic vectors.

### P2. Dimension Bound

**Invariant:** If `Dimensions` is set and the model supports dimension control, all returned vectors have exactly that length.

```
Property: forall dims int (minDims..maxDims):
    resp := adapter.Embed(model{supportsDimCtrl: true, minDims, maxDims}, EmbeddingRequest{Texts: ["x"], Dimensions: dims})
    assert len(resp.Embeddings[0].Values) == dims
```

### P3. Cost Monotonicity

**Invariant:** Cost scales linearly with token count for the same model. Two requests with token counts T1 < T2 satisfy Cost1 < Cost2.

```
Property: forall t1, t2 int (t1 < t2):
    cost1 := calculateCost(t1, model.Cost.PerMTok)
    cost2 := calculateCost(t2, model.Cost.PerMTok)
    assert cost1 < cost2
```

### P4. Registry Isolation

**Invariant:** Embedding provider registration/lookup never affects chat provider registration/lookup and vice versa.

```
Property: forall ops []RegistryOp:
    apply ops to embeddingProviderRegistry
    assert chatProviderRegistry unchanged
    apply ops to chatProviderRegistry
    assert embeddingProviderRegistry unchanged
```

### P5. Task Type Mapping Completeness

**Invariant:** Every `EmbeddingTaskType` constant maps to a valid provider-specific value (or explicit "ignore") for every provider adapter.

```
Property: forall taskType EmbeddingTaskType, provider ProviderAdapter:
    mapping := provider.mapTaskType(taskType)
    assert mapping is defined (not missing from map)
```

---

## F: Fault Injection Tests

### F1. Provider API Error Handling

Inject HTTP errors at the transport level and verify:
- 429 (rate limit) → returned as a retryable error with retry-after hint
- 400 (bad request) → returned as a validation error, not retried
- 500 (server error) → returned as internal error
- Timeout → returned as deadline exceeded
- Connection refused → returned as unavailable

### F2. Partial Batch Failure

For a request with N texts that requires 3 batches:
- Batch 1 succeeds
- Batch 2 fails with 500
- Batch 3 not attempted

Verify: error returned includes batch 2 failure. Successfully embedded texts from batch 1 are not lost (available in error details or partial response).

### F3. Malformed Response Handling

Inject malformed JSON responses:
- Empty response body
- Missing `data` field
- Embedding vector with wrong dimensions
- NaN values in embedding vector
- Negative index values

Verify: each case returns a clear error, no panics.

### F4. Context Cancellation

Start an embedding request with a context, cancel it mid-flight. Verify:
- In-flight HTTP request is cancelled
- Error returned is `context.Canceled`
- No goroutine leaks

---

## O: Oracle / Golden Tests

### O1. Request Wire Format

For each provider adapter, verify the JSON request body matches the expected wire format:

**OpenAI:**
```json
{"model": "text-embedding-3-small", "input": ["hello", "world"], "dimensions": 512}
```

**Google:**
```json
{"requests": [{"model": "models/gemini-embedding-001", "content": {"parts": [{"text": "hello"}]}, "taskType": "RETRIEVAL_DOCUMENT", "outputDimensionality": 768}]}
```

**Cohere:**
```json
{"model": "embed-v4.0", "texts": ["hello", "world"], "input_type": "search_document", "embedding_types": ["float"], "output_dimension": 1024}
```

Use a mock HTTP server that captures and compares request bodies.

### O2. Response Parsing

For each provider, feed known JSON responses through the adapter and verify the parsed `EmbeddingResponse` matches expected values. Use real API response samples (sanitized) as golden fixtures.

### O3. Embed() Entry Point Contract

Golden test for the full `Embed()` flow with a mock provider:
1. Register mock embedding provider and model
2. Call `Embed(ctx, modelID, req)`
3. Verify: provider received correct model and request, response has cost calculated, embeddings in order

---

## B: Benchmarks

### B1. Registry Lookup Latency

Benchmark `GetEmbeddingModel` and `GetEmbeddingProvider` with varying registry sizes (10, 100, 1000 entries). Target: < 100ns per lookup.

### B2. Batch Splitting Overhead

Benchmark the overhead of splitting N texts into batches of B, merging results, and maintaining index ordering. Measure with N=10000, B=100. The overhead should be negligible compared to network I/O (< 1ms).

### B3. Vector Validation

Benchmark the NaN/Inf/dimension validation for vectors of 256, 1024, 3072 dimensions. Target: < 1us per vector.

### B4. Cost Calculation

Benchmark cost calculation for batches of 1, 100, 10000 embeddings. Should be < 100ns regardless of batch size.

---

## ST: Stress Tests

### ST1. Concurrent Registry Access

100 goroutines concurrently registering, looking up, and unregistering embedding providers and models. No races detected with `-race`. Registry remains consistent after all goroutines complete.

### ST2. Large Batch Embedding

Embed 100,000 texts with a mock provider (batch size 100). Verify:
- All 100,000 embeddings returned in correct order
- Memory usage doesn't spike (no accumulating intermediate results)
- Completes within a reasonable time bound

### ST3. Concurrent Embed Calls

50 concurrent `Embed()` calls, each with 1000 texts. Verify no races, correct results, no goroutine leaks.

---

## SEC: Security Tests

### SEC1. Input Validation

- Empty text list → error
- Text with null bytes → handled (not truncated silently)
- Very long text (1MB) → handled (provider may truncate, no crash)
- Dimensions = 0, -1, MaxInt → appropriate error or default behavior

### SEC2. API Key Handling

- API key passed through `EmbeddingRequest` options, not logged or included in error messages
- If API key is missing, error message says "missing API key" not the key value

---

## CI Tier Mapping

| Tier | Tests | When |
|------|-------|------|
| Tier 1 (PR) | P1-P5, F1-F4, O1-O3, SEC1-SEC2 | Every PR |
| Tier 2 (Merge) | B1-B4, ST1-ST3 | Post-merge |
| Tier 3 (Nightly) | Integration tests with real provider APIs | Nightly |

---

## Exit Criteria

1. All property tests pass with rapid (100+ iterations)
2. All fault injection tests pass — no panics on any error path
3. Golden tests match expected wire formats for all 3 provider adapters
4. Benchmarks meet stated targets
5. Stress tests pass with `-race`
6. 90%+ code coverage on `embedding*.go` files
