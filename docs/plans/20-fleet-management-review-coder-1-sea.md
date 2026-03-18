# Review: 20-fleet-management (coder-1-sea)

- Source doc: `docs/plans/20-fleet-management.md`
- Reviewed commit: b06c24e79c72ffdd33a6e5734f8f609356aceeea
- Reviewer: coder-1-sea

## Findings

### P1 - Crash-Recovery Design Cannot Be Implemented Via `InstanceProvisioner`

**Problem**
The plan’s recommended crash-recovery path is to rebuild fleet state from cloud provider inventory using provider-level list/filter operations (tag-based discovery) (`docs/plans/20-fleet-management.md:958-963`).
However, the proposed `InstanceProvisioner` interface only supports point lookups by known instance ID (`Launch/Terminate/Stop/Start/Describe`) and has no list/filter method (`docs/plans/20-fleet-management.md:132-153`). After process crash, instance IDs are exactly what the fleet manager no longer has. This forces an EC2-specific escape hatch and breaks the provider-agnostic abstraction.

**Required fix**
Add a provider-agnostic inventory API to `InstanceProvisioner` (for example `ListInstances(ctx, filter InstanceFilter) ([]InstanceStatus, error)` where `InstanceFilter` includes fleet ID and managed-by tags/labels). Update OQ2 and implementation order to require this seam, and add recovery tests that rebuild state from provider inventory.

---

### P1 - `InstanceConfig` Is AWS-Shaped and Leaks Provider-Specific Semantics

**Problem**
The plan repeatedly claims provider-agnostic portability (`docs/plans/20-fleet-management.md:6`, `:124-125`, `:247`), but `InstanceConfig` hard-codes AWS concepts (`SecurityGroupIDs`, `SubnetID`, `KeyName`, `IAMRole`) (`docs/plans/20-fleet-management.md:163-183`). This is not a neutral cloud model and will force awkward/partial mappings in GCP/Azure implementations.

**Required fix**
Refactor config layering so the fleet core consumes provider-neutral requirements (capacity intent, network intent), while provider-specific launch details are encapsulated in provisioner-specific config (constructor options or typed provider config). Keep the fleet package free of provider-specific fields in shared interfaces.

---

### P1 - Provisioning Path Has Control-Loop Stall and Over-Provision Race Risk

**Problem**
`LaunchInstance` is explicitly blocking until instance is running (`docs/plans/20-fleet-management.md:134-137`, `:241`), and the control loop invokes it inline during phase 3 (`docs/plans/20-fleet-management.md:431`). This can stall loop progress for long boot durations and delay health/drain handling. The doc also allows synchronous provisioning from `CreateSandbox` when no capacity (`docs/plans/20-fleet-management.md:478`), but does not specify singleflight/pending-launch coordination, so concurrent callers can trigger duplicate launches and capacity overshoot.

**Required fix**
Specify a non-blocking provisioning model: track pending launches in fleet state, enforce max concurrent launches, and deduplicate no-capacity provisions with singleflight or equivalent reservation logic. Ensure control-loop iterations remain bounded and do not block on cloud boot latency.

---

### P2 - SandboxID Encoding Is Ambiguous and Parser/Test Contract Is Inconsistent

**Problem**
The ID format uses `fleet:<instanceID>:<sessionID>` with first-colon parsing (`docs/plans/20-fleet-management.md:561-595`). This is delimiter-fragile and currently does not validate empty components, while the test plan expects empty-component rejection (`docs/plans/20-fleet-management.md:810`). It also has no escaping/versioning strategy for future provider/session ID formats.

**Required fix**
Define a robust, versioned encoding (for example `fleet:v1:<base64url-json>`), or enforce strict charset + full validation for both components (non-empty, delimiter-safe) and align parser behavior with listed tests.

---

### P2 - Test Strategy Omits Recovery/Abstraction Seams It Relies On

**Problem**
The doc relies on restart recovery from provider inventory (`docs/plans/20-fleet-management.md:958-963`) and on provider-agnostic abstraction quality (`docs/plans/20-fleet-management.md:122-125`), but the test strategy lists no crash-recovery/bootstrap tests and no cross-provider contract tests for `InstanceProvisioner` behavior (`docs/plans/20-fleet-management.md:765-838`).

**Required fix**
Add explicit tests for: startup recovery from provider inventory, stale/orphan instance reconciliation, and an interface contract suite that every provisioner implementation must pass (state mapping, transient errors, idempotency behavior).

---

### P3 - Shaping Source Metadata Uses the Wrong Shape Label

**Problem**
The plan header says “Shape B selected: in-process fleet management” (`docs/plans/20-fleet-management.md:7`), but the shaping doc marks in-process fleet management as Shape A and Shape B as ASG-based (`docs/shaping/ec2-fleet-management.md:47-68`).

**Required fix**
Correct the plan metadata to reference the selected shape label consistently with the shaping source.

---

## Summary

6 findings: 0 P0, 3 P1, 2 P2, 1 P3

**Verdict**: Not approved
