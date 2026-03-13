# Code Review: aiag-mac.1 (R1, reviewer-sea)

- Bead: aiag-mac.1
- Commit range: a007afe..99c1a75
- Plan doc: docs/plans/03-provider-openai.md §1-§9, docs/plans/00-implementation-guide.md §1.1, §1.3
- Reviewer: reviewer-sea
- Review commit: db9a654

## Findings

### P2 - Dead branching in convertMessages: both branches are identical

**Location:** `internal/ai/provider/openai/request.go:82-86`

**Problem**
The `convertMessages` function has a conditional that does the same thing in both branches:
```go
if requiresAssistantAfterToolResult(model) {
    out = append(out, converted...)
} else {
    out = append(out, converted...)
}
```
Both branches execute `out = append(out, converted...)`. The assistant injection logic already lives inside `convertToolResult` (which appends the synthetic assistant message when the flag is set), so this branching in `convertMessages` is dead code that was likely left over from an earlier approach where the injection happened at the caller level.

**Suggested fix**
Remove the `if/else` and just use `out = append(out, converted...)` directly:
```go
case *ai.ToolResultMessage:
    out = append(out, convertToolResult(m, model)...)
```

---

### P3 - toolJSONParser + cloneMap/cloneValue duplicated from Anthropic provider

**Location:** `internal/ai/provider/openai/tool_json_parser.go` (89 lines)

**Problem**
The entire `tool_json_parser.go` file (including `cloneMap` and `cloneValue`) is copied verbatim from the Anthropic provider. When the Google provider lands (aiag-gjz.1), there will be three identical copies. This is acceptable for now since the implementations might diverge per-provider, but it should be extracted to a shared package (e.g., `internal/ai/provider/shared/` or `internal/ai/testutil/`) before the third copy is created.

**Suggested fix**
No change needed now — flag for extraction when Google provider is implemented.

---

### P3 - Text/thinking delta events lack start/end lifecycle framing

**Location:** `internal/ai/provider/openai/sse_parser.go:103-118`

**Problem**
The OpenAI provider emits `EventTextDelta` and `EventThinkingDelta` without corresponding `EventTextStart`/`EventTextEnd` or `EventThinkingStart`/`EventThinkingEnd` events. The Anthropic provider emits start/end events for all content block types because Anthropic's wire format has explicit `content_block_start`/`content_block_stop` framing.

This is an inherent difference in the wire formats — OpenAI doesn't have block boundary events. However, consumers that track content block lifecycle across providers would need to handle this asymmetry. Tool calls correctly get start/end events (since OpenAI has index-based tool call lifecycle).

**Suggested fix**
Document this difference in the implementation guide or the plan doc's §3.4 mapping table. No code change needed — synthesizing fake start/end events would add complexity with unclear benefit.

---

## Plan Compliance Check

| Deliverable | Status |
|---|---|
| provider.go: Provider struct, New(), API() returning 'openai-completions' | ✅ |
| request.go: Message conversion to OpenAI wire format | ✅ |
| request.go: developer role via ModelCompat | ✅ (compat.go:systemRole) |
| request.go: Tool schema with strict mode | ✅ (compat.go:supportsStrictMode) |
| compat.go: ModelCompat flag helpers | ✅ (8 helpers covering all key flags) |
| stream.go: Stream/StreamSimple entrypoints | ✅ |
| stream.go: Provider goroutine with `defer es.Close()` | ✅ (Implementation Guide §1.1) |
| stream.go: Reasoning effort mapping for o-series | ✅ (ReasoningEffortMap lookup) |
| sse_parser.go: SSE event loop with [DONE] sentinel | ✅ |
| sse_parser.go: Per-choice processing | ✅ |
| sse_parser.go: Streaming usage support | ✅ |
| tool_json_parser.go: Index-based multi-tool parsing | ✅ |
| tool_json_parser.go: Deterministic finalization order (sort by index) | ✅ |
| tool_json_parser.go: Strict final parse | ✅ |
| usage.go: OpenAI usage mapping (prompt/completion/total/cached) | ✅ |
| usage.go: Stop reason mapping (length→StopReasonLength, not MaxTokens) | ✅ (Implementation Guide §1.3) |
| errors.go: Error classification (401→ErrAuth, 429→ErrRateLimit, 5xx→ErrServerError) | ✅ |
| errors.go: Context overflow detection | ✅ |
| types_wire.go: Wire structs | ✅ |
| No reverse import from internal/ai into provider | ✅ |
| Unit tests: ModelCompat flags (developer role, strict, max_tokens field, store, reasoning effort, tool result name, assistant injection) | ✅ (7 tests) |
| Unit tests: stop reason mapping, usage mapping, error classification | ✅ |
| Component tests: text stream, tool call, multi-tool, reasoning fixtures | ✅ (4 fixture tests) |
| Component tests: HTTP error classification | ✅ |
| Component tests: StreamSimple reasoning effort mapping | ✅ |
| Component tests: Request capture/headers | ✅ |
| Integration test: network-gated smoke test | ✅ (OPENAI_API_KEY) |
| All tests pass with -race | ✅ (20 pass, 1 skip) |
| make check clean | ✅ |

## Implementation Quality Notes

- **ModelCompat flag dispatch**: Clean helper functions in compat.go with consistent `model.Compat != nil && boolVal(...)` pattern. Safe nil handling throughout.
- **Multi-tool finalization**: `finalizeToolCalls` sorts tool indices before finalizing, ensuring deterministic content block order. This is important for test reproducibility and comparison oracle testing.
- **Reasoning effort mapping**: Smart dual path in StreamSimple — uses `ReasoningEffortMap` for models that support `reasoning_effort` parameter (o-series), falls back to `AdjustMaxTokensForThinking` for models without reasoning effort support.
- **Wire type separation**: Clean request/response split in types_wire.go. `chatMessage.Content` correctly uses `any` type to support both string and `[]contentPart` (OpenAI's polymorphic content field).
- **convertAssistantMessage**: Handles the single-text-as-string vs multi-part-as-array complexity correctly, with proper switching when ThinkingContent blocks force multi-part mode.
- **Test coverage**: Excellent breadth — every ModelCompat flag has a dedicated test, plus 4 fixture streaming tests covering text, tool call, multi-tool, and reasoning paths.

## Summary

3 findings: 0 P0, 0 P1, 1 P2, 2 P3

**Verdict**: Approved with revisions

Strong implementation that follows the Anthropic pattern leader well while correctly handling OpenAI-specific concerns (ModelCompat flags, index-based multi-tool deltas, [DONE] sentinel, reasoning effort mapping). The P2 is a trivial dead code issue. Test coverage is thorough with 20 tests covering all major code paths.
