# 15: Mode 2 E2E — Review Findings (reviewer-sea, R1)

**Plan:** [15-mode2-e2e.md](./15-mode2-e2e.md)
**Test Harness:** [15-mode2-e2e-test-harness.md](./15-mode2-e2e-test-harness.md)
**Reviewer:** reviewer-sea
**Round:** 1
**Date:** 2026-03-12

---

## Summary

The plan covers Mode 2's unique concerns well: driver launch, credential injection, event normalization, and PTY lifecycle. However, there is a critical dependency error in the plan header, the event normalization conflict resolution policy is missing, and the deterministic driver simulator mechanism needs specification to make the PR-fast lane viable.

## Findings

### [F1] Dependency references non-existent plan "09-h2-termmux-port" — P1

**Section:** Plan header (Depends on)
**Issue:** The dependency header says `Depends on: 09-h2-termmux-port, 11-sandbox-host-service`. In the plan index (00-plan-index.md), plan 09 is "sandbox-zfs," not "h2-termmux-port." There is no plan in the index for the terminal mux / termmux component. This means either: (a) the dependency reference is wrong and should point to a different plan, or (b) a plan for `internal/termmux` is missing from the plan index entirely. Given that Mode 2 fundamentally depends on the terminal mux for PTY session management and event normalization, a missing plan for this component is a significant gap.
**Recommendation:** Determine the correct dependency. If `internal/termmux` is part of h2-orchestrator and outside the scope of this runtime, document that explicitly and define the interface contract that Mode 2 E2E tests will code against. If it should be a plan in this project, add it to the plan index and write the plan before implementing Mode 2 E2E tests.

### [F2] Event normalization conflict resolution policy is missing — P2

**Section:** Plan §4.2 (Event Normalization Correctness)
**Issue:** The plan describes mixed-source event normalization from three sources (OTEL, hooks, session-log tail) but doesn't define what happens when sources disagree. Real-world scenarios where this matters: OTEL reports tool_completed but session-log still shows output streaming (timing skew). Hooks report idle but OTEL shows ongoing activity. Session-log shows a crash but OTEL is silent (process died before sending telemetry). Without a conflict resolution policy, the normalizer will produce inconsistent events depending on which source is processed first.
**Recommendation:** Define a source priority hierarchy and conflict resolution rules. Suggested approach: (1) OTEL is highest fidelity when available (structured, timestamped). (2) Hooks are second (reliable delivery but coarser granularity). (3) Session-log is fallback (always available but requires parsing). When sources conflict, the higher-priority source wins. When a source is silent, degrade gracefully using lower-priority sources with explicit "confidence" markers on normalized events.

### [F3] Deterministic driver simulator is unspecified — P2

**Section:** Plan §7.1 (Deterministic Mode)
**Issue:** The PR-fast lane depends on "deterministic driver simulators/replayed logs" but the plan doesn't define: how the simulator works, what interface it implements, how realistic it needs to be, or what format replayed logs use. Without this, implementors will create ad-hoc mocks that may not exercise the real normalization paths. Key questions: Does the simulator produce real PTY output? Does it emit OTEL spans? Does it respond to terminal input?
**Recommendation:** Specify the simulator as a concrete component: a process that reads a replay script (captured from a real driver session) and replays PTY output, OTEL spans, and hook callbacks at recorded timestamps. The script format should be defined (e.g., JSON lines with `{timestamp, source, data}` tuples). This enables recording real driver sessions and replaying them deterministically.

### [F4] Missing plan for `internal/termmux` component — P2

**Section:** Plan §5 (Connected Components)
**Issue:** The Connected Components table references `internal/termmux` for PTY/session control, event normalization, and driver-specific parsers. But there is no plan document in the plan index that defines the termmux component's design, interfaces, or implementation. Mode 2 E2E tests can't be written without knowing the termmux API contracts: how to launch a driver, how to attach/detach, what the normalized event interface looks like.
**Recommendation:** Either (a) create a plan doc for `internal/termmux` covering PTY session management, event normalization, and driver adapters, or (b) if termmux is owned by h2-orchestrator, define the interface contract in this plan so Mode 2 E2E tests have something concrete to code against.

### [F5] Config path stability definition is vague — P3

**Section:** Plan §4.5 (Config Directory Persistence)
**Issue:** The scenario says "per-session stable path" and "path-sensitive auth tokens remain valid" but doesn't define: what the path format is (e.g., `/sandbox/config/<session-id>/`?), whether "stable" means across host restarts, whether it survives ZFS snapshot/rollback cycles (config dir may be inside or outside the ZFS dataset), or what happens if the host's filesystem layout changes.
**Recommendation:** Define the config directory path scheme explicitly: e.g., `<sandbox-data-dir>/configs/<session-id>/`. Specify whether this is inside the ZFS dataset (snapshottable) or on a separate filesystem (persistent across rollbacks). If inside ZFS, note that rollback will revert config changes too (which may break auth tokens modified during the session).

### [F6] Test harness O2 (Driver parity oracle) may not be practically testable — P3

**Section:** Test Harness §3, O2
**Issue:** O2 requires running "equivalent scripted scenario on Claude and Codex adapters" and comparing normalized milestone semantics. But Claude Code and Codex have fundamentally different interaction models (Claude: prompt → multi-turn conversation with tool calls; Codex: one-shot or limited-turn). Defining truly "equivalent" scenarios across both drivers may not be feasible, making this oracle brittle or trivial.
**Recommendation:** Scope O2 to compare the lifecycle semantics (launch, idle detection, pause/resume, exit) rather than task-level outcomes. These lifecycle patterns should be consistent regardless of the driver's conversation model.

---

## Statistics

- Total findings: 6
- P0 (blocking): 0
- P1 (significant): 1
- P2 (moderate): 3
- P3 (minor): 2
