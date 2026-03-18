# Code Review: aiag-agy.4.2 (R1, reviewer-sea)

- Bead: aiag-agy.4.2
- Commit range: 42fdec5..0beb54b
- Plan doc: docs/plans/21-serve-orchestrator.md
- Reviewer: reviewer-sea
- Review commit: 0beb54b

## Findings

### P3-1 — TestBuildNodeOrFleetControlFleetMode may leak goroutines

**Location:** `cmd/flexagent/serve_orchestrator_test.go:177-186`

**Problem**
`buildNodeOrFleetControl` with 2+ hosts constructs a real `FleetSandboxControl`. If `fleet.NewFleetSandboxControl` starts background goroutines (health check loop, scaling loop), the test returns without calling `Close()` on the returned control, leaking those goroutines for the test process lifetime.

**Suggested fix**
Add `t.Cleanup(func() { _ = control.Close() })` after the nil-error check to ensure clean shutdown.

---

## Non-finding notes

**Design is clean and well-motivated.** The three new components — `staticFleetProvisioner`, `fleetNodeClientAdapter`, and `parseSandboxHostAddr` — are the minimal wiring needed to bridge static host lists into the existing fleet infrastructure:

- **staticFleetProvisioner**: Adapts a fixed host list to `InstanceProvisioner`. `LaunchInstance` dispenses hosts sequentially; `TerminateInstance`/`StopInstance` are no-ops (we don't own static hosts). Thread-safe via mutex. The `nextLaunch` counter correctly exhausts capacity rather than wrapping.

- **fleetNodeClientAdapter**: Embeds `SandboxControl` and adds `HealthCheck` by wrapping `SandboxService.HealthCheck`. Compile-time interface assertion ensures completeness.

- **parseSandboxHostAddr**: Handles both raw `host:port` and `http://host:port` forms. Rejects URL paths, validates port range. Port uniformity check across hosts is correct since `FleetConfig.SandboxHostPort` is a single value.

- **Fleet config choices**: `MinInstances=0`, `MaxInstances=len(hosts)`, `WarmPoolTarget=len(hosts)`, `LeaveInstancesOnClose=true` — all correct for static host semantics.

## Summary

1 finding: 0 P0, 0 P1, 0 P2, 1 P3

**Verdict**: Approved

Clean, focused wiring with good validation. The only finding is a minor test cleanup issue.
