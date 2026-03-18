# Code Review: aiag-agy.2 (R1, reviewer-sea)

- Bead: aiag-agy.2
- Commit range: d9bb23d (single commit)
- Plan doc: docs/plans/21-serve-orchestrator.md §2.4–2.5, §3.5, §3.8, §6 Bead 2
- Reviewer: reviewer-sea
- Review commit: d9bb23d

## Prior Review Findings Addressed

This commit also incorporates R1 findings from aiag-agy.1:
- **P2-1 fixed**: Auth token `--auth-token` CLI arg removed from `agentLaunchArgs`; now relies solely on env var.
- **P2-2 fixed**: Added `TestCreateSessionFactoryFailureCompensatesKillAndDestroy` and `TestCreateSessionBackendFailureCompensatesFullTeardown`.
- **P3-3 fixed**: `buildDirectControl` now gated behind `DirectAMIID != ""` check.

## Findings

### P2-1 — PauseSession and ResumeSessionSandbox mutate sessionEntry without synchronization

**Location:** `internal/orchestrator/proxy.go:316-339` (PauseSession), `proxy.go:343-380` (ResumeSessionSandbox)

**Problem**
Both functions read and write `entry.state` without holding any lock. `ResumeSessionSandbox` additionally mutates `entry.processID`, `entry.agentService`, and `entry.lastHealth`. The plan §3.4 specifies `mu sync.Mutex` on `sessionEntry` for exactly this reason.

While the risk is low today (sessions are single-tenant, no health goroutine yet), bead 3 (health monitoring) will introduce a background goroutine that concurrently reads and writes `entry.state` and `entry.lastHealth`. Without per-entry synchronization, bead 3 will have data races.

**Suggested fix**
Add `mu sync.Mutex` to `sessionEntry` as specified in the plan. Hold it during state checks and field mutations in `PauseSession`, `ResumeSessionSandbox`, and `destroySessionEntry`. Must be resolved before bead 3 lands.

---

### P3-1 — createAgentSandboxSession is a near-verbatim copy of createAgentDirectSession

**Location:** `internal/orchestrator/proxy.go:131-201` vs `proxy.go:63-129`

**Problem**
The two functions are ~70 lines each and differ only in the `SandboxControl` source (`o.config.NodeControl` vs `o.config.DirectControl`). The plan §3.5 notes "Both agent-direct and agent-sandbox follow the same pattern — they differ only in which SandboxControl implementation is used." This duplication means any future fix to compensation logic must be applied in two places.

**Suggested fix**
Extract a shared `createRemoteAgentSession(ctx, entry, req, sc)` helper parameterized on the `SandboxControl`. Low priority since both paths are well-tested.

---

### P3-2 — ResumeSessionSandbox does not compensate on partial re-launch failure

**Location:** `internal/orchestrator/proxy.go:359-378`

**Problem**
If `LaunchProcess` succeeds but `AgentServiceFactory` fails during agent-direct resume, the newly launched process is orphaned — `entry.processID` was already updated (line 369) but the old agent service was not yet replaced. The function returns an error with the session in an inconsistent state (paused state but with a running orphan process).

**Suggested fix**
On factory failure after successful `LaunchProcess`, kill the newly launched process before returning the error:
```go
if err != nil {
    _ = entry.sandboxControl.KillProcess(ctx, control.KillProcessRequest{
        SandboxID: entry.sandboxID, ProcessID: launchResp.ProcessID,
    })
    return fmt.Errorf("reconnect agent after resume: %w", err)
}
```
Also consider reverting `entry.processID` to the old value, or at minimum documenting that the session is in a degraded state.

---

### P3-3 — Fleet mode returns error in buildNodeOrFleetControl

**Location:** `cmd/flexagent/serve_orchestrator.go:331-333`

**Problem**
Multiple sandbox-host addresses return `"fleet mode (multiple sandbox-host addresses) is not yet wired"`. This is documented and expected, but the validation in `validate()` doesn't catch this — a user passing `--sandbox-host-addr "host1:8082,host2:8082"` would only see the error at runtime during `buildNodeOrFleetControl`, not at config validation time.

**Suggested fix**
Add a validation check in `validate()` that rejects multiple comma-separated sandbox-host addresses until fleet mode is wired. Low priority.

---

## Summary

4 findings: 0 P0, 0 P1, 1 P2, 3 P3

**Verdict**: Approved with revisions

Excellent implementation. All three placement modes work correctly — agent-sandbox reuses the same proxy pattern as agent-direct, and tools-sandbox cleanly wires `ToolEnvironmentConfig` with the correct host address resolution (Node configured addr vs Fleet response addr) and host-local session ID extraction. The `fleet.ParseSandboxID` export and `extractHostLocalSessionID` helper handle the dual-ID contract precisely, including colons in session IDs. Pause/resume correctly handles lossy pause for agent-direct (abort first, then re-launch on resume) and simple sandbox pause/resume for the other modes. Compensation cleanup is thorough across all creation flows. The 17 new tests cover all critical paths including Fleet vs Node address resolution, dual ID tracking, and pause state transitions.

The P2-1 (per-entry mutex) should be resolved before bead 3 since health monitoring will introduce concurrent entry mutation. The R1 findings from aiag-agy.1 were all cleanly addressed.
