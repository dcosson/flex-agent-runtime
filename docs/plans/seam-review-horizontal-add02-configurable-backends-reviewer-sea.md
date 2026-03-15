# Seam Review: add02-configurable-backends (horizontal) — reviewer-sea

- Mode: horizontal
- Seam: add02-configurable-backends
- Reviewed commit: 4fb01fa
- Reviewer: reviewer-sea
- Plan docs reviewed:
  - `docs/plans/11-sandbox-host-service.add02.md` (source of changes)
  - `docs/plans/11-sandbox-host-service.md` (parent plan — SandboxHostService)
  - `docs/plans/11-sandbox-host-service.add01.md` (ExecutionEnvironment interface)
  - `docs/plans/13-rpc-layer.md` (RPC API types, SandboxService interface)
  - `docs/plans/17-external-testing.md` (external testing referencing backend configs)

## Seam Boundaries Analyzed

### Seam: add02 ↔ Plan 13 (RPC Layer)

**Plan docs**: `11-sandbox-host-service.add02.md` (producer), `13-rpc-layer.md` (consumer/provider of RPC types)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | add02 §9.2 specifies `CreateSessionResponse` gains `ServerCapabilities` field; plan 13 §4.1 defines `CreateSession` returning `*CreateSessionResponse` — additive field change is compatible |
| Data formats/types | FAIL | add02 references `Capabilities` struct in `CreateSessionResponse` but doesn't specify whether this is `environment.Capabilities` or a separate RPC-level transport type (see findings) |
| Lifecycle ordering | PASS | Capability negotiation at `Create()` time fits the existing session lifecycle flow |
| Configuration contracts | PASS | add02 §9.2 mixed-version rollout behavior is well-specified |
| Error handling | PASS | Capability mismatch returns a clear `fmt.Errorf` at Create() time on the client side |

---

### Seam: add02 ↔ Plan 11 (SandboxHostService parent)

**Plan docs**: `11-sandbox-host-service.add02.md` (modifier), `11-sandbox-host-service.md` (provider of SandboxHostService)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | FAIL | add02 §9.2 says "SandboxHostService.CreateSession computes capabilities and includes them in the response" but plan 11's `CreateSession` returns `(*SessionInfo, error)` — no mechanism to return capabilities (see findings) |
| Data formats/types | PASS | `ServiceConfig` gains new fields (StorageBackend, ContainerRuntime) in a backward-compatible way |
| Lifecycle ordering | PASS | Constructor validation (§2.3) runs before any session operations |
| Configuration contracts | PASS | nil ZFSManager/GVisorManager handling is well-specified |
| Error handling | PASS | `ErrSnapshotsNotAvailable` (server-side) vs `ErrCapabilityNotSupported` (client-side) are properly layered |

---

### Seam: add02 ↔ Plan 11.add01 (ExecutionEnvironment)

**Plan docs**: `11-sandbox-host-service.add02.md` (modifier), `11-sandbox-host-service.add01.md` (provider of NativeSandboxCapabilities, interface)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | `ExecutionEnvironment` interface is unchanged; `NativeSandboxEnvironment` signature change (gains `NativeSandboxConfig`) documented in §9.1 |
| Data formats/types | PASS | `Capabilities` struct unchanged; `NativeSandboxCapabilities` var removed, replaced by config-derived `Capabilities()` |
| Lifecycle ordering | PASS | Capability negotiation at `Create()` is compatible with add01's lifecycle |
| Configuration contracts | PASS | `NativeSandboxConfig.StorageBackend`/`ContainerRuntime` types from `internal/sandbox` are importable by native package without circular dependency |
| Error handling | PASS | Capability-gated methods return `ErrCapabilityNotSupported` consistent with add01's contract |

---

### Seam: add02 ↔ Plan 17 (External Testing)

**Plan docs**: `11-sandbox-host-service.add02.md` (provider of backend configs), `17-external-testing.md` (consumer)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Plan 17 uses `native.NewNativeSandboxEnvironment(client, native.NativeSandboxConfig{...}, logger)` — matches add02's constructor |
| Data formats/types | PASS | `StorageBackendLocalDisk`, `ContainerRuntimeNone`, `ContainerRuntimeGVisor` constant names and values match |
| Lifecycle ordering | PASS | Test scenarios follow create → execute → destroy lifecycle correctly |
| Configuration contracts | PASS | Plan 17's Docker compose configs use env vars (`SANDBOX_STORAGE_BACKEND: "local-disk"`) consistent with add02's enum string values |
| Error handling | PASS | Plan 17 S9 references `ErrSnapshotsNotAvailable` (server), R8 references `ErrCapabilityNotSupported` (client) — both correct for their respective layers |

---

## Acceptance Criteria Cross-Reference

| Acceptance criterion (source doc) | Seams touched | Status | Details |
|-----------------------------------|---------------|--------|---------|
| AC1 — Full degraded mode lifecycle (add02) | add02↔plan11, add02↔plan13 | PASS | Local-disk + no gVisor lifecycle uses existing RPC path; no new interface needed |
| AC2 — Capability negotiation failure (add02) | add02↔plan13 | PASS | Requires ServerCapabilities in CreateSessionResponse (§9.2 tracks dependency) |
| AC3 — Constructor validation (add02) | add02↔plan11 | PASS | Internal to SandboxHostService, no cross-plan seam |
| AC4 — Mixed mode ZFS + no gVisor (add02) | add02↔plan11, add02↔plan13 | PASS | ZFS snapshots work via existing RPC; Tier 1 downgrade is service-internal |
| AC5 — PerToolSnapshots skip (add02) | add02↔plan11 | PASS | Service-internal no-op, no cross-plan impact |
| AC6 — Session ID path traversal (add02) | add02↔plan11 | PASS | ValidateSessionID is service-internal |

## Seam Compatibility Matrix

| Seam | Signatures | Data | Lifecycle | Config | Errors | Overall |
|------|-----------|------|-----------|--------|--------|---------|
| add02 ↔ Plan 13 | PASS | FAIL | PASS | PASS | PASS | FAIL |
| add02 ↔ Plan 11 | FAIL | PASS | PASS | PASS | PASS | FAIL |
| add02 ↔ add01 | PASS | PASS | PASS | PASS | PASS | PASS |
| add02 ↔ Plan 17 | PASS | PASS | PASS | PASS | PASS | PASS |

## Findings

### P2 - SandboxHostService lacks a mechanism to expose capabilities to the RPC handler

**Seam**: add02 ↔ Plan 11 (SandboxHostService)
**Category**: Interface signatures

**Problem**
Add02 §9.2 says "SandboxHostService.CreateSession computes capabilities from its ServiceConfig and includes them in the response." But plan 11's `SandboxHostService.CreateSession` returns `(*SessionInfo, error)` — it has no mechanism to return capabilities alongside the session info. The RPC handler (`internal/rpc/server/sandbox_server.go`, plan 13) constructs `CreateSessionResponse` from the `SessionInfo` returned by `SandboxHostService`, but has no access to the service's capability configuration.

The data flow gap: `SandboxHostService` knows its config → but doesn't expose a `Capabilities()` method → so the RPC handler can't include capabilities in `CreateSessionResponse` → so the client can't perform capability negotiation.

**Required fix**
Specify one of:
(a) `SandboxHostService` gains a public `Capabilities() Capabilities` method that computes capabilities from its `ServiceConfig`. The RPC handler calls this when constructing `CreateSessionResponse`.
(b) The RPC handler receives `ServiceConfig` at construction time and computes capabilities itself.

Option (a) is cleaner — the service owns the capability computation, and the RPC handler just proxies it.

---

### P3 - Capabilities type at the RPC API boundary is ambiguous

**Seam**: add02 ↔ Plan 13 (RPC Layer)
**Category**: Data formats/types

**Problem**
Add02 §9.2 says `CreateSessionResponse` gains a `ServerCapabilities Capabilities` field and lists 5 bool fields (`Snapshots`, `Rollback`, `Pause`, `TierRouting`, `StreamingProgress`). It doesn't specify whether this is `environment.Capabilities` (from `internal/sandbox/environment`) or a separate RPC-level transport type.

Plan 13 follows a codec pattern — `internal/rpc/codec/sandbox_map.go` maps between domain types and RPC API types. Following this pattern, there should be a separate `api.Capabilities` type with codec mapping. But `environment.Capabilities` also has `MaxSessionDuration time.Duration` and `ConcurrentSessions int` fields (add01 §4.1) not listed in add02 §9.2's 5-field subset.

If the implementer imports `environment.Capabilities` directly into `rpc/api`, the response would include those extra fields. If a separate `api.Capabilities` is defined with only 5 fields, the codec must select which fields to include.

**Required fix**
Clarify that the RPC layer should define its own `api.Capabilities` struct (matching plan 13's existing codec pattern) containing the 5 fields listed in §9.2, with a codec mapping from `SandboxHostService`'s computed capabilities.

---

## Summary

4 seam boundaries analyzed, 2 findings: 0 P0, 0 P1, 1 P2, 1 P3

Seam compatibility: 2/4 seams fully compatible

**Verdict**: Seams compatible with revisions
