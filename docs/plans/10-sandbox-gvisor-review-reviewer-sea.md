# Review: 10-sandbox-gvisor (R2)

- Source doc: `docs/plans/10-sandbox-gvisor.md`
- Test harness: `docs/plans/10-sandbox-gvisor-test-harness.md`
- Reviewed commit: 9352bce
- Reviewer: reviewer-sea
- Round: 2

## Findings

### P1 - checkOOMKill misclassifies timeouts and cancellations as OOM kills

**Problem**

`checkOOMKill` (§7.4, line ~1091-1116) determines OOM status by checking `exitCode == 137 || exitCode == -1`. However, `exitCode == -1` is a local sentinel value set by `Run()` for **three** distinct failure modes:

- `context.DeadlineExceeded` (timeout) — §7.3 line ~1031-1036
- `context.Canceled` (caller cancellation) — §7.3 line ~1037-1039
- Non-`ExitError` wait failures — §7.3 line ~1043-1048

In `Run()`, status is set correctly *before* the OOM check (e.g., `StatusTimedOut` for deadline exceeded), but then `checkOOMKill` returns `true` because `exitCode == -1`, and lines ~1053-1058 overwrite the status:

```go
result.OOMKilled = m.checkOOMKill(containerID, result.ExitCode)
if result.OOMKilled {
    result.Status = StatusOOMKilled  // overwrites StatusTimedOut or StatusKilled
    if m.oomKillCount != nil {
        m.oomKillCount.Add(ctx, 1)   // double-counts: timeoutCount already incremented
    }
}
```

Consequences:
1. Every timeout is reported as OOM kill to callers via `ContainerResult.Status` and `ContainerResult.OOMKilled`
2. Both `timeouts_total` and `oom_kills_total` OTEL metrics are incremented for a single timeout event
3. The sandbox host service (plan 11) may make wrong remediation decisions (e.g., reducing memory allocation instead of extending timeout)

Additionally, `checkOOMKill` queries `runsc state` and parses the response into a `state` struct, but **never uses the parsed state in its return condition**. The state check is dead code.

**Required fix**

`checkOOMKill` must not rely on `exitCode == -1`. Options:

1. **Check runsc state for OOM indicators**: Parse the `runsc state` JSON response for OOM-specific fields. gVisor reports OOM events in container state/events.
2. **Check cgroup memory.events**: Read `memory.oom_kill` counter from the container's cgroup v2 directory before the cgroup is cleaned up.
3. **Only use exitCode == 137 AND verify no context error**: Check `exitCode == 137` and confirm `execCtx.Err() == nil` (i.e., the SIGKILL came from the kernel OOM killer, not from context cancellation).

The OOM check should also run **only when the status hasn't already been determined** (i.e., skip when status is already `StatusTimedOut` or `StatusKilled`).

---

### P2 - ContainerResult.Duration never set; exec_duration_seconds metric always reports zero

**Problem**

`ContainerResult` defines both `BootDuration` and `Duration` fields, and `initMetrics()` (§11.1, line ~1360) creates two separate OTEL histograms:
- `sandbox.gvisor.boot_duration_seconds` → records `result.BootDuration`
- `sandbox.gvisor.exec_duration_seconds` → records `result.Duration`

In `Run()` (§7.3), `BootDuration` is set to `time.Since(bootStart)` where `bootStart` is just before `cmd.Start()` and the measurement is taken after `cmd.Wait()`. This captures the **total execution time** (boot + command execution + shutdown), not just boot time.

`result.Duration` is never assigned anywhere in `Run()`. It remains at its zero value, so `exec_duration_seconds` always records 0.

This means:
- `boot_duration_seconds` actually reports total execution time (misnamed)
- `exec_duration_seconds` always reports 0 (never set)
- Operators monitoring dashboards would see misleading metrics

**Required fix**

Either:
1. Set `result.Duration` to the total execution time and rename `BootDuration` to measure only the time from `cmd.Start()` to when the container is ready (if measurable), or
2. Remove the separate `Duration` field and `exec_duration_seconds` metric if they're redundant with `BootDuration` for single-command containers, or
3. Keep both and set `result.Duration = time.Since(bootStart)` (same as BootDuration for now, but allows future differentiation when container pre-warming is added per D1)

---

## Summary

2 findings: 0 P0, 1 P1, 1 P2, 0 P3

**Verdict**: Approved with revisions

R1 findings were all properly incorporated (7/7). The disposition table is present and complete. Cross-doc terminology is consistent (uses "Tool Call Sandbox" correctly, references sandbox host service and ZFS manager appropriately). The test harness doc is comprehensive and required no changes.
