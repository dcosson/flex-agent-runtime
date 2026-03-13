# Code Review: aiag-gjz.2 (R1, reviewer-sea)

- Bead: aiag-gjz.2
- Commit range: b625afe (single commit on batch3/google-test-harness)
- Plan doc: docs/plans/04-provider-google-test-harness.md
- Reviewer: reviewer-sea
- Review commit: b625afe

## Findings

### P2 - P6 tests shared CalculateCost instead of Google-specific mapUsage

**Location:** `internal/ai/provider/google/harness_test.go:303-337`

**Problem**
The plan specifies P6 should property-test `mapUsage(meta, model)` to verify Google-specific field mapping — particularly `ThoughtsTokenCount`, `CachedContentTokenCount`, and the interaction between cache tokens and prompt tokens. The implementation bypasses `mapUsage` entirely: it constructs an `ai.Usage` struct directly and calls `ai.CalculateCost(model, &usage)`. This tests the shared cost arithmetic but does not exercise the Google→common type conversion layer. If `mapUsage` misassigns tokens (e.g., drops `ThoughtsTokenCount` or double-counts `CachedContentTokenCount`), P6 would not catch it.

**Suggested fix**
Change P6 to draw random `usageMetadata` fields (matching the plan's approach) and call `mapUsage(meta, model)` directly. Then verify cost consistency on the returned `ai.Usage`. This is a straightforward change — the plan doc has the exact code pattern at §1 P6.

---

### P3 - SEC1 narrower scope than sibling provider test harnesses

**Location:** `internal/ai/provider/google/harness_test.go:778-797`

**Problem**
SEC1 tests only API key leakage through a single 500 error path. After R1 incorporation, the Anthropic and OpenAI test harnesses expanded their SEC1 tests to include malicious payload classes (XSS, SQL injection, null bytes, etc.) streamed through the full SSE path with JSON validity assertions. The Google harness has the same SSE attack surface but lacks equivalent payload resilience testing.

**Suggested fix**
Consider adding a few representative malicious payload classes to SEC1 (or as a new SEC4) that flow through `buildGeminiTextSSE` → stubserver → provider → event stream, verifying events contain valid JSON and the provider doesn't crash or corrupt state. Low priority since the shared SSE scanner is the same across providers.

---

### P3 - P5 and F2 use fixed cases instead of rapid-based generation

**Location:** `internal/ai/provider/google/harness_test.go:271-301` (P5), `internal/ai/provider/google/harness_test.go:362-386` (F2)

**Problem**
The plan specifies `rapid.Check` for both P5 (camelCase verification with random requests) and F2 (malformed JSON injection at random positions). The implementation uses fixed test cases. This is a pragmatic trade-off — P5's camelCase behavior is struct-tag-driven so random inputs don't add value, and F2's four fixed malformed categories cover the important failure modes.

**Suggested fix**
No change needed. Documenting as a known deviation from the plan. The fixed cases provide sufficient coverage.

---

## Overall Assessment

Strong implementation at 1143 lines (879 harness + 264 benchmark). Coverage is comprehensive:
- **Property tests (P1-P6):** All implemented, P1/P2/P6 with rapid
- **Fault injection (F1-F5):** TCP reset, malformed JSON, HTTP errors, context cancellation races, prompt-level block
- **Simulations (S1-S2):** Stream event ordering via rapid + concurrent streams with content verification
- **Google-specific (GS1-GS5):** Thinking+text+tool with signatures, safety block content discarding, all safety block reasons, multiple tool calls, synthetic ID generation
- **Security (SEC1-SEC3):** API key leakage, thought signature opacity, empty body responses
- **Error classification (EC1):** Both 401 and 403 → ErrAuth confirmed
- **Benchmarks (B1-B4):** SSE throughput, message conversion, request serialization, thought signature encoding
- **Stress/soak (SK1-SK3):** 5000-iteration sequential soak with memory/goroutine leak detection, 200 concurrent streams, 50 concurrent tool call streams with arg verification
- Deferred items (O1, O2, S1 FSM) properly documented with TODO comments

All tests pass with `-race`. `make check` clean.

## Summary

3 findings: 0 P0, 0 P1, 1 P2, 2 P3

**Verdict**: Approved with revisions
