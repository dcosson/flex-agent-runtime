# Seam Review: Plan 19 (Direct Adapter) + Plan 20 (Fleet Management)

**Reviewer:** Claude Opus 4.6
**Date:** 2026-03-18
**Plans reviewed:** 19-ec2-direct-adapter.md, 20-fleet-management.md
**Reference documents:** 00-architecture.md, 18-agent-loop-rpc.md, internal/sandbox/control/control.go, internal/sandbox/control/native/native.go

This review checks the interfaces and seams between Plans 19 and 20 and their connected components to catch wiring omissions before implementation.

---

## Seam 1: SandboxControl Interface Compliance

Both `DirectSandboxControl` (Plan 19) and `FleetSandboxControl` (Plan 20) must satisfy the `SandboxControl` interface defined in `internal/sandbox/control/control.go`.

### Method Signature Audit

| Method | Interface Signature | Plan 19 (Direct) | Plan 20 (Fleet) | Match? |
|--------|-------------------|-------------------|------------------|--------|
| `CreateSandbox(ctx, req) (*resp, error)` | `context.Context, CreateSandboxRequest` -> `*CreateSandboxResponse, error` | Matches (section 3.2) | Matches (section 4.2) | OK |
| `DestroySandbox(ctx, sandboxID) error` | `context.Context, string` -> `error` | Matches (section 3.3) | Matches (section 4.3) | OK |
| `LaunchProcess(ctx, req) (*resp, error)` | `context.Context, LaunchProcessRequest` -> `*LaunchProcessResponse, error` | Matches (section 3.4) | Matches (section 4.3) | OK |
| `KillProcess(ctx, req) error` | `context.Context, KillProcessRequest` -> `error` | Matches (section 3.5) | Matches (section 4.3) | OK |
| `GetProcessStatus(ctx, req) (*resp, error)` | `context.Context, GetProcessStatusRequest` -> `*GetProcessStatusResponse, error` | Matches (section 3.6) | Matches (section 4.3) | OK |
| `PauseSandbox(ctx, sandboxID) error` | `context.Context, string` -> `error` | Matches (section 3.7) | Matches (section 4.3) | OK |
| `ResumeSandbox(ctx, sandboxID) error` | `context.Context, string` -> `error` | Matches (section 3.8) | Matches (section 4.3) | OK |
| `Capabilities() SandboxCapabilities` | No args -> `SandboxCapabilities` | Matches (section 3.9) | Matches (section 4.5) | OK |

All eight methods have matching signatures in both plans. No findings.

### Capabilities Flag Consistency

| Capability | Native (existing code) | Direct (Plan 19) | Fleet (Plan 20) |
|-----------|----------------------|-------------------|------------------|
| `Snapshots` | true | false | true |
| `Rollback` | true | false | true |
| `Pause` | true | true (lossy) | true (lossless, delegates to Native) |
| `LaunchProcess` | true | true | true |
| `DeepPause` | false (not set) | false | not mentioned |
| `ConcurrentSandboxes` | 0 (not set) | 0 (not set) | `MaxInstances * MaxSessionsPerInstance` |
| `MaxSandboxDuration` | 0 (not set) | 0 (not set) | 0 (not set) |

**Finding F1 (P2): Fleet's `Capabilities()` does not set `DeepPause`.** Fleet's capabilities code in section 4.5 only sets Snapshots, Rollback, Pause, LaunchProcess, and ConcurrentSandboxes. It does not set `DeepPause: false` explicitly. While Go's zero value handles this, the existing Native implementation also does not set it. This is consistent but worth noting for when DeepPause is actually implemented -- the Fleet adapter would need updating.

**Finding F2 (P2): Direct reports `Pause: true` but semantics differ from Native.** Direct's pause kills all processes (lossy), while Native preserves full process state via gVisor container pause. Both report `Pause: true`. Plan 19 acknowledges this gap in OQ5 and proposes adding `PausePreservesProcesses` to `SandboxCapabilities`. Until this is added, orchestrator code that calls `PauseSandbox` based on `Capabilities().Pause == true` will silently get different behavior depending on the adapter. Fleet delegates to Native, so its pause is lossless.

**Recommendation:** Add `PausePreservesProcesses bool` to `SandboxCapabilities` as a small addendum to Plan 18 before implementing either Plan 19 or Plan 20. This prevents the orchestrator from needing adapter-specific branching logic for pause/resume.

---

## Seam 2: InstanceProvisioner Shared Interface

Plan 19 imports and uses Plan 20's `InstanceProvisioner` interface (`internal/sandbox/control/fleet/provisioner.go`). This creates a hard dependency from `internal/sandbox/control/direct` to `internal/sandbox/control/fleet`.

### Method Usage Audit

| InstanceProvisioner Method | Plan 19 Usage | Plan 20 Definition | Compatible? |
|---------------------------|---------------|-------------------|-------------|
| `LaunchInstance(ctx, InstanceConfig) (*InstanceInfo, error)` | CreateSandbox step 3 | Section 3 | OK |
| `TerminateInstance(ctx, instanceID) error` | DestroySandbox step 2 | Section 3 | OK |
| `StopInstance(ctx, instanceID) error` | PauseSandbox step 2 | Section 3 | OK |
| `StartInstance(ctx, instanceID) (*InstanceInfo, error)` | ResumeSandbox step 2 | Section 3 | OK |
| `DescribeInstance(ctx, instanceID) (*InstanceStatus, error)` | CreateSandbox polling step 5 | Section 3 | OK |
| `ListInstances(ctx, InstanceFilter) ([]InstanceInfo, error)` | Crash recovery (section 3.10) | Section 3 | OK |

All six methods are used consistently. No signature mismatches.

### Type Usage Audit

| Type | Plan 19 Usage | Plan 20 Definition | Compatible? |
|------|---------------|-------------------|-------------|
| `InstanceConfig` | CreateSandbox builds it (Image, InstanceType, DiskSizeGB, UserData, Tags) | Section 3, 5 fields | OK |
| `InstanceInfo` | Reads `InstanceID`, `PrivateIP`, `PublicIP`, `State` | Section 3, all 4 fields | OK |
| `InstanceStatus` | Reads `State`, `PrivateIP`, `PublicIP` | Section 3, 4 fields | OK |
| `InstanceFilter` | Builds with Tags + States for crash recovery | Section 3, 2 fields | OK |
| `CloudInstanceState` | Uses `CloudInstanceRunning`, `CloudInstanceStopped` | Section 3, 6 constants | OK |

**Finding F3 (P1): Import direction creates a coupling concern.** Plan 19's `internal/sandbox/control/direct` imports Plan 20's `internal/sandbox/control/fleet` package solely for the `InstanceProvisioner` interface and its associated types. This means the direct package depends on the fleet package, but there is no reverse dependency. This works technically (no circular import), but it creates an unexpected coupling: `direct` logically has nothing to do with fleet management, yet it imports the fleet package.

If `InstanceProvisioner` and its types were extracted to a shared package (e.g., `internal/sandbox/control/provisioner` or `internal/sandbox/control/instance`), both `direct` and `fleet` could import it independently, and neither would depend on the other.

**Recommendation:** Before implementation, extract `InstanceProvisioner`, `InstanceConfig`, `InstanceInfo`, `InstanceStatus`, `InstanceFilter`, `CloudInstanceState`, and related types from `internal/sandbox/control/fleet/provisioner.go` into a shared package like `internal/sandbox/control/instance/`. Both `fleet` and `direct` then import this shared package. This is a mechanical refactor with no behavior change, but it removes the semantic oddity of "direct imports fleet" and allows each adapter to be developed and tested independently.

**Finding F4 (P3): Plan 19 does not specify which `EC2LaunchConfig` fields it uses.** Plan 19's `Config` struct has fields for `SecurityGroupIDs`, `SubnetID`, `InstanceProfileARN`, `KeyPairName` (sections 3.1). These correspond to Plan 20's `EC2LaunchConfig` struct fields (`SecurityGroupIDs`, `SubnetID`, `IAMRole`, `KeyName`). But Plan 19 does not show how it constructs the `EC2InstanceProvisioner` with an `EC2LaunchConfig`. This is left to the caller/orchestrator, which is fine, but there is a naming inconsistency: Plan 19 uses `InstanceProfileARN` while Plan 20's `EC2LaunchConfig` uses `IAMRole` (which should be the instance profile name, not the ARN). These may not be the same value -- an instance profile ARN includes the AWS account ID and resource path, while the `IAMRole` field name in Plan 20 suggests just the role name.

**Recommendation:** Clarify in Plan 20 that `EC2LaunchConfig.IAMRole` takes an instance profile name (not a role name or ARN), and ensure Plan 19's documentation matches when constructing the provisioner.

---

## Seam 3: DirectSandboxControl and FleetSandboxControl Boundary

### Could Fleet Wrap Direct?

Fleet wraps `NodeSandboxControl` (Native), not Direct. This is correct -- Fleet manages a pool of sandbox-host instances running the full ZFS + gVisor stack, which Node connects to via RPC. Direct manages raw EC2 instances with no sandbox-host service. The adapters target fundamentally different instance configurations, so Fleet wrapping Direct would not make sense.

However, an orchestrator might want to use both simultaneously: Fleet for isolated sandboxes and Direct for shared-workspace collaboration. Both implement `SandboxControl`, so the orchestrator can hold references to both and route based on use case. No finding here -- the designs are compatible for side-by-side use.

### SandboxID Encoding Compatibility

| Adapter | SandboxID Format | Example |
|---------|-----------------|---------|
| Native (Node) | Raw session ID from sandbox-host | `sess-abc123` |
| Direct | Raw EC2 instance ID | `i-0abc123def456` |
| Fleet | `fleet:{instanceID}:{sessionID}` | `fleet:i-0abc123def456:sess-7890xyz` |

**Finding F5 (P1): No prefix-based disambiguation of SandboxID provenance.** Fleet uses a `fleet:` prefix, but Native and Direct use unprefixed IDs. If an orchestrator manages sandboxes from multiple adapters (a likely production scenario), it must track which adapter owns which SandboxID externally. Passing a Direct SandboxID (`i-0abc123...`) to `FleetSandboxControl.DestroySandbox` would fail at `parseSandboxID` (no `fleet:` prefix), which is safe. But passing a Fleet SandboxID to `DirectSandboxControl.DestroySandbox` would attempt to look up `fleet:i-0abc...` as an instance ID in the `instances` map, fail with `ErrInstanceNotFound`, which is also safe.

The current design is safe against cross-adapter ID confusion (errors, not silent corruption), but a more robust approach would be to add prefixes to all adapters:
- Native: `native:{sessionID}`
- Direct: `direct:{instanceID}`
- Fleet: `fleet:{instanceID}:{sessionID}`

This would give the orchestrator a cheap way to route operations to the correct adapter by inspecting the prefix, and would make error messages clearer if IDs are misrouted.

**Recommendation:** Consider adding a `direct:` prefix to Direct's SandboxID format. This is a minor change (add prefix in CreateSandbox, strip in all other methods) that improves multi-adapter orchestration. It also future-proofs against potential ID format collisions (e.g., if a session ID from a future sandbox-host happens to start with `i-`).

### Address Semantics

| Adapter | CreateSandboxResponse.Address | Semantics |
|---------|-------------------------------|-----------|
| Native | ZFS mountpoint (e.g., `/pool/sessions/sess-abc`) | Filesystem path, NOT a network endpoint |
| Direct | `ip:0` (e.g., `10.0.1.42:0`) | Host:port format; port 0 = no active endpoint yet |
| Fleet | `privateIP:sandboxHostPort` (e.g., `10.0.1.5:9100`) | Routable network endpoint |

**Finding F6 (P2): Inconsistent `Address` semantics across adapters.** The `CreateSandboxResponse.Address` field is documented as `"How to reach this sandbox (host:port)"` in `control.go`, but Native returns a filesystem path, not a network endpoint. Fleet must rewrite Native's address (section 4.2 documents this explicitly). Direct returns `ip:0` which satisfies the format but port 0 is a non-standard signal.

The orchestrator must know adapter-specific conventions to interpret this field correctly:
- Native: Address is a filesystem path, not usable for network connections.
- Direct: Address has port 0, which means "call LaunchProcess to get a real endpoint."
- Fleet: Address is a real routable endpoint for the sandbox-host RPC.

This is technically functional but semantically messy. The `Address` field is doing double duty as "location information" without a clear contract for what "reachable" means.

**Recommendation:** Add a doc comment to `CreateSandboxResponse.Address` clarifying that it is adapter-specific and may require `LaunchProcess` to get a usable agent endpoint. Alternatively, consider splitting this into `HostAddress string` (always host:port of the management endpoint) and `WorkspacePath string` (filesystem path, if applicable), but that is a larger interface change.

---

## Seam 4: Agent Session Integration

Both adapters ultimately result in the orchestrator calling `CreateAgentSession` via RPC on a `flexagent serve agent` process. The session lifecycle must be consistent.

### CreateAgentSessionRequest / ToolEnvironmentConfig

For both adapters, agents run on the instance with `LocalEnvironment`. The orchestrator passes:

```go
CreateAgentSessionRequest{
    SessionConfig: SessionConfig{
        // ... model, provider, tools, etc.
        ToolEnvironment: ToolEnvironmentConfig{
            Type:         ToolEnvLocal,
            LocalRootDir: "/workspace",
        },
    },
}
```

This is consistent between Plan 19 (section 4, step 3) and Plan 18 (section 4, step 7 `ToolEnvLocal`). The actual code in `internal/agent/service.go` validates `ToolEnvironmentConfig` and creates a `LocalEnvironment` when `Type == ToolEnvLocal`.

**No finding for Direct.** The flow is consistent.

**For Fleet**, the agent loop runs inside a gVisor sandbox, and the orchestrator passes:

```go
ToolEnvironmentConfig{
    Type:             ToolEnvSandbox,
    SandboxHostAddr:  "sandbox-host:9100",
    SandboxSessionID: "sess-abc",
}
```

This causes the `AgentLoopService` to create a `NativeSandboxEnvironment` pointing at the sandbox-host. Plan 20 does not explicitly document this ToolEnvironmentConfig usage because it delegates entirely to `NodeSandboxControl`, which was designed in Plan 18.

**Finding F7 (P3): Plan 20 does not explicitly document the ToolEnvironmentConfig flow for fleet-launched agents.** While the flow is implicitly correct (Fleet delegates to Node, which delegates to sandbox-host, and the orchestrator follows the same pattern as Plan 18 section 9.2), Plan 20 should include a brief note confirming that the orchestrator passes `ToolEnvSandbox` with the instance's sandbox-host address and the session ID from `CreateSandbox`. This makes the wiring explicit for implementers.

### LocalRootDir Consistency

Plan 19 specifies `/workspace` as the default workspace directory (section 3.4, 4). This is configured via `LocalRootDir` in the `CreateAgentSessionRequest`, not as a CLI flag on `flexagent serve agent`. This is consistent with Plan 18 which removed `--root-dir` from the agent server.

The existing code in `internal/agent/service.go` handles `LocalRootDir` correctly:

```go
case "", agentapi.ToolEnvLocal:
    // Creates LocalEnvironment with the specified root dir
```

**No finding.** The workspace directory configuration path is consistent.

### Session Lifecycle: Create -> Message -> Destroy

Both adapters follow the same lifecycle pattern:

1. `SandboxControl.CreateSandbox()` -- provision the execution environment
2. `SandboxControl.LaunchProcess()` -- start `flexagent serve agent` in the environment
3. `AgentServiceClient.CreateSession()` -- create an agent session via RPC
4. `AgentServiceClient.SendMessage()` -- interact with the agent
5. `AgentServiceClient.DestroySession()` -- tear down the agent session
6. `SandboxControl.KillProcess()` -- stop the agent process
7. `SandboxControl.DestroySandbox()` -- tear down the execution environment

**Finding F8 (P2): Plan 19 does not document who calls `DestroySession` before `KillProcess`.** Plan 19's sequence diagram (section 2.2) shows the orchestrator calling `KillProcess` directly, but does not show `DestroySession` being called first. If the orchestrator kills the process without destroying the session, the agent's graceful shutdown handler (Plan 18 section 7.4) would not get a chance to emit `EventSessionEnded`. This matters for event stream consumers.

The correct sequence should be:
1. Call `AgentServiceClient.DestroySession(sessionID)` to gracefully stop the session
2. Wait briefly for the agent to clean up (or monitor the event stream for `EventSessionEnded`)
3. Then call `SandboxControl.KillProcess()` to stop the process

For `DestroySandbox`, which kills everything (step 7), skipping DestroySession is acceptable because the entire instance is terminated. But for `KillProcess` in isolation (e.g., stopping one of multiple agents on a shared instance), DestroySession should come first.

**Recommendation:** Add a note to Plan 19 section 3.5 that the orchestrator should call `DestroySession` before `KillProcess` when stopping an individual agent without destroying the sandbox.

---

## Seam 5: Vertical Slice -- CreateSandbox to DestroySandbox

### Direct Adapter Full Path

```
Orchestrator
  -> DirectSandboxControl.CreateSandbox(req)
     -> provisioner.LaunchInstance(InstanceConfig{...})        // EC2 RunInstances
     -> poll provisioner.DescribeInstance until running          // EC2 DescribeInstances
     -> poll ssmClient.DescribeInstanceInformation for SSM reg  // SSM API
     <- returns {SandboxID: instanceID, Address: "ip:0"}

  -> DirectSandboxControl.LaunchProcess(req)
     -> ssmClient.SendCommand(instanceID, launch script)       // SSM RunShellScript
     -> poll ssmClient.GetCommandInvocation for completion      // SSM API
     -> poll health via SSM (curl localhost:PORT/health)         // SSM RunShellScript
     <- returns {ProcessID, Address: "ip:PORT", Status: Running}

  -> AgentServiceClient(address).CreateSession(req)            // ConnectRPC
     -> AgentLoopService creates Agent + LocalEnvironment
     <- returns {SessionID, State: idle}

  -> AgentServiceClient.SendMessage(sessionID, prompt)         // ConnectRPC server stream
     -> Agent runs tools via LocalEnvironment on instance FS
     <- event stream

  -> AgentServiceClient.DestroySession(sessionID)              // ConnectRPC
  -> DirectSandboxControl.KillProcess(req)
     -> ssmClient.SendCommand(instanceID, "kill PID")          // SSM RunShellScript
  -> DirectSandboxControl.DestroySandbox(sandboxID)
     -> provisioner.TerminateInstance(instanceID)               // EC2 TerminateInstances
```

**Finding F9 (P2): No gap in the Direct vertical slice, but the CreateSandbox -> LaunchProcess handoff has an implicit assumption.** The `CreateSandboxResponse.Address` returns `ip:0`, and the orchestrator must extract the host part from this address and combine it with the `ExposePort` to form the `LaunchProcessRequest`. But `LaunchProcess` does not take an address -- it takes a `SandboxID` (which for Direct is the instance ID). The address construction happens inside `LaunchProcess`, which returns `Address: "ip:PORT"`. The orchestrator then uses this address to create the `AgentServiceClient`.

The handoff works, but there is a subtle issue: the IP in `CreateSandboxResponse.Address` and the IP in `LaunchProcessResponse.Address` must be the same (the instance's selected IP). Plan 19 does not explicitly guarantee this -- `CreateSandbox` selects an IP based on `IPSelectionMode`, and `LaunchProcess` constructs `Address = instanceIP:PORT` from stored state. As long as `instanceIP` is the same IP selected during `CreateSandbox`, this is fine. And it should be, since both read from the same `instanceState` struct.

No actionable finding -- the implementation will naturally ensure consistency via the shared `instanceState`.

### Fleet Adapter Full Path

```
Orchestrator
  -> FleetSandboxControl.CreateSandbox(req)
     -> CapacityRouter.SelectInstance()                          // find best instance
     -> ClaimSlot: increment SessionCount on selected instance   // atomic under inst lock
     -> NodeSandboxControl(instance).CreateSandbox(req)          // RPC to sandbox-host
        -> sandbox-host creates ZFS dataset, gVisor container
     <- returns {SandboxID: "fleet:instanceID:sessionID", Address: "ip:9100"}

  -> FleetSandboxControl.LaunchProcess(req)
     -> parseSandboxID -> instanceID, sessionID
     -> NodeSandboxControl(instance).LaunchProcess(rewritten req) // RPC to sandbox-host
        -> sandbox-host launches flexagent inside container
     <- returns {ProcessID, Address: "proxyAddr:PORT"}

  -> AgentServiceClient(address).CreateSession(req)              // ConnectRPC
     -> AgentLoopService creates Agent + NativeSandboxEnvironment
     <- returns {SessionID, State: idle}

  (... same message/destroy flow ...)

  -> FleetSandboxControl.DestroySandbox(sandboxID)
     -> parseSandboxID -> instanceID, sessionID
     -> decrementSessionCount
     -> NodeSandboxControl(instance).DestroySandbox(sessionID)   // RPC to sandbox-host
```

**Finding F10 (P2): Fleet's `LaunchProcess` address rewriting is underspecified for the response.** Fleet's section 4.3 documents request rewriting (changing `req.SandboxID` from the fleet-encoded ID to the session-local ID), but it does not discuss response rewriting. When `NodeSandboxControl.LaunchProcess` returns `LaunchProcessResponse`, the `Address` field will be set by the sandbox-host's proxy port allocation. This address may be relative to the sandbox-host (e.g., `localhost:9101` or `sandbox-host-ip:9101`). Fleet must ensure this address is routable from the orchestrator. If the sandbox-host returns the proxy address with its own IP, this should work because Fleet already knows the instance's private IP. But if the sandbox-host returns `localhost:PORT`, Fleet needs to rewrite it.

The existing `NativeSandboxControl.LaunchProcess` implementation (in `native.go` line 89-106) passes through `resp.Address` from the RPC response without modification. The sandbox-host's `LaunchProcess` RPC handler is responsible for returning a routable address. As long as the sandbox-host returns the host's advertised address (not localhost), this works.

**Recommendation:** Fleet should document that it expects `LaunchProcessResponse.Address` from the per-instance Node client to be routable (i.e., using the instance's IP, not localhost). If the sandbox-host's `AdvertiseAddr` is misconfigured, the returned address will be wrong. Add a note to Plan 20 about this assumption, and consider adding address validation in Fleet's `LaunchProcess` (e.g., reject addresses containing `localhost` or `127.0.0.1`).

---

## Seam 6: Error Types and Error Handling

### Error Type Consistency

| Error Scenario | Direct (Plan 19) | Fleet (Plan 20) | Native (existing code) |
|---------------|-------------------|------------------|----------------------|
| Sandbox not found | `ErrInstanceNotFound` | `parseSandboxID` error or instance not in registry | Returns error from RPC |
| Process not found | `ErrProcessNotFound` | Delegates to Node, which returns RPC error | Returns error from RPC |
| No capacity | N/A (1:1 sandbox:instance) | `ErrNoCapacity` | N/A (single node) |
| Service closed | N/A | `ErrFleetClosed` | N/A |
| Timeout | `ErrInstanceReadyTimeout`, `ErrProcessReadyTimeout`, `ErrSSMTimeout` | `ProvisionTimeout` -> terminate instance | N/A |
| Resource limit | `ErrResourcesExceedMaximum` | N/A | N/A |

**Finding F11 (P2): No shared error taxonomy across adapters.** Each adapter defines its own sentinel errors with its own naming conventions. Direct uses `direct:` prefixed errors. Fleet uses `ErrNoCapacity` and `ErrFleetClosed` (presumably in the fleet package). Native uses raw RPC errors from the sandbox-host.

The orchestrator must handle errors differently per adapter: for Direct, use `errors.Is(err, direct.ErrInstanceNotFound)`. For Fleet, check `parseSandboxID` errors. For Native, inspect RPC error codes.

This is not a blocking issue for initial implementation, but it will become painful as more adapters are added. A shared error set in `internal/sandbox/control/` (e.g., `control.ErrSandboxNotFound`, `control.ErrNoCapacity`) that all adapters wrap or return would simplify orchestrator error handling.

**Recommendation:** Define shared sentinel errors in `internal/sandbox/control/errors.go`:
- `ErrSandboxNotFound` -- sandbox ID not recognized
- `ErrProcessNotFound` -- process ID not recognized
- `ErrNoCapacity` -- no available capacity for new sandboxes
- `ErrProviderClosed` -- adapter is shutting down

Each adapter wraps these with adapter-specific context. The orchestrator uses `errors.Is(err, control.ErrSandboxNotFound)` regardless of adapter.

### Retry Semantics

**Finding F12 (P3): Inconsistent retry guidance between adapters.** Plan 19 provides a clear error classification table (section 8.2) categorizing errors as retry-safe or not. Plan 20 does not provide equivalent guidance. Fleet's `CreateSandbox` no-capacity path has built-in retry logic (singleflight provision + wait), but errors from the underlying Node RPC calls are not classified for the orchestrator's benefit.

**Recommendation:** Plan 20 should include a brief error classification table for errors surfaced to the orchestrator, especially for: `ErrNoCapacity` (retriable after wait), `parseSandboxID` errors (not retriable), Node RPC errors (depends on error type), `ErrFleetClosed` (not retriable).

---

## Seam 7: Additional Cross-Cutting Findings

**Finding F13 (P2): Close() is not part of SandboxControl but both adapters implement it.** Both Plan 19 (section 3.11) and Plan 20 (section 4.4) implement `Close() error` via `io.Closer`. Plan 20 explicitly notes that `Close()` is NOT part of `SandboxControl` because `NodeSandboxControl` does not need it. But the pattern of "the orchestrator must know the concrete type and call Close()" is fragile.

If the orchestrator holds a `SandboxControl` interface, it cannot call `Close()` without a type assertion. Both plans recommend `if closer, ok := sc.(io.Closer); ok { closer.Close() }`, which works but is easy to forget.

**Recommendation:** Consider adding `Close() error` to `SandboxControl`. For `NodeSandboxControl`, implement it as a no-op. This ensures all adapters can be cleanly shut down through the interface without type assertions. The cost is one trivial no-op method on Native.

**Finding F14 (P1): Plan 19's Direct adapter has no mechanism to wait for in-flight CreateSandbox during Close().** Plan 20's `FleetSandboxControl.Close()` explicitly handles in-flight operations: "In-flight `CreateSandbox` calls that have already claimed a slot are allowed to complete their RPC. The `Close` method waits for all in-flight operations to finish before beginning the drain sequence." Plan 19's `DirectSandboxControl.Close()` (section 3.11) simply iterates `c.instances` and terminates them if `TerminateOnClose` is true, or clears the map. It does not address concurrent `CreateSandbox` calls that may be in progress (polling DescribeInstance, waiting for SSM registration, etc.). If `Close()` runs concurrently with `CreateSandbox`, the created instance could be orphaned (the polling goroutine finishes, adds the instance to the map, but `Close()` already cleared the map and will not terminate it).

**Recommendation:** Plan 19 should add a `closing` flag (set under `c.mu` in `Close()`) that causes `CreateSandbox` to return an error if set. Additionally, `Close()` should wait for any in-flight `CreateSandbox` operations to complete (using a `sync.WaitGroup` or similar) before clearing state and optionally terminating instances.

---

## Summary

### P0 Findings (Must fix before implementation)
None.

### P1 Findings (Should fix before implementation)

| ID | Finding | Affected Plans |
|----|---------|---------------|
| F3 | Import direction: `direct` imports `fleet` for `InstanceProvisioner`. Extract to shared package. | 19, 20 |
| F5 | No prefix-based SandboxID disambiguation across adapters. | 19 |
| F14 | Direct's `Close()` has no mechanism to wait for in-flight `CreateSandbox` calls, risking orphaned instances. | 19 |

### P2 Findings (Should fix, can be during implementation)

| ID | Finding | Affected Plans |
|----|---------|---------------|
| F1 | Fleet does not explicitly set `DeepPause: false` in Capabilities (benign, zero value). | 20 |
| F2 | `Pause: true` means different things for Direct (lossy) vs Native/Fleet (lossless). Need `PausePreservesProcesses`. | 19, 20, 18 |
| F6 | `CreateSandboxResponse.Address` has inconsistent semantics across adapters (filesystem path vs host:port vs ip:0). | 19, 20 |
| F8 | Plan 19 does not document `DestroySession` before `KillProcess` for individual agent shutdown. | 19 |
| F9 | Direct vertical slice implicit assumption about IP consistency (benign via shared state). | 19 |
| F10 | Fleet's `LaunchProcess` does not document response address rewriting assumptions (expects non-localhost). | 20 |
| F11 | No shared error taxonomy across adapters. Each defines its own sentinel errors. | 19, 20 |
| F13 | `Close()` not in `SandboxControl` interface, requiring type assertions for cleanup. | 19, 20 |

### P3 Findings (Nice to have)

| ID | Finding | Affected Plans |
|----|---------|---------------|
| F4 | `IAMRole` naming inconsistency between Plan 19 (`InstanceProfileARN`) and Plan 20 (`IAMRole`). | 19, 20 |
| F7 | Plan 20 does not explicitly document the ToolEnvironmentConfig flow for fleet-launched agents. | 20 |
| F12 | Inconsistent retry guidance; Plan 20 lacks an error classification table. | 20 |
