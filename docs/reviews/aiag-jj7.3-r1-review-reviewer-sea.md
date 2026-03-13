# Code Review: aiag-jj7.3 (R1, reviewer-sea)

- Bead: aiag-jj7.3
- Commit range: 4c48898 (single commit)
- Plan doc: `docs/plans/02-provider-anthropic-test-harness.md`
- Reviewer: reviewer-sea
- Review commit: 4c48898

## Findings

### P1 - ST1 and ST3 are empty stubs, not implemented

**Location:** `internal/ai/provider/anthropic/harness_test.go:380-390`

**Problem**
ST1 (Long Session Soak) and ST3 (Burst Tool-Call Stress) are listed as key deliverables in the bead description but are only env-var-gated stubs with no implementation. They simply skip. The plan specifies:
- ST1: 8-hour soak (or compressed 5000-iteration loop) checking memory growth and goroutine leaks
- ST3: High-frequency tool call argument deltas with concurrent streams verifying parser stability

The OpenAI harness (aiag-mac.2) implements both: ST1 with 5000 iterations + memory/goroutine checks every 500 iterations, ST3 with 50 concurrent streams each parsing 10KB+ tool args. The Anthropic harness should have equivalent coverage since it's the pattern leader provider.

**Suggested fix**
Implement ST1 and ST3 following the same pattern as the OpenAI harness. ST1: 5000-iteration loop with diverse fixtures (text, tool call, thinking+tool), GC + memstats checks every 500 iterations, goroutine leak detection. ST3: 50 concurrent streams with large tool call args split into small chunks, verify final args correctness. Gate with `testing.Short()` skip, not env vars.

---

### P1 - SEC1 tests single injection string on parser only, not through streaming

**Location:** `internal/ai/provider/anthropic/harness_test.go:306-319`

**Problem**
SEC1 tests a single command injection string (`$(rm -rf /)`) directly on `toolJSONParser`, not through the full streaming pipeline. The plan specifies testing "malicious strings through `input_json_delta`" including SQL injection, path traversal, null bytes, deeply nested objects, and oversized strings. The OpenAI harness (SEC1) tests 7 different malicious payloads through the full streaming pipeline (stub server → provider → event stream → verify args are valid JSON). The Anthropic SEC1 only exercises the parser in isolation with one payload.

**Suggested fix**
Expand SEC1 to test multiple malicious payloads (matching or exceeding the OpenAI harness's coverage) and route them through the full streaming path using SSE fixtures built from the `textFixture` pattern. Verify that final tool call arguments are valid, serializable JSON.

---

### P2 - P2 tests parser directly, not through streaming path

**Location:** `internal/ai/provider/anthropic/harness_test.go:72-92`

**Problem**
P2 (Tool JSON Delta Convergence) tests `toolJSONParser` directly via `AppendDelta`/`ParseFinal`, not through the full streaming pipeline. The OpenAI P2 builds SSE fixtures with random JSON objects split at random chunk boundaries and streams them through the full provider stack, verifying that the `EventToolCallEnd` event contains the correct parsed arguments. While DS2 and DS3 test tool calls through streaming, they use fixed payloads — P2 should use rapid-generated random payloads through the full path to catch integration issues between SSE parsing, content block tracking, and tool JSON accumulation.

**Suggested fix**
Build an SSE fixture builder for tool call sequences (like the OpenAI harness's `buildToolCallSSE`) and test P2 through the full streaming path with rapid-generated JSON objects and random chunk boundaries.

---

### P2 - ST2 runs 100 concurrent vs plan's 500, no rationale

**Location:** `internal/ai/provider/anthropic/harness_test.go:366`

**Problem**
ST2 runs 100 concurrent streams. The plan specifies 500. The OpenAI harness uses 200 with a comment explaining the reduction. The Anthropic harness has no comment explaining why 100 was chosen. Additionally, ST2 doesn't verify success counts or log results — it just waits for all goroutines to finish without checking if they succeeded.

**Suggested fix**
Increase to at least 200 concurrent (matching OpenAI), add `atomic.Int32` success/error counters as in the OpenAI harness, assert all streams completed, and add a comment noting the plan target and reason for reduction.

---

### P3 - F4 backpressure test takes 1.8s due to timing sensitivity

**Location:** `internal/ai/provider/anthropic/harness_test.go:182-192`

**Problem**
F4 uses a 120ms context timeout with 200ms event delay. Under the race detector, the test took 1.81s — 15x the expected ~120ms. The tight timing margin (only 80ms gap between timeout and first event) may cause flakiness under load. The test logic is correct but the timing constants are fragile.

**Suggested fix**
Widen the gap: use 50ms context timeout with 200ms event delay, or 120ms timeout with 500ms event delay. This preserves the test intent (context cancels before stream completes) while being more robust under race detector and CI load.

---

## Summary

5 findings: 0 P0, 2 P1, 2 P2, 1 P3

**Verdict**: Approved with revisions

The harness covers the core property tests (P1-P4 with rapid), fault injection (F1-F5), deterministic simulations (DS1-DS3), and benchmarks (B1-B4). P1 event ordering and terminal uniqueness is well-designed. The deterministic simulation fixtures for multi-tool isolation and thinking+tool interleaving are solid. However, the harness is significantly thinner than the OpenAI counterpart (492 lines vs 1384 lines) with key gaps: ST1/ST3 are empty stubs rather than implemented, SEC1 is minimal, and P2 doesn't exercise the full streaming path. As the pattern leader provider, the Anthropic harness should set the standard that other providers follow, not trail behind.
