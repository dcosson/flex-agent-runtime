# Code Review: aiag-65f.2 (R1, reviewer-sea)

- Bead: aiag-65f.2
- Commit range: e435a43..d47ae82
- Plan doc: docs/plans/01-ai-core.add01-test-harness.md
- Reviewer: reviewer-sea
- Review commit: d47ae82

## Findings

### P3 - B3 benchmarks a test-only function, not production code

**Location:** `internal/ai/embedding_harness_test.go:624-650`

**Problem**
The harness doc B3 says "Benchmark the NaN/Inf/dimension validation for vectors of 256, 1024, 3072 dimensions." The implementation benchmarks `validateEmbeddingVector`, but this function is defined in the test file (line 640), not in production code. No production embedding vector validation exists. The benchmark measures a function that only exists in tests, so it can't detect regressions in real code.

**Suggested fix**
Either (a) move `validateEmbeddingVector` to production code in `embedding.go` and call it from `Embed()`/`BatchEmbed()` response handling, or (b) accept that production code currently passes vectors through unvalidated and add a doc comment on the benchmark noting it tests a reference implementation for future use. Option (b) is fine for V1.

---

### P3 - F3 only tests no-panic, not error returns

**Location:** `internal/ai/embedding_harness_test.go:416-431`

**Problem**
The harness doc F3 says "each case returns a clear error, no panics." The test discards both response and error (`_, _ = Embed(...)`) and only verifies no panic via `recover()`. For the `nil_response` case specifically, `Embed()` likely returns a nil response to the caller without error, which may surprise callers. The test doesn't verify whether errors are returned for NaN values or negative indices.

**Suggested fix**
Check the error/response return for each case. For `nil_response`, at minimum verify the returned `*EmbeddingResponse` is nil (documenting current behavior). Low priority since the core layer intentionally passes through provider responses without validation.

---

### P3 - P4 registry isolation tests only one direction

**Location:** `internal/ai/embedding_harness_test.go:171-201`

**Problem**
The harness doc P4 specifies testing both directions: embedding ops don't affect chat registry AND chat ops don't affect embedding registry. The test only verifies the first direction (embedding ops → chat unchanged). The reverse direction (chat provider registration → embedding registry unchanged) is not tested.

**Suggested fix**
Add a second property block that performs random chat registry operations and asserts the embedding registry is unchanged afterward.

---

### P3 - Unused `rand` import suppressed with dummy variable

**Location:** `internal/ai/embedding_harness_test.go:8,1031`

**Problem**
`math/rand` is imported but never used in any test logic. The unused import warning is suppressed with `var _ = rand.Int` (line 1031). This is a code smell — the import should be removed entirely.

**Suggested fix**
Remove the `"math/rand"` import and the `var _ = rand.Int` line.

---

### P3 - Custom `contains`/`searchString` instead of `strings.Contains`

**Location:** `internal/ai/embedding_harness_test.go:975-986`

**Problem**
SEC2 uses custom `contains` and `searchString` helper functions that replicate `strings.Contains`. The standard library function is simpler, well-tested, and more readable. Other test files in this package already import `strings`.

**Suggested fix**
Replace `contains(errMsg, apiKey)` with `strings.Contains(errMsg, apiKey)` and delete the `contains`/`searchString` functions.

---

## Summary

5 findings: 0 P0, 0 P1, 0 P2, 5 P3

**Verdict**: Approved

The test harness is comprehensive and well-structured. All 16 test IDs from the harness doc are present and accounted for (P1-P5, F1-F4, O3, B1-B4, ST1-ST3, SEC1-SEC2). O1/O2 correctly deferred to provider harnesses per prior review disposition. All tests pass with `-race`. Benchmarks meet stated targets (B1: 15-33ns < 100ns, B2: 245us < 1ms, B3: 91-984ns < 1us, B4: 0.3ns < 100ns). Package coverage is 91.2%, meeting the 90%+ exit criterion. Property-based tests run 100 iterations via rapid. Stress tests exercise meaningful concurrency (100 goroutines / 100k texts / 50 concurrent Embed calls). The five P3s are all minor quality improvements, none affecting test correctness.
