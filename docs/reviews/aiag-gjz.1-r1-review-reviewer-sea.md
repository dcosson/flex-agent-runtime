# Code Review: aiag-gjz.1 (R1, reviewer-sea)

- Bead: aiag-gjz.1
- Commit range: c0f2553 (single commit)
- Plan doc: `docs/plans/02-provider-google.md`
- Reviewer: reviewer-sea
- Review commit: c0f2553

## Findings

### P2 - Missing 401 (UNAUTHENTICATED) error classification

**Location:** `internal/ai/provider/google/errors.go:13`

**Problem**
`classifyHTTPError` maps 403→ErrAuth but does not map 401→ErrAuth. Google Gemini API returns 401 (UNAUTHENTICATED) for invalid/missing API keys, distinct from 403 (PERMISSION_DENIED) for valid keys without sufficient permissions. Both Anthropic and OpenAI providers use `status == 401 || status == 403` for ErrAuth (see `internal/ai/provider/anthropic/errors.go:13` and `internal/ai/provider/openai/errors.go:13`). Currently a 401 falls through to `ErrUnknown`.

**Suggested fix**
Change `case status == 403:` to `case status == 401 || status == 403:` to match the other providers.

---

### P3 - thoughtsTokenCount parsed but silently discarded

**Location:** `internal/ai/provider/google/usage.go:5-14`, `internal/ai/provider/google/types_wire.go:115`

**Problem**
`usageMetadata.ThoughtsTokenCount` is parsed from the wire (types_wire.go:115) but `mapUsage` does not map it to any `ai.Usage` field. `ai.Usage` currently lacks a reasoning/thoughts token field, so the data is silently lost. The test at provider_test.go:96-97 sets `ThoughtsTokenCount: 20` but doesn't verify it's captured anywhere — which is correct given the current `ai.Usage` struct, but the silent discard could be surprising.

**Suggested fix**
Add a comment in `mapUsage` noting that `ThoughtsTokenCount` is intentionally unmapped because `ai.Usage` has no corresponding field yet. When `ai.Usage` gains a reasoning tokens field, this should be mapped.

---

### P3 - Safety block fixture test doesn't verify content is not leaked

**Location:** `internal/ai/provider/google/provider_test.go:425-440`

**Problem**
`TestStreamSafetyBlockFixture` sends a partial text chunk ("partial") followed by a safety block chunk. It correctly verifies an error is returned, but doesn't verify that the final assembled message contains no text content. The safety pre-emission blocking (sse_parser.go:88-96) is a key correctness invariant — the test should verify it more explicitly. Note: streaming text deltas from earlier chunks are inherently unretractable, but the test should verify the final `AssistantMessage` (if any) doesn't contain leaked content.

**Suggested fix**
The test already works correctly since `Drain()` returns an error and the message is nil/incomplete. However, to make the safety invariant explicit, consider collecting events (instead of just `Drain()`) and asserting that no `EventDone` with text content was emitted after the safety block.

---

### P3 - HTTP error fixture test doesn't verify ProviderError code

**Location:** `internal/ai/provider/google/provider_test.go:458-473`

**Problem**
`TestHTTPErrorClassification` tests end-to-end that a 403 response produces an error, but only checks `err != nil`. It doesn't verify that the `ProviderError` has code `ErrAuth`. The unit test `TestErrorClassification` does check codes, but the end-to-end fixture test should verify the code propagates correctly through the full stream→error→event pipeline.

**Suggested fix**
Assert that the error is a `*ai.ProviderError` with `Code == ai.ErrAuth` (or check the error event from the stream contains the expected code).

---

### P3 - thinkingLevel field in requestParams is unreachable

**Location:** `internal/ai/provider/google/request.go:13`, `request.go:58-63`

**Problem**
The `thinkingLevel` field in `requestParams` and its code path in `buildGenerationConfig` (lines 58-63) are never populated by any caller. `Stream()` passes zero-value `requestParams{}`, and `StreamSimple()` only sets `thinkingBudget`. The `thinkingLevel` branch in `buildGenerationConfig` is dead code.

**Suggested fix**
If intentionally forward-looking for future thinking level support, add a brief comment noting it's not yet wired up. If not needed, remove the field and the code path. Dead code that passes `make check` and staticcheck is harmless but can be confusing.

---

## Summary

5 findings: 0 P0, 0 P1, 1 P2, 4 P3

**Verdict**: Approved with revisions

The Google Gemini provider is well-implemented. The parts-based content model, safety pre-emission blocking, thought signature attachment, and complete function call handling (no streaming JSON parser needed) are all correct. Test coverage is thorough with 27 tests including 7 fixture streaming tests. The single P2 (missing 401 mapping) is a straightforward fix for cross-provider consistency. The P3s are minor improvements to test rigor and code hygiene.
