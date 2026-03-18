# Seam Review: fleet-management (horizontal) — coder-1-sea

- Mode: Horizontal
- Seam: fleet-management
- Reviewed commit: d157360
- Reviewer: coder-1-sea
- Plan docs reviewed:
  - `docs/plans/20-fleet-management.md`
  - `docs/plans/18-agent-loop-rpc.md`
  - `docs/plans/11-sandbox-host-service.md`
- Code contracts reviewed:
  - `internal/sandbox/control/control.go`
  - `internal/sandbox/control/native/native.go`
  - `internal/rpc/api/types.go`
  - `internal/rpc/server/sandbox_server.go`
  - `internal/sandbox/service.go`

## Seam Boundaries Analyzed

### Seam: FleetSandboxControl ↔ SandboxControl interface

**Artifacts**: `docs/plans/20-fleet-management.md`, `internal/sandbox/control/control.go`, `docs/plans/18-agent-loop-rpc.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Plan method set matches `SandboxControl` (`Create/Destroy`, process ops, pause/resume, `Capabilities`). |
| Data formats/types | FAIL | `CreateSandboxResponse.Address` contract is `"host:port"` in control interface/plan 18, but plan 20 delegation path does not pin fleet-side normalization and relies on delegated response payload. |
| Lifecycle ordering | PASS | Claim-slot + delegation + rollback ordering is consistent with interface semantics. |
| Configuration contracts | PASS | `FleetConfig` is additive and does not conflict with `SandboxControl` request/response types. |
| Error handling | PASS | Invalid ID and RPC failure paths are documented and mapped to caller-visible errors. |

---

### Seam: FleetSandboxControl ↔ NativeSandboxControl

**Artifacts**: `docs/plans/20-fleet-management.md`, `internal/sandbox/control/native/native.go`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Delegation model and `SandboxClientFactory` return type (`control.SandboxControl`) align. |
| Data formats/types | FAIL | `native.CreateSandbox` currently maps `Address` to session mountpoint, while fleet/control contract expects routable `"host:port"` address semantics. |
| Lifecycle ordering | PASS | Delegation after sandbox ID parse and slot accounting is coherent. |
| Configuration contracts | PASS | Per-instance client factory pattern is compatible with native control construction. |
| Error handling | PASS | Delegated errors are surfaced; rollback/reconciliation paths are documented. |

---

### Seam: InstanceProvisioner ↔ EC2InstanceProvisioner

**Artifacts**: `docs/plans/20-fleet-management.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | `InstanceProvisioner` method set maps cleanly to EC2 operations, including `ListInstances`. |
| Data formats/types | PASS | `InstanceConfig` is provider-neutral; AWS-only fields moved to `EC2LaunchConfig`. |
| Lifecycle ordering | PASS | Non-blocking `LaunchInstance` + readiness detection via health polling is coherent. |
| Configuration contracts | PASS | Provider-specific launch config injection at constructor boundary is clear. |
| Error handling | PASS | Idempotent terminate and retry expectations are specified in contract tests. |

---

### Seam: FleetSandboxControl ↔ sandbox-host HealthCheck RPC

**Artifacts**: `docs/plans/20-fleet-management.md`, `internal/sandbox/control/control.go`, `internal/rpc/api/types.go`, `internal/rpc/server/sandbox_server.go`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | FAIL | Plan requires periodic `HealthCheck` calls, but `ManagedInstance.Client`/`SandboxClientFactory` are typed only as `control.SandboxControl` (no `HealthCheck` method). |
| Data formats/types | FAIL | Plan text claims HealthCheck provides CPU/memory/pool-space data; current RPC health payload exposes `status/pool_state/session_count/active_tools/uptime/errors` only. |
| Lifecycle ordering | FAIL | Control-loop phases depend on health polling, but the seam for obtaining a health-capable client is not specified. |
| Configuration contracts | PASS | `SandboxHostPort` and per-instance addressing assumptions are consistent at high level. |
| Error handling | PASS | Degraded/unhealthy/error handling policy is documented once health calls are available. |

---

### Seam: SandboxID encoding/decoding boundary

**Artifacts**: `docs/plans/20-fleet-management.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | ID format `fleet:<instanceID>:<sessionID>` is stable and routing-focused. |
| Data formats/types | FAIL | Parser snippet omits explicit empty-component checks, while adjacent validation/tests require rejecting empty components. |
| Lifecycle ordering | PASS | ID decode precedes delegation consistently. |
| Configuration contracts | PASS | No cross-component config dependency beyond prefix/format constants. |
| Error handling | PASS | Prefix/separator error paths are documented. |

---

### Seam: Fleet control loop ↔ InstanceProvisioner lifecycle/error handling

**Artifacts**: `docs/plans/20-fleet-management.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Scale-up/down/drain flows map to provisioner lifecycle methods. |
| Data formats/types | PASS | `InstanceInfo/Status` and cloud state mapping are sufficient for state transitions. |
| Lifecycle ordering | PASS | Provisioning, draining, timeout, and termination phases are consistent. |
| Configuration contracts | PASS | `Min/Max/WarmPool/Timeout` controls align with control-loop decisions. |
| Error handling | PASS | Timeout + retry + idempotent terminate expectations are coherent. |

## Acceptance Criteria Cross-Reference

| Acceptance criterion (source doc) | Seams touched | Status | Details |
|-----------------------------------|---------------|--------|---------|
| `CreateSandbox` routes and returns fleet-prefixed ID (`20` §11.1) | Fleet↔SandboxControl, Fleet↔Native, SandboxID boundary | FAIL | Sandbox ID path is specified, but response address semantics are currently ambiguous/drifting at the native boundary. |
| Health reconciliation + unhealthy drain logic (`20` §4.1.3, §5.1, §11.1) | Fleet↔HealthCheck, Control loop↔Provisioner | FAIL | Health-driven phases are specified but the health-capable client seam is currently undefined. |
| Crash recovery via `ListInstances` (`20` §14 OQ2, §11.1) | Provisioner↔EC2, Control loop↔Provisioner | PASS | Discovery/filtering/re-adoption path is coherent with updated `ListInstances` contract. |
| Delegated process/sandbox ops via encoded SandboxID (`20` §4.3, §11.1) | Fleet↔SandboxControl, Fleet↔Native, SandboxID boundary | FAIL | Delegation shape is coherent, but parser snippet inconsistency and create-address drift leave boundary ambiguity. |

## Seam Compatibility Matrix

| Seam | Signatures | Data | Lifecycle | Config | Errors | Overall |
|------|-----------|------|-----------|--------|--------|---------|
| FleetSandboxControl ↔ SandboxControl | PASS | FAIL | PASS | PASS | PASS | FAIL |
| FleetSandboxControl ↔ NativeSandboxControl | PASS | FAIL | PASS | PASS | PASS | FAIL |
| InstanceProvisioner ↔ EC2InstanceProvisioner | PASS | PASS | PASS | PASS | PASS | PASS |
| FleetSandboxControl ↔ HealthCheck RPC | FAIL | FAIL | FAIL | PASS | PASS | FAIL |
| SandboxID encoding/decoding | PASS | FAIL | PASS | PASS | PASS | FAIL |
| Fleet control loop ↔ InstanceProvisioner | PASS | PASS | PASS | PASS | PASS | PASS |

## Findings

### P1 - HealthCheck seam is not implementable as currently typed

**Seam**: FleetSandboxControl ↔ sandbox-host HealthCheck RPC  
**Category**: Interface signatures / Lifecycle

**Problem**  
Plan 20 requires control-loop health polling (`HealthCheck` in §§5.1/5.2 and reconciliation in §4.1.3), but the only per-instance client seam is `SandboxClientFactory func(addr string) (control.SandboxControl, error)` and `ManagedInstance.Client control.SandboxControl` (`20` §§4.1, 8). `control.SandboxControl` has no `HealthCheck` method (`internal/sandbox/control/control.go`). The plan does not define a second health client seam, so phase-1 health polling is not wired.

**Required fix**  
Add an explicit health client seam in plan 20, for example:
- introduce `type SandboxHealthChecker interface { HealthCheck(ctx context.Context) (*api.HealthCheckResponse, error) }`
- add `HealthClient` to `ManagedInstance` (or a combined per-instance client struct)
- define a factory path and test double strategy for both control delegation and health polling.

---

### P1 - CreateSandbox address contract drifts across native delegation

**Seam**: FleetSandboxControl ↔ SandboxControl interface and NativeSandboxControl  
**Category**: Data formats/types

**Problem**  
`CreateSandboxResponse.Address` is defined as routable `"host:port"` in control contract (`internal/sandbox/control/control.go`; `docs/plans/18-agent-loop-rpc.md` §6). Plan 20’s create flow delegates to native and returns delegated response (`20` §4.2 sequence) without explicitly normalizing address at fleet boundary. Current native implementation maps address to session mountpoint (`internal/sandbox/control/native/native.go`), which is not a network endpoint.

**Required fix**  
In plan 20, explicitly require fleet to set `CreateSandboxResponse.Address` from instance network endpoint (`AdvertiseAddr`/`PrivateIP:SandboxHostPort`) rather than passing through delegated create response. Add a unit test that asserts `Address` is valid `"host:port"` for fleet-created sandboxes.

---

### P2 - HealthCheck data contract is overstated for current RPC schema

**Seam**: FleetSandboxControl ↔ sandbox-host HealthCheck RPC  
**Category**: Data formats/types

**Problem**  
Plan 20 states HealthCheck provides CPU/memory/pool-space data (`20` §6.2 and OQ6 text), but the current RPC contract exposes only `Status`, `PoolState`, `SessionCount`, `ActiveTools`, `Uptime`, and `Errors` (`internal/rpc/api/types.go`, mirrored in `internal/rpc/server/sandbox_server.go`). This mismatches the documented capacity-data seam.

**Required fix**  
Revise plan 20 to scope current fleet logic to fields actually present on the RPC seam. If CPU/memory/pool-space are required for future routing, add an explicit follow-up contract change (plan 11 + RPC types + compatibility tests) before depending on those fields.

---

### P3 - SandboxID parser snippet conflicts with stated validation contract

**Seam**: SandboxID encoding/decoding boundary  
**Category**: Data formats/types

**Problem**  
Parser pseudocode in §7.2 returns split components without explicit empty checks, but §7.4 and test list (§11.1) require rejection of empty `instanceID`/`sessionID`.

**Required fix**  
Update parser snippet in §7.2 to include explicit empty-component checks so implementation guidance matches acceptance tests.

## Summary

6 seam boundaries analyzed, 4 findings: 0 P0, 2 P1, 1 P2, 1 P3.

Seam compatibility: 2/6 seams fully compatible.

**Verdict**: Seams compatible with revisions.
