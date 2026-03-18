# 20: Fleet Management -- Round 2 Review (reviewer B)

**Reviewer:** coder-2-sea (round 2, post-revision)
**Date:** 2026-03-17
**Verdict:** Approved. All P0 and P1 findings from round 1 are adequately resolved.

---

## Verification of Round 1 Fixes

### P0-1: TOCTOU race -- RESOLVED

Section 4.1.1 specifies a clear atomic claim-slot protocol. The key design choice -- increment session count under the instance lock *before* the RPC, roll back on failure -- eliminates the window between routing and the RPC where a drain could interleave. The invariant (fleet-tracked count >= actual sessions) ensures drain decisions are conservative. TLA+ coverage of `ClaimSlot`, `CreateRPC`, `RollbackSlot`, and `BeginDrain` interleavings is appropriate.

### P0-2: Session count reconciliation -- RESOLVED

Section 4.1.3 provides a complete algorithm. Both overcount and undercount cases are handled by trusting the sandbox-host's HealthCheck as authoritative. Edge cases are documented (sandbox-host restart, stale HealthCheck). TLA+ models drift and verifies convergence within one control loop iteration.

### P1-1: Lock ordering -- RESOLVED

Section 4.1.2 specifies strict fleet-before-instance ordering. Both mutex declarations are documented with cross-references. TLA+ verifies deadlock freedom via the two-lock model (safety property 6).

### P1-2: Non-blocking LaunchInstance + singleflight -- RESOLVED

LaunchInstance returns after instance ID assignment (section 3.1, EC2 `RunInstances` returns immediately). CreateSandbox no-capacity path uses `singleflight.Group` on a shared key to prevent N callers from launching N instances (section 5.3). `MaxConcurrentProvisions` provides an additional bound. Provision continues in background if caller context is cancelled.

### P1-3: ListInstances on InstanceProvisioner -- RESOLVED

`ListInstances(ctx, InstanceFilter)` added to the interface (section 3). `InstanceFilter` supports tag and state filtering. Crash recovery sequence in OQ2 uses it for full fleet rediscovery. EC2 maps to `DescribeInstances` with tag filters. Contract tests cover list operations.

### P1-4: InstanceConfig provider-neutral -- RESOLVED

`InstanceConfig` now contains only generic fields: Image, InstanceType, UserData, Tags, DiskSizeGB (section 3). AWS-specific fields (SecurityGroupIDs, SubnetID, KeyName, IAMRole) moved to `EC2LaunchConfig`, passed to the provisioner constructor (section 3.1). Future providers follow the same pattern with their own typed config.

### P1-5: Close/shutdown protocol -- RESOLVED

Section 4.4 specifies an 8-step shutdown sequence: cancel control loop, wait for exit, reject new creates, drain all instances, wait up to DrainTimeout, force-terminate if needed, terminate all, return. `LeaveInstancesOnClose` option handles restart scenarios for re-adoption via crash recovery.

---

## New Observations (P2-P3)

### P2: DestroySandbox decrement-before-RPC briefly violates count invariant

Section 4.3 decrements the session count *before* the DestroySandbox RPC. If the RPC fails (session still alive on remote), there is a brief window where `fleet_count < actual_sessions`, violating the stated invariant in 4.1.1 ("session count is always >= actual sessions"). The re-increment on failure closes the window, and the reconciliation loop (4.1.3) provides an ultimate backstop. The window is narrow (duration of one failed RPC) and the consequence is minor (a drain decision during this window could start draining an instance that still has one session, but the drain waits for session count to reach 0 anyway).

Consider either: (a) inverting to increment-after-RPC for DestroySandbox (decrement on success, no change on failure), which would maintain the >= invariant consistently, or (b) explicitly weakening the invariant statement to note it applies strictly only to the CreateSandbox path. No action required for initial implementation -- the reconciliation loop handles correctness regardless.

### P3: Close method does not return context parameter

`Close() error` has no context parameter. If the caller wants to bound the overall shutdown duration (independent of DrainTimeout), they cannot. Minor -- DrainTimeout already bounds the wait. Could add `Close(ctx context.Context) error` in a follow-up if needed.

---

## Summary

The plan is thorough and production-quality. All round 1 P0 and P1 findings have been substantively addressed with clear protocols, documented invariants, and TLA+ coverage. The review disposition table (section 16) accurately tracks all findings. The two new observations above are minor (P2, P3) and do not block implementation.

**Approved for implementation.**
