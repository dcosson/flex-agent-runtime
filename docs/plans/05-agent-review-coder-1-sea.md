# Review: 05-agent (coder-1-sea)

- Source doc: `docs/plans/05-agent.md`
- Reviewed commit: 048ca418f610ebe7f4854d04d66cc56916e6d657
- Reviewer: coder-1-sea

## Findings

### P1 - [IG] Plan omits the RuntimeController seam required by architecture

**Problem**
Architecture now defines `RuntimeController` as the top-layer control-plane interface (`docs/plans/00-architecture.md:32-56`), but this plan only specifies `Agent`/`AgentDriver` contracts and does not define how session lifecycle and subscription APIs integrate at the controller seam (`docs/plans/05-agent.md:112-186`, `docs/plans/05-agent.md:271-279`). This leaves a cross-doc wiring gap for implementations in Mode 2/3/4.

**Required fix**
Add explicit `internal/agent` integration contract for controller-facing APIs (or explicit delegation boundaries to another package), including lifecycle ownership, subscription model, and session lookup semantics keyed by runtime session ID.

---

### P1 - [IG] “Per-turn snapshots” conflict with follow-up chaining semantics

**Problem**
The algorithm says follow-ups are applied immediately after turn completion (`docs/plans/05-agent.md:223-236`) while snapshot triggering is tied to `turn_completed` then `state_change(idle)` (`docs/plans/05-agent.md:245-251`). For chained follow-ups, intermediate turns may not transition to idle, so “per-turn snapshot by default” is not actually guaranteed.

**Required fix**
Define snapshot trigger semantics that work even without idle transitions between turns (for example, explicit `turn_completed` trigger independent of idle, with optional idle-coalescing policy). Update acceptance/tests to validate multi-follow-up turn chains still produce intended snapshot boundaries.

---

## Summary

2 findings: 0 P0, 2 P1, 0 P2, 0 P3

**Verdict**: Approved with revisions
