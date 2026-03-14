# Code Review: aiag-glo.2 (R1, reviewer-sea)

- Bead: aiag-glo.2
- Commit range: 5857621..7b3338d (batch4/rpc-layer-harness)
- Plan doc: docs/plans/13-rpc-layer-test-harness.md
- Reviewer: reviewer-sea
- Review commit: 7b3338d

## Findings

### P3 - SEC2b big params map has only 1 entry due to repeated key

**Location:** `internal/rpc/rpctest/security_test.go:98-102`

**Problem**
The "very large params map" test generates 1000 entries but uses the same key (`strings.Repeat("k", 50)`) each time. Since map keys are unique, the loop overwrites the same entry 1000 times, resulting in a map with 1 entry, not 1000.

```go
bigParams := make(map[string]any)
for i := 0; i < 1000; i++ {
    bigParams[strings.Repeat("k", 50)] = strings.Repeat("v", 100)  // same key every iteration
}
```

**Suggested fix**
Use `fmt.Sprintf("k%d", i)` or `strings.Repeat("k", 50) + fmt.Sprint(i)` for distinct keys.

---

### P3 - P5 sessions accumulate without cleanup across rapid iterations

**Location:** `internal/rpc/rpctest/property_test.go:338-411`

**Problem**
`TestPropertySessionIDAuthority` creates sessions in each rapid iteration but never destroys them. The `testStack` is created once outside `rapid.Check`, so all iterations share it. With the default 100+ rapid iterations, this accumulates sessions. If `DefaultServiceConfig()` has a `MaxSessions` limit, later iterations could fail with `ErrMaxSessionsReached`.

Currently this works because the default config likely has a high or zero limit, but it's fragile.

**Suggested fix**
Add `defer stack.Server.DestroySession(ctx, &api.DestroySessionRequest{SessionID: runtimeID})` after successful creation. The existing comment `_ = stack.Server.DestroySession // cleanup happens via t.Cleanup` is misleading — `t.Cleanup` is not registered for session cleanup.

---

## Overall Assessment

Excellent test harness — 2318 lines across 9 well-organized files covering all plan categories:

- **Property tests (P1-P5):** DTO round-trip fidelity (3 variants), error mapping stability + cause preservation, retry policy soundness + backoff monotonicity + error classification, event stream ordering, session ID authority. All use `pgregory.net/rapid`.
- **Fault injection (F1-F5):** Network flapping, half-open stream stalls, transient overload, malformed payloads (including client-side validation), duplicate delivery (sequential + streaming + concurrent idempotency).
- **Oracle tests (O1-O3):** Golden response structure for all 9 RPC methods, API version propagation, SandboxBackend parity (direct server vs RPC client comparison).
- **Simulation (S1-S3):** Full FSM transitions with invalid state assertions, end-to-end lifecycle (create→tools→turns→rollback→pause/resume→destroy), snapshot workflow with rollback verification, stream reconnection with isolation, multiple concurrent subscribers, concurrent multi-session (10 sessions).
- **Stress tests (ST1-ST3):** Mixed traffic soak (5×50×3 = 750 ops), high-concurrency agents (20×100 = 2000 ops), burst-failure resilience (5 bursts of 50 with fault injection). All behind `testing.Short()` skip.
- **Security (SEC1-SEC4):** Unauthorized session access (all 7 ops), input validation (6 malicious IDs + nil/empty requests), error message safety (no path leakage), DoS controls (duplicate creation), stream close idempotency, sender close semantics.
- **Coverage tests:** 20+ gap-filling tests for edge cases: nil inputs, boundary values, fallback paths, unknown methods, stream lifecycle.
- **Benchmarks (B1-B4):** Unary latency (GetSession, CreateSession, HealthCheck), tool dispatch throughput (server + client), event stream throughput (batched), TurnComplete latency.

Test infrastructure (`testStack`, `assertRPCError`, helpers) is clean and reusable. The `testGVisor` mock with mutex-guarded error injection is well-designed. The B3 benchmark uses a clever batched publish-then-consume pattern to avoid channel overflow.

91.4% coverage exceeds the 85% exit criterion. All tests pass with `-race`.

## Summary

2 findings: 0 P0, 0 P1, 0 P2, 2 P3

**Verdict**: Approved with suggestions
