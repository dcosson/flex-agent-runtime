# 14: Mode 3 E2E — Test Harness

**Companion to:** [14-mode3-e2e.md](./14-mode3-e2e.md)
**Scope:** High-assurance harness for distributed Mode 3 behavior: RPC dispatch, remote lifecycle controls, snapshots, rollback, pause/resume, and event streaming.

---

## 1. Property-Based Tests

### P1. Lifecycle State Invariant

Invariant:
- Session lifecycle transitions over RPC follow legal FSM (`created -> active/paused -> destroyed`).

### P2. Snapshot Monotonicity

Invariant:
- Snapshot identifiers for a session are unique and monotonically ordered by creation sequence.

### P3. Rollback Consistency

Invariant:
- Rolling back to snapshot `S` restores workspace hash to hash captured at `S`.

### P4. Remote Dispatch Integrity

Invariant:
- Every tool invocation in Mode 3 produces exactly one corresponding ExecuteTool RPC call with matching tool_call_id.

### P5. Event/RPC Correlation

Invariant:
- `tool_started`/`tool_completed` events correlate 1:1 with ExecuteTool RPC spans for same session/tool_call_id.

---

## 2. Fault Injection / Chaos Tests

### F1. RPC transient failure during tool call

- Inject `unavailable`/timeout errors and verify retry and final behavior.

### F2. Host restart during active session

- Simulate sandbox-host restart and test reconnection/recovery path.

### F3. Rollback race with concurrent tool request

- Issue rollback while tool call is in flight.
- Validate deterministic conflict handling and no state corruption.

### F4. Pause/resume race conditions

- Rapid pause/resume requests during multi-turn runs.
- Validate final state correctness and no orphaned session handles.

### F5. Event stream interruption

- Drop stream connections mid-run and validate re-subscription behavior.

---

## 3. Comparison / Oracle Tests

### O1. Mode 1 vs Mode 3 semantic parity oracle

- Run equivalent scenarios in local and remote modes.
- Compare semantic outcomes (file diffs, tool sequence classes, terminal state).

### O2. Snapshot oracle

- Cross-check snapshot list/results against expected mutation timeline from scenario script.

### O3. Lifecycle oracle

- Replay recorded lifecycle traces and verify no divergence in state transitions.

---

## 4. Deterministic Simulation Tests

### S1. Distributed timeline simulation

- Simulate ordered RPC + event timeline and validate expected milestones.

### S2. Rollback branching simulation

- Simulate divergent post-rollback execution branches and verify deterministic restored baseline.

### S3. Session recovery simulation

- Simulate interrupted runs resumed from snapshot and verify end-state equivalence.

---

## 5. Benchmarks and Performance Targets

### B1. End-to-end tool dispatch latency

Target:
- Added Mode 3 RPC overhead p95 <= 10ms over Mode 1 baseline for Tier 1 calls (excluding host tool execution time).

### B2. Lifecycle API latency

Target:
- create/pause/resume/destroy operations p95 <= 100ms in test environment.

### B3. Rollback latency

Target:
- rollback-to-ready p95 <= 500ms for small fixture datasets.

### B4. Event stream throughput

Target:
- stable delivery of >= 5k events/minute per session with bounded memory.

---

## 6. Stress and Soak Tests

### ST1. Multi-session distributed stress

- Run many concurrent Mode 3 sessions with mixed Tier 1/2 tool workloads.
- Assertions: stable completion and bounded error rates.

### ST2. Long-running mode3 soak

- 12-hour continuous scenario rotation with periodic pause/resume/rollback actions.
- Assertions: no leaks, stable latencies, no snapshot metadata corruption.

### ST3. Burst recovery stress

- Repeated transient outage bursts on RPC path.
- Assertions: controlled retry behavior and recovery without cascade failures.

---

## 7. Security Tests

### SEC1. Session isolation

- Ensure RPC calls cannot access another session's workspace/snapshots.

### SEC2. Snapshot authorization checks

- Verify rollback/list operations require valid session ownership context.

### SEC3. Transport hardening

- Validate TLS/auth hooks and rejection of malformed/oversized payloads.

### SEC4. Sensitive metadata handling

- Ensure logs/artifacts redact credentials and secrets in RPC payload paths.

---

## 8. Manual QA Plan

1. Run a full Mode 3 workflow and inspect end-to-end timeline (agent events + RPC calls).
2. Pause/resume session manually mid-task and verify continuity.
3. Roll back to an earlier snapshot and verify restored repository state by inspection.
4. Force transient RPC failures and confirm visible recovery behavior.
5. Compare one Mode 1 run and one Mode 3 run for same task and verify semantic parity.

---

## 9. CI Tier Mapping

| Tier | Runs | Contents |
|------|------|----------|
| PR-fast | every PR | deterministic fake-host Mode 3 smoke (happy path + lifecycle basics) |
| PR-standard | every PR | deterministic full Mode 3 scenario matrix incl. rollback/pause-resume |
| Nightly | nightly | chaos injection suite + parity oracle + benchmark collection |
| Weekly | weekly | real-host integration + soak/stress lanes |

---

## 10. Exit Criteria

1. Property tests P1-P5 pass consistently.
2. Fault injection tests F1-F5 pass with expected behavior.
3. Oracle tests O1-O3 pass or accepted divergences are documented.
4. Deterministic simulations S1-S3 pass.
5. Benchmark targets B1-B4 are met or approved exceptions recorded.
6. Stress/soak tests ST1-ST3 pass in scheduled CI.
7. Security tests SEC1-SEC4 pass.
8. Manual QA checklist completed for release-candidate commit.
