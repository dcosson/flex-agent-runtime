# 05: Agent Loop and Driver Abstraction — R2 Review

**Reviewer:** coder-2-sea
**Round:** 2
**Plan:** [05-agent.md](./05-agent.md)
**Test Harness:** [05-agent-test-harness.md](./05-agent-test-harness.md)

---

## Summary

R1 findings (P1 RuntimeController seam omission, P1 snapshot/follow-up semantics conflict) were properly incorporated. §7.1 adds a comprehensive RuntimeController integration contract. §5.5 clearly distinguishes `turn_completed` (snapshot trigger, per-turn) from `state_change(idle)` (informational, per-chain).

Terminology check: RuntimeController used consistently. No stale references.

The plan is well-structured with clear concurrency rules (§6), event contracts (§3.4), and driver abstraction (§3.2).

## Findings

| # | Severity | Section | Summary |
|---|----------|---------|---------|
| F1 | P1 | Test harness §1 P3 | Snapshot Trigger Cardinality property contradicts plan §5.5 follow-up semantics |

### F1 (P1): Snapshot Trigger Cardinality property is incorrect

**Location:** Test harness §1, Property P3

**Problem:** P3 states the invariant as:

> Each successful turn completion emits exactly one snapshot trigger boundary sequence: `turn_completed` then `state_change(idle)`.

This contradicts §5.5 of the plan, which explicitly states:

> `turn_completed` is the primary snapshot trigger signal [...] `state_change(idle)` is emitted only when the follow-up queue is empty and no more turns will execute.

For a follow-up chain with N turns, the expected event sequence is:
- `turn_completed` (turn 1)
- `turn_completed` (turn 2)
- ...
- `turn_completed` (turn N)
- `state_change(idle)` (only once, after final turn)

The P3 property as written expects every turn to emit both `turn_completed` AND `state_change(idle)`, which would only be true for the final turn. Intermediate turns in a follow-up chain emit only `turn_completed` without `state_change(idle)`.

If implemented as-is, either:
1. The property test always fails for follow-up chains (test is wrong), or
2. The implementation emits `state_change(idle)` after every turn to satisfy the property (violating the plan's intended semantics and potentially triggering premature controller actions).

**Suggested fix:** Rewrite P3 to match the plan's semantics:

> **Invariant:** Each successful turn completion emits exactly one `turn_completed` event. `state_change(idle)` is emitted exactly once after the final turn in a sequence (when the follow-up queue is drained). The total `turn_completed` count equals the number of completed turns. The total `state_change(idle)` count equals the number of complete turn-sequences (1 per prompt/continue cycle, regardless of follow-up chain length).

The check should verify: `count(turn_completed) == completed_turns` AND `count(state_change_idle) == completed_sequences`.
