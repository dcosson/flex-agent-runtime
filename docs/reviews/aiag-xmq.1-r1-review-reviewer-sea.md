# R1 Review: aiag-xmq.1 -- gVisor Core Types, OCI Spec Builder, Cgroup Translator

**Reviewer:** reviewer-sea
**Branch:** batch4/gvisor-core @ f48ef23
**Plan:** docs/plans/10-sandbox-gvisor.md (sections 3, 4.1, 4.2, 4.3, 5, 6)
**Date:** 2026-03-13

---

## Summary

Solid implementation that faithfully follows the plan for core types, OCI spec builder, and cgroup v2 translator. All 38 tests pass with `-race`. Code is clean (`gofmt`, `go vet`, `staticcheck` all pass). The OCI spec structure is correct and produces valid JSON. Types match plan specifications closely with only minor deviations.

**Verdict: Approve with findings.** Two P2 findings should be fixed before merge; the rest are P3 recommendations for follow-up.

---

## Findings

### F1 [P2] -- `ToCgroupV2Entries` writes incorrect `memory.swap.max` value

**File:** `internal/sandbox/gvisor/cgroups.go:127-130`

The `ToCgroupV2Entries` function writes `memory.swap.max` with the full memory limit value, but in cgroup v2, `memory.swap.max` controls the maximum **swap-only** portion (not the combined total). Setting it equal to the memory limit means the cgroup can use that much swap *in addition* to its memory allocation -- the opposite of "disable swap."

```go
// Current (incorrect for cgroup v2 semantics):
entries = append(entries, CgroupV2Entry{
    File:  "memory.swap.max",
    Value: fmt.Sprintf("%d", limit),  // allows `limit` bytes of swap
})
```

To truly disable swap in cgroup v2, `memory.swap.max` should be `0`:
```go
entries = append(entries, CgroupV2Entry{
    File:  "memory.swap.max",
    Value: "0",
})
```

Note: The OCI spec builder (`buildCgroupResources`) is correct -- the OCI `Swap` field represents "total memory+swap," so setting `Swap == Limit` properly disables swap through the runtime. Only the `ToCgroupV2Entries` helper has wrong semantics. Since `ToCgroupV2Entries` is described as "used for testing and documentation," this is still important to fix so tests and docs reflect reality.

### F2 [P2] -- `ManagerConfig` missing `Logger` field from plan

**File:** `internal/sandbox/gvisor/types.go:166-175`

The plan (section 4.4) specifies `Logger *slog.Logger` as a field on `ManagerConfig`. The implementation omits it. Since `ManagerConfig` is being defined in this bead and the next bead (aiag-xmq.2 -- manager implementation) will need it, adding it now avoids a type-breaking change later.

```go
// Plan specifies:
type ManagerConfig struct {
    // ...existing fields...
    Logger *slog.Logger  // missing
    // ...
}
```

### F3 [P2] -- `detectOOMKill` exit code 137 fallback deviates from plan

**File:** `internal/sandbox/gvisor/cgroups.go:92-98`

The plan's `detectOOMKill` (section 6.3) returns `false` when the cgroup path is unavailable or `oom_kill` is 0, delegating exit-code-based OOM classification to the manager's `checkOOMKill` method instead. The implementation adds a blanket `return exitCode == 137` fallback, which means ANY exit code 137 (even non-OOM SIGKILL) is classified as OOM when cgroup data is unavailable.

This creates a false-positive risk: a process killed by `kill -9` from inside the container would be incorrectly flagged as OOM. The plan intentionally separated the cgroup-level OOM detection (`detectOOMKill`) from the manager-level exit-code classification (`checkOOMKill` with `runsc state` inspection).

**Recommendation:** Remove the `return exitCode == 137` fallback. Let the caller (future manager code) handle exit-code-based OOM classification with additional context from `runsc state`.

### F4 [P3] -- `ManagerConfig` missing health/debug fields from implementation guide

**File:** `internal/sandbox/gvisor/types.go:166-175`

The implementation guide (section 3.1 Config Contract) lists additional `ManagerConfig` fields: `HealthCheckInterval`, `StaleContainerTimeout`, and `DebugLogging`. These are not present in the current implementation. While these could be deferred to aiag-xmq.2, defining the full config surface now would give the manager implementation a stable type to work with.

### F5 [P3] -- No `Hostname` field in OCI spec

**File:** `internal/sandbox/gvisor/spec.go:11-19`

The generated OCI spec does not set the `Hostname` field. While not required, setting a deterministic hostname (e.g., based on the container ID or a fixed string like `sandbox`) improves debuggability and prevents the container from inheriting the host's hostname, which could leak host identity information.

### F6 [P3] -- Missing `CgroupNamespace` in namespace list

**File:** `internal/sandbox/gvisor/spec.go:87-93`

The spec creates PID, Mount, IPC, UTS, and Network namespaces but omits `CgroupNamespace`. Adding a cgroup namespace provides better isolation of the cgroup hierarchy visible inside the container. This is recommended for defense-in-depth. The plan does not explicitly require it, so this is advisory.

### F7 [P3] -- `Inheritable` and `Ambient` capabilities not explicitly set

**File:** `internal/sandbox/gvisor/spec.go:43-47`

The capabilities only set `Bounding`, `Effective`, and `Permitted`. Best practice for OCI specs is to explicitly set `Inheritable` and `Ambient` to empty slices to prevent capability inheritance. gVisor handles this safely regardless, but being explicit is defense-in-depth.

### F8 [P3] -- Test helper `contains`/`searchString` reimplements `strings.Contains`

**File:** `internal/sandbox/gvisor/types_test.go:126-133`

The test file defines custom `contains` and `searchString` functions that replicate `strings.Contains` from the standard library. These should be replaced with `strings.Contains` for clarity.

---

## Plan Compliance

| Plan Section | Status | Notes |
|---|---|---|
| 3 (Package structure) | Compliant | types.go, spec.go, cgroups.go match plan layout |
| 4.1 (GVisorManager interface) | Compliant | Exact match with plan signatures |
| 4.2 (ContainerOptions, ResourceSpec, types) | Compliant | All fields present and correct |
| 4.3 (ContainerResult, ContainerStatus) | Compliant | All status values match plan |
| 4.4 (ManagerConfig) | Partial | Missing `Logger` field (F2) |
| 5.1-5.4 (OCI spec builder) | Compliant | Matches plan closely |
| 6.1 (Resource translation) | Compliant | OCI cgroup resources correct |
| 6.2 (Resource validation) | Compliant | All boundaries checked |
| 6.3 (OOM detection) | Deviant | Exit code fallback differs (F3) |

## Test Coverage

- **types_test.go** (142 lines): Covers validation (valid/invalid options), status/network mode enumerations, constants. Good edge case coverage for validation bounds.
- **spec_test.go** (280 lines): Covers basic structure, read-only root, process config, user handling, env defaults/overrides/sorting, namespace verification, mount verification including extra mounts, JSON round-trip. Thorough.
- **cgroups_test.go** (327 lines): Covers CPU (integer, fractional, zero), memory (with swap), PIDs (custom, default), full spec, validation (valid/invalid), OOM detection (cgroup events, exit code, missing path), cgroup v2 entry generation. Comprehensive.

Total: 749 lines of tests for 484 lines of implementation (1.55:1 ratio). Good coverage.

## Security

- `NoNewPrivileges: true` is set correctly.
- Capabilities are minimal and appropriate for build/test workloads.
- Mount options include `nosuid`, `noexec`, `nodev` on sensitive filesystems.
- `/sys` is mounted read-only.
- Namespace isolation covers the essential set (PID, Mount, IPC, UTS, Network).
- Resource validation prevents unreasonable limits (negative values, excessive CPU/memory).

No security issues identified.

## Race Conditions

All 38 tests pass with `-race` enabled. The types in this bead are value types and builder functions with no shared mutable state, so race conditions are not applicable at this level. The concurrent access patterns will be relevant in aiag-xmq.2 when the `Manager` implementation is added.
