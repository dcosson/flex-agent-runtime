# Code Review: aiag-7ya.5 (R1, reviewer-sea)

- Bead: aiag-7ya.5
- Commit range: 3ceb9aa..007cb3b
- Plan doc: docs/plans/19-*.md §3.7, §3.8, §3.10, §3.11
- Reviewer: reviewer-sea
- Review commit: 007cb3b

## Findings

### P3-1 - Recovered process state lacks binary, args, and port

**Location:** `internal/sandbox/control/direct/recover.go:139-145`

**Problem**
`recoverProcesses` reconstructs process state from PID files, recovering processID, PID, and startTime. The `binary`, `args`, and `port` fields remain zero-valued. Post-recovery, `GetProcessStatus` and `KillProcess` work correctly (they only need PID and startTime), but if future code reads `proc.port` or `proc.binary`, it would silently get zero values. The PID file format doesn't store this information, so recovery is inherently limited.

**Suggested fix**
No change needed — the recovered fields are sufficient for all current operations. If needed later, store binary/args/port in a separate metadata file written alongside the PID file during LaunchProcess.

---

### P3-2 - Close doesn't terminate instances when TerminateOnClose is false but an inflight CreateSandbox stored one

**Location:** `internal/sandbox/control/direct/close.go:21-31`

**Problem**
If CreateSandbox completes and stores an instance *before* Close sets `closing=true`, and `TerminateOnClose=false`, the instance remains running after Close. This is the correct behavior by design (instances are left for crash recovery). But the test `TestClose_WaitsForInflightCreateSandbox` only covers the case where CreateSandbox hits the post-poll closing check and orphan-terminates. There's no test for the case where CreateSandbox successfully stores the instance, then Close leaves it running. Cosmetic gap — the code is correct.

**Suggested fix**
No change needed. The behavior is intentional per the TerminateOnClose design.

---

### P3-3 - processRecoverCommand output parsing silently skips malformed lines

**Location:** `internal/sandbox/control/direct/recover.go:123-137`

**Problem**
Lines that don't have exactly 3 fields, or where PID/startTime fail to parse, are silently skipped. This is the right behavior for robustness (corrupt PID files shouldn't crash recovery), but a debug-level log would help troubleshooting. Currently there's no visibility into skipped lines.

**Suggested fix**
Add a `d.logger.Debug` call for skipped lines during process recovery. Low priority.

---

## Summary

3 findings: 0 P0, 0 P1, 0 P2, 3 P3

**Verdict**: Approved

The implementation is well-structured:
- **Recover**: Clean tag-based instance discovery with best-effort SSM process probing. The shell command for PID file scanning with start-time verification is correct and robust.
- **Close**: The `closing` + `inflightWg` pattern works correctly across all interleavings — verified by tracing the Close/CreateSandbox race through both the "CreateSandbox exits early via pollPreflight" and "CreateSandbox stores before closing" paths.
- **Pause/Resume**: Lossy pause correctly marks all processes exited and clears the process map on resume. IP refresh after resume handles elastic IP reassignment.
- **Constructor**: Fail-fast on `ListInstances` error is the right choice — silently leaking orphaned instances would be worse than a startup failure.
- **Tests**: All 12 bead-scoped test cases covered. `TestClose_WaitsForInflightCreateSandbox` is particularly well-designed — the channel-blocking + launchHook pattern verifies the full Close/inflight interaction without timing dependencies.
