# Code Review: aiag-65f.1 (R1, reviewer-sea)

- Bead: aiag-65f.1
- Commit range: bc0cf0f..e435a43
- Plan doc: docs/plans/01-ai-core.add01.md
- Reviewer: reviewer-sea
- Review commit: e435a43

## Findings

### P3 - BatchEmbed context cancellation returns partial results but Embed() discards them

**Location:** `internal/ai/embedding_batch.go:29-31`, `internal/ai/embedding_api.go:34-37`

**Problem**
`BatchEmbed` returns both a partial `*EmbeddingResponse` and an error when context is cancelled mid-batch (line 30). This matches the plan's URP §11.1 intent ("return successfully embedded results plus a wrapped error"). However, the top-level `Embed()` entry point discards partial results on any error:

```go
resp, err := provider.Embed(ctx, model, req)
if err != nil {
    return nil, err  // partial results lost
}
```

This means direct `BatchEmbed` callers get partial results, but `Embed()` callers don't. The behavior is consistent with Go convention (`nil, err`) but worth documenting — callers who need partial results on cancellation should use `BatchEmbed` directly rather than `Embed()`.

**Suggested fix**
No code change needed. Consider adding a doc comment on `Embed()` noting that partial results from batch splitting are not returned on error. Low priority.

---

### P3 - Missing test for BatchEmbed context cancellation path

**Location:** `internal/ai/embedding_batch_test.go`

**Problem**
`BatchEmbed` has explicit context cancellation handling (line 29-31 of `embedding_batch.go`), but no test exercises this path. The existing tests cover: no-split, split with ordering/progress, and partial results on provider error. A context cancellation test would verify the partial-results-plus-ctx-error behavior.

**Suggested fix**
Add a test that cancels context after the first batch completes and verifies:
- Error is `context.Canceled`
- Partial response contains embeddings from completed batches

---

### P3 - BatchEmbed wraps errors with additional context (cosmetic deviation from plan)

**Location:** `internal/ai/embedding_batch.go:42`

**Problem**
The plan specifies `return nil, err // preserve ProviderError type` but the implementation wraps: `fmt.Errorf("embed batch [%d:%d] failed: %w", start, end, err)`. This is actually an improvement — the batch range context is useful for debugging, and `%w` preserves `errors.Is`/`errors.As` for `ProviderError` unwrapping. The test at `embedding_batch_test.go:80` confirms `errors.Is(err, baseErr)` works.

**Suggested fix**
None — this is better than the plan. Just noting the deviation.

---

## Summary

3 findings: 0 P0, 0 P1, 0 P2, 3 P3

**Verdict**: Approved

Implementation is a clean, faithful realization of the plan with both seam review P1 fixes correctly applied (separate EmbedFunc parameter in BatchEmbed, separate provider structs documented). All types, interfaces, registries, catalog loading, batch splitting, entry point, and public re-exports match the plan spec. Test coverage is solid — constants, registry CRUD, concurrent access (20 goroutines), mutation isolation via deepCopy, validation paths, cost calculation, ProviderError preservation, batch splitting with ordering and progress callbacks, and partial results on error. All tests pass with `-race`.
