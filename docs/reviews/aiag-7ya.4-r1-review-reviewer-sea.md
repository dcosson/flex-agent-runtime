# Code Review: aiag-7ya.4 (R1, reviewer-sea)

- Bead: aiag-7ya.4
- Commit range: a32cf55..3ceb9aa
- Plan doc: docs/plans/19-*.md §3.4, §3.5, §3.6
- Reviewer: reviewer-sea
- Review commit: 3ceb9aa

## aiag-7ya.3 P2 Disposition from R1

| Finding | Status | Notes |
|---------|--------|-------|
| P2-1: SandboxID placeholder doc | **Fixed** | Comment updated in direct.go:108-110. Clear guidance to use EC2 metadata for self-identification. |
| P2-2: shellQuote template func | **Fixed** | Registered in renderUserData (create.go:236-238). New test `TestCreateSandbox_UserDataTemplateShellQuote` verifies escaping of single quotes in label values. |
| P2-3: Closing guard tests | **Fixed** | Two new tests: `TestCreateSandbox_ClosedAtEntry` and `TestCreateSandbox_ClosedDuringPolling`. The polling test uses a `describeHook` callback to set `closing=true` during SSM describe, verifies `ErrDirectClosed` and orphan termination. Well-designed test pattern. |

## Findings

### P3-1 - LaunchProcess health poll doesn't check closing flag

**Location:** `internal/sandbox/control/direct/process.go:207-232`

**Problem**
`waitForProcessHealth` does not call `pollPreflight` (which checks `d.closing` and `ctx.Err()`). If the adapter enters closing state during health polling, the launch will wait up to `ProcessReadyTimeout` (30s default) before failing, instead of failing fast like `waitForInstanceReady` does. Since `LaunchProcess` doesn't use `inflightWg`, this doesn't block `Close()`, but the delayed error response is inconsistent with the `CreateSandbox` path.

**Suggested fix**
Add a `pollPreflight` check at the start of the health poll loop, or accept the inconsistency with a comment noting that `LaunchProcess` doesn't block shutdown.

---

### P3-2 - No validation of ExposePort or Signal values

**Location:** `internal/sandbox/control/direct/process.go:63, 126-131`

**Problem**
`ExposePort` is used directly in the health check URL (`curl localhost:PORT/health`) and response address. A zero or negative port would cause the health check to fail with timeout rather than returning a clear error. Similarly, a negative `Signal` value would produce a shell command like `kill --1 PID` which bash would reject, returning a confusing SSM error rather than a clear validation error.

**Suggested fix**
Add early validation: `ExposePort > 0` in `LaunchProcess`, `Signal >= 0` in `KillProcess`. Low priority since callers are internal.

---

### P3-3 - Health check endpoint hardcoded to /health

**Location:** `internal/sandbox/control/direct/process.go:210`

**Problem**
`waitForProcessHealth` hardcodes `curl localhost:PORT/health`. Processes that use a different health check path (e.g., `/ready`, `/healthz`) would never pass the readiness check. This is a known design constraint from the plan doc, not a bug.

**Suggested fix**
No change needed now. If needed later, add an optional `HealthPath` field to `LaunchProcessRequest`.

---

## Summary

3 findings: 0 P0, 0 P1, 0 P2, 3 P3

**Verdict**: Approved

All aiag-7ya.3 P2 fixes verified clean. The process management implementation is well-structured: PID recycling guard via /proc/PID/stat start time comparison is robust, the `ssmSendPlan` mock pattern makes tests readable, and the lock-only-for-map-access concurrency pattern is consistent with CreateSandbox/DestroySandbox. All 10 test cases from the bead scope are covered.
