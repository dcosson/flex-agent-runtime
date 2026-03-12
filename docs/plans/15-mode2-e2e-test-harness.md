# 15: Mode 2 E2E — Test Harness

**Companion to:** [15-mode2-e2e.md](./15-mode2-e2e.md)
**Scope:** High-assurance validation for sandboxed 3rd-party driver execution, terminal-mux lifecycle, event normalization, credential/config handling, and idle-snapshot semantics.

---

## 1. Property-Based Tests

### P1. Session Lifecycle Invariant

Invariant:
- Terminal mux session transitions follow valid lifecycle graph (created -> active/idle -> paused/resumed -> exited).

### P2. Event Normalization Consistency

Invariant:
- Equivalent semantic driver activity represented through different source combinations (OTEL/hooks/log) normalizes to equivalent canonical event classes.

### P3. Config Path Stability

Invariant:
- For a given logical runtime session, computed config directory path is stable across pause/resume/relaunch.

### P4. Idle Snapshot Trigger Invariant

Invariant:
- Each idle boundary intended for snapshotting produces exactly one snapshot trigger marker.

### P5. Attach/Detach Safety

Invariant:
- Attach/detach operations do not alter driver semantic state beyond connection metadata.

---

## 2. Fault Injection / Chaos Tests

### F1. PTY write timeout / hung child

- Simulate child process stop-reading behavior.
- Verify timeout detection and controlled teardown.

### F2. Event-source partial failure

- Disable one source at a time (OTEL, hooks, session-log tail).
- Verify normalizer degrades gracefully and still emits critical milestones.

### F3. Credential injection failure

- Corrupt/missing injected config files.
- Verify predictable launch failure diagnostics.

### F4. Pause/resume race under output burst

- Rapid pause/resume while high-volume output is flowing.
- Verify no deadlock and consistent final state.

### F5. Driver crash mid-turn

- Force abrupt CLI exit.
- Verify terminal events, artifact capture, and recovery path behavior.

---

## 3. Comparison / Oracle Tests

### O1. Source-fusion oracle

- Compare normalized event stream against hand-labeled ground truth traces for representative sessions.

### O2. Driver parity oracle

- Run equivalent scripted scenario on Claude and Codex adapters.
- Compare normalized **lifecycle semantics** (launch, idle detection, pause/resume, exit) rather than task-level outcomes. These lifecycle patterns should be consistent regardless of the driver's conversation model (e.g., Claude's multi-turn vs. Codex's limited-turn interaction models).
- Driver-specific text differences and task-level behavioral differences are expected and excluded from comparison.

### O3. Mode 1/Native reference oracle

- Compare canonical milestone patterns against analogous NativeDriver scenario for shared semantics.

---

## 4. Deterministic Simulation Tests

### S1. Session-log replay simulation

- Replay captured driver logs through normalizer and verify deterministic outputs.

### S2. Lifecycle command simulation

- Simulate attach/detach/pause/resume/stop command sequences and verify expected state transitions.

### S3. Config-dir migration simulation

- Simulate path changes and verify auth invalidation behavior is detected and surfaced clearly.

---

## 5. Benchmarks and Performance Targets

### B1. Event normalization latency

Target:
- p95 normalization lag < 200ms from raw input to canonical event emission.

### B2. PTY throughput

Target:
- sustain high-output driver sessions without dropped frames beyond configured buffer policy.

### B3. Attach latency

Target:
- attach/reattach operations complete p95 < 150ms in test env.

### B4. Idle snapshot trigger latency

Target:
- idle detection to snapshot trigger signal p95 < 500ms.

---

## 6. Stress and Soak Tests

### ST1. Long-running driver soak

- 12-hour continuous driver sessions in sandbox with periodic control actions.
- Assertions: no leaks, stable event processing.

### ST2. Multi-session mode2 stress

- Run many concurrent Mode 2 sessions across drivers.
- Assertions: session isolation, stable PTY operations.

### ST3. Burst lifecycle stress

- Rapid attach/detach/pause/resume commands across many sessions.
- Assertions: bounded failure rate and clean recovery.

---

## 7. Security Tests

### SEC1. Credential isolation

- Ensure injected credentials/config for one session are inaccessible to others.

### SEC2. Config-dir permission checks

- Verify config dirs created with restrictive permissions and no broad read exposure.

### SEC3. Session-log sanitization

- Ensure sensitive fields in logs/events/artifacts are redacted.

### SEC4. PTY input/output boundary hardening

- Fuzz control sequences and malformed terminal streams to ensure parser robustness.

---

## 8. Manual QA Plan

1. Launch real 3rd-party driver in sandbox and verify interactive behavior through PTY attach.
2. Validate credential/config injection by running an authenticated action.
3. Observe normalized events in real time and compare against visible CLI behavior.
4. Pause/resume a session and verify continuity without re-auth.
5. Force driver crash and validate diagnostics + recovery behavior.

---

## 9. CI Tier Mapping

| Tier | Runs | Contents |
|------|------|----------|
| PR-fast | every PR | deterministic replay tests + lifecycle simulation smoke |
| PR-standard | every PR | deterministic mode2 matrix incl. normalization + config tests |
| Nightly | nightly | chaos injections + driver parity oracle + benchmarks |
| Weekly | weekly | real-driver sandbox lane + soak/stress tests |

---

## 10. Exit Criteria

1. Property tests P1-P5 pass across repeated seeds.
2. Fault injection tests F1-F5 pass with expected diagnostics.
3. Oracle tests O1-O3 pass or approved differences are documented.
4. Deterministic simulations S1-S3 pass.
5. Benchmark targets B1-B4 are met or approved exceptions recorded.
6. Stress/soak tests ST1-ST3 pass in scheduled CI.
7. Security tests SEC1-SEC4 pass.
8. Manual QA checklist completed for release-candidate commit.
