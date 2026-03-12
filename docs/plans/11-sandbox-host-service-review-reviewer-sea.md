# 11: Sandbox Host Service — Review Findings (reviewer-sea, R1)

**Plan:** [11-sandbox-host-service.md](./11-sandbox-host-service.md)
**Test Harness:** [11-sandbox-host-service-test-harness.md](./11-sandbox-host-service-test-harness.md)
**Reviewer:** reviewer-sea
**Round:** 1
**Date:** 2026-03-12

---

## Summary

The plan is thorough and well-structured, with detailed code examples for all major components. The architecture is clean with good separation between ZFS/gVisor and the coordinator. However, there are several concurrency correctness issues in the code examples that would cause real bugs if implemented as-written, a state machine inconsistency between diagram and types, and a path validation gap in Tier 1 execution.

## Findings

### [F1] TOCTOU race in CreateSession capacity check — P1

**Section:** Plan §4.2 (CreateSession code)
**Issue:** The capacity check reads session count (step 1), then later stores the session (step 7) via `svc.sessions.Store()`. Under concurrent CreateSession calls, two goroutines can both pass the capacity check and both store sessions, exceeding `MaxSessions`. `sync.Map` does not provide atomic load-count-then-store semantics.
**Recommendation:** Use `sync.Map.LoadOrStore` with a placeholder, or guard the capacity-check + store sequence with a separate mutex. An alternative is to use an `atomic.Int32` for the session count, `Add(1)` before the clone, and `Add(-1)` on failure, but this is fragile. A dedicated `sessionsMu sync.Mutex` around the critical section is simpler and correct.

### [F2] TurnComplete increments turnCount before snapshot succeeds — P1

**Section:** Plan §6.1 (TurnComplete code)
**Issue:** `sess.turnCount++` happens under lock, then the lock is released, then `svc.zfs.CreateSnapshot()` is called. If the snapshot fails, `turnCount` is already incremented and never rolled back. This means `turnCount` and actual snapshot count will diverge. The test harness property P3 (Snapshot Ordering Consistency) assumes `TurnComplete` returns `i+1` as `TurnNumber` and that there are exactly N snapshots after N calls — this will fail when snapshot creation fails.
**Recommendation:** Either (a) increment turnCount after successful snapshot creation, or (b) decrement on failure. Option (a) is simpler: move the increment and snapshot name generation inside a two-phase approach where the snapshot is created first, then session state is updated.

### [F3] Rollback turnCount reset assumes all snapshots are turn-snapshots — P2

**Section:** Plan §6.3 (RollbackSession code)
**Issue:** The rollback code sets `sess.turnCount = targetIdx + 1`. This assumes every snapshot in the `sess.snapshots` slice corresponds to a turn. But `CreateSnapshot()` (§6.2) also appends explicit snapshots to the same slice. If explicit snapshots were interleaved, `turnCount` will be wrong after rollback.
**Recommendation:** Either (a) track turn-snapshots and explicit-snapshots separately, or (b) include a `isTurnSnapshot bool` flag with each snapshot entry so rollback can compute the correct turn count.

### [F4] State diagram includes RollingBack state but code types don't define it — P2

**Section:** Plan §2.4 (state diagram) vs §3.2 (SessionState constants)
**Issue:** The state diagram shows `Active → RollingBack → Active/Failed` transitions. But the `SessionState` constants only define `Creating`, `Active`, `Paused`, `Destroying`, `Failed`. There is no `SessionRollingBack` constant. This means either the state machine is wrong (rollback is synchronous and doesn't need a transient state) or the types are incomplete. If rollback is synchronous, the diagram should match the implementation. If it can be long-running, the state is needed.
**Recommendation:** Clarify. If rollback is synchronous (ZFS rollback is typically fast), remove `RollingBack` from the diagram. If it could take meaningful time or needs to block other operations, add `SessionRollingBack` to the constants and implement the state transitions.

### [F5] DestroySession doesn't check for in-flight tools — P2

**Section:** Plan §4.4 (DestroySession code)
**Issue:** `DestroySession` directly sets state to `Destroying` and destroys the ZFS dataset without checking `activeTools`. In contrast, `PauseSession` checks `activeTools` and returns `ErrToolsInFlight`. If tools are in-flight when Destroy is called, they could be reading/writing the dataset as it's being destroyed, causing undefined behavior (panics, corrupted output, etc.).
**Recommendation:** Add the same guard as PauseSession: either return `ErrToolsInFlight` if tools are active, or wait for them to drain with a timeout (matching the comment pattern in PauseSession). The Shutdown code already demonstrates the wait-with-timeout pattern.

### [F6] PauseSession comment says "wait" but code returns error — P3

**Section:** Plan §4.3 (PauseSession code)
**Issue:** The code has the comment `// Wait for in-flight tools to complete (with timeout)` but the implementation immediately returns `ErrToolsInFlight` without waiting. This inconsistency will confuse implementors about the intended behavior.
**Recommendation:** Either update the comment to match the return-error behavior, or implement the wait-with-timeout pattern (preferred, since the Shutdown code already demonstrates it and callers would expect pause to gracefully wait).

### [F7] Tier 1 executor lacks explicit path traversal guard at service layer — P2

**Section:** Plan §5.3 (Tier 1 executor code)
**Issue:** The Tier 1 executor passes `mountpoint` and `params` directly to tool functions without any service-layer validation. A `read_file` call with `path: "../../etc/passwd"` would escape the session filesystem if the underlying tool implementation doesn't validate. The plan defers path safety entirely to `internal/tools` implementations, but the sandbox host service is the isolation boundary — it should enforce containment regardless of tool implementation quality.
**Recommendation:** Add a path validation helper in the service layer that resolves the requested path against the mountpoint and rejects any path that escapes it (using `filepath.Rel` + checking for `..` prefix). This is defense-in-depth — the tool may also validate, but the service must not trust its callees for security-critical containment.

### [F8] Quota setting after clone uses wrong API — P3

**Section:** Plan §4.2, step 5 (CreateSession code)
**Issue:** After cloning (step 4 creates the dataset), step 5 calls `svc.zfs.CreateDataset(ctx, dataset, ...)` on the already-existing dataset to set a quota. The comment acknowledges this (`// Dataset already exists from clone`) and the error is silently swallowed. This is incorrect — should use `SetProperty` or a dedicated quota-setting method on the ZFS manager.
**Recommendation:** Use `svc.zfs.SetProperty(ctx, dataset, "quota", formatBytes(quota))` or ensure the ZFSManager's `SetQuota` method (if it exists from plan 09) is used here. Do not silently swallow errors from quota setting — a failed quota leaves the session with unlimited disk usage.

### [F9] cleanupOldSnapshots has non-atomic two-phase locking — P3

**Section:** Plan §6.4 (cleanupOldSnapshots code)
**Issue:** The function reads `snapCount` under lock, releases lock, then re-acquires lock to do removal. Between the two locks, another goroutine could modify the snapshots slice (e.g., a concurrent TurnComplete adding a snapshot), leading to incorrect removal or index out of bounds.
**Recommendation:** Perform the entire check-and-remove under a single lock hold: determine which snapshots to remove and truncate the slice atomically, then destroy them outside the lock.

### [F10] buildTier2Command default case has shell injection risk — P3

**Section:** Plan §5.4 (buildTier2Command code)
**Issue:** The default fallback `fmt.Sprintf("echo 'Unknown tool: %s'", toolName)` interpolates toolName into a shell command. If toolName contains shell metacharacters (e.g., `'; rm -rf /`), this is injectable. Although unknown tools should be caught earlier, this is a defense-in-depth issue.
**Recommendation:** The default case should return an error rather than constructing a shell command with untrusted input. Or at minimum, use `shellescape.Quote(toolName)` if an error message must be emitted via the container.

### [F11] Test harness P1 (Session State Machine) doesn't track expected valid states — P2

**Section:** Test Harness §1, P1
**Issue:** The property test generates random operation sequences but only asserts that the final state is one of the valid states. It doesn't track whether each operation's success/failure is correct for the current state. For example, it doesn't verify that `ExecuteTool` on a paused session returns an error, or that `ResumeSession` on an active session returns an error. The test could miss state machine bugs where invalid transitions silently succeed.
**Recommendation:** Maintain a shadow state machine in the test that tracks the expected state and expected success/failure of each operation. After each operation, assert both the return value and the resulting state match expectations.

---

## Statistics

- Total findings: 11
- P0 (blocking): 0
- P1 (significant): 2
- P2 (moderate): 5
- P3 (minor): 4
