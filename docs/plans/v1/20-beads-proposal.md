# Plan 20: EC2 Fleet Management -- Bead Decomposition Proposal

**Plan:** docs/plans/20-fleet-management.md
**Required reading for all tasks:** docs/plans/00-implementation-guide.md
**Architecture context:** docs/plans/00-architecture.md

---

## Catalog of Components

Before decomposing, here is the full inventory of types, interfaces, and components from plan 20:

### Types & Interfaces (from plan 20 section 3)
- `InstanceProvisioner` interface (shared `instance/` package -- 6 methods)
- `InstanceConfig`, `InstanceInfo`, `InstanceStatus`, `InstanceFilter` structs
- `CloudInstanceState` enum (6 values)
- `EC2LaunchConfig` struct
- `EC2InstanceProvisioner` struct (implements `InstanceProvisioner`)

### Types & Interfaces (from plan 20 sections 4-8)
- `FleetSandboxControl` struct (implements `SandboxControl` + `io.Closer`)
- `FleetNodeClient` interface (`SandboxControl` + `HealthCheck`)
- `SandboxClientFactory` function type
- `FleetConfig` struct + `DefaultFleetConfig()`
- `FleetOption` functional options
- `ManagedInstance` struct
- `InstanceState` enum (5 values: Provisioning, Ready, Active, Draining, Terminating)
- `CapacityRouter` struct
- SandboxID encode/parse functions
- Error types: `ErrNoCapacity`, `ErrFleetClosed`

### Control Loop (plan 20 section 5)
- 6-phase control loop (health, unhealthy, scale-up, scale-down, drain, provisioning)
- Session count reconciliation algorithm
- Singleflight provision dedup
- Non-blocking `LaunchInstance`

### Observability (plan 20 section 15)
- Metrics (gauges, counters, histograms)
- Structured logging
- `FleetStatus()` method

### TLA+ Spec (plan 20 section 10)
- `specs/fleet_control.tla`
- `specs/fleet_control_mc.cfg`

---

## Cross-Epic Dependency: Shared instance/ Package

Plan 20 depends on the `internal/sandbox/control/instance/` package which contains the `InstanceProvisioner` interface and associated types. This package is shared between plans 19 (Direct) and 20 (Fleet).

**Key decision:** The `instance/` package is defined canonically in plan 20 section 3 but extracted to a shared location per seam review F3. Plan 19 also depends on it. Since both plans need it, it should be created as a standalone task. If plan 19 already has a bead for this, plan 20 tasks should depend on it. If not, we create one here.

**No existing plan 19 beads were found** in the current bead list. The instance/ package task is therefore created in this epic, and any future plan 19 epic should depend on it.

---

## Proposed Epic + Tasks

### Epic: EC2 Fleet Management (plan 20)

**7 tasks** total. Implementation order follows plan 20 section 13 but groups related work into properly-sized tasks (~200+ lines each).

---

### Task 1: InstanceProvisioner Interface + Shared Types + FleetConfig

**Package:** `internal/sandbox/control/instance/`, `internal/sandbox/control/fleet/`
**Plan sections:** 3, 4.1 (FleetConfig only), 9
**Estimated size:** ~250 lines

**Scope:**
- `internal/sandbox/control/instance/provisioner.go`: `InstanceProvisioner` interface (6 methods), `InstanceConfig`, `InstanceInfo`, `InstanceStatus`, `InstanceFilter`, `CloudInstanceState` enum (6 values)
- `internal/sandbox/control/fleet/config.go`: `FleetConfig` struct, `DefaultFleetConfig()`, `FleetOption` type
- `internal/sandbox/control/fleet/errors.go`: `ErrNoCapacity`, `ErrFleetClosed`, error classification

**Unit tests:**
- `CloudInstanceState` string values are stable
- `DefaultFleetConfig()` returns sensible defaults
- `FleetConfig` validation (min <= max instances, etc.)

**No dependencies** (this is the foundation task).

**Note:** This task is a shared dependency for both plan 19 (Direct) and plan 20 (Fleet). The `instance/` package is provider-neutral.

---

### Task 2: SandboxID Encoding + Instance State Machine

**Package:** `internal/sandbox/control/fleet/`
**Plan sections:** 7 (SandboxID), 8 (Instance State Machine)
**Estimated size:** ~350 lines

**Scope:**
- `fleet/sandbox_id.go`: `encodeSandboxID()`, `parseSandboxID()`, `sandboxIDPrefix` const, colon validation
- `fleet/instance_state.go`: `InstanceState` enum (5 values), `ManagedInstance` struct (all fields), state transition validation methods, `DrainReason`, `ConsecutiveSuccesses`/`ConsecutiveFailures` counters, lock ordering comments

**Unit tests:**
- `sandbox_id_test.go`: Round-trip encoding/parsing, missing prefix error, missing separator error, empty components error, colon-in-instanceID rejection, empty fields rejection
- `instance_state_test.go`: All valid state transitions succeed, invalid transitions return error, Ready->Active on first session, Active->Ready on last session destroyed, Draining->Ready recovery (health reason only), Draining->Ready rejected for scale-down/shutdown reasons

**Depends on:** Task 1 (uses `FleetConfig` for threshold values)

---

### Task 3: Capacity-Aware Routing

**Package:** `internal/sandbox/control/fleet/`
**Plan sections:** 6 (Capacity-Aware Routing)
**Estimated size:** ~250 lines

**Scope:**
- `fleet/routing.go`: `CapacityRouter` struct, `SelectInstance()` with spread strategy, headroom scoring, `CapacityHeadroom` threshold filtering, two-level tiebreaker (IdleSince then atomic round-robin counter)
- Router configuration from `FleetConfig`

**Unit tests:**
- `routing_test.go`: Returns instance with most headroom, breaks ties using IdleSince, breaks ties with round-robin when equal IdleSince, distributes evenly over many calls, skips Draining/Terminating/Provisioning instances, skips instances above CapacityHeadroom, returns `ErrNoCapacity` when all full, single available instance returned correctly

**Depends on:** Task 2 (uses `ManagedInstance`, `InstanceState`)

---

### Task 4: FleetSandboxControl Core (CreateSandbox, Delegation, Close)

**Package:** `internal/sandbox/control/fleet/`
**Plan sections:** 4.1 (constructor, claim-slot, lock ordering), 4.2 (CreateSandbox), 4.3 (delegation methods), 4.4 (Close), 4.5 (Capabilities), 15.4 (error classification)
**Estimated size:** ~600 lines

This is the largest task -- the core FleetSandboxControl struct and all SandboxControl method implementations.

**Scope:**
- `fleet/fleet.go`: `FleetSandboxControl` struct, constructor `NewFleetSandboxControl()`, `FleetNodeClient` interface, `SandboxClientFactory` type
- `CreateSandbox` with atomic claim-slot protocol (section 4.1.1), address rewriting (section 4.2)
- `DestroySandbox` with decrement-before-RPC + rollback-on-failure pattern
- `LaunchProcess`, `KillProcess`, `GetProcessStatus`, `PauseSandbox`, `ResumeSandbox` -- all with SandboxID parsing and request struct rewriting to session-local ID
- `Capabilities()` returning `ConcurrentSandboxes = MaxInstances * MaxSessionsPerInstance`
- `Close()` implementing `io.Closer`: cancel control loop, drain instances, terminate or leave running
- Singleflight provision coordination (provisionGroup, pendingLaunches)
- `getInstanceClient()`, `incrementSessionCount()`, `decrementSessionCount()` helpers
- `FleetStatus()` method

**Unit tests:**
- `fleet_test.go` (extensive -- see plan 20 section 11.1 "FleetSandboxControl" list):
  - CreateSandbox routes to least-loaded instance and returns fleet-prefixed SandboxID
  - CreateSandbox with no capacity waits on singleflight provision
  - CreateSandbox concurrent callers share single provision (no overshoot)
  - CreateSandbox at MaxInstances with all full returns error
  - CreateSandbox claim-slot rolls back on RPC failure
  - CreateSandbox racing with drain transition handled by claim-slot
  - DestroySandbox parses SandboxID, delegates, decrements count
  - DestroySandbox failed RPC re-increments count
  - DestroySandbox invalid SandboxID returns error
  - LaunchProcess / KillProcess / GetProcessStatus route correctly
  - PauseSandbox / ResumeSandbox route correctly
  - CreateSandbox overrides Address with routable host:port
  - LaunchProcess rewrites request SandboxID to session-local ID
  - KillProcess / GetProcessStatus rewrite SandboxID
  - Capabilities returns correct ConcurrentSandboxes
  - Concurrent CreateSandbox serialized correctly
  - Close stops loop, drains, terminates
  - Close with LeaveInstancesOnClose leaves instances running
  - Close rejects new CreateSandbox with ErrFleetClosed

**Test doubles:**
- `MockInstanceProvisioner` (records calls, returns configured responses)
- Mock `SandboxClientFactory` returning mock `FleetNodeClient`

**Depends on:** Task 2 (SandboxID, ManagedInstance, InstanceState), Task 3 (CapacityRouter)

---

### Task 5: Fleet Control Loop

**Package:** `internal/sandbox/control/fleet/`
**Plan sections:** 5 (Control Loop), 4.1.3 (Session Count Reconciliation), 15.1 (Metrics), 15.2 (Structured Logging)
**Estimated size:** ~550 lines

**Scope:**
- `fleet/control_loop.go`: Background goroutine, 6-phase control loop:
  1. Health Check -- poll every instance via `FleetNodeClient.HealthCheck()`
  2. Handle Unhealthy -- consecutive failure tracking, transition to Draining
  3. Scale-Up -- warm pool check, non-blocking `LaunchInstance`, `MaxConcurrentProvisions` guard
  4. Scale-Down -- idle cooldown check, `MinInstances`/`WarmPoolTarget` guards
  5. Drain Completion -- terminate drained instances (state -> Terminating AFTER API success), `MaxTerminateRetries` alert, Phase 5b stuck Terminating cleanup
  6. Provisioning Completion -- HealthCheck polling on Provisioning instances, timeout handling
- Session count reconciliation algorithm (section 4.1.3)
- `Draining -> Ready` recovery for health-drained instances
- Crash recovery via `ListInstances` (tag-based rediscovery)
- Injectable clock for deterministic time control in tests
- Observability: metrics (gauges, counters, histograms), structured logging for state transitions

**Unit tests:**
- `control_loop_test.go` (see plan 20 section 11.1 "Control Loop" list):
  - Health check failure increments counter
  - 3 consecutive failures -> Draining with DrainReason "health"
  - Healthy response resets counter
  - Session count reconciliation: overcount, undercount, sandbox-host restart
  - Scale-up triggered when capacity < WarmPoolTarget
  - Scale-up respects MaxInstances, MaxConcurrentProvisions
  - Scale-down triggered when idle > IdleCooldown
  - Scale-down respects MinInstances, WarmPoolTarget
  - Drain completion terminates at 0 sessions (Terminating AFTER API success)
  - Drain completion failure leaves Draining for retry
  - Drain completion retries next iteration
  - Drain completion alerts after MaxTerminateRetries
  - Phase 5b cleans up stuck Terminating instances
  - Drain timeout force-terminates
  - Health-drained instance recovers after 3 healthy checks
  - Scale-down drained instance does NOT recover
  - Provisioning timeout terminates
  - Provisioning -> Ready on first healthy HealthCheck
  - Warm pool provisions to fill gap
  - Crash recovery: ListInstances rediscovers instances
  - Crash recovery: stale orphan terminated

**Depends on:** Task 4 (FleetSandboxControl struct, helpers, FleetNodeClient)

---

### Task 6: EC2InstanceProvisioner + Contract Tests

**Package:** `internal/sandbox/control/fleet/ec2/`
**Plan sections:** 3.1 (EC2InstanceProvisioner), 3.3 (Contract Tests)
**Estimated size:** ~450 lines

**Scope:**
- `fleet/ec2/ec2_provisioner.go`: `EC2InstanceProvisioner` struct, `EC2LaunchConfig` struct, `NewEC2InstanceProvisioner()` constructor
  - `LaunchInstance` -> `RunInstances` (non-blocking, returns after instance ID assigned)
  - `TerminateInstance` -> `TerminateInstances` (idempotent)
  - `StopInstance` -> `StopInstances`
  - `StartInstance` -> `StartInstances` + `DescribeInstances` (wait for running)
  - `DescribeInstance` -> `DescribeInstances` with state mapping
  - `ListInstances` -> `DescribeInstances` with tag filters
  - EC2 state -> `CloudInstanceState` mapping
  - Tags always include `ManagedBy: flex-agent-runtime`, `fleet-id`
- `fleet/provisioner_contract_test.go`: Shared contract test suite verifiable by any `InstanceProvisioner` implementation:
  - LaunchInstance returns valid InstanceInfo
  - DescribeInstance on launched instance returns consistent state
  - TerminateInstance succeeds, subsequent Describe returns Terminated
  - ListInstances with matching tags returns launched instances
  - ListInstances with non-matching tags returns empty
  - LaunchInstance with invalid config returns error
  - TerminateInstance on terminated instance is idempotent
  - (Contract tests run against the mock provisioner; integration tests run against real EC2)

**Unit tests:**
- `ec2/ec2_provisioner_test.go` (mock EC2 API client):
  - LaunchInstance calls RunInstances with correct params including EC2LaunchConfig fields
  - LaunchInstance applies tags
  - LaunchInstance returns immediately
  - TerminateInstance calls TerminateInstances
  - TerminateInstance idempotent on terminated
  - StopInstance calls StopInstances
  - StartInstance calls StartInstances and waits
  - DescribeInstance maps EC2 states to CloudInstanceState
  - ListInstances with tag filters
  - ListInstances with state filter maps states
  - ListInstances with no matches returns empty

**Depends on:** Task 1 (InstanceProvisioner interface, InstanceConfig, etc.)

**Note:** This task is independent of Tasks 2-5 (fleet core). It only depends on Task 1 (shared types). Can be worked in parallel with Tasks 2-5.

---

### Task 7: TLA+ Formal Specification

**Location:** `specs/fleet_control.tla`, `specs/fleet_control_mc.cfg`
**Plan sections:** 10 (TLA+ Specification)
**Estimated size:** ~400 lines

**Scope:**
- TLA+ specification module modeling:
  - Claim-slot protocol (atomic increment, CreateRPC, RollbackSlot)
  - Session count drift and reconciliation
  - Two-lock structure (fleet lock, instance lock)
  - Concurrent CreateSandbox callers racing with control loop
  - Warm pool maintenance racing with session assignment
  - Instance failure during active sessions
- Safety invariants (7 properties):
  1. No routing to draining/terminating instances
  2. Scale-down never terminates with active sessions without drain
  3. Session count monotonicity (>= actual)
  4. Session-to-instance uniqueness
  5. Instance state exclusivity
  6. Lock ordering (deadlock freedom)
  7. Reconciliation convergence
- Liveness properties (4 properties):
  1. CreateSandbox eventually completes
  2. Drain eventually terminates
  3. Scale-up fires under pressure
  4. Warm pool replenishment
- TLC model checker configuration: N=3 instances, M=2 callers

**Depends on:** Task 4 (must understand the FleetSandboxControl implementation to model correctly)

**Note:** This task can technically start after Task 4 is designed (even before full implementation), but depends on it to ensure the spec matches implementation.

---

## Dependency DAG

```
Task 1: InstanceProvisioner Interface + Shared Types + FleetConfig
  |
  +---> Task 2: SandboxID Encoding + Instance State Machine
  |       |
  |       +---> Task 3: Capacity-Aware Routing
  |               |
  |               +---> Task 4: FleetSandboxControl Core
  |                       |
  |                       +---> Task 5: Fleet Control Loop
  |                       |
  |                       +---> Task 7: TLA+ Formal Specification
  |
  +---> Task 6: EC2InstanceProvisioner + Contract Tests (PARALLEL with Tasks 2-5)
```

**Parallelism opportunities:**
- Task 6 (EC2InstanceProvisioner) can be worked in parallel with Tasks 2-5 since it only depends on Task 1.
- Task 7 (TLA+) can be worked in parallel with Task 5 since both depend on Task 4.

**Cross-epic dependency:**
- Any future plan 19 (Direct Adapter) epic should depend on Task 1 from this epic, since plan 19 also imports the shared `instance/` package.

---

## Integration Tests (not a separate bead)

Integration tests (plan 20 section 11.2) are build-tag gated (`//go:build integration`) and require real AWS resources. They are NOT included as a separate task because:
1. They require infrastructure (AMI, VPC, etc.) that is out of scope for this plan (OQ4)
2. They would be created as a follow-up bead after the core implementation is complete and infrastructure is available

---

## Summary Table

| # | Title | Est. Lines | Depends On | Parallel With |
|---|-------|-----------|------------|---------------|
| 1 | InstanceProvisioner Interface + Shared Types + FleetConfig | ~250 | (none) | -- |
| 2 | SandboxID Encoding + Instance State Machine | ~350 | Task 1 | Task 6 |
| 3 | Capacity-Aware Routing | ~250 | Task 2 | Task 6 |
| 4 | FleetSandboxControl Core | ~600 | Task 2, Task 3 | Task 6 |
| 5 | Fleet Control Loop | ~550 | Task 4 | Task 6, Task 7 |
| 6 | EC2InstanceProvisioner + Contract Tests | ~450 | Task 1 | Tasks 2-5 |
| 7 | TLA+ Formal Specification | ~400 | Task 4 | Task 5 |

**Total estimated:** ~2,850 lines of implementation + tests
