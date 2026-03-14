# R2 Re-Review: aiag-xmq.1 & aiag-xmq.2 -- gVisor Core Types + Container Lifecycle

**Reviewer:** reviewer-sea
**Round:** R2 (re-review of R1 findings)
**Branch:** batch4/gvisor-lifecycle @ c3f4509
**Date:** 2026-03-13

---

## Summary

All five actionable findings from R1 have been addressed correctly. The fixes are clean and follow the recommended approaches. Tests pass with `-race` (60+ tests, 10.9s). `go vet` passes clean. Two minor `gofmt` issues remain (import order in exec.go, trailing newline in types_test.go) -- noted below but not blocking.

**Verdict: Approved.**

---

## R1 Finding Resolution

### aiag-xmq.1 Findings

| Finding | Severity | Status | Notes |
|---|---|---|---|
| F1: `ToCgroupV2Entries` swap.max incorrect | P2 | **Fixed** | Now writes `"0"` instead of the memory limit value. Test updated to assert `"0"`. Correct cgroup v2 semantics. |
| F2: `ManagerConfig` missing `Logger` field | P2 | N/A | Not in scope for this fix round (deferred to manager integration). |
| F3: `detectOOMKill` exit code 137 fallback | P2 | **Fixed** | Renamed to `detectOOMFromCgroup`. Exit code parameter and 137 fallback removed. Function now only reads cgroup memory.events and returns false when unavailable. Clean separation of concerns. |
| F4: Missing health/debug config fields | P3 | N/A | Deferred (P3). |
| F5-F7: Hostname, CgroupNamespace, capabilities | P3 | N/A | Deferred (P3 advisory). |
| F8: Custom `contains`/`searchString` helpers | P3 | **Fixed** | Replaced with `strings.Contains` in both `types_test.go` and `cgroups_test.go`. Custom helper functions removed. |

### aiag-xmq.2 Findings

| Finding | Severity | Status | Notes |
|---|---|---|---|
| F1: `checkOOMKill` dead state-query code | MEDIUM | **Fixed** | Completely rewritten. Now delegates to `detectOOMFromCgroup` via `cgroupPathForContainer`. No more dead `runsc state` JSON parsing. No exit code 137 fallback. |
| F2: `readPeakMemory` no-op stub | MEDIUM | **Fixed** | Stub removed entirely. The call from `runContainer` is also removed. `PeakMemoryBytes` field remains in `ContainerResult` (correct -- it's a type field for future use). |
| F3: `detectOOMKill` orphaned in cgroups.go | MEDIUM | **Fixed** | The old `detectOOMKill` is now `detectOOMFromCgroup` and is actively called by `Manager.checkOOMKill` via `cgroupPathForContainer`. No more duplicate/orphaned implementations. |
| F4-F10 | LOW/MINOR | N/A | These were not targeted for this fix round. |

---

## New Observations

### N1 [MINOR] -- `gofmt` not clean on two files

`exec.go` has imports in non-alphabetical order (`"path/filepath"` before `"os/exec"`), and `types_test.go` has a trailing blank line. Both are trivially fixable with `gofmt -w`. Not blocking.

---

## Verification

- All tests pass: `go test -race ./internal/sandbox/gvisor/... -count=1` -- OK (10.9s)
- `go vet ./internal/sandbox/gvisor/...` -- clean
- `gofmt` -- two minor issues noted above (N1)
- Diff reviewed: 39cf839..c3f4509 confirms only the targeted fixes, no unrelated changes
