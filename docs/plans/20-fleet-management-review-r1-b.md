# Plan 20: Fleet Management — Review R1-B

**Reviewer:** subagent
**Date:** 2026-03-17
**Plan doc:** docs/plans/20-fleet-management.md

## Findings

### P0 — Critical

#### P0-1: Race condition between CreateSandbox routing and control loop drain/termination

The plan claims that routing and drain are "mutually exclusive with respect to instance state" (section 10.1), but the implementation does not enforce this. `CreateSandbox` reads instance state under `mu.RLock` (section 4.1 shows `instances` guarded by `sync.RWMutex`), selects an instance in Ready/Active state via `SelectInstance`, then delegates to the instance's `NativeSandboxControl.CreateSandbox`. Between the `SelectInstance` return and the actual `CreateSandbox` RPC call, the control loop can transition that instance to `Draining` (sections 5.2-5.4). This is a classic TOCTOU (time-of-check/time-of-use) race.

The consequences are severe: a sandbox gets created on an instance that is draining, and the drain logic believes the instance has N sessions, but it actually has N+1. The `DrainTimeout` force-termination (section 5.5) could then kill this newly created sandbox. The TLA+ spec (section 10.2, invariant 1) explicitly states this must never happen, but the Go implementation as designed cannot guarantee it because the RWMutex does not span the routing decision plus the downstream RPC call.

**Required fix:** Either (a) hold a write lock or per-instance lock across the entire `SelectInstance -> CreateSandbox -> incrementSessionCount` sequence so the control loop cannot transition the instance during this window, (b) implement an atomic "claim slot" protocol where `SelectInstance` atomically reserves a session slot (incrementing the count before the RPC) and rolls back on RPC failure, or (c) add a per-instance state guard that rejects the `Draining` transition while any in-flight CreateSandbox is pending. Option (b) is likely best since it avoids holding a global lock during an RPC call. The TLA+ spec should model this exact interleaving.

#### P0-2: Session count drift after DestroySandbox failure is unrecoverable

`DestroySandbox` (section 4.3) calls `client.DestroySandbox(ctx, sessionID)` and then `decrementSessionCount(instanceID)`. If the RPC succeeds but the process crashes before decrementing, or if the RPC returns an error but the session was actually destroyed on the remote side (network timeout after server-side completion), the fleet-tracked session count permanently diverges from reality. The plan mentions "periodic reconciliation" from HealthCheck data (section 6.2), but the HealthCheck response provides `SessionCount` from the sandbox-host's perspective, and the reconciliation logic is never specified. There is no defined algorithm for how the fleet manager detects and corrects a count mismatch, no defined threshold for when to trust the remote count over the local count, and no handling for the case where the HealthCheck itself is stale or the sandbox-host crashed and restarted (resetting its own count to 0).

Without robust reconciliation, a stuck session count prevents an instance from ever reaching 0 sessions, which means it can never complete draining, which means it leaks indefinitely (terminated only by `DrainTimeout` force-kill, which destroys any real sessions on the instance).

**Required fix:** Specify the reconciliation algorithm completely: when the fleet count disagrees with the HealthCheck-reported count, which wins and under what conditions? Define a reconciliation phase in the control loop (or integrate it into the health check phase). Consider making the sandbox-host `HealthCheck` response authoritative for session count, with the fleet count used only as a fast-path routing hint between health polls. The TLA+ spec should model count drift and verify the reconciliation converges.

### P1 — Important

#### P1-1: `ManagedInstance` has nested mutex creating deadlock potential

`ManagedInstance` has its own `sync.RWMutex` (section 8, line 624), and `FleetSandboxControl` has a separate `sync.RWMutex` (section 4.1, line 268). The plan does not specify a lock ordering discipline. Any code path that acquires `FleetSandboxControl.mu` and then `ManagedInstance.mu` (e.g., iterating instances to find one, then updating its session count) must never have a concurrent path that acquires them in reverse order. The current design makes this easy to violate: `SelectInstance` iterates under the fleet lock reading instance fields, while `decrementSessionCount` likely needs the instance lock. If the control loop holds instance locks while calling back into fleet-level operations, deadlock occurs.

**Required fix:** Either (a) eliminate the nested lock by making `ManagedInstance` fields only accessible under the fleet-level lock (simpler but coarser), or (b) document and enforce a strict lock ordering (fleet lock always acquired before instance lock, never the reverse), with a code comment or linter check. The TLA+ spec should model the two-lock structure.

#### P1-2: Synchronous provisioning from CreateSandbox blocks the caller unboundedly

When no instance has capacity, `CreateSandbox` can "start a provision and block (with `ProvisionTimeout`)" (section 5.3). The default `ProvisionTimeout` is 5 minutes (section 4.1, line 302). This means a `CreateSandbox` caller can block for up to 5 minutes. Most callers will have their own context deadline that is shorter than this, so the actual behavior is likely a context cancellation, but the plan does not address: (a) what happens to the in-flight provisioning if the caller's context is cancelled -- is the instance still provisioned and added to the pool, or is it abandoned? (b) if multiple callers hit `ErrNoCapacity` simultaneously, do they each trigger separate provisioning, causing a burst of N instances for N concurrent callers? The plan mentions this risk in passing but does not specify deduplication.

**Required fix:** Specify singleflight or pending-provision coordination. When the first caller triggers a provision, subsequent callers waiting for capacity should wait on the same provision rather than launching their own. The provision should complete and add the instance to the pool regardless of whether the initiating caller's context was cancelled. Add a `MaxConcurrentProvisions` config to bound burst provisioning.

#### P1-3: InstanceProvisioner lacks `ListInstances` method, breaking crash recovery

The plan's OQ2 recommends crash recovery via cloud provider describe calls with tag filters (section 959-963), but the `InstanceProvisioner` interface (section 3) only has `DescribeInstance(instanceID)` which requires knowing the instance ID. After a crash, the fleet manager has lost all instance IDs. There is no `ListInstances` or `DescribeInstances(filter)` method.

This means crash recovery requires either: (a) calling the cloud API directly, bypassing the `InstanceProvisioner` abstraction, which defeats the provider-agnostic goal, or (b) adding the method to the interface.

**Required fix:** Add `ListInstances(ctx context.Context, filter InstanceFilter) ([]InstanceInfo, error)` to `InstanceProvisioner`, where `InstanceFilter` supports filtering by tags (at minimum `ManagedBy` and `fleet-id`). This is essential for the crash recovery path and for any operational tooling that needs fleet discovery.

#### P1-4: `InstanceConfig` fields leak AWS-specific concepts into the provider-agnostic interface

`InstanceConfig` (section 3, lines 155-184) contains `SecurityGroupIDs`, `SubnetID`, `KeyName`, `IAMRole` -- all AWS-specific concepts. GCP uses firewall rules (not security groups), network/subnetwork (not subnet IDs in the same format), and service accounts (not IAM roles). Azure uses NSGs, VNets/subnets, and managed identities. The plan claims this interface is "designed to accommodate" GCP and Azure (section 3.2), but the struct fields are AWS-shaped.

**Required fix:** Split `InstanceConfig` into provider-neutral fields (`Image`, `InstanceType`, `Tags`, `DiskSizeGB`) and provider-specific fields. Either use a `ProviderConfig map[string]any` or `ProviderConfig interface{}` escape hatch, or have each provisioner accept its own typed config at construction time (e.g., `EC2InstanceProvisioner` constructor takes `EC2LaunchConfig` containing security groups, subnets, key name, IAM role) rather than threading these through the shared interface. The `InstanceConfig` in the interface should contain only what every provider needs.

#### P1-5: No FleetSandboxControl.Close or shutdown protocol specified

The plan describes `Close stops control loop, drains all instances` in the test list (section 11.1, line 775) but never defines the `Close` method or shutdown protocol in the design sections. Key questions unanswered: Does `Close` drain all instances synchronously? Does it block until all sessions are destroyed? What is the timeout? Does it terminate instances or leave them running (so a restarted fleet manager can re-adopt them)? Does it cancel in-flight `CreateSandbox` calls? The `cancelLoop` and `loopDone` fields (section 4.1) suggest a cancellation model, but the graceful shutdown sequence needs explicit specification.

**Required fix:** Add a section describing the `Close` method semantics: what it cancels, what it waits for, what timeout applies, and whether instances are terminated or left for re-adoption. This is critical for clean orchestrator shutdown and for tests.

### P2 — Moderate

#### P2-1: Spread strategy biases toward a single instance when fleet is lightly loaded

The routing algorithm (section 6.1) picks the instance with the MOST headroom (highest `headroom` score). When multiple instances have equal headroom (e.g., all empty in a warm pool), the algorithm picks the first one encountered in the map iteration (Go maps have randomized iteration order, but in practice the same instance may win repeatedly within a short time window). This defeats the spread intent and can lead to one instance accumulating sessions while others stay at zero.

**Required fix:** Add a tiebreaker. When multiple instances have the same headroom score (within a small epsilon), either randomize among them or use a secondary criterion like `IdleSince` (prefer the instance that has been idle longest) or round-robin via an atomic counter. This ensures actual spread across instances.

#### P2-2: No resource-aware routing despite `CreateSandboxRequest.Resources` being available

`CreateSandboxRequest` includes a `Resources` field (CPU, memory) from `control.go:49-53`, and the shaping doc's Shape A emphasizes resource-based packing (A3, A4). However, the plan's `SelectInstance` (section 6.1) only considers session count, not resource requirements. An instance running 2 lightweight sandboxes has the same score as one running 2 heavy sandboxes. This can lead to over-committing resources on instances where heavy sandboxes are packed.

**Required fix:** Either (a) add per-instance resource tracking (allocated CPU/memory) and factor it into routing, consistent with the shaping doc's vision, or (b) explicitly document that resource-aware routing is deferred and explain why session-count-only is sufficient for the initial implementation. If deferred, add it to Open Questions with a clear plan for when it will be added.

#### P2-3: Warm pool target invariant (TLA+ property 3) is too strong and will fail in practice

TLA+ safety property 3 (section 10.2) states: "the number of idle instances never drops below `WarmPoolTarget`". This is stated as an invariant (holds in every reachable state), but it trivially fails during legitimate transient states: (a) when a warm instance is activated by `CreateSandbox` and the replacement has not yet been provisioned, (b) when provisioning a replacement fails, (c) at fleet startup before the warm pool is filled. The property as stated will produce spurious TLC counterexamples or require artificial model constraints that hide real bugs.

**Required fix:** Weaken this to a liveness property: "If the warm pool drops below target and `MaxInstances` is not reached, the control loop eventually provisions a replacement." This is already stated as liveness property 4 (section 10.3). Remove or rephrase safety property 3 to something achievable, such as "the control loop never voluntarily reduces idle instances below WarmPoolTarget during scale-down decisions" (which is about the scale-down guard, not a global invariant).

#### P2-4: No metrics, logging, or observability specification

The plan describes a production fleet manager but specifies no structured metrics (instance counts by state, provision latency, routing decisions, health check results, session counts, error rates). For a system managing EC2 instances with real cost implications, operational visibility is essential. The shaping doc's capability comparison table rates Shape A's observability as "Logs, metrics" but the plan only mentions "a warning is logged" for drain timeout (section 5.5).

**Required fix:** Add a section on observability: define key metrics to emit (as prometheus-style counters/gauges/histograms or structured log fields), specify structured logging for state transitions and error conditions, and consider a `FleetStatus` method or health endpoint for the orchestrator to expose.

#### P2-5: `Draining` -> `Ready` transition not supported, preventing recovery from transient health failures

The state machine (section 8.1) has no path from `Draining` back to `Ready` or `Active`. If an instance enters `Draining` due to 3 consecutive health check failures (which could be a transient network partition), there is no way to recover it even if it becomes healthy again. The instance will be terminated, a replacement provisioned (costing instance boot time and potentially losing cached ZFS data), and the process repeated if the network issue is intermittent.

**Required fix:** Consider adding a `Draining -> Ready` transition that activates if: the instance has active sessions, it was drained due to health (not scale-down), and it passes N consecutive health checks while draining. Alternatively, increase the `UnhealthyThreshold` or add a "probation" state between healthy and draining. At minimum, document why recovery from draining is intentionally not supported.

#### P2-6: Shaping doc OQ8 recommended opaque SandboxIDs (option a), but plan chose encoded IDs (option b) without justifying the divergence

The shaping doc's OQ8 (line 626-636) explicitly recommended option (a): maintain a `sandboxID -> instanceAddress` map with rebuild-on-restart, keeping SandboxIDs opaque. The plan chose option (b): encoding instance ID in the SandboxID. The plan's section 7.3 justifies the choice but does not acknowledge that it diverges from the shaping recommendation. This is not necessarily wrong -- the encoded approach has real advantages for statelessness -- but the divergence should be explicitly called out and the tradeoffs discussed against the shaping doc's reasoning.

**Required fix:** Add a note in section 7.3 acknowledging the shaping doc recommended option (a) and explaining why option (b) was chosen instead. Address the shaping doc's concern about leaking infrastructure details into the ID.

### P3 — Minor

#### P3-1: Shape label mismatch between plan and shaping doc

The plan header (line 7) says "Shape B selected: in-process fleet management with provider-agnostic InstanceProvisioner" but the shaping doc labels in-process fleet management as Shape A (line 43-44). Shape B in the shaping doc is AWS ASG-based. This is confusing and could cause miscommunication during implementation.

**Required fix:** Correct the plan header to say "Shape A selected" to match the shaping doc's labeling.

#### P3-2: `CloudInstanceState` values duplicate what could be provider-specific

The `CloudInstanceState` enum (section 3, lines 202-212) defines `pending`, `running`, `stopping`, `stopped`, `terminating`, `terminated` -- these are the exact EC2 instance states. GCP Compute Engine uses different states (`PROVISIONING`, `STAGING`, `RUNNING`, `STOPPING`, `STOPPED`, `TERMINATED`, `SUSPENDING`, `SUSPENDED`). The fleet manager's internal state machine (`InstanceState`) already abstracts this, so the `CloudInstanceState` enum is an unnecessary AWS-ism in the provider-agnostic layer.

**Required fix:** Either make `CloudInstanceState` a `string` (letting each provider return its native states, with the fleet manager only caring about the `InstanceState` it derives) or define a truly minimal provider-neutral set (`pending`, `running`, `stopped`, `terminated`) and document that providers must map to these. The current set works but needlessly mirrors EC2.

#### P3-3: `SandboxClientFactory` return type should be `control.SandboxControl`, not `api.SandboxService`

The `SandboxClientFactory` (section 4.1, line 279) returns `api.SandboxService`, but the `ManagedInstance.Client` field (section 8, line 630) is typed as `control.SandboxControl`. The fleet manager delegates to `NativeSandboxControl` (which wraps `api.SandboxService`), so the factory should either return a `control.SandboxControl` directly (wrapping the RPC client in a `NativeSandboxControl`) or the `Client` field should be `api.SandboxService`. The current design has a type mismatch between the factory output and the instance field.

**Required fix:** Align the types. The cleanest approach is to have `SandboxClientFactory` return `control.SandboxControl` (the factory constructs a `NativeSandboxControl` wrapping the RPC client), and `ManagedInstance.Client` stores `control.SandboxControl`. This keeps the fleet package working purely at the `SandboxControl` abstraction level.

#### P3-4: Test list omits concurrent CreateSandbox + control loop drain interaction

The unit test list (section 11.1) includes "Concurrent CreateSandbox calls are serialized correctly" but does not include a test for the critical interleaving: CreateSandbox selects an instance while the control loop concurrently transitions it to Draining (the P0-1 race). This is the most important concurrency test for the fleet manager and should be explicitly listed.

**Required fix:** Add a test case: "CreateSandbox that selects an instance racing with control loop drain transition is handled correctly (either the sandbox is created and counted, or CreateSandbox retries on a different instance)."

## Summary

- Total findings: 14
- P0: 2, P1: 5, P2: 6, P3: 4

The two P0 findings are the most critical: the TOCTOU race between routing and drain, and the unspecified session count reconciliation. Both can cause data loss (sandboxes killed on draining instances) or resource leaks (instances stuck in draining forever). The P1 findings around deadlock potential, unbounded blocking, missing ListInstances, and AWS-specific config leakage are significant design issues that should be resolved before implementation begins.

The plan is well-structured and thorough in most areas. The TLA+ specification is a strong addition, but it needs to model the actual locking protocol (P0-1) and count reconciliation (P0-2) to be useful rather than aspirational. The architecture, import flow, and separation of concerns are clean. The testing strategy is solid but has gaps around crash recovery and the critical concurrency paths.

**Verdict:** Not approved as-is. The P0 findings must be resolved and P1 findings should be addressed before implementation.
