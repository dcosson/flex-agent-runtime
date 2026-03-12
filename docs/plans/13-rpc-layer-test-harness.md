# 13: RPC Layer — Test Harness

**Companion to:** [13-rpc-layer.md](./13-rpc-layer.md)
**Scope:** High-assurance tests for protocol correctness, streaming reliability, retries, and compatibility.

---

## 1. Property-Based Tests

### P1. DTO Round-trip Fidelity

Invariant:
- Domain request/response mapped to transport and back remains semantically equivalent.

### P2. Error Mapping Stability

Invariant:
- Transport errors map deterministically to canonical error classes.

### P3. Retry Policy Soundness

Invariant:
- Only retryable classes trigger retries; non-retryable classes never retried.

### P4. Event Stream Ordering

Invariant:
- Per-stream event order is preserved end-to-end.

### P5. Session ID Authority

Invariant:
- All RPC calls and stream envelopes use runtime session ID fields consistently.

---

## 2. Fault Injection / Chaos Tests

### F1. Network flapping

- Inject connection resets and short partitions.
- Validate reconnect behavior and bounded retry storms.

### F2. Half-open stream stalls

- Simulate stalled stream producer/consumer.
- Ensure deadlines and cancellation close resources cleanly.

### F3. Server-side transient overload

- Return `unavailable` bursts.
- Validate backoff/jitter behavior and eventual recovery.

### F4. Malformed payload injection

- Corrupt transport payloads.
- Validate decode failures produce typed errors without panics.

### F5. Duplicate delivery scenarios

- Simulate retries around ambiguous completion windows.
- Validate idempotency expectations and duplicate handling strategy.

---

## 3. Comparison / Oracle Tests

### O1. Contract golden tests

- Golden request/response fixtures for each RPC method.
- Ensure backward-compatible field behavior.

### O2. Cross-version compatibility oracle

- Matrix test old client/new server and new client/old server.
- Validate negotiated compatibility and graceful rejection where unsupported.

### O3. SandboxBackend parity oracle

- Compare direct in-process ToolBackend results against RPC-mediated SandboxBackend results over deterministic fake host.

---

## 4. Deterministic Simulation Tests

### S1. Session lifecycle FSM simulation

- Simulate create/pause/resume/destroy transitions and invalid transitions.
- Verify RPC error codes match expected state-machine outcomes.

### S2. Snapshot workflow simulation

- Simulate snapshot lists and rollback sequences across tool operations.
- Verify snapshot IDs propagated end-to-end.

### S3. Stream reconnection simulation

- Simulate producer restarts and stream re-subscriptions.
- Verify consumer behavior and loss-handling policy.

---

## 5. Benchmarks and Performance Targets

### B1. Unary RPC latency

Target:
- p95 unary call overhead < 5ms in local integration environment.

### B2. Tool dispatch throughput

Target:
- >= 2k lightweight ExecuteTool RPC/s on benchmark host with fake backend.

### B3. Streaming throughput

Target:
- >= 100k small AgentEvent envelopes/minute per stream with bounded memory.

### B4. Retry overhead

Target:
- under induced 5% transient failure rate, throughput degradation < 20%.

---

## 6. Stress and Soak Tests

### ST1. 24-hour RPC soak

- Continuous mixed method traffic + event streaming.
- Assertions: no goroutine leaks, no fd leaks, stable latency percentiles.

### ST2. High-concurrency agent simulation

- Simulate many concurrent agent sessions dispatching tools via RPC.
- Assertions: stable connection pool behavior and bounded resource usage.

### ST3. Burst-failure resilience

- Repeated transient outage bursts.
- Assertions: controlled recovery without cascading retry amplification.

---

## 7. Security Tests

### SEC1. AuthN/AuthZ enforcement hooks

- Verify unauthenticated/unauthorized requests are rejected consistently.

### SEC2. Input validation hardening

- Fuzz/invalid payloads for all methods; ensure robust validation and no panics.

### SEC3. Sensitive data redaction

- Ensure logs/traces redact secrets and do not leak token-bearing headers.

### SEC4. DoS controls

- Validate max message sizes, stream limits, and timeout enforcement.

---

## 8. Manual QA Plan

1. Run RPC smoke workflow: create session -> execute tool -> list snapshots -> rollback -> destroy.
2. Observe live event stream during an active agent run and verify expected timeline.
3. Inject transient failures and verify retry/backoff behavior in logs.
4. Validate that disabling RPC endpoint auth causes expected access denials.
5. Confirm no MCP dependency in sandbox backend RPC path by inspecting module wiring and runtime behavior.

---

## 9. CI Tier Mapping

| Tier | Runs | Contents |
|------|------|----------|
| PR-fast | every PR | unit mapping/error/retry tests + core unary RPC roundtrips |
| PR-standard | every PR | component stream tests + deterministic simulations |
| PR-race | every PR | focused concurrent client/server race suite |
| Nightly | nightly | chaos injections + compatibility matrix + benchmarks |
| Weekly | weekly | soak tests and high-concurrency stress |

---

## 10. Exit Criteria

1. Property tests P1-P5 pass across repeated seeds.
2. Fault injection tests F1-F5 pass with expected recovery behavior.
3. Oracle tests O1-O3 pass and compatibility expectations are documented.
4. Deterministic simulations S1-S3 pass.
5. Benchmark targets B1-B4 are met or approved exceptions recorded.
6. Stress/soak tests ST1-ST3 pass in scheduled CI.
7. Security tests SEC1-SEC4 pass.
8. Manual QA checklist completed for release-candidate commit.
