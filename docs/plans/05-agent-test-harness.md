# 05: Agent Loop and Driver Abstraction — Test Harness

**Companion to:** [05-agent.md](./05-agent.md)
**Scope:** High-assurance tests for agent control flow, concurrency safety, event correctness, and driver parity.

---

## 1. Property-Based Tests

### P1. State Transition Validity

Invariant:
- All emitted state transitions are legal under the agent FSM.
- `Exited` is terminal.

Generator:
- Randomized sequences of operations (`Prompt`, `Steer`, `FollowUp`, `Abort`, provider/tool outcomes).

Check:
- Transition validator accepts every emitted edge.

### P2. Follow-up FIFO Preservation

Invariant:
- Follow-up messages are executed exactly once and in enqueue order.

Generator:
- Random count/content follow-ups interleaved with random provider latencies.

Check:
- observed execution order equals enqueue order.

### P3. Snapshot Trigger Cardinality

Invariant:
- Each successful turn completion emits exactly one snapshot trigger boundary sequence: `turn_completed` then `state_change(idle)`.

Generator:
- Random turns with/without tool calls and with optional follow-up chaining.

Check:
- boundary count equals completed turns.

### P4. Session ID Authority

Invariant:
- Any boundary event used for snapshots/rollback references runtime `Session.ID`, never `DriverSessionID`.

Generator:
- Random session IDs with conflicting driver-native IDs.

Check:
- all control-plane hooks use authoritative runtime ID.

### P5. Abort Idempotency

Invariant:
- Multiple concurrent `Abort()` calls cause a single final terminal outcome and no panic/deadlock.

---

## 2. Fault Injection / Chaos Tests

### F1. Provider stream panic simulation

- Mock provider panics mid-stream.
- Expect safe recovery path: terminal error event and clean idle/exited state.

### F2. Tool hang and timeout

- Mock tool blocks forever; enforce timeout/cancel.
- Expect tool error event + loop recovery without goroutine leak.

### F3. Subscriber panic isolation

- One subscriber panics on every event.
- Expect other subscribers continue receiving events; agent loop remains healthy.

### F4. Driver adapter malformed events

- Feed malformed termmux adapter events.
- Expect dropped/typed error events, not corrupted state.

### F5. Concurrent control-plane storms

- Repeated steer/follow-up/abort calls from many goroutines during streaming.
- Expect no data races and deterministic final state.

---

## 3. Comparison / Oracle Tests

### O1. Native vs Adapter Event Parity

- Use equivalent scripted task traces for NativeDriver and termmux-backed driver adapter.
- Compare normalized event sequences by semantic class (ignoring vendor text deltas).

### O2. Replay Oracle

- Record canonical event log from a successful run.
- Replay through state reducer and compare final session state + metrics to original.

---

## 4. Deterministic Simulation Tests

### S1. Turn engine simulation

- Simulated provider emits deterministic event trace including tool calls and follow-ups.
- Verify loop actions at each step against expected timeline.

### S2. Control-boundary simulation

- Inject steering at every possible boundary in a turn.
- Verify exactly where steering is applied and that no in-flight call is mutated.

### S3. Terminal tool simulation

- Simulate terminal-tool output in the middle of a multi-step plan.
- Verify immediate turn short-circuit and structured terminal event emission.

---

## 5. Benchmarks and Performance Targets

### B1. Event fan-out throughput

Target:
- >= 100k events/sec fan-out to 10 subscribers on baseline dev hardware.

### B2. Prompt-to-first-delta overhead

Target:
- agent-layer overhead < 2ms p95 vs direct provider stream in mock environment.

### B3. Control queue latency

Target:
- steer/follow-up command enqueue-to-apply < 5ms p95 under 100 concurrent goroutines.

### B4. Memory growth bound

Target:
- steady-state long run does not exceed configured conversation retention budget.

---

## 6. Stress and Soak Tests

### ST1. 24-hour multi-agent soak

- 100 concurrent agents with mixed tool and non-tool turns.
- Assertions: no goroutine leak, no unbounded memory growth, stable completion.

### ST2. Race-heavy stress lane

- Run entire component suite with `-race` under repeated random seeds.
- Assertions: zero race reports.

### ST3. Burst follow-up stress

- Single agent with deep follow-up queue bursts (1k+ messages).
- Assertions: FIFO preserved, no starvation/deadlock.

---

## 7. Security Tests

### SEC1. Event payload sanitization

- Ensure emitted events do not leak provider credentials or sensitive env vars.

### SEC2. Tool-result boundary safety

- Inject adversarial tool outputs (very large text, invalid UTF-8, JSON bombs).
- Ensure robust truncation/encoding behavior without crashes.

### SEC3. Driver-native ID trust boundaries

- Validate driver-native session IDs cannot influence snapshot/rollback/session lookup paths.

---

## 8. Manual QA Plan

1. Run a live Anthropic-backed session with streaming output and verify event timeline in logs.
2. During active generation, issue a steering instruction and confirm behavioral redirection.
3. Queue multiple follow-ups and confirm user-visible execution order.
4. Run with sandbox-backed tools and verify one snapshot trigger at each idle turn boundary.
5. Run one session with NativeDriver and one with a termmux-backed driver; compare orchestrator UX parity.

---

## 9. CI Tier Mapping

| Tier | Runs | Contents |
|------|------|----------|
| PR-fast | every PR | unit tests for types/state/control queue/event bus |
| PR-standard | every PR | component tests, deterministic simulations, bounded property runs |
| PR-race | every PR | focused race tests for agent package |
| Nightly | nightly | extended property/chaos suites + benchmark trend collection |
| Weekly | weekly | 24-hour soak + parity oracle + live provider integration |

Credentialed/live tests are secret-gated and skipped for forks.

---

## 10. Exit Criteria

1. Property tests P1-P5 pass across repeated seeds.
2. Fault injection tests F1-F5 pass with no deadlocks/panics/leaks.
3. Native vs adapter oracle tests pass for canonical event parity.
4. Deterministic simulations S1-S3 pass.
5. Benchmark targets B1-B4 are met or have approved regression rationale.
6. Stress and soak suites ST1-ST3 pass in scheduled CI.
7. Security tests SEC1-SEC3 pass.
8. Manual QA checklist completed for release-candidate commit.
9. CI tier matrix wired and green.
