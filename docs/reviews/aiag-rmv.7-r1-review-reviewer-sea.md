# Code Review: aiag-rmv.7 (R1, reviewer-sea)

- Bead: aiag-rmv.7
- Commit range: 6a0a8fb (single commit)
- Plan doc: docs/plans/11-sandbox-host-service.add03.md
- Reviewer: reviewer-sea
- Review commit: 6a0a8fb601d4528c96d9a2004388675c90ed1c4e

## Findings

### P1-1 - handleConn goroutines not tracked or bounded during proxy close

**Location:** `internal/sandbox/process.go:389-413`

**Problem**
`portProxy.close()` closes all tracked connections and waits for the serve() goroutine via `<-p.done`. However, `handleConn` spawns two `io.Copy` goroutines (lines 404-410) that are not tracked. When `close()` forcibly closes the connections, io.Copy will eventually return, but the second io.Copy goroutine may linger if only one direction's connection close is noticed first. More critically, the `<-done` channel in handleConn only waits for one of the two copies to finish before returning — the other io.Copy goroutine keeps running until the deferred `Close()` in handleConn triggers.

The real issue: `handleConn` returns after `<-done` (one copy finishes), then defers close both connections. But the second io.Copy goroutine is still running. It will exit when the deferred closes happen, but there's a small window where the goroutine is orphaned. Under heavy connection load during destroy, this could accumulate.

**Suggested fix**
Wait for both io.Copy goroutines in handleConn (use a WaitGroup or receive from `done` twice since it's buffered at 2). This ensures no goroutine outlives handleConn's defer cleanup:
```go
<-done
<-done // wait for both directions
```

---

### P1-2 - gVisor launchInGVisor doesn't actually start the container

**Location:** `internal/sandbox/process.go:198-222`

**Problem**
`launchInGVisor` stores the gVisor context and options on the process struct but never calls `svc.gvisor.Run()` or `CreateContainer`/`StartContainer`. The actual container launch is deferred to `monitorGVisorProcess` (line 248), which calls `svc.gvisor.Run(runCtx, opts)`. This means LaunchProcess returns `ProcessStatusRunning` to the caller before the container has actually started — the status is a lie.

If `gvisor.Run()` fails immediately (e.g., invalid binary, OOM), the caller already has a "running" process ID and address. They'd have to poll GetProcessStatus to discover the failure, and the error detail is lost (exit code -1 with no error message).

**Suggested fix**
Either: (a) start the container synchronously in `launchInGVisor` and only return success after Run begins (similar to how `launchDirect` calls `cmd.Start()` synchronously), or (b) add a `ProcessStatusStarting` → `ProcessStatusRunning` transition in the monitor once Run() is actually underway, and return `ProcessStatusStarting` from LaunchProcess for the gVisor path. Option (a) is cleaner — block until the gVisor container is running, then return.

---

### P1-3 - Proxy created after process launch but before monitor starts — race window

**Location:** `internal/sandbox/process.go:107-120`

**Problem**
The flow is: launch process → create proxy → start monitor goroutine. If the process exits immediately (before proxy is created), the monitor goroutine will call `markProcessExited` which closes the proxy. But the proxy hasn't been assigned to `proc.proxy` yet (it's set at line 115-117 after `createPortProxy` returns). So `markProcessExited` reads `proc.proxy` as nil and skips proxy cleanup.

The reverse ordering also has issues: if proxy creation fails, the code kills the process and removes it from the map — but the monitor goroutine hasn't started yet, so `proc.done` is never closed. Any code waiting on `<-proc.done` will hang.

**Suggested fix**
Set up the proxy before launching the process (or at least before starting the monitor goroutine and in the same critical section). Ensure the monitor goroutine is always started if the process is in the map, so `proc.done` is always closed. Consider: create proxy → launch process (if fail, close proxy) → assign proxy → start monitor.

---

### P2-1 - destroySessionProcesses sends SIGTERM then waits only 2s before SIGKILL

**Location:** `internal/sandbox/process.go:322-348`

**Problem**
The plan specifies a 5-second drain timeout. The implementation uses 2 seconds for SIGTERM grace, then 2 more seconds after SIGKILL, totaling 4 seconds max. For gVisor containers where `Run()` is the blocking call, the SIGTERM path cancels the context, and `Run()` may take time to propagate. 2 seconds may be tight for container cleanup.

**Suggested fix**
Consider making the SIGTERM grace period configurable (like the plan's 5 seconds), or at minimum document why 2+2 was chosen over the plan's 5-second drain. This is P2 because the shorter timeout is defensible for test/dev, but could cause premature force-kills in production with slow container shutdown.

---

### P2-2 - LaunchProcess returns ProcessStatusRunning for direct path before monitor confirms

**Location:** `internal/sandbox/process.go:123-131`

**Problem**
After `launchDirect` succeeds, the status is set to `ProcessStatusRunning` inside `launchDirect` (line 216). The response at line 124 reads this status. This is correct for direct processes since `cmd.Start()` has succeeded. However, the status could race: if the process exits extremely quickly between Start() and the response read, the monitor goroutine could have already set status to `ProcessStatusExited`. The response would then say "running" but the process is already dead.

This is a minor TOCTOU inherent to async monitoring and acceptable per the plan's stance on monitoring lag. Flagging for awareness only.

**Suggested fix**
No change needed — document that status in the LaunchProcess response is best-effort and may be stale by the time the caller reads it.

---

### P2-3 - buildProcessEnv leaks host environment to launched processes

**Location:** `internal/sandbox/process.go:468-477`

**Problem**
`buildProcessEnv` starts from `os.Environ()` (the sandbox-host's full environment) and appends the request's env vars. This means every launched process inherits the sandbox-host service's environment, which may include credentials, internal paths, or debug flags not intended for the child process. The plan calls out env var injection as a security consideration but doesn't prescribe a clean environment.

For gVisor containers, this is less concerning since `opts.Env` is set to only `req.Env` (line 211) — the container starts with a clean environment. The asymmetry between direct and gVisor execution paths is a security gap.

**Suggested fix**
For direct execution, either: (a) start from a minimal base environment (PATH, HOME, TERM) rather than os.Environ(), or (b) document that direct execution inherits the host environment and ensure sensitive env vars are stripped. At minimum, filter out known sensitive vars like `ANTHROPIC_API_KEY` unless explicitly passed in `req.Env`.

---

### P2-4 - MemorySandboxService.DestroySession doesn't clean up processes

**Location:** `tests/integration/mode3/harness/memory_sandbox_service.go:243-253`

**Problem**
The existing `DestroySession` in `MemorySandboxService` deletes the session from the map but doesn't clean up the `processes` map. While this is test infrastructure, it means integration tests using MemorySandboxService won't catch bugs where destroy fails to clean up processes. The real SandboxHostService correctly calls `destroySessionProcesses` before ZFS/disk cleanup.

**Suggested fix**
Add process cleanup in MemorySandboxService.DestroySession: mark all processes as exited before deleting the session. This makes the mock more faithful to the real implementation.

---

### P3-1 - exitedProcessRetention (10 min) is reasonable but not configurable

**Location:** `internal/sandbox/process.go:21`

**Problem**
The 10-minute retention for exited processes is hardcoded. For long-running sessions with many process launches, this is reasonable. For tests, it means exited processes never get pruned within test timeframes.

**Suggested fix**
No change needed now. If this becomes an issue, make it a ServiceConfig field.

---

### P3-2 - Codec ToLaunchProcessRequest copies Args defensively — good

**Location:** `internal/rpc/codec/sandbox_map.go:97`

**Problem** (positive observation)
`append([]string(nil), req.Args...)` correctly creates a defensive copy of the args slice, preventing the caller from mutating the request after submission. This is the right pattern for codec boundaries.

---

### P3-3 - pruneExitedProcessesLocked called on every LaunchProcess

**Location:** `internal/sandbox/process.go:86`

**Problem**
Pruning on every launch is fine for typical workloads but could be slow if the process map is very large (thousands of entries). This is unlikely in practice — the 10-minute retention keeps the map small.

**Suggested fix**
No change needed. If this becomes a hotspot, amortize by pruning every Nth launch.

---

### P3-4 - KillProcess idempotency is correctly handled

**Location:** `internal/sandbox/process.go:280-284`

**Problem** (positive observation)
`killManagedProcess` returns nil if the process is already exited. The test at process_test.go:79 verifies this. Good.

---

### P3-5 - Interface compliance stubs are clean

**Location:** `internal/sandbox/environment/compliance_test.go`, `native/native_test.go`, `mode3/harness/`

**Problem** (positive observation)
All SandboxService interface implementors (complianceMockService, mockSandboxService, chaosSandboxService, MemorySandboxService) have been updated with the new methods. The patterns are consistent.

---

## Summary

11 findings: 0 P0, 3 P1, 4 P2, 4 P3

**Verdict**: Approved with revisions

The implementation is well-structured with clean separation across sandbox/process.go, RPC layer, codec, and transport. Process lifecycle, port proxying, and destroy cleanup are all present. The pruneExitedProcessesLocked addresses the memory leak concern raised during pre-review. Tests cover direct process lifecycle, port proxy traffic forwarding, session destroy cleanup, gVisor path, RPC round-trips, and codec mappings.

P1 items to address: (1) handleConn should wait for both io.Copy goroutines, (2) gVisor path returns "running" before container actually starts — either start synchronously or use "starting" status, (3) proxy setup and process launch ordering has a race window that can leak goroutines or skip proxy cleanup.
