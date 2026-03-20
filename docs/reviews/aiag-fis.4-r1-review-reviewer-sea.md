# Code Review: aiag-fis.4 (R1, reviewer-sea)

- Bead: aiag-fis.4
- Commit range: 27d5b60..71fa926
- Plan doc: docs/plans/20-fleet-management.md §11.2
- Reviewer: reviewer-sea
- Review commit: 71fa926

## Findings

### P3 - Integration test harness could verify Close() behavior

**Location:** `internal/sandbox/control/fleet/fleet_integration_test.go`

**Problem**
The full lifecycle test (TestIntegrationFleet_FullLifecycle) tests provision → route → destroy → scale-down but doesn't exercise `Close()` on a fleet with active instances. This is a key integration scenario — Close() must drain active sessions and terminate all instances. The unit tests cover this, but an integration-level Close() test would add confidence in the full teardown path.

**Suggested fix**
Consider adding a test that creates multiple sandboxes across multiple instances, then calls `Close()` and verifies all instances are terminated and the fleet rejects new requests. Low priority since unit tests cover this path.

---

### P3 - WarmPoolProvisioning test relies on exact iteration count

**Location:** `internal/sandbox/control/fleet/fleet_integration_test.go:196-198`

**Problem**
The test runs `runIteration` twice and expects exactly 2 instances. This works because the control loop provisions one warm instance per iteration, but this is an implementation detail. If the control loop were changed to provision multiple in one pass, the test would break. The coupling is acceptable for now since the test is verifying current behavior.

**Suggested fix**
No fix needed — just noting the implicit coupling. Could use a polling helper that runs iterations until the desired state is reached, but that would over-engineer this.

---

## Summary

2 findings: 0 P0, 0 P1, 0 P2, 2 P3

**Verdict**: Approved

The integration test suite is well-structured:
- Clean harness pattern with `integrationProvisioner` and `integrationNodeRuntime` — properly thread-safe with their own mutexes
- Reuses existing `mockNodeClient` and `fakeClock` from untagged test files (correct build-tag composition)
- Four scenarios covering the key fleet lifecycle paths: full lifecycle with scale-down, warm pool provisioning, unhealthy drain, crash recovery re-adoption
- All tests pass under `-tags integration`
- Properly build-tag gated (`//go:build integration`)
