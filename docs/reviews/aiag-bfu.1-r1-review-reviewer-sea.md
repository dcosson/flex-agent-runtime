# Code Review: aiag-bfu.1 (R1, reviewer-sea)

- Bead: aiag-bfu.1
- Commit range: b139d5e..1070660
- Plan doc: docs/plans/01-ai-core.add01.md §8.1
- Reviewer: reviewer-sea
- Review commit: 1070660

## Findings

### P3 - No test for malformed JSON response body

**Location:** `internal/ai/provider/openai/embedding_test.go`

**Problem**
The tests cover success, HTTP error status codes, batch splitting, and baseURL override, but no test exercises the `json.Unmarshal` failure path at `embedding.go:102-104` (valid HTTP 200 but invalid/empty JSON body). This is a minor coverage gap — the error handling is straightforward (`fmt.Errorf("decode embedding response: %w", err)`) but untested.

**Suggested fix**
Add a subtest to `TestEmbeddingProvider_HTTPErrorClassification` (or a new test) that returns `200 OK` with `"not json"` body and verifies an error is returned.

---

### P3 - Retry-After header not extracted from 429 responses

**Location:** `internal/ai/provider/openai/errors.go:28-36`, `internal/ai/provider/openai/embedding.go:97-99`

**Problem**
The `providerError` helper doesn't extract `Retry-After` from HTTP response headers — the `ProviderError.RetryAfter` field is always zero. This is a pre-existing limitation shared with the chat provider (not specific to this adapter), but worth noting since the plan §F1 mentions "429 → returned as a retryable error with retry-after hint." The embedding adapter passes `resp.StatusCode` and decoded message but not the response headers, so `providerError` can't access `Retry-After` even if it wanted to.

**Suggested fix**
Low priority. If retry-after support is needed, the embedding adapter (and chat adapter) would need to pass the header value to `providerError` or handle it separately. Could be a follow-up bead.

---

### P3 - Voyage `input_type` not implemented

**Location:** `internal/ai/provider/openai/embedding.go:59-71`

**Problem**
Plan §8.1 notes: "Voyage adds `input_type` — the adapter can include it when the model has `supportsTaskType: true`." The adapter currently ignores `TaskType` unconditionally. This is fine for V1 since Voyage models aren't in the catalog yet, but it's a known gap for OpenAI-compatible endpoint compatibility.

**Suggested fix**
No action needed now. Track as a future enhancement when Voyage models are added to the catalog.

---

## Summary

3 findings: 0 P0, 0 P1, 0 P2, 3 P3

**Verdict**: Approved

Clean, faithful implementation of plan §8.1. The separate `EmbeddingProvider` struct correctly addresses the seam review API() collision fix. `Embed()` properly delegates to `ai.BatchEmbed(ctx, p.embedSingle, ...)` using the `EmbedFunc` pattern to avoid recursion. Request/response wire mapping matches the plan spec exactly (texts→input, dimensions→dimensions, encoding→encoding_format, TaskType ignored, data→Embeddings, usage.total_tokens→Tokens). BaseURL override supports OpenAI-compatible endpoints (Voyage, vLLM, Ollama). Error handling reuses the shared `providerError`/`classifyHTTPError` infrastructure. Defensive copies on both input texts and output embedding values. Tests cover registration, full request/response round-trip, model baseURL override, HTTP error classification (429/401/500), and batch splitting with progress callbacks. All tests pass with `-race`. This is a solid pattern leader for the Google and Cohere adapters to follow.
