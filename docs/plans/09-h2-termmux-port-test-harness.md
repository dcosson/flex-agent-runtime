# 09: Terminal Multiplexer Port from h2 — Test Harness

**Companion to:** [09-h2-termmux-port.md](./09-h2-termmux-port.md)
**Scope:** Deep validation for PTY lifecycle, three-source event normalization, state-machine correctness, panic safety, multi-client behavior, and adapter contracts.

---

## 1. Property-Based Tests

### P1. State Machine Transition Validity

Invariant:
- Randomized event sequences never produce illegal state transitions.
- Terminal states always converge to `Exited` then stop emitting mutating transitions.

### P2. Event Normalization Idempotence

Invariant:
- Equivalent raw OTEL/hook/session-log payload permutations normalize to identical canonical `internal/agent.AgentEvent` sequences (after timestamp normalization).

### P3. Subscriber Isolation

Invariant:
- Slow or blocked subscribers never block monitor progress.
- Event loss is allowed only on slow subscriber channels, never on the core state transition path.

### P4. PTY Write Timeout Safety

Invariant:
- `WritePTY` timeout always resolves with bounded latency.
- Timeout paths never leave `VirtualTerminal.Mu` locked and never deadlock subsequent writes.

### P5. ConfigDir Stable Path Determinism

Invariant:
- `ConfigDirManager.StablePath(sessionID)` is deterministic and collision-free for generated session IDs.

---

## 2. Fault Injection / Chaos Tests

### F1. Panic in Critical Section

- Inject panic while holding VT/session mutex.
- Assert panic recovery executes, lock is released, and session remains operable.

### F2. OTEL Startup Failure

- Force bind failure (`127.0.0.1:0` replacement with occupied socket).
- Assert session start fails fast with explicit error and no goroutine leak.

### F3. Child Hung on Stdin

- Simulate child process not reading stdin.
- Assert write timeout marks hung state, kills child, and transitions session to expected terminal state.

### F4. Out-of-Order Event Bursts

- Inject interleaved OTEL/hook/session-log events with skew and duplicates.
- Assert debouncing and final canonical state are correct.

### F5. Close/Detach Race

- Race multi-client detach operations with session shutdown.
- Assert no panic, no blocked goroutines, and deterministic cleanup.

---

## 3. Deterministic Simulation Tests

### S1. Three-Source Event Multiplexer Simulation

- Feed deterministic timelines with precise interleaving from all sources.
- Compare normalized events against golden expectations.

### S2. Idle Debounce Simulation

- Simulate sub-second activity/no-activity patterns around debounce thresholds.
- Assert no spurious Active/Idle oscillation.

### S3. Interrupt Suppression Simulation (Codex)

- Simulate post-interrupt event storms.
- Assert suppression window blocks invalid transitions while preserving valid terminal events.

### S4. Session Resume Log Conversion (Claude Code)

- Parse native log -> canonical entries -> write native log.
- Assert semantic equivalence and tool-call continuity metadata preservation.

---

## 4. Benchmarks and Targets

### B1. Event Throughput

Target:
- >= 50k normalized events/sec on CI reference runner with no subscriber stalls.

### B2. Attach/Detach Latency

Target:
- p95 attach < 20ms, p95 detach < 10ms with 50 concurrent clients.

### B3. PTY Broadcast Fan-out

Target:
- p95 fan-out delivery < 5ms for 10 subscribers at 4KB message size.

### B4. Tailer Polling Cost

Target:
- CPU overhead for default 500ms poll interval remains < 1% per idle session.

---

## 5. Stress and Soak

### ST1. Long-Run Session Churn

- 12-hour churn: create/attach/detach/stop sessions repeatedly.
- Assert no goroutine, FD, or event-store growth leaks.

### ST2. Multi-Session Parallel Load

- 100 concurrent sessions with mixed event activity.
- Assert stable event ordering, bounded memory, and clean shutdown.

### ST3. Event Storm Resilience

- Burst 1M synthetic raw events over mixed sources.
- Assert monitor remains responsive and maintains invariants.

---

## 6. Security and Isolation Tests

### SEC1. OTEL Endpoint Scope

- Verify per-session OTEL collector only accepts/processes events for that session context.

### SEC2. Session Log Path Containment

- Tailer must reject path traversal and only read configured session log path.

### SEC3. ConfigDir Isolation

- Session config directories are isolated by session ID and permissions.

---

## 7. Manual QA Plan

1. Launch a real Claude Code session, attach two clients, and verify both receive coherent streamed output.
2. Force a panic injection build and verify runtime recovers and emits actionable error diagnostics.
3. Kill child process mid-turn and verify session transitions to `Exited` without hanging attach clients.
4. Validate preserved failure artifacts (event log + raw payload traces) are sufficient for replay.

---

## 8. CI Tier Mapping

| Tier | Trigger | Contents |
|------|---------|----------|
| T1-fast | every PR | state-machine properties, adapter unit tests, panic/lock safety |
| T2-integration | PR merge/nightly | OTEL/hook/session-log integration, PTY lifecycle, multi-client attach/detach |
| T3-stress | nightly | session churn, multi-session load, event storm |
| T4-release | release candidate | long soak + manual QA checklist |

---

## 9. Exit Criteria

1. Property tests P1-P5 pass with deterministic seeds in CI.
2. Chaos tests F1-F5 pass with no deadlocks or leaks.
3. Simulation tests S1-S4 pass against golden outputs.
4. Benchmarks B1-B4 meet targets or have approved waivers.
5. Stress tests ST1-ST3 complete without invariant violations.
6. Security tests SEC1-SEC3 pass.
7. Manual QA checklist completed for release candidate.

## Review Disposition

| # | Reviewer | Severity | Summary | Disposition | Notes |
|---|----------|----------|---------|-------------|-------|
| 1 | coder-2-sea | P0 | Companion test harness doc missing | Incorporated | Created this document with property, chaos, simulation, benchmark, stress, and security coverage. |
