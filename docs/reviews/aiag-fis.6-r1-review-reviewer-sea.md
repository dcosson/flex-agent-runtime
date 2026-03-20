# Code Review: aiag-fis.6 (R1, reviewer-sea)

- Bead: aiag-fis.6
- Commit range: 71fa926..87a0fec
- Plan doc: docs/plans/20-fleet-management.md §15.1
- Reviewer: reviewer-sea
- Review commit: 87a0fec

## Findings

### P2 - Health check result labels don't match plan spec

**Location:** `internal/sandbox/control/fleet/control_loop.go:86-90`

**Problem**
Plan §15.1 specifies `fleet_health_checks_total{result}` with labels `healthy, degraded, unhealthy, error`. The implementation only emits `healthy` and `error`. The `degraded` and `unhealthy` labels are not used — health check failures all map to `error`, and the transition to unhealthy state (when `ConsecutiveHealthFailures >= UnhealthyThreshold`) doesn't emit a separate counter increment with an `unhealthy` label.

This is likely intentional since the current health check contract only returns healthy or error, with no degraded/unhealthy distinction at the RPC level. But the plan should be updated to match, or a comment should note the simplification.

**Suggested fix**
Update Plan 20 §15.1 to show `healthy, error` as the result labels for `fleet_health_checks_total`, removing `degraded` and `unhealthy` since those states are tracked via instance state transitions rather than individual health check results.

---

### P3 - `fleet_instances_total` naming could be confused with a counter

**Location:** `internal/sandbox/control/fleet/metrics.go:45`

**Problem**
The metric name `fleet_instances_total` uses the `_total` suffix, which by Prometheus naming conventions is typically reserved for counters. This is a gauge. The plan doc uses the same name, so this is plan-consistent, but it could cause confusion in Grafana/alerting. The other gauge `fleet_sessions_total` has the same pattern.

**Suggested fix**
Consider renaming to `fleet_instances` and `fleet_sessions` (without `_total`) to follow Prometheus conventions. Low priority since the plan specifies these names.

---

### P3 - CreateSandbox duration recorded multiple times on retry path

**Location:** `internal/sandbox/control/fleet/fleet.go:118-160`

**Problem**
In `CreateSandbox`, when `selectAndClaim` returns `ErrNoCapacity` and `provisionInstance` fails, the method records the counter and histogram then returns. But on a successful provision + failed claim in a subsequent attempt, the duration recorded covers the full method time including the retry. This is actually correct behavior — we want end-to-end latency — but the `no_capacity` counter at line 78-79 could fire and then the loop could continue (via `continue`) without a corresponding histogram observation if the provision succeeds but a later claim attempt fails differently. Actually, re-reading: the `continue` on line 82 skips the histogram, and only the final exit paths record duration. Wait — line 79 records duration, then line 80 returns. So the early exit path is fine.

Let me re-read... Actually, the logic is correct: every return path records exactly one counter increment and one histogram observation. The `continue` on the retry path doesn't record (which is correct — the call hasn't finished yet).

**Suggested fix**
No fix needed. Documenting that the instrumentation was verified correct across all exit paths.

---

## Summary

3 findings: 0 P0, 0 P1, 1 P2, 2 P3

**Verdict**: Approved with revisions

The implementation is comprehensive and well-tested:
- All 16 Plan §15.1 metrics are now implemented (4 gauges, 8 counters, 4 histograms), plus 3 bonus metrics from the earlier aiag-fis.2 work
- 14 new test cases cover every new metric with targeted scenarios
- Metrics are wired into correct code paths with proper error/success labeling
- Nil-safe observer methods prevent panics if metrics are uninitialized
- `updateMetricsSnapshot` correctly aggregates per-instance state counts and sessions under proper locking

The P2 is a plan-doc alignment issue only (no code change needed). The code correctly implements what makes sense for the current health check contract.
