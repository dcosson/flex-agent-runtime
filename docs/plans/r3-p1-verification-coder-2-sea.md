# R3 P1 Fix Verification — Consolidated Review

**Reviewer:** coder-2-sea
**Round:** 3 (targeted verification of R2 P1 fixes)
**Date:** 2026-03-12

---

## Verification Summary

All 6 plans with R2 P1 findings were reviewed. The P1 fixes are correct. One downstream issue discovered: plan 03 (OpenAI) uses a StopReason constant that doesn't exist in 01-ai-core.

| Plan | R2 P1 Finding | Fix Correct? | Notes |
|------|--------------|:---:|-------|
| 04-provider-google | StopReason mapping mismatch (was P2, verifying alignment) | Yes | `mapFinishReason` correctly uses canonical `ai.StopReasonLength`. See F1 below. |
| 05-agent (test harness) | Snapshot trigger cardinality contradicted follow-up semantics | Yes | P3 rewritten with per-turn `turn_completed` cardinality and per-sequence idle cardinality. Matches §5.5. |
| 10-sandbox-gvisor | `checkOOMKill` could misclassify timeout/cancel exits | Yes | OOM check now gated on `StatusExited + exitCode==137`. Timeout/cancel have separate status codes handled first. Clean. |
| 11-sandbox-host-service | `TurnComplete` returned undefined `turnNum` symbol | Yes | Uses `prospectiveTurn` consistently: computed before snapshot, committed after success, returned in result. Clean. |
| 13-rpc-layer | Server-streaming API returned producer-side type | Yes | `StreamAgentEvents` now returns `AgentEventReceiver`. Type directionality enforced. Clean. |
| 13-rpc-layer | Event taxonomy drifted from canonical agent lifecycle names | Yes | Terminal/lifecycle semantics aligned to `session_started`/`session_ended` with `state_change` transitions. Clean. |
| 14-mode3-e2e | Event assertions referenced non-canonical lifecycle names | Yes | Canonical sequence uses `session_started → ... → session_ended`. Ordering guarantees reference plan 05 taxonomy. Clean. |

---

## Findings

| # | Severity | Plan | Summary |
|---|----------|------|---------|
| F1 | P1 | 03-provider-openai (cross-ref from 04 verification) | OpenAI provider uses non-existent `ai.StopReasonMaxTokens` constant |

### F1 (P1): OpenAI provider references undefined StopReason constant

**Discovered during:** Verification of 04-provider-google R2 F1 fix.

**Problem:** The R2 fix for plan 04 correctly uses `ai.StopReasonLength` for the max-token termination case. Checking 01-ai-core confirms the canonical StopReason constants are:

```go
const (
    StopReasonStop    StopReason = "stop"
    StopReasonLength  StopReason = "length"
    StopReasonToolUse StopReason = "toolUse"
    StopReasonError   StopReason = "error"
    StopReasonAborted StopReason = "aborted"
)
```

There is no `StopReasonMaxTokens`. However, plan 03 (OpenAI) §5.7 `mapStopReason` still maps `"length" → ai.StopReasonMaxTokens`. This would be a compile error during implementation.

**Suggested fix:** In plan 03-provider-openai.md §5.7, change:
```go
case "length":
    return ai.StopReasonMaxTokens
```
to:
```go
case "length":
    return ai.StopReasonLength
```

This is a one-line fix. The original R2 finding correctly identified the cross-provider inconsistency, but the incorporation only clarified the Google side (which was already correct) without fixing the OpenAI side.
