# 04: Google Provider (Gemini) — R2 Review

**Reviewer:** coder-2-sea
**Round:** 2
**Plan:** [04-provider-google.md](./04-provider-google.md)
**Test Harness:** [04-provider-google-test-harness.md](./04-provider-google-test-harness.md)

---

## Summary

R1 finding (P1 safety-blocked responses leaking partial content) was properly incorporated. §5.5 now checks finishReason/safety before processCandidateParts and skips content emission for blocked candidates.

Terminology check: RuntimeController used consistently. No stale references.

The plan correctly identifies that Gemini delivers function calls complete (no streaming JSON lexer needed), which is a clean simplification.

## Findings

| # | Severity | Section | Summary |
|---|----------|---------|---------|
| F1 | P2 | §5.8 `mapFinishReason` | StopReason constant mismatch across providers for "max tokens reached" |
| F2 | P3 | §7.1 `isContextOverflow` | Local context-overflow detection diverges from shared `ai.IsContextOverflow` |

### F1 (P2): StopReason constant mismatch for max-tokens case

**Location:** §5.8 `mapFinishReason`

**Problem:** The Google provider maps `"MAX_TOKENS"` → `ai.StopReasonLength`, while the OpenAI provider (plan 03, §5.7) maps `"length"` → `ai.StopReasonMaxTokens`. These appear to be different `ai.StopReason` constants for the same semantic concept: "the model stopped because it hit the output token limit."

If these are indeed distinct constants in 01-ai-core, any agent loop or RuntimeController code that checks for the max-tokens case would need to check for both constants, which is fragile and error-prone. All three providers should map their respective "hit the token limit" finish reasons to the same canonical `ai.StopReason` constant.

**Suggested fix:** Define one canonical constant in 01-ai-core (e.g., `ai.StopReasonMaxTokens`) and use it consistently. Update whichever provider is using the non-canonical name. If both `StopReasonLength` and `StopReasonMaxTokens` are defined in 01-ai-core, one should be removed or aliased to the other.

### F2 (P3): Local context-overflow detection vs shared utility

**Location:** §7.1 `classifyHTTPError` → `isContextOverflow`

**Problem:** The Google provider defines its own local `isContextOverflow(msg)` function with heuristic string matching, while both Anthropic (§6) and OpenAI (§7.1) providers use the shared `ai.IsContextOverflow(msg)` utility from 01-ai-core. This creates a maintenance risk: if context-overflow detection patterns are updated in the shared utility, the Google provider won't benefit.

**Suggested fix:** Use `ai.IsContextOverflow(msg)` and extend the shared utility if Google-specific error message patterns need to be added.
