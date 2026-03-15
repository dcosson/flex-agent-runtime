# Review: 11-sandbox-host-service.add02 (reviewer-sea)

- Source doc: `docs/plans/11-sandbox-host-service.add02.md`
- Reviewed commit: c42f450
- Reviewer: reviewer-sea

## Findings

### P1 - No acceptance criteria section

**Problem**
The plan has no acceptance criteria section. Both the parent plan (11-sandbox-host-service.md) and addendum 01 define concrete acceptance criteria (AC1-AC5 in add01). This addendum introduces four backend modes, capability negotiation, constructor validation, and degraded-mode behavior that all need end-to-end acceptance criteria crossing component boundaries.

Without acceptance criteria, there is no testable definition of "done" for this plan.

**Required fix**
Add an acceptance criteria section with scenarios covering:
1. Session lifecycle with local-disk + no gVisor (full degraded mode) — create session, execute tool, verify no snapshots, destroy
2. Capability negotiation failure — client configured for ZFS, server running local-disk, Create() fails with clear error
3. Constructor validation — ZFS configured but ZFSManager nil returns error at construction time
4. Mixed mode — ZFS + no gVisor: snapshots work, all tools route to Tier 1
5. PerToolSnapshots silently skipped on local-disk — no error, warning logged

Each scenario should cross at least one component boundary (e.g., NativeSandboxEnvironment -> SandboxHostService via RPC).

---

### P2 - No testing strategy section

**Problem**
The plan has no testing strategy section. Add01 specifies T1-T11 covering unit, integration, backward compatibility, and contract tests. This addendum adds new behavior (four backend modes, constructor validation, capability negotiation, capability-gated methods, local-disk session management) that needs test coverage specified.

**Required fix**
Add a testing section covering at minimum:
- Constructor validation tests (all four valid configs + invalid combos like ZFS with nil ZFSManager)
- Local-disk session lifecycle tests (CreateSession creates directory, DestroySession removes it)
- Capability-gated method tests (CreateSnapshot returns ErrCapabilityNotSupported on local-disk)
- ExecuteTool tier downgrade test (Tier 2 tool runs as Tier 1 when ContainerRuntime is "none")
- Capability negotiation test (client/server mismatch detected at Create time)
- Dynamic Capabilities() correctness (each of the four config combos returns expected capabilities)
- HealthCheck behavior per backend

---

### P2 - Missing connected components / seam impacts section

**Problem**
The plan makes several changes with cross-component impact but does not document them:
1. Constructor signature changes from `*SandboxHostService` to `(*SandboxHostService, error)` — breaks all callers (cmd/sandbox-host, RPC server setup, tests)
2. `NativeSandboxCapabilities` package var removal — breaks compliance tests and property tests that reference it
3. `CreateSessionResponse` gains `ServerCapabilities` field — requires plan 13 RPC layer change
4. Native package gains import dependency on `internal/sandbox` for `StorageBackend`/`ContainerRuntime` types — not documented in add01's import flow (§2.4)

Add01 had a thorough §9 section covering new seams, modified seams, unchanged seams, and explicit cross-plan dependency tracking (§9.5 for CreateSnapshot RPC). This addendum should do the same.

**Required fix**
Add a connected components section documenting:
- Modified seams (constructor signature, NativeSandboxCapabilities removal)
- Cross-plan dependency: plan 13 needs `ServerCapabilities` in `CreateSessionResponse`
- Updated import flow: `internal/sandbox/environment/native` now imports `internal/sandbox` for config types

---

### P2 - Constructor doesn't validate unknown StorageBackend/ContainerRuntime values

**Problem**
§2.3 validates config/manager consistency (ZFS needs ZFSManager, gVisor needs GVisorManager, local-disk needs SessionsRootDir) but does not reject unknown or empty values for `StorageBackend` and `ContainerRuntime`. An empty string, typo like `"zf"`, or any unrecognized value would pass all three validation checks and cause unpredictable runtime failures downstream (e.g., falling through switch statements in CreateSession, ExecuteTool).

**Required fix**
Add validation at the top of the constructor:
```go
switch cfg.StorageBackend {
case StorageBackendZFS, StorageBackendLocalDisk:
default:
    return nil, fmt.Errorf("sandbox: unknown storage_backend: %q", cfg.StorageBackend)
}
switch cfg.ContainerRuntime {
case ContainerRuntimeGVisor, ContainerRuntimeNone:
default:
    return nil, fmt.Errorf("sandbox: unknown container_runtime: %q", cfg.ContainerRuntime)
}
```

---

### P2 - Capability negotiation requires plan 13 RPC change but not tracked as cross-plan dependency

**Problem**
§6.1 requires adding a `ServerCapabilities` field to `CreateSessionResponse` in the RPC layer. This is a plan 13 change. Add01 explicitly tracked the `CreateSnapshot` RPC gap in §9.5 with exact request/response types specified. This addendum should similarly document the RPC change needed for capability negotiation, since plan 13 implementers need to know about it.

**Required fix**
Add a cross-plan dependency section (similar to add01 §9.5) documenting:
- Plan 13's `CreateSessionResponse` needs a `ServerCapabilities` field
- The `Capabilities` struct type that will be included
- How `SandboxHostService` computes its capabilities from its config (this method doesn't exist yet)

---

### P3 - Agent-loop capability validation mechanism is underspecified

**Problem**
§7.2 says the agent loop should validate capabilities "either by calling a `GetCapabilities` RPC on its first interaction, or by receiving capabilities in the response to its first `ExecuteTool` call." Neither mechanism exists:
- No `GetCapabilities` RPC is defined anywhere in the plan or plan 13
- `ExecuteToolResponse` does not include capabilities

This leaves the agent-loop-side validation as a hand-wave rather than a concrete design.

**Required fix**
Commit to one approach and specify it. Recommended: define a `GetCapabilities` RPC in the cross-plan dependency section, or state that the agent loop relies on config consistency enforced at deployment time and defers runtime validation to a future iteration.

---

### P3 - Periodic capability re-validation is speculative

**Problem**
The last paragraph of §6.1 says "consider adding periodic health checks that re-validate capabilities have not changed." The word "consider" is not a decision. This should either be specified concretely (what RPC, what interval, what happens on mismatch) or explicitly deferred.

**Required fix**
Either commit to a design (e.g., "HealthCheck RPC returns capabilities; client compares every N seconds") or explicitly defer: "Periodic capability re-validation is deferred to a future addendum."

---

### P3 - PerToolSnapshots grouped under "General config" but is ZFS-specific

**Problem**
§2.2 places `PerToolSnapshots` in the "General config (used regardless of backend)" section, but the comment says "only effective when StorageBackend == zfs" and §8.3 documents it as silently skipped on local-disk. This is misleading — a config field in the "used regardless of backend" section that only works with one backend violates the section's own contract.

**Required fix**
Move `PerToolSnapshots` to the "ZFS-specific config" section, or rename the "General config" section to something that doesn't imply universal applicability (e.g., "Behavioral config").

---

## Summary

8 findings: 0 P0, 1 P1, 4 P2, 3 P3

**Verdict**: Approved with revisions
