# Code Review: aiag-zvy.1 Sandbox Host Service (R1, reviewer-sea)

- Bead: aiag-zvy.1
- Commit range: 0302556..ce7e604
- Plan doc: docs/plans/11-sandbox-host-service.md
- Reviewer: reviewer-sea
- Review commit: ce7e604

## Findings

### P1 - TurnComplete has TOCTOU race under concurrent calls

**Location:** `internal/sandbox/snapshot.go:16-34`

**Problem**
`TurnComplete` reads `sess.turnCount` under a lock (line 21), computes `prospectiveTurn`, releases the lock, performs the ZFS snapshot (line 26), then re-acquires the lock to commit the new turn count (line 31-33). If two concurrent `TurnComplete` calls arrive, both will read the same `turnCount`, compute the same `prospectiveTurn`, and attempt to create the same snapshot name. The second ZFS `CreateSnapshot` call will fail (duplicate name), but the error path does not handle this gracefully -- the first call's committed state could be inconsistent if ordering is unlucky.

The plan (section 6.1, lines 864-880) uses `sess.mu.RLock()` for the read and `sess.mu.Lock()` for the commit, which has the same issue but at least doesn't block concurrent reads. The implementation uses a full `Lock` for the read, which is slightly safer but still has the fundamental TOCTOU gap.

In practice, `TurnComplete` is typically called sequentially by the agent loop, so this is unlikely to manifest. However, the plan's `prospectiveTurn` pattern was designed to be safe, and this implementation leaves a window where it is not.

**Suggested fix**
Hold the lock across the entire operation. ZFS `CreateSnapshot` is fast (<100ms per the plan), so holding the mutex during the call is acceptable. This eliminates the race entirely:

```go
sess.mu.Lock()
// ... check state, compute prospectiveTurn ...
info, err := svc.zfs.CreateSnapshot(ctx, dataset, snapName)
if err != nil {
    sess.mu.Unlock()
    return nil, ...
}
sess.turnCount = prospectiveTurn
sess.snapshots = append(...)
sess.mu.Unlock()
```

Alternatively, if holding the lock during I/O is undesirable, add a `snapshotInProgress` flag (like `rollingBack`) that prevents concurrent `TurnComplete` calls.

---

### P1 - Shutdown destroys all sessions instead of pausing them

**Location:** `internal/sandbox/service.go:351-360`

**Problem**
The plan (section 8, lines 1108-1147) specifies that `Shutdown` should:
1. Stop accepting new sessions (`shuttingDown.Store(true)`)
2. Wait for in-flight tools to complete
3. **Pause** all active sessions (preserving data on disk)

The implementation instead **destroys** all sessions via `DestroySession`, which calls `zfs.DestroyDataset` and removes session data permanently. This means a service restart loses all session state and filesystem data. The plan explicitly designed graceful shutdown to preserve sessions so they can be recovered on restart (see section 14.1 "Session Recovery on Restart").

Additionally, the implementation has no `shuttingDown` flag, so new sessions can still be created during shutdown.

**Suggested fix**
Replace `DestroySession` calls with `PauseSession` calls in `Shutdown`. Add an `atomic.Bool` `shuttingDown` field to `SandboxHostService` and check it at the top of `CreateSession`.

---

### P1 - ExecuteTool does not propagate onProgress callback

**Location:** `internal/sandbox/execute.go:15-72`

**Problem**
Per the implementation guide section 1.6, the `ToolBackend` contract requires that `SandboxBackend` must accept and propagate `onProgress`. The `SandboxHostService.ExecuteTool` method signature does not accept an `onProgress` callback at all. When the RPC layer (plan 13) wraps this service, there will be no way to stream Tier 2 tool output progressively back to the client -- the entire output is buffered and returned only after completion.

The plan's section 5.2 (line 624) `ExecuteTool` signature also omits `onProgress`, so this is a plan gap that propagated to the implementation. However, the implementation guide (which takes precedence on cross-seam contracts) explicitly calls this out as a P1 finding from the seam review (section 4.6, finding #1).

**Suggested fix**
Add an `onProgress func(string)` parameter to `ExecuteTool` (or to `ExecuteToolRequest`). For Tier 2 execution, pipe stdout/stderr incrementally from the gVisor container through this callback. For Tier 1, this can be nil/ignored since Tier 1 operations are fast.

---

### P2 - Tier 1 creates a new LocalBackend on every call

**Location:** `internal/sandbox/execute.go:78`

**Problem**
`executeTier1` calls `tools.NewLocalBackend(mountpoint)` on every tool invocation, which creates a new `LocalBackend` struct and re-initializes the tool map (12 tool implementations). While the allocation cost is small, this is unnecessary churn in a hot path.

More importantly, this means the sandbox host service delegates to `LocalBackend` for Tier 1 tools, but `LocalBackend` also includes Tier 2 tools like `bash`, `git_add`, and `git_commit` in its tool map. The tier classification is enforced at a higher level, so this is not a bug, but it creates a confusing arrangement where the backend being used has capabilities that should not be reachable.

**Suggested fix**
Cache a `LocalBackend` per session (since mountpoint is stable per session), or at minimum cache one and share it across calls for the same session. This avoids repeated allocation and makes the code more readable.

---

### P2 - Missing SessionID in Tier 1 ToolRequest

**Location:** `internal/sandbox/execute.go:79`

**Problem**
The `ToolRequest` passed to `LocalBackend.ExecuteTool` includes `ToolName`, `ToolCallID`, and `Params`, but omits `SessionID`. The `ToolRequest` struct in `internal/tools/iface.go` has a `SessionID` field. While `LocalBackend` currently ignores `SessionID`, this omission breaks the end-to-end traceability contract from the implementation guide section 1.6, which states that `SandboxBackend` propagates `ToolCallID` in both request and response for traceability.

**Suggested fix**
Include `SessionID: req.SessionID` in the `ToolRequest` construction.

---

### P2 - DestroySession silently ignores ZFS cleanup errors

**Location:** `internal/sandbox/service.go:290`

**Problem**
`DestroySession` discards the error from `zfs.DestroyDataset` with `_ =`. The plan (section 4.4, lines 569-577) explicitly notes this should be logged: "Log but continue — remove from registry even if ZFS cleanup fails (orphaned datasets can be cleaned up by a background sweep)." The implementation has no logging.

Similarly, `Shutdown` at line 354 also discards `DestroySession` errors silently.

**Suggested fix**
Log the error at warn level, matching the plan's specification:
```go
if err := svc.zfs.DestroyDataset(...); err != nil {
    svc.logger.WarnContext(ctx, "failed to destroy dataset", "session_id", sessionID, "error", err)
}
```

---

### P2 - PauseSession does not validate state before drain loop

**Location:** `internal/sandbox/service.go:221-248`

**Problem**
`PauseSession` calls `getSession` (not `getActiveSession`) and immediately enters the drain loop without checking if the session is already paused, destroying, or failed. If the session is already paused, the implementation will unnecessarily wait for the drain timeout (or succeed immediately if activeTools is 0), then fail at the state check on line 243.

The plan (section 4.3, lines 472-512) calls `getActiveSession` first, which would return `ErrSessionPaused` immediately. The implementation should fail fast for sessions that are not in the `Active` state.

**Suggested fix**
Either use `getActiveSession` (which validates state is `Active`), or add an early state check before the drain loop.

---

### P2 - Missing metrics/OTEL instrumentation

**Location:** `internal/sandbox/service.go` (entire file)

**Problem**
The plan (section 1 "Scope", line 26) explicitly lists "Observability: structured logging, OTEL metrics for session count, tool latency, snapshot latency, pool space" as in-scope. The implementation has no OTEL metrics at all -- no `serviceMetrics` struct, no counters, no histograms. The plan has detailed metrics code in sections 4.2-4.4 and 6.1.

While the bead description does not explicitly call out metrics as a deliverable, the plan clearly includes them, and the implementation guide references `svc.metrics.*` calls throughout.

**Suggested fix**
This is a significant omission but could be deferred to a follow-up bead if metrics are not on the critical path for T6/T10/T11 dependents. At minimum, add a stub `serviceMetrics` struct with no-op recording so the code structure is ready for metrics later.

---

### P2 - `mergeDefaultConfig` trigger condition is fragile

**Location:** `internal/sandbox/service.go:78-79`

**Problem**
`NewSandboxHostService` checks `cfg.SnapshotPrefix == ""` to decide whether to call `mergeDefaultConfig`. This means if only `SnapshotPrefix` is set but other fields like `ToolTimeout` are zero, defaults will not be applied. The intent is to merge defaults for any unset field, but the gate is tied to a single field.

**Suggested fix**
Always call `mergeDefaultConfig(cfg)` unconditionally. The function already checks each field individually, so calling it when fields are already set is a no-op for those fields.

---

### P2 - Session ID generation uses monotonic counter instead of UUIDs

**Location:** `internal/sandbox/ids.go:8-13`

**Problem**
`generateSessionID` uses a monotonic atomic counter producing IDs like `sess-00000001`. This has two problems:
1. IDs are predictable, which could be a security concern if session IDs are exposed in RPC APIs.
2. IDs reset to zero on service restart, causing collisions with sessions that survived restart (per the plan's session recovery feature in section 14.1).

The plan doesn't specify the format, but the examples use UUIDs (`sess-abc`) and the implementation guide's section 3.2 says "SessionID: Generated by CreateSession or passed in request."

**Suggested fix**
Use `crypto/rand` or a UUID library for unpredictable, globally unique session IDs.

---

### P2 - Missing test coverage for key behaviors

**Location:** `internal/sandbox/service_test.go`

**Problem**
The test file covers the happy path well (lifecycle, TOCTOU, tier routing, turn complete + rollback, shutdown), but several important behaviors are untested:

1. **Per-tool snapshots**: `PerToolSnapshots` config option is never tested. There is no test that verifies `ExecuteTool` creates a snapshot when this flag is enabled.
2. **CreateSnapshot (explicit)**: No test for `svc.CreateSnapshot()`.
3. **ListSnapshots**: No test for `svc.ListSnapshots()`.
4. **HealthCheck**: No test for health check with pool in various states (healthy, degraded, unhealthy).
5. **State machine invalid transitions**: No test verifying that e.g. `ResumeSession` on an active session returns an error, or `PauseSession` on a paused session returns an error.
6. **Concurrent tool execution**: No test exercising concurrent `ExecuteTool` calls to verify `activeTools` counting works correctly.
7. **Path validation/escape**: No test for `validatePathParams` or `validatePath` directly, and no test verifying path traversal is rejected.
8. **ListSessions**: No test.

The plan (section 13.1-13.2) lists specific test requirements including tier classifier tests, session state machine tests for all transitions, config defaults tests, and error wrapping tests.

**Suggested fix**
Add tests for at minimum: per-tool snapshots, invalid state transitions, path validation (including escape attempts), and concurrent tool execution.

---

### P3 - `ExecuteTool` returns wrong sentinel for rollingBack guard

**Location:** `internal/sandbox/execute.go:32`

**Problem**
When `sess.rollingBack` is true, `ExecuteTool` returns `ErrToolsInFlight`. This error is misleading -- the tools are not in flight; the session is rolling back. A more accurate error would be `ErrInvalidState` with a message about rollback being in progress.

**Suggested fix**
Return `fmt.Errorf("%w: session is rolling back", ErrInvalidState)` instead.

---

### P3 - cmd/sandbox-host is a minimal skeleton without config loading

**Location:** `cmd/sandbox-host/main.go`

**Problem**
The binary skeleton is extremely minimal -- it accepts a `--listen` flag and waits for a signal, but does not actually create a `SandboxHostService`, ZFS manager, or gVisor manager. The plan (section 9.1-9.2) specifies a more complete skeleton including config loading from flags/env/config file, manager initialization, and health monitor startup.

This is acknowledged as a "skeleton" in the commit message, so it may be intentional to defer the full binary to the RPC layer bead (T6). However, it would be more useful if it at least demonstrated the service construction pattern.

**Suggested fix**
At minimum, add a comment or TODO indicating this is a placeholder pending the RPC server implementation from plan 13.

---

### P3 - `buildTier2Command` silently coerces empty bash commands

**Location:** `internal/sandbox/execute.go:119`

**Problem**
`buildTier2Command` for "bash" uses `params["cmd"].(string)` which will silently produce an empty string if the key is missing (the comma-ok pattern is used but the `ok` value is discarded). Then it checks `strings.TrimSpace(cmd) == ""` and returns an error. This works correctly but the type assertion pattern `params["cmd"].(string)` could panic if `params["cmd"]` exists but is not a string (e.g., if it's an int).

**Suggested fix**
Use the comma-ok form consistently: `cmd, ok := params["cmd"].(string)`.

The current code already uses comma-ok (line 119: `cmd, _ := params["cmd"].(string)`), so this is actually fine. Withdrawing this finding -- the `_` discards the ok value intentionally since the zero-value empty string is handled by the subsequent trim check.

---

### P3 - Snapshot cleanup in `cleanupOldSnapshots` accesses `sess.dataset` outside lock

**Location:** `internal/sandbox/snapshot.go:129-131`

**Problem**
`cleanupOldSnapshots` releases the session lock after truncating the snapshot list (line 127), then calls `svc.zfs.DestroySnapshot(ctx, sess.dataset, snap.Name)` on line 130. `sess.dataset` is accessed without holding the lock. In practice, `dataset` is set once during session creation and never changes, so this is safe. But it would be more correct to capture `dataset` before releasing the lock, consistent with how other methods handle this (e.g., `TurnComplete` line 23, `RollbackSession` line 77).

**Suggested fix**
Capture `dataset := sess.dataset` before unlocking, and use the local variable in the loop.

---

## Summary

14 findings: 0 P0, 3 P1, 8 P2, 3 P3

**Verdict**: Approved with revisions

The implementation captures the core structure well: session lifecycle, state machine, tier routing, prospective-turn snapshot pattern, rollback guards, and TOCTOU-safe session creation all follow the plan. The code compiles, tests pass (including under `-race`), and the major architectural decisions are sound.

The three P1 findings should be addressed before downstream beads (T6 RPC, T10 test harness, T11 integration) can safely build on this:

1. **TurnComplete TOCTOU** - concurrent TurnComplete calls can produce duplicate snapshot names. Hold the lock across the ZFS call or add a guard flag.
2. **Shutdown destroys instead of pausing** - this fundamentally breaks the session recovery design. Switch to pausing sessions.
3. **Missing onProgress** - the RPC layer (T6) needs this to stream Tier 2 output. Adding it now avoids a seam mismatch later.

The P2 findings are quality improvements (metrics, tests, state validation, logging) that strengthen the implementation but don't block the critical path.
