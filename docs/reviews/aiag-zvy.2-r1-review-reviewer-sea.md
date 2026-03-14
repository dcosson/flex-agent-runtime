# Code Review: aiag-zvy.2 (R1, reviewer-sea)

- Bead: aiag-zvy.2
- Commit range: d7b84f6 (single commit on batch4/sandbox-host-harness)
- Plan doc: docs/plans/11-sandbox-host-service-test-harness.md
- Reviewer: reviewer-sea
- Review commit: d7b84f6

## Findings

### P3 - Temp directory leak in `rapidCreateSession`

**Location:** `internal/sandbox/property_test.go:92-95`

**Problem**
`rapidCreateSession` uses `os.MkdirTemp("", "rapid-session-*")` to create temp directories but never cleans them up. Since `rapid.T` does not provide a `TempDir()` method or `Cleanup()` hook, these directories accumulate across rapid iterations. With property tests running 100+ iterations by default, this can leave significant temp directory litter on disk.

**Suggested fix**
Register cleanup via `t.Cleanup(func() { os.RemoveAll(tmpDir) })` after `MkdirTemp`. Alternatively, since `rapid.T` embeds `*testing.T`, the `Cleanup` method should be available. If not, the enclosing `rapid.Check` could use a deferred cleanup list.

---

### P3 - FI6 does not verify HealthCheck degraded status

**Location:** `internal/sandbox/fault_test.go:246-272`

**Problem**
The plan specifies FI6 should verify: "Health check reports unhealthy", "Existing sessions continue working", and "New sessions are rejected if pool is critically full". The implementation verifies that `TurnComplete` fails with pool exhaustion and that Tier 1 tools still work, but never calls `svc.HealthCheck(ctx)` to verify the health status transitions to "degraded" or "unhealthy" as specified in the plan.

Note that `coverage_test.go` does test `HealthCheck` with pool errors (`TestHealthCheckDegraded`), so this path is covered elsewhere. However, FI6 as a fault injection scenario should demonstrate the integrated flow: inject pool exhaustion, then observe the health degradation without clearing the error.

**Suggested fix**
Add a `HealthCheck` call after injecting the snapshot failure to verify the status is reported as "unhealthy" or at minimum document why this specific assertion was deferred.

---

### P3 - ST3 does not verify ActiveTools peak count

**Location:** `internal/sandbox/stress_test.go:110-161`

**Problem**
The plan for ST3 specifies: "ActiveTools count reaches 100 and returns to 0." The implementation verifies the count returns to 0 and that at least some tools succeed, but never samples the ActiveTools count during execution to verify it reaches the expected peak. This means the test could pass even if tool executions were being serialized instead of running concurrently.

**Suggested fix**
Add an atomic max-tracker that records `s.activeTools.Load()` during or after each tool starts, then assert the max observed value equals `burstSize`. This would verify true concurrent execution.

---

### P4 - Non-idiomatic time duration in `rapidCreateSession`

**Location:** `internal/sandbox/property_test.go:78-79`

**Problem**
`cfg.PauseDrainTimeout = 50 * 1e6` uses a float constant multiplication to derive nanoseconds, while the rest of the codebase (e.g., `harness_test.go:157`) uses the idiomatic `50 * time.Millisecond`. The numeric result is identical but the float-based approach is non-obvious and inconsistent.

**Suggested fix**
Change to `50 * time.Millisecond` and `50 * time.Millisecond` for `ShutdownTimeout` to match the pattern in `harness_test.go`.

---

### P4 - P1 shadow model skips rollback error path verification

**Location:** `internal/sandbox/property_test.go:140-153`

**Problem**
When `len(snaps) == 0` and op is Rollback (4), the shadow model's `expectResult(4)` correctly predicts an error (because `snapCount == 0`). However, line 142 `continue`s before the error assertion at line 162, so the "rollback with no snapshots returns error" path is never actually verified by the property test. This is not a false positive (the test never incorrectly passes), but it silently skips a valid error-path assertion.

**Suggested fix**
Instead of `continue`, execute `svc.RollbackSession(ctx, sess.ID, "nonexistent")` and verify it returns an error. Then `continue` to skip `applySuccess`.

---

## Overall Assessment

Excellent test harness with strong coverage across all plan categories. The implementation spans 8 test files covering:

- **Property tests (P1-P5):** All implemented with `pgregory.net/rapid`. P1's shadow state machine is thorough with rollback handling. P2 covers both random and known tool names. P3 properly verifies snapshot ordering and rollback-to-middle semantics. P4 validates concurrent tool safety with active tool counter checks. P5 tests capacity enforcement with create/destroy/re-create cycle.
- **Fault injection (FI1-FI6):** All 6 scenarios implemented. FI1 includes a bonus FI1b (mountpoint failure). FI2 covers crash/OOM/timeout. FI4 properly tests context cancellation with slow mock. FI5 uses a channel-based mock to coordinate tool start/destroy timing.
- **Deterministic simulation (DS1-DS3):** Complete lifecycle (50 tools, 10 turns, rollback, pause/resume, destroy). Concurrent multi-session with 10 sessions. Graceful shutdown with in-flight tools.
- **Benchmarks (B1-B5):** All 5 benchmarks implemented and hitting targets (session create ~2.7us, tier1 ~42us, turn complete ~1.2us, health check ~1.2us, session lookup ~138ns).
- **Stress tests (ST1-ST3):** 50-goroutine/200-op churn, 500-turn soak with periodic rollbacks and MaxSnapshotsPerSession, 100-concurrent burst.
- **Security tests (SEC1-SEC3):** Session isolation with cross-session path traversal, multiple traversal patterns, 7 malicious session IDs including null bytes and shell metacharacters, tool parameter injection (empty bash, whitespace bash, path traversal in read_file/write_file, unknown tool).
- **Coverage tests:** Additional coverage for config defaults, health check, getActiveSession state variants, explicit snapshots, buildTier2Command, labels, blocksToText, totalActiveTools, rollingBack guard, per-tool snapshots, edge cases.

Coverage is 92.7% of statements, exceeding the 85% exit criterion. All tests pass with `-race`. All benchmarks meet specified targets.

The mock infrastructure (`mockGVisor` in `harness_test.go`) is well-designed with support for fault injection, delay simulation, custom run functions, and call tracking. The `createTestSession` helper properly patches mountpoints with real temp directories for Tier 1 execution.

## Summary

5 findings: 0 P0, 0 P1, 0 P2, 3 P3, 2 P4

**Verdict**: Approved with suggestions
