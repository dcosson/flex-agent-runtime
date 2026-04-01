# Plan 19: Direct Adapter -- Bead Decomposition (Dry Run)

**Plan:** docs/plans/19-ec2-direct-adapter.md
**Epic:** EC2 Direct Adapter Implementation
**Project label:** project=flex-agent-runtime

---

## Catalog of Components

### Types & Interfaces (from plan)
- `InstanceProvisioner` interface + `InstanceConfig`, `InstanceInfo`, `InstanceStatus`, `InstanceFilter`, `CloudInstanceState` (shared `instance/` package)
- `DirectSandboxControl` struct + `Config`, `instanceState`, `processState`, `instanceStatus`
- `SSMAPI` interface (SSM SDK wrapper)
- `SandboxCapabilities` return value
- Sentinel errors (`ErrInstanceNotFound`, `ErrProcessNotFound`, etc.)
- SandboxID helpers (`parseSandboxID`, `encodeSandboxID`)
- SSM command helpers (`shellQuote`, `buildSSMCommand`)
- Instance type mapping function
- UserData template types (`UserDataTemplateData`)

### Methods (SandboxControl implementation)
- `CreateSandbox` -- instance provisioning + polling + SSM readiness
- `DestroySandbox` -- instance termination
- `LaunchProcess` -- SSM-based process launch + PID file tracking + health check
- `KillProcess` -- SSM-based process termination + PID recycling guard
- `GetProcessStatus` -- SSM-based process status check
- `PauseSandbox` -- EC2 stop + process state cleanup
- `ResumeSandbox` -- EC2 start + IP refresh + SSM re-registration
- `Capabilities` -- static capability set
- `Close` -- shutdown with inflight-wait + optional terminate-all
- `Recover` -- tag-based crash recovery at construction time

### Tests
- Unit tests with mocked `InstanceProvisioner` and `SSMAPI` (all methods, ~30+ cases from plan 11.1)
- SSM command/shell escaping edge case tests
- SandboxID prefix round-trip tests
- Instance type mapping tests
- Concurrency tests (Close + CreateSandbox race, concurrent LaunchProcess)
- Integration tests (gated, real EC2 -- separate bead)

---

## Proposed Epic + Tasks

### Epic: `EC2 Direct Adapter`

Parent epic for all plan 19 implementation work.

---

### Task 1: Shared Instance Provisioner Package

**Package:** `internal/sandbox/control/instance/`
**Plan references:** Plan 19 section 5.1, Plan 20 section 3
**Implementation guide:** docs/plans/00-implementation-guide.md

**Rationale:** This is the shared `InstanceProvisioner` interface package used by both plan 19 (Direct) and plan 20 (Fleet). Neither adapter should import the other; instead both import from this shared package. Must be implemented first since all other tasks depend on it.

**Scope:**
- `provisioner.go`: `InstanceProvisioner` interface with all 6 methods (`LaunchInstance`, `TerminateInstance`, `StopInstance`, `StartInstance`, `DescribeInstance`, `ListInstances`)
- Associated types: `InstanceConfig`, `InstanceInfo`, `InstanceStatus`, `InstanceFilter`, `CloudInstanceState` enum constants
- Unit tests: interface contract test helpers (reusable by any provisioner implementation), type construction/validation tests
- `doc.go`: package documentation

**Estimated size:** ~250 lines (types + interface + contract test helpers)
**Dependencies:** None (leaf package, stdlib only)

---

### Task 2: Direct Adapter Foundation -- Types, Errors, Helpers

**Package:** `internal/sandbox/control/direct/`
**Plan references:** Plan 19 sections 3.1, 5.1, 8, 10
**Implementation guide:** docs/plans/00-implementation-guide.md

**Scope:**
- `errors.go`: All sentinel errors (`ErrInstanceNotFound`, `ErrProcessNotFound`, `ErrInstanceNotReady`, `ErrInstanceReadyTimeout`, `ErrProcessReadyTimeout`, `ErrSSMUnavailable`, `ErrSSMTimeout`, `ErrProcessLaunchFailed`, `ErrResourcesExceedMaximum`, `ErrDirectClosed`)
- `aws.go`: `SSMAPI` interface definition (3 methods wrapping SSM SDK)
- `ssm_command.go` + `ssm_command_test.go`: `shellQuote()` helper, `buildSSMCommand()` function with comprehensive edge case tests (single quotes, double quotes, newlines, dollar signs, backticks, empty strings)
- `sandbox_id.go` + `sandbox_id_test.go`: `parseSandboxID()`, `encodeSandboxID()` with `direct:` prefix handling, round-trip tests, wrong-prefix error tests
- `instance_type.go` + `instance_type_test.go`: Resource-to-instance-type mapping function, `ErrResourcesExceedMaximum` for oversized requests, mapping table tests
- `direct.go`: `Config` struct, `instanceState`, `processState`, `instanceStatus` typed constants, `UserDataTemplateData` struct, `NewDirectSandboxControl` constructor signature (constructor body is a stub that will be filled in Task 5 when crash recovery is added), functional option types
- `capabilities.go`: `Capabilities()` method returning fixed capability set

**Estimated size:** ~500 lines (types + helpers + tests)
**Dependencies:** Task 1 (imports `instance.InstanceProvisioner`)

---

### Task 3: Core Sandbox Lifecycle -- CreateSandbox, DestroySandbox

**Package:** `internal/sandbox/control/direct/`
**Plan references:** Plan 19 sections 3.2, 3.3, 9
**Implementation guide:** docs/plans/00-implementation-guide.md

**Scope:**
- `create.go`: Full `CreateSandbox` implementation
  - Closing guard with `inflightWg` tracking
  - Instance type determination from `req.Resources`
  - `InstanceConfig` construction with tag merging
  - `provisioner.LaunchInstance` call
  - Exponential backoff polling of `DescribeInstance` until running (2s start, 10s cap, `InstanceReadyTimeout`)
  - SSM registration polling via `DescribeInstanceInformation`
  - Double-check `closing` flag before storing state
  - Orphan prevention: terminate instance if context cancelled or `closing` set during polling
  - UserData template rendering
  - `CreateSandboxResponse` with `direct:{instanceID}` format and `ip:0` address
- `destroy.go`: `DestroySandbox` implementation
  - Parse `direct:` prefix
  - Look up instance state
  - Call `provisioner.TerminateInstance`
  - Remove from `c.instances`
- Concurrency: lock-only-for-map-access pattern (no lock held during AWS API calls), post-API existence checks
- Unit tests in `direct_test.go` (or `create_test.go` / `destroy_test.go`):
  - CreateSandbox happy path
  - CreateSandbox with instance type mapping
  - CreateSandbox with template override
  - CreateSandbox timeout (`ErrInstanceReadyTimeout`)
  - CreateSandbox SSM not ready (`ErrSSMUnavailable`)
  - CreateSandbox resources exceed maximum
  - DestroySandbox happy path
  - DestroySandbox unknown sandbox (`ErrInstanceNotFound`)

**Estimated size:** ~500 lines (implementation + tests)
**Dependencies:** Task 2 (uses types, errors, helpers, SSMAPI)

---

### Task 4: SSM Process Management -- LaunchProcess, KillProcess, GetProcessStatus

**Package:** `internal/sandbox/control/direct/`
**Plan references:** Plan 19 sections 3.4, 3.5, 3.6
**Implementation guide:** docs/plans/00-implementation-guide.md

**Scope:**
- `process.go`: All three process management methods
  - `LaunchProcess`:
    - Generate process ID (UUID)
    - Build daemonized SSM command with `buildSSMCommand()` (env vars, nohup, PID file, start time tracking)
    - `ssmClient.SendCommand` with `AWS-RunShellScript`
    - Poll `GetCommandInvocation` for completion
    - Read PID and start time from PID file via follow-up SSM command
    - Health check polling via SSM (`curl localhost:PORT/health`), exponential backoff (500ms start, 5s cap, `ProcessReadyTimeout`)
    - Store `processState` with PID, start time, port
    - Return `LaunchProcessResponse` with `ProcessID`, `Address = instanceIP:PORT`
  - `KillProcess`:
    - Parse `direct:` prefix, look up process
    - PID recycling guard: check `/proc/PID/stat` start time via SSM
    - Send `kill -SIGNAL PID` via SSM
    - Clean up PID file via SSM
    - Update process state to `ProcessExited`
  - `GetProcessStatus`:
    - Look up process, return immediately if cached `ProcessExited`
    - SSM command to check `/proc/PID/stat` existence + start time
    - PID recycling detection
    - Return `GetProcessStatusResponse`
- Unit tests:
  - LaunchProcess happy path
  - LaunchProcess with env vars (shell escaping verified)
  - LaunchProcess command failure (`ErrProcessLaunchFailed`)
  - LaunchProcess readiness timeout (`ErrProcessReadyTimeout`)
  - KillProcess happy path
  - KillProcess with custom signal
  - KillProcess PID recycled (`ErrProcessNotFound`)
  - GetProcessStatus running (matching start time)
  - GetProcessStatus exited
  - GetProcessStatus PID recycled

**Estimated size:** ~600 lines (implementation + tests)
**Dependencies:** Task 3 (CreateSandbox must exist to have instances to launch processes on; tests build on create flow)

---

### Task 5: Crash Recovery, Close, Pause/Resume

**Package:** `internal/sandbox/control/direct/`
**Plan references:** Plan 19 sections 3.7, 3.8, 3.10, 3.11
**Implementation guide:** docs/plans/00-implementation-guide.md

**Scope:**
- `recover.go`: `Recover()` method
  - `provisioner.ListInstances` with tag filter (`ManagedBy=flex-agent-runtime`, `adapter=direct`)
  - Fail-fast if `ListInstances` fails (constructor returns error)
  - For each discovered instance: extract `flex-sandbox-id` tag, read IPs
  - For running instances: probe PID files via SSM (`ls /var/run/flex-agent-*.pid`), verify processes via `/proc/PID/stat`, reconstruct process map
  - Best-effort SSM probing (individual failures logged, instance tracked with empty process map)
  - Log recovery summary
- Wire `Recover()` into `NewDirectSandboxControl` constructor (filling in the stub from Task 2)
- `pause.go`: `PauseSandbox` and `ResumeSandbox`
  - `PauseSandbox`: `provisioner.StopInstance`, mark all processes `ProcessExited`, update instance state to `stopped`
  - `ResumeSandbox`: `provisioner.StartInstance`, poll `DescribeInstance` until running (exponential backoff), update IP addresses, wait for SSM re-registration, update instance state to `running`, clear process map
- `direct.go` updates: Full `Close()` implementation
  - Set `closing = true` under lock
  - `inflightWg.Wait()` for in-flight CreateSandbox operations
  - If `TerminateOnClose`, terminate all instances
  - If not, leave instances for crash recovery
  - Clear internal state
- Unit tests:
  - Recover happy path (mock ListInstances returns tagged instances)
  - Recover with zero instances (clean start)
  - Recover with ListInstances failure (constructor error)
  - Recover with SSM probe failure (best-effort, instance tracked without processes)
  - PauseSandbox happy path (processes marked exited)
  - ResumeSandbox happy path (IP refreshed)
  - ResumeSandbox timeout (`ErrInstanceReadyTimeout`)
  - Close with `TerminateOnClose=true` (all instances terminated)
  - Close with `TerminateOnClose=false` (instances left running)
  - Close waits for in-flight CreateSandbox
  - Close then CreateSandbox returns `ErrDirectClosed`
  - Concurrent operations race test (`go test -race`)

**Estimated size:** ~600 lines (implementation + tests)
**Dependencies:** Task 4 (needs process management to test recovery of processes; pause clears process state)

---

### Task 6: End-to-End Unit Tests + Integration Test Skeleton

**Package:** `internal/sandbox/control/direct/`
**Plan references:** Plan 19 sections 11.1, 11.2, 11.3
**Implementation guide:** docs/plans/00-implementation-guide.md

**Scope:**
- Full lifecycle unit test with mocks: `CreateSandbox -> LaunchProcess -> GetProcessStatus -> KillProcess -> DestroySandbox`
- Multi-agent unit test: `CreateSandbox -> LaunchProcess x3 -> GetProcessStatus x3 -> KillProcess x3 -> DestroySandbox`
- Pause/Resume lifecycle: `CreateSandbox -> LaunchProcess -> PauseSandbox -> ResumeSandbox -> LaunchProcess -> DestroySandbox`
- Crash recovery lifecycle: `CreateSandbox -> LaunchProcess -> new adapter (with recovery) -> verify state -> DestroySandbox`
- Integration test skeleton (`//go:build integration_ec2`):
  - Test helper for EC2 instance lifecycle (create, tag, sweep)
  - Sweep function: terminate test-tagged instances older than 1 hour
  - Skeleton test cases (full lifecycle, multi-agent, pause/resume, crash recovery, network connectivity)
  - These are NOT expected to pass in CI -- they require AWS credentials and infrastructure

**Estimated size:** ~400 lines (lifecycle tests + integration skeleton)
**Dependencies:** Task 5 (all functionality must be complete)

---

## Dependency DAG

```
Task 1: Shared Instance Provisioner Package
  |
  v
Task 2: Direct Adapter Foundation (types, errors, helpers)
  |
  v
Task 3: Core Sandbox Lifecycle (CreateSandbox, DestroySandbox)
  |
  v
Task 4: SSM Process Management (LaunchProcess, KillProcess, GetProcessStatus)
  |
  v
Task 5: Crash Recovery, Close, Pause/Resume
  |
  v
Task 6: E2E Unit Tests + Integration Test Skeleton
```

All tasks are strictly sequential. This is intentional:
- Task 1 defines the shared interface both plans 19 and 20 depend on
- Task 2 establishes the type foundation, errors, and helpers everything else uses
- Task 3 builds instance lifecycle (CreateSandbox/DestroySandbox) that process management depends on
- Task 4 adds process management which crash recovery and pause need to be meaningful
- Task 5 adds recovery (needs process state to recover), close (needs inflight tracking from create), and pause/resume (needs process state to clear)
- Task 6 validates the full vertical integration

---

## Cross-Plan Dependencies

- **Plan 20 (Fleet Management)** depends on Task 1 (`instance/` package) for `InstanceProvisioner` interface and types
- **Plan 18 (Agent Loop RPC)** provides the `SandboxControl` interface that Direct implements -- this is already in `internal/sandbox/control/control.go`
- **Plan 20 Task: EC2InstanceProvisioner** (fleet/ec2 package) depends on Task 1

---

## Notes

- Task 1 is shared infrastructure. Plan 20's `EC2InstanceProvisioner` and `FleetSandboxControl` both import from it. It should be implemented first regardless of which plan proceeds next.
- The AWS SSM SDK dependency (`github.com/aws/aws-sdk-go-v2/service/ssm`) will be added to `go.mod` in Task 2.
- The AWS EC2 SDK dependency is NOT directly imported by the direct package -- it comes in via whichever `InstanceProvisioner` implementation is injected.
- All unit tests mock both `InstanceProvisioner` and `SSMAPI` -- no real AWS calls in unit tests.
- Integration tests are behind `//go:build integration_ec2` and will not run in CI.
