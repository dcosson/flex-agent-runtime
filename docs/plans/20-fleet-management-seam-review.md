# Seam Review: fleet-management (horizontal)

- Mode: Horizontal
- Seam: fleet-management
- Reviewed commit: d157360
- Reviewers: coder-1-sea, Claude Opus 4.6
- Date: 2026-03-17
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

### Seam 1: FleetSandboxControl <-> SandboxControl interface

**Artifacts**: `docs/plans/20-fleet-management.md`, `internal/sandbox/control/control.go`, `docs/plans/18-agent-loop-rpc.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Plan method set matches `SandboxControl` (`Create/Destroy`, process ops, pause/resume, `Capabilities`). All 8 methods accounted for. |
| Data formats/types | FAIL | `CreateSandboxResponse.Address` contract is `"host:port"` in control interface/plan 18, but plan 20 delegation path does not pin fleet-side normalization and relies on delegated response payload. Additionally, `Capabilities()` does not populate `ConcurrentSandboxes` or `MaxSandboxDuration`. |
| Lifecycle ordering | PASS | Claim-slot + delegation + rollback ordering is consistent with interface semantics. |
| Configuration contracts | PASS | `FleetConfig` is additive and does not conflict with `SandboxControl` request/response types. |
| Error handling | PASS | Invalid ID and RPC failure paths are documented and mapped to caller-visible errors. |

---

### Seam 2: FleetSandboxControl <-> NativeSandboxControl

**Artifacts**: `docs/plans/20-fleet-management.md`, `internal/sandbox/control/native/native.go`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Delegation model and `SandboxClientFactory` return type (`control.SandboxControl`) align. |
| Data formats/types | FAIL | `native.CreateSandbox` currently maps `Address` to session mountpoint, while fleet/control contract expects routable `"host:port"` address semantics. Request struct rewriting (fleet SandboxID -> session-local ID) for `LaunchProcess`/`KillProcess`/`GetProcessStatus` is implied but not shown. |
| Lifecycle ordering | PASS | Delegation after sandbox ID parse and slot accounting is coherent. |
| Configuration contracts | PASS | Per-instance client factory pattern is compatible with native control construction. |
| Error handling | PASS | Delegated errors are surfaced; rollback/reconciliation paths are documented. |

---

### Seam 3: InstanceProvisioner <-> EC2InstanceProvisioner

**Artifacts**: `docs/plans/20-fleet-management.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | `InstanceProvisioner` method set maps cleanly to EC2 operations, including `ListInstances`. |
| Data formats/types | PASS | `InstanceConfig` is provider-neutral; AWS-only fields moved to `EC2LaunchConfig`. |
| Lifecycle ordering | PASS | Non-blocking `LaunchInstance` + readiness detection via health polling is coherent. |
| Configuration contracts | PASS | Provider-specific launch config injection at constructor boundary is clear. |
| Error handling | PASS | Idempotent terminate and retry expectations are specified in contract tests. |

---

### Seam 4: FleetSandboxControl <-> sandbox-host HealthCheck RPC

**Artifacts**: `docs/plans/20-fleet-management.md`, `internal/sandbox/control/control.go`, `internal/rpc/api/types.go`, `internal/rpc/server/sandbox_server.go`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | FAIL | Plan requires periodic `HealthCheck` calls, but `ManagedInstance.Client`/`SandboxClientFactory` are typed only as `control.SandboxControl` (no `HealthCheck` method). |
| Data formats/types | FAIL | Plan text claims HealthCheck provides CPU/memory/pool-space data; current RPC health payload exposes `status/pool_state/session_count/active_tools/uptime/errors` only. `PoolSpace` data (capacity, free space) is present in `sandbox.HealthStatus` but missing from `api.HealthCheckResponse`. |
| Lifecycle ordering | FAIL | Control-loop phases depend on health polling, but the seam for obtaining a health-capable client is not specified. |
| Configuration contracts | PASS | `SandboxHostPort` and per-instance addressing assumptions are consistent at high level. |
| Error handling | PASS | Degraded/unhealthy/error handling policy is documented once health calls are available. |

---

### Seam 5: SandboxID encoding/decoding boundary

**Artifacts**: `docs/plans/20-fleet-management.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | ID format `fleet:<instanceID>:<sessionID>` is stable and routing-focused. |
| Data formats/types | FAIL | Parser snippet omits explicit empty-component checks, while adjacent validation/tests require rejecting empty components. No validation that instanceID does not contain colons. |
| Lifecycle ordering | PASS | ID decode precedes delegation consistently. |
| Configuration contracts | PASS | No cross-component config dependency beyond prefix/format constants. |
| Error handling | PASS | Prefix/separator error paths are documented. |

---

### Seam 6: Fleet control loop <-> InstanceProvisioner lifecycle/error handling

**Artifacts**: `docs/plans/20-fleet-management.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Scale-up/down/drain flows map to provisioner lifecycle methods. |
| Data formats/types | PASS | `InstanceInfo/Status` and cloud state mapping are sufficient for state transitions. |
| Lifecycle ordering | PASS | Provisioning, draining, timeout, and termination phases are consistent. |
| Configuration contracts | PASS | `Min/Max/WarmPool/Timeout` controls align with control-loop decisions. |
| Error handling | FAIL | `TerminateInstance` failure handling in drain completion is ambiguous -- unclear if state transitions to `Terminating` before or after the API call succeeds. |

## Acceptance Criteria Cross-Reference

| Acceptance criterion (source doc) | Seams touched | Status | Details |
|-----------------------------------|---------------|--------|---------|
| `CreateSandbox` routes and returns fleet-prefixed ID (`20` SS11.1) | Fleet<->SandboxControl, Fleet<->Native, SandboxID boundary | FAIL | Sandbox ID path is specified, but response address semantics are currently ambiguous/drifting at the native boundary. |
| Health reconciliation + unhealthy drain logic (`20` SS4.1.3, SS5.1, SS11.1) | Fleet<->HealthCheck, Control loop<->Provisioner | FAIL | Health-driven phases are specified but the health-capable client seam is currently undefined. |
| Crash recovery via `ListInstances` (`20` SS14 OQ2, SS11.1) | Provisioner<->EC2, Control loop<->Provisioner | PASS | Discovery/filtering/re-adoption path is coherent with updated `ListInstances` contract. |
| Delegated process/sandbox ops via encoded SandboxID (`20` SS4.3, SS11.1) | Fleet<->SandboxControl, Fleet<->Native, SandboxID boundary | FAIL | Delegation shape is coherent, but parser snippet inconsistency and create-address drift leave boundary ambiguity. |

## Seam Compatibility Matrix

| Seam | Signatures | Data | Lifecycle | Config | Errors | Overall |
|------|-----------|------|-----------|--------|--------|---------|
| FleetSandboxControl <-> SandboxControl | PASS | FAIL | PASS | PASS | PASS | FAIL |
| FleetSandboxControl <-> NativeSandboxControl | PASS | FAIL | PASS | PASS | PASS | FAIL |
| InstanceProvisioner <-> EC2InstanceProvisioner | PASS | PASS | PASS | PASS | PASS | PASS |
| FleetSandboxControl <-> HealthCheck RPC | FAIL | FAIL | FAIL | PASS | PASS | FAIL |
| SandboxID encoding/decoding | PASS | FAIL | PASS | PASS | PASS | FAIL |
| Fleet control loop <-> InstanceProvisioner | PASS | PASS | PASS | PASS | FAIL | FAIL |

## Findings

### P1-1: HealthCheck seam is not implementable as currently typed

**Seam**: FleetSandboxControl <-> sandbox-host HealthCheck RPC
**Category**: Interface signatures / Lifecycle

**Problem**
Plan 20 requires control-loop health polling (`HealthCheck` in SS5.1/5.2 and reconciliation in SS4.1.3), but the only per-instance client seam is `SandboxClientFactory func(addr string) (control.SandboxControl, error)` and `ManagedInstance.Client control.SandboxControl` (SS4.1, 8). `control.SandboxControl` has no `HealthCheck` method (`internal/sandbox/control/control.go`). The plan's import flow (SS2.2) says the fleet package imports `internal/rpc/api (SandboxService, HealthCheckResponse)`, confirming it needs HealthCheck access. But the plan does not define how to obtain a health-capable client from the `control.SandboxControl` value.

**Required fix**
Add an explicit health client seam in plan 20:
- Define `type FleetNodeClient interface { control.SandboxControl; HealthCheck(ctx context.Context) (*api.HealthCheckResponse, error) }` in the fleet package
- Update `SandboxClientFactory` to return `FleetNodeClient` instead of `control.SandboxControl`
- Update `ManagedInstance.Client` type to `FleetNodeClient`
- Define test double strategy for both control delegation and health polling

---

### P1-2: CreateSandbox address contract drifts across native delegation

**Seam**: FleetSandboxControl <-> SandboxControl interface and NativeSandboxControl
**Category**: Data formats/types

**Problem**
`CreateSandboxResponse.Address` is defined as routable `"host:port"` in control contract (`internal/sandbox/control/control.go`; `docs/plans/18-agent-loop-rpc.md` SS6). Plan 20's create flow delegates to native and returns delegated response (SS4.2 sequence) without explicitly normalizing address at fleet boundary. Current native implementation (`internal/sandbox/control/native/native.go` line 74) maps `Address` to `resp.Session.Mountpoint`, which is a filesystem path (e.g. `/pool/sessions/sess-abc`), not a network endpoint.

When `FleetSandboxControl` delegates `CreateSandbox` to a per-instance `NativeSandboxControl`, it receives this mountpoint-based address in the response. The fleet layer must replace it with the instance's network address (e.g., `{privateIP}:{sandboxHostPort}`) for the orchestrator to reach the sandbox over the network.

**Required fix**
In plan 20, explicitly require fleet to override `CreateSandboxResponse.Address` with the instance network endpoint (`PrivateIP:SandboxHostPort`) rather than passing through the delegated response address. Add a unit test that asserts `Address` is valid `"host:port"` for fleet-created sandboxes.

---

### P1-3: TerminateInstance failure handling in drain completion is ambiguous

**Seam**: Fleet control loop <-> InstanceProvisioner
**Category**: Error handling

**Problem**
Section 5.1 phase 5 (Drain Completion) shows: "Find Draining instances with 0 sessions -> TerminateInstance -> Transition -> Terminated, remove from registry." The plan does not specify whether the state transition to `Terminating` happens BEFORE or AFTER the `TerminateInstance` API call succeeds.

If the transition happens BEFORE the API call, and the API call fails, the instance is stuck in `Terminating` state. The next loop iteration's phase 5 only looks for `Draining` instances -- not `Terminating` ones -- so it will never retry.

If the fleet manager crashes after transitioning to `Terminating` but before the API call, the recovered fleet may not find the instance because `ListInstances` could return it in a state that doesn't match the expected `Terminating` state.

**Required fix**
Specify that the transition to `Terminating` happens AFTER `TerminateInstance` succeeds. On failure, the instance stays in `Draining` (with 0 sessions) and phase 5 retries on the next iteration. Add a timeout for repeated `TerminateInstance` failures (e.g., after N consecutive failures, log an alert for manual intervention). Also add a control loop phase that cleans up instances stuck in `Terminating` state after crash recovery.

---

### P1-4: Capabilities() does not populate fleet-level limits

**Seam**: FleetSandboxControl <-> SandboxControl interface
**Category**: Data formats/types

**Problem**
The plan's `Capabilities()` (SS4.5) returns hardcoded native capabilities with `ConcurrentSandboxes` and `MaxSandboxDuration` at zero (unlimited). But `FleetSandboxControl` has a hard concurrency limit: `MaxInstances * MaxSessionsPerInstance`. If the orchestrator uses `Capabilities().ConcurrentSandboxes` for admission control, it will believe the fleet has unlimited capacity.

**Required fix**
Populate `ConcurrentSandboxes` with `MaxInstances * MaxSessionsPerInstance`. Document that this is the theoretical ceiling and the actual available capacity depends on fleet state. Alternatively, document explicitly that the orchestrator must use `ErrNoCapacity` as the backpressure signal and not rely on `ConcurrentSandboxes` for fleet providers.

---

### P2-1: HealthCheck data contract is overstated for current RPC schema

**Seam**: FleetSandboxControl <-> sandbox-host HealthCheck RPC
**Category**: Data formats/types

**Problem**
Plan 20 SS6.2 states: "HealthCheck response -- provides authoritative session count, CPU, memory, and ZFS pool space from the sandbox-host's perspective." The actual RPC contract (`api.HealthCheckResponse` in `internal/rpc/api/types.go`) includes only `Status`, `PoolState`, `SessionCount`, `ActiveTools`, `Uptime`, and `Errors`. Neither CPU/memory utilization nor ZFS pool space data (`PoolSpace`) is present in the wire type.

The sandbox-host's internal `HealthStatus` struct (`internal/sandbox/service.go`) DOES include `PoolSpace zfs.PoolSpace`, but the RPC server adapter (`internal/rpc/server/sandbox_server.go` lines 234-241) drops this field during conversion. It is lost in translation.

**Required fix**
Revise plan 20 SS6.2 to accurately state: "HealthCheck response -- provides authoritative session count, ZFS pool health status, and uptime." If pool space data is needed for future resource-aware routing (OQ6), add `PoolCapacity float64` and `PoolFree int64` fields to `api.HealthCheckResponse` and update the RPC server adapter. CPU/memory metrics would require a new data source (not currently in sandbox-host).

---

### P2-2: Request struct rewriting for delegated LaunchProcess/KillProcess/GetProcessStatus not shown

**Seam**: FleetSandboxControl <-> NativeSandboxControl
**Category**: Data formats/types

**Problem**
When `FleetSandboxControl` delegates `LaunchProcess`, it must replace the fleet-encoded SandboxID (`fleet:i-0abc:sess-xyz`) with the session-local ID (`sess-xyz`) before passing to the per-instance client. The plan's SS4.3 shows explicit code for `DestroySandbox` (which takes a bare `sandboxID string`), but for `LaunchProcess`, `KillProcess`, and `GetProcessStatus`, the delegation must construct new request structs with the session-local ID in the `SandboxID` field.

The plan says "the same pattern applies" but does not show request struct rewriting. An implementer might pass the full fleet SandboxID through, which would fail because `NativeSandboxControl` passes `req.SandboxID` directly to `api.LaunchProcessRequest.SessionID`.

**Required fix**
Add explicit pseudocode showing request struct rewriting for `LaunchProcess`:
```go
delegateReq := control.LaunchProcessRequest{
    SandboxID:  sessionID,  // parsed local part, NOT the fleet SandboxID
    Binary:     req.Binary,
    Args:       req.Args,
    Env:        req.Env,
    ExposePort: req.ExposePort,
}
```

---

### P2-3: StopInstance/StartInstance IP change semantics across providers not documented

**Seam**: InstanceProvisioner <-> EC2InstanceProvisioner
**Category**: Data formats/types

**Problem**
`StopInstance` + `StartInstance` is used for cost savings on idle warm-pool instances. `StartInstance` returns `*InstanceInfo` with potentially new IP addresses (GCP releases external IP on stop; Azure deallocate may change private IP). The plan does not document that callers must use the returned `InstanceInfo` IPs rather than cached values. If the fleet control loop caches the original `PrivateIP` from `LaunchInstance` and does not update it after `StartInstance`, the `NativeSandboxControl` client will connect to a stale address.

**Required fix**
Add a doc comment to `StartInstance` noting that IPs may change and callers must reconstruct the `NativeSandboxControl` client using the returned `InstanceInfo`. The fleet control loop's handling of `StartInstance` should explicitly update the `ManagedInstance.PrivateIP` and recreate the client via `SandboxClientFactory`.

---

### P2-4: SandboxID instanceID colon validation missing

**Seam**: SandboxID encoding/decoding boundary
**Category**: Data formats/types

**Problem**
The `parseSandboxID` function splits on the first colon after the `fleet:` prefix. This works correctly as long as instance IDs never contain colons (EC2 instance IDs use `i-{hex}` format, no colons). Session IDs safely handle embedded colons because everything after `{instanceID}:` is treated as the sessionID.

However, there is no validation in `encodeSandboxID` that rejects instance IDs containing colons. If a future cloud provider used instance IDs with colons, encoding would produce an ambiguous string that parses incorrectly. SS7.4 says "Characters are not restricted beyond the colon delimiter" but does not address this.

**Required fix**
Add a runtime check in `encodeSandboxID` that rejects instance IDs containing `:`. Document that this is a hard constraint on provider instance ID formats.

---

### P2-5: SandboxControl interface lacks Close() but FleetSandboxControl needs one

**Seam**: FleetSandboxControl <-> SandboxControl interface
**Category**: Lifecycle ordering

**Problem**
The plan defines `FleetSandboxControl.Close() error` (SS4.4) for shutting down the background control loop, but `SandboxControl` has no `Close()` method. The orchestrator must type-assert or hold a direct reference to call `Close()`. `NativeSandboxControl` has no background goroutines and does not need `Close()`, creating an asymmetry.

**Required fix**
Document explicitly that lifecycle management (Close) is outside the `SandboxControl` interface and the orchestrator is responsible for calling it on the concrete type. Consider having `FleetSandboxControl` also implement `io.Closer` so generic cleanup code can use type assertion.

---

### P3-1: SandboxID parser snippet conflicts with stated validation contract

**Seam**: SandboxID encoding/decoding boundary
**Category**: Data formats/types

**Problem**
Parser pseudocode in SS7.2 returns split components without explicit empty checks, but SS7.4 and test list (SS11.1) require rejection of empty `instanceID`/`sessionID`. An input like `fleet::sess-abc` would return `instanceID = ""` with no error.

**Required fix**
Update parser snippet in SS7.2 to include explicit empty-component checks:
```go
instanceID, sessionID = rest[:idx], rest[idx+1:]
if instanceID == "" || sessionID == "" {
    return "", "", fmt.Errorf("invalid fleet sandbox ID: empty component: %q", sandboxID)
}
```

---

### P3-2: No startup-time validation of InstanceConfig.InstanceType for the active provider

**Seam**: InstanceProvisioner <-> EC2InstanceProvisioner
**Category**: Configuration contracts

**Problem**
`InstanceConfig.InstanceType` is a raw string like `"m5.xlarge"` or `"n2-standard-4"`. If misconfigured for the wrong provider, the error only surfaces at the first `LaunchInstance` call (potentially minutes after startup). There is no validation hook to fail fast.

**Required fix**
Consider adding a `ValidateConfig(cfg InstanceConfig) error` method to `InstanceProvisioner`, called at fleet startup. Or document that `LaunchInstance` must return a clear error for invalid instance types, and the fleet manager should attempt one launch at startup to validate the configuration.

## Summary

6 seam boundaries analyzed, 11 findings: 0 P0, 4 P1, 5 P2, 2 P3.

Seam compatibility: 1/6 seams fully compatible (InstanceProvisioner <-> EC2InstanceProvisioner).

**Verdict**: Seams compatible with revisions. The highest-priority issues are (1) the HealthCheck client type gap preventing the control loop from calling HealthCheck, (2) the CreateSandboxResponse.Address contract drift where mountpoints leak through as addresses, (3) ambiguous TerminateInstance failure handling in drain completion, and (4) Capabilities() misreporting fleet limits. All are fixable without architectural changes.
