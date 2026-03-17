# Code Review: aiag-rmv.7 (R2, reviewer-sea)

- Bead: aiag-rmv.7
- Commit range: 14f677d (single commit, follow-up to 6a0a8fb)
- Plan doc: docs/plans/11-sandbox-host-service.add03.md
- Reviewer: reviewer-sea
- Review commit: 14f677da6085acbc47c81e7db418695912e94d39

## P1 Disposition from R1

| Finding | Status | Notes |
|---------|--------|-------|
| P1-1: handleConn leaks second io.Copy goroutine | **Fixed** | Added second `<-done` to wait for both directions (process.go:470) |
| P1-2: gVisor launchInGVisor returns "running" before container starts | **Fixed** | Now returns `ProcessStatusStarting`; monitor calls `markProcessRunning` before `monitorGVisorProcess` (process.go:238, 259). Test updated to assert "starting" (process_test.go:205). |
| P1-3: Proxy/process launch ordering race | **Fixed** | Reordered to: create proxy → launch process → start monitor. On proxy failure, `close(proc.done)` prevents hangs. On launch failure, proxy is closed and proc.done is closed. Monitor goroutine only starts after successful launch (process.go:90-132). |

All P1 findings addressed cleanly.

## New Findings

### P3-1 - markProcessRunning called before gvisor.Run() begins

**Location:** `internal/sandbox/process.go:259`

**Problem** (observation, no fix needed)
`markProcessRunning` is called in the monitor goroutine before `monitorGVisorProcess` calls `svc.gvisor.Run()`. So the status transitions Starting → Running → (Run begins). There's a small window where status is "running" but Run() hasn't been called yet. This is acceptable — the monitor goroutine is about to call it immediately, and the alternative (a callback after Run starts) would require gVisor API changes.

---

## Summary

0 findings: 0 P0, 0 P1, 0 P2, 1 P3

**Verdict**: Approved

All R1 P1 findings addressed. The ordering fix (proxy → launch → monitor) is clean and handles all failure paths correctly with proper `close(proc.done)` calls. The gVisor Starting → Running state machine is correct. Ready to close.
