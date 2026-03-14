# Review: aiag-xmq.2 -- gVisor Container Lifecycle, GVisorManager impl + test harness

**Reviewer:** reviewer-sea
**Round:** R1
**Branch:** batch4/gvisor-lifecycle @ 9bcf7e0 (+ 482ad28 bead update)
**Plan doc:** docs/plans/10-sandbox-gvisor.md
**Date:** 2026-03-13

---

## Summary

The implementation delivers the GVisorManager interface, container lifecycle (Run/Close/CleanupStale/ActiveContainers), OCI spec builder, cgroup v2 translation, bundle management, health monitoring, and a mock-based test suite with property tests and benchmarks. All 60+ tests pass with `-race`, no data races detected.

Overall verdict: **Approve with findings.** The core architecture is sound, the code is well-structured, and test coverage is strong. Several findings below should be addressed before merge.

---

## Findings

### F1 [MEDIUM] -- checkOOMKill always returns true for exit code 137

**File:** `internal/sandbox/gvisor/exec.go`, lines ~180-205

The `checkOOMKill` method attempts to get container state from runsc, parses the JSON, but then unconditionally returns `exitCode == 137` regardless of what the state contains. The state query and JSON parse are effectively dead code -- they have no effect on the return value.

```go
// After parsing state JSON:
return exitCode == 137  // always true since caller already checked exitCode == 137
```

This means every SIGKILL (exit 137) is classified as OOM, including cases where the process was killed by a signal for non-OOM reasons. While the guard in `runContainer` (only checking when `Status==StatusExited && ExitCode==137`) limits the blast radius (timeout and cancellation are handled first), this is still a false-positive OOM attribution risk.

**Recommendation:** Either use the state JSON to make a real decision (e.g., check for specific fields like `"oom"` in the status), or document the intentional conservative heuristic and add a TODO for future refinement. The plan doc at section 7.4 acknowledges this is a conservative approach, but the implementation should either implement actual state inspection or simplify to just check exit code without the dead state-query code.

### F2 [MEDIUM] -- readPeakMemory is a no-op stub

**File:** `internal/sandbox/gvisor/exec.go`, lines ~210-214

`readPeakMemory` always returns 0 with a comment saying cgroup path tracking is needed. This is referenced in `runContainer` and the result's `PeakMemoryBytes` field will always be 0.

The plan doc specifies `readPeakMemory` should read from `cgroup memory.peak`. The standalone `readPeakMemory` function in `cgroups.go` (from the dependency task aiag-xmq.1) does read the file, but the Manager method does not delegate to it and has no way to look up the cgroup path.

**Recommendation:** Either connect the Manager's `readPeakMemory` method to the real cgroup-based implementation (constructing the cgroup path from runsc state), or make the stub explicit by removing the call and documenting peak memory tracking as a follow-up. As-is, it's misleading to have the field populated but always zero.

### F3 [MEDIUM] -- detectOOMKill in cgroups.go is never called

**File:** `internal/sandbox/gvisor/cgroups.go`, lines ~84-103

The `detectOOMKill` function (standalone, not a method) is defined and tested, but never called by the Manager. The Manager uses `m.checkOOMKill()` (a method on Manager in exec.go) instead. There are now two competing OOM detection implementations with different behaviors:
- `detectOOMKill` (cgroups.go): reads cgroup memory.events, falls back to exit code 137
- `Manager.checkOOMKill` (exec.go): queries runsc state, falls back to exit code 137

**Recommendation:** Remove `detectOOMKill` from cgroups.go or integrate it into `checkOOMKill`. Having two unused implementations creates confusion. If the intent is that `detectOOMKill` is reusable library code, it should be called from `checkOOMKill` as the primary detection method.

### F4 [LOW] -- Plan specifies ErrManagerClosed as sentinel but implementation returns it correctly

The implementation correctly returns `ErrManagerClosed` as a sentinel error (`errors.New`), while the plan doc section 7.2 uses `fmt.Errorf("manager is closed")`. The implementation is actually better than the plan here since it enables `errors.Is(err, ErrManagerClosed)` checking. No action needed -- just noting the plan deviation is an improvement.

### F5 [LOW] -- OTEL metrics omitted from Manager struct

**File:** `internal/sandbox/gvisor/manager.go`

The plan doc (section 7.1) includes OTEL metric fields (`bootDuration`, `execDuration`, `containerCount`, `oomKillCount`, `timeoutCount`) and `initMetrics()`/`recordMetrics()` methods. The implementation omits all OTEL instrumentation. The `EnableMetrics` config field exists but is never read.

**Recommendation:** This is acceptable for the initial implementation if OTEL is being deferred, but should be noted in the bead as a follow-up. The `EnableMetrics` field should either be implemented or removed to avoid dead configuration.

### F6 [LOW] -- Missing health_test.go

The plan's package structure (section 3) specifies `health_test.go`. The implementation has `health.go` with `CleanupStale` but no corresponding test file. The `CleanupStale` method is only tested indirectly through the mock system. A dedicated test would be valuable since this is crash recovery code.

**Recommendation:** Add a `health_test.go` with a mock runsc script that returns JSON container lists, verifying that stale containers are detected and cleaned up while active ones are preserved.

### F7 [LOW] -- Missing options.go and integration_test.go from plan

The plan specifies `options.go` as a separate file and `integration_test.go` with build-tag gated real runsc tests. The implementation merged options/types into `types.go` (acceptable naming choice) and omitted integration tests entirely (understandable since real runsc requires Linux + root).

### F8 [MINOR] -- Plan specifies ContainerError type, not implemented

The plan (section 12.1) defines a `ContainerError` struct with `ContainerID` and `Phase` fields for richer error context. The implementation uses plain `fmt.Errorf` wrapping throughout. This is a minor gap -- the current error handling works but loses container context in error messages when the container ID hasn't been set yet.

### F9 [LOW] -- limitedBuffer.truncated upgraded to atomic.Bool (good deviation)

The plan uses a plain `bool` guarded by the mutex, while the implementation uses `atomic.Bool`. This is technically unnecessary since `Truncated()` could just take the lock, but it provides a minor performance improvement for the read path by avoiding lock contention. The `Write()` method still holds the mutex when setting it. This is a clean, safe improvement.

### F10 [LOW] -- Timeout test takes ~3.5s instead of ~0.5s

**File:** `internal/sandbox/gvisor/manager_test.go`, `TestManager_Run_Timeout` and `TestManager_ResourceTimeout`

Both timeout tests set 500ms timeouts but take ~3.5s. This is caused by `cmd.WaitDelay = 3 * time.Second` in exec.go -- after context cancellation, the process gets killed but cmd.Wait() waits up to 3 additional seconds for pipe cleanup. While functionally correct (the test passes and elapsed < 5s threshold), the WaitDelay adds significant time to the test suite.

**Recommendation:** Consider making WaitDelay configurable or reducing it in test configurations. Not blocking, but worth noting for test suite speed as more timeout tests are added.

---

## Plan Compliance

| Plan Section | Status | Notes |
|---|---|---|
| 4.1 GVisorManager interface | PASS | All four methods implemented |
| 4.2 ContainerOptions | PASS | All fields present in types.go |
| 4.3 ContainerResult | PASS | All fields present |
| 4.4 ManagerConfig | PASS | All fields except EnableMetrics is dead |
| 5.x OCI Spec Builder | PASS | Faithful implementation |
| 6.x cgroup v2 Resources | PASS | Translation, validation, V2 entries all present |
| 7.x Container Lifecycle | PASS | Run, bundle, exec, delete flow correct |
| 8. Health/CleanupStale | PARTIAL | Implemented but untested |
| 9. Container ID | PASS | Format and uniqueness verified |
| 10. runsc Verification | PASS | Version check implemented |
| 11. OTEL Metrics | NOT IMPL | Deferred, config field exists but unused |
| 12. Error Handling | PARTIAL | No ContainerError type, plain errors used |
| Test harness P1-P5 | PASS | All property tests present and passing |
| Test harness F1-F5 | PARTIAL | F5 (concurrent run+close) present, F1-F4 missing |
| Test harness S1-S2 | NOT IMPL | Stress tests not present |
| Test harness B1-B5 | PARTIAL | B2, B5, and 2 extra benchmarks present |
| Test harness SK1-SK3 | NOT IMPL | Soak tests not present |
| Test harness SEC1-SEC7 | NOT IMPL | Security tests not present |
| Test harness O1-O2 | NOT IMPL | Observability tests not present |

---

## Security Review

The security model is sound for the implemented scope:

- **NoNewPrivileges: true** -- correctly set in OCI spec
- **Minimal capabilities** -- appropriate set for build/test workloads
- **All 5 namespaces** -- PID, mount, IPC, UTS, network all isolated
- **Bundle dir permissions: 0700** -- config.json at 0600
- **Network default: none** -- most restrictive default
- **cgroup limits always set** -- PIDs default to 1024 even when not specified

No security concerns in the current code.

---

## Concurrency Review

- **sync.Map for active containers** -- appropriate for concurrent read/write with unique keys
- **atomic.Int32 for active count** -- correct usage
- **atomic.Bool for closed flag** -- CompareAndSwap for idempotent Close
- **limitedBuffer mutex** -- correct locking for Write, Bytes; atomic.Bool for Truncated read path
- **Semaphore for concurrency limit** -- proper context-aware select
- **Re-check closed after semaphore wait** -- correctly prevents TOCTOU race
- **WaitDelay on cmd** -- prevents goroutine leak from lingering pipe readers

All concurrent access patterns look correct. Race detector confirms no races across the test suite.

---

## Resource Leak Review

- **Bundle cleanup via defer** -- always runs regardless of error path
- **Container delete (best-effort)** -- always called after runContainer
- **Context cancellation** -- execCancel called in defer
- **done channel** -- closed in defer for shutdown coordination
- **Semaphore slot** -- released via defer
- **No goroutine leaks** -- no background goroutines spawned; all async work is in the mock scripts

No resource leaks identified.

---

## Test Quality

The test suite is thorough for unit-level testing:

- **60+ test cases** covering all major code paths
- **Mock runsc approach** is clever and portable (bash scripts work on macOS/Linux)
- **Property tests (P1-P5)** provide good randomized coverage
- **Concurrent tests** verify thread safety
- **Benchmark suite** provides performance baselines
- **Edge cases covered:** zero limits, exact limits, truncation, delete failures, close-after-close

Gaps: no tests for CleanupStale logic, missing fault injection tests (F1-F4 per plan), no stress/soak/security test suites. These could be follow-up items since they may require more infrastructure.

---

## Verdict

**Approve with findings.** The implementation is solid, well-tested, and follows the plan closely. The main issues to address before or shortly after merge:

1. **F1 (checkOOMKill dead code):** Clean up the state query or make it actually influence the result
2. **F2 (readPeakMemory stub):** Remove the call or connect it to real cgroup reading
3. **F3 (detectOOMKill orphaned):** Remove or integrate with Manager.checkOOMKill
4. **F5 (EnableMetrics dead config):** Remove the field or implement metrics
5. **F6 (health_test.go missing):** Add tests for CleanupStale

Items 1-3 could be addressed in a quick follow-up commit. Items 4-5 could be tracked as separate follow-up beads.
