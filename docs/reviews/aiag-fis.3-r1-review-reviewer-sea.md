# Code Review: aiag-fis.3 (R1, reviewer-sea)

- Bead: aiag-fis.3
- Commit range: 938c918..07525d0
- Plan doc: docs/plans/20-fleet-management.md §15.3
- Reviewer: reviewer-sea
- Review commit: 2309ee5

## Findings

### P3 - SessionCount type mismatch with TotalSessions

**Location:** `internal/sandbox/control/fleet/fleet.go:475`

**Problem**
`TotalSessions` is `int64` (from atomic operations), but `mi.SessionCount` is added via `+=` under a read lock. The field type in `ManagedInstance` is `int64` so this works, but the accumulation happens under `mu.RLock` (fleet-level) + `mi.mu.RLock` (instance-level), which is correct. No actual bug — just noting the mixed access patterns are intentional and safe.

**Suggested fix**
No fix needed. Documenting for the record that the locking strategy was verified as correct.

---

### P3 - Healthy condition could use clearer documentation

**Location:** `internal/sandbox/control/fleet/fleet.go:485`

**Problem**
The `Healthy` field condition `warm >= f.config.MinInstances && healthy > 0` is reasonable but the plan doc comment says "true if warm pool >= MinInstances and healthy instances > 0" — this matches. However, an edge case: if `MinInstances == 0` and there are no instances at all, `Healthy` would be true (0 >= 0 && healthy > 0 is false... actually healthy would be 0 so it would be false). The logic is correct.

**Suggested fix**
No fix needed. Logic handles edge cases correctly.

---

## Summary

0 actionable findings: 0 P0, 0 P1, 0 P2, 2 P3 (informational only)

**Verdict**: Approved

The FleetStatus implementation is clean:
- Proper per-instance locking (`mi.mu.RLock`) within fleet-level lock (`f.mu.RLock`)
- WarmPoolSize correctly counts only `InstanceReady` with `SessionCount == 0`
- Healthy logic matches plan specification
- 3 targeted tests cover state reflection, unhealthy condition, and warm pool counting
- Plan doc §15.3 properly updated to match implementation types (`int64`, `int32`, `Closed` field)
