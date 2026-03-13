# Code Review: aiag-aps (R1, reviewer-sea)

- Bead: aiag-aps
- Commit range: bd8ab86 (on branch batch2-followup/compat-fixes)
- Plan doc: docs/plans/04-provider-google.md
- Reviewer: reviewer-sea
- Review commit: 82f3385

## Findings

### P3 - mapThinkingLevel returns empty string for unrecognized levels

**Location:** `internal/ai/provider/google/stream.go:44-57`

**Problem**
The `default` case in `mapThinkingLevel` returns `""`. Since `ClampReasoning` already handles the xhigh→high clamping, the only way to reach the default is with `ThinkingNone` or an entirely unknown level. The empty string gets written into `thinkingConfig.ThinkingLevel` in the request JSON. Gemini will likely ignore an empty string, but it would be marginally cleaner to either omit the field (use a pointer) or explicitly return `"THINKING_LEVEL_NONE"`.

**Suggested fix**
Low priority. The current behavior is acceptable — reasoning models with `ThinkingNone` is an edge case (if reasoning is disabled, the `model.Reasoning` check in `StreamSimple` prevents entering this path entirely). No change needed.

---

## Summary

1 findings: 0 P0, 0 P1, 0 P2, 1 P3

**Verdict**: Approved

**Review notes:**
- `mapThinkingLevel()` correctly uses `ai.ClampReasoning()` to normalize xhigh→high before mapping
- Minimal→LOW mapping is a reasonable choice given Gemini has no "minimal" tier, with clear comment explaining the decision
- `thinkingLevel` field is cleanly threaded through `requestParams` → `buildGenerationConfig` → wire format
- Image finish reasons (`IMAGE_SAFETY`, `IMAGE_PROHIBITED_CONTENT`, `IMAGE_RECITATION`) are consistently added across all three touch points: `isSafetyBlock()`, `mapFinishReason()`, and harness tests (P3, GS3)
- `TestStreamSimpleThinkingLevelMapping` is well-structured: covers all 5 levels with table-driven tests, verifies the actual wire format string sent to the API
- The existing `TestStreamSimpleThinkingBudget` is correctly extended to also verify the thinkingLevel field alongside the budget
- `TestIsSafetyBlock` refactored from individual assertions to table-driven — cleaner and covers all 8 blocked + 3 non-blocked reasons
- Race detector passes clean
