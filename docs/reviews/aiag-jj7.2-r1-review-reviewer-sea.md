# Code Review: aiag-jj7.2 (R1, reviewer-sea)

- Bead: aiag-jj7.2
- Commit range: cb8bae0..ed289f4
- Plan doc: docs/plans/02-provider-anthropic.md §1-§9, docs/plans/00-implementation-guide.md §1.1, §1.3, §4.2
- Reviewer: reviewer-sea
- Review commit: db9a654

## Findings

### P2 - `lastValid` field is dead code (written but never read)

**Location:** `internal/ai/provider/anthropic/tool_json_parser.go:17,37`

**Problem**
The `toolJSONParser` struct has a `lastValid map[string]any` field that is written on every successful partial parse (line 37: `p.lastValid = cloneMap(args)`) but is never read anywhere. `ParseFinal` correctly does strict parsing from raw input without falling back to `lastValid`, matching the plan's requirement (§5.3: "do NOT fall back to lastValid"). However, since this is the **pattern leader** that OpenAI and Google providers will copy, the dead field and writes should be removed now to avoid propagating dead code into all three providers.

**Suggested fix**
Remove the `lastValid` field from the struct and the `p.lastValid = cloneMap(args)` write in `AppendDelta`. This also eliminates one `cloneMap` call per successful partial parse, which is a measurable allocation reduction.

---

### P2 - Missing thinking stream and API error stream fixture tests

**Location:** `internal/ai/provider/anthropic/provider_test.go`

**Problem**
Plan §9.2 specifies four SSE fixture replay test scenarios: plain text stream, thinking stream, tool-use stream, and API error stream. The implementation has text (TestStreamTextFixture) and tool-use (TestStreamToolCallFixture), but is missing:
1. **Thinking stream fixture test** — a fixture with `content_block_start(thinking)` + `thinking_delta` events, verifying `ThinkingContent` accumulation and `EventThinkingStart/Delta/End` emission.
2. **API error stream fixture test** — a fixture with an SSE `error` event mid-stream, verifying it produces a terminal `ProviderError` with correct classification.

These are important for the pattern leader since they exercise code paths in `onBlockStart`/`onBlockDelta`/`onBlockStop` (thinking branch) and the `error` case in `process()` that are currently untested.

**Suggested fix**
Add `TestStreamThinkingFixture` and `TestStreamAPIErrorFixture` tests with appropriate SSE fixture data.

---

### P3 - `cloneValue` default case does unnecessary JSON roundtrip for primitives

**Location:** `internal/ai/provider/anthropic/tool_json_parser.go:77-98`

**Problem**
The `cloneValue` function handles `map[string]any` and `[]any` recursively, then falls through to a default case that does `json.Marshal` + `json.Unmarshal` for every other value. In practice, the "other values" from JSON unmarshaling are Go primitives: `string`, `float64`, `bool`, and `nil`. These are all value types in Go and don't need cloning — they can be returned directly.

The JSON roundtrip adds allocation overhead proportional to the number of primitive values in the tool arguments, which matters during streaming deltas where `cloneMap` is called on every successful partial parse.

**Suggested fix**
Replace the default case with `return x`. Go JSON primitives are value types and safe to share.

---

### P3 - Redundant context overflow check in `providerError`

**Location:** `internal/ai/provider/anthropic/errors.go:28-34`

**Problem**
`providerError` calls `classifyHTTPError(status, msg)` which already checks `IsContextOverflow` for any status that doesn't match the specific HTTP code cases (401/403/429/5xx). Then `providerError` has a second block:
```go
if status == 0 && code == ai.ErrUnknown {
    probe := &ai.AssistantMessage{...}
    if ai.IsContextOverflow(probe, 0) { ... }
}
```
When `status == 0`, `classifyHTTPError` won't match any status-specific case and falls through to the same `IsContextOverflow` check with the same probe. If that check returns false (yielding `ErrUnknown`), the duplicate check in `providerError` will also return false. This block is dead code.

**Suggested fix**
Remove the `if status == 0 && code == ai.ErrUnknown` block.

---

## Plan Compliance Check

| Deliverable | Status |
|---|---|
| provider.go: Provider struct, New(), API() returning 'anthropic-messages' | ✅ |
| request.go: Message conversion to Anthropic wire format | ✅ (user/assistant/tool_result mapping) |
| request.go: Tool schema conversion | ✅ (JSON parameters → input_schema) |
| request.go: System prompt via top-level field | ✅ |
| request.go: Thinking mapping | ✅ (wireThinking with type + budget_tokens) |
| stream.go: Stream/StreamSimple entrypoints | ✅ |
| stream.go: Provider goroutine with `defer es.Close()` | ✅ (Implementation Guide §1.1 contract) |
| sse_parser.go: SSE event dispatch via sseProcessor | ✅ (all 8 event types handled) |
| sse_parser.go: streamAccumulator with ordered content blocks | ✅ |
| sse_parser.go: content_block_start/delta/stop handling | ✅ (text, thinking, tool_use) |
| tool_json_parser.go: streaming-json-go lexer | ✅ (v0.0.4) |
| tool_json_parser.go: Strict final parse (no lastValid fallback) | ✅ |
| usage.go: Anthropic usage mapping (input/output/cache_read/cache_write) | ✅ |
| usage.go: Cost via ai.CalculateCost | ✅ |
| usage.go: Stop reason mapping (end_turn→stop, max_tokens→length) | ✅ (Implementation Guide §1.3) |
| errors.go: HTTP error classification (401→ErrAuth, 429→ErrRateLimit, 5xx→ErrServerError) | ✅ |
| errors.go: Context overflow detection | ✅ (via IsContextOverflow probe) |
| types_wire.go: Wire payload structs | ✅ |
| No reverse import from internal/ai into provider | ✅ (Implementation Guide §5.1) |
| Unit tests: message conversion, stop reason, tool parser | ✅ |
| Component tests: text stream fixture, tool call fixture | ✅ |
| Component tests: thinking stream fixture | ❌ (missing) |
| Component tests: API error stream fixture | ❌ (missing) |
| Integration test: network-gated smoke test | ✅ (ANTHROPIC_API_KEY) |
| All tests pass with -race | ✅ (7 pass, 1 skip) |
| make check clean | ✅ |

## Implementation Quality Notes

- **Provider goroutine pattern**: Clean `defer es.Close()` in the goroutine matches the Implementation Guide §1.1 contract exactly. Error events are sent before the goroutine returns, ensuring terminal-state guarantee.
- **SSE processor architecture**: Well-structured separation — `sseProcessor` handles event dispatch, `streamAccumulator` manages state, `toolJSONParser` handles partial JSON. Each component has a clear responsibility boundary.
- **Stop reason mapping**: Correctly maps `max_tokens → StopReasonLength` (not `StopReasonMaxTokens`, which doesn't exist per Implementation Guide §1.3). Also handles `pause_turn` and empty stop reasons gracefully.
- **Header layering**: `applyHeaders` correctly applies provider defaults, then model-specific headers, then per-request options headers, with later layers overriding earlier ones.
- **Wire type design**: Clean separation between wire types (types_wire.go) and runtime types. The `wireContentBlock` struct is reused for both request and response content blocks, which is pragmatic for Anthropic's API shape.
- **Stub server integration**: Tests use the shared `stubserver` package from jj7.1 — validates the pattern leader chain works.

## Summary

4 findings: 0 P0, 0 P1, 2 P2, 2 P3

**Verdict**: Approved with revisions

Strong pattern leader implementation. The streaming architecture, error classification, and plan compliance are solid. The two P2s should be addressed before other providers copy this pattern: remove dead `lastValid` field to prevent propagating dead code, and add the two missing fixture tests (thinking stream, API error stream) since those code paths need test coverage as reference examples.
