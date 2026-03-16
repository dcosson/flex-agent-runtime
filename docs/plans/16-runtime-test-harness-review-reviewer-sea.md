# 16: Runtime Cross-Cutting Test Harness — Review Findings (reviewer-sea, R1)

**Plan:** [16-runtime-test-harness.md](./16-runtime-test-harness.md)
**Test Harness:** [16-runtime-test-harness-test-harness.md](./16-runtime-test-harness-test-harness.md)
**Reviewer:** reviewer-sea
**Round:** 1
**Date:** 2026-03-12

---

## Summary

The plan provides a solid high-level framework for load, soak, and performance testing. The harness topology, workload profiles, and metrics coverage are well thought out. The main gaps are around the workload composition mechanism (how prior E2E scenarios are scaled), infrastructure provisioning for large profiles, baseline management workflow, and underspecified soak test failure criteria.

## Findings

### [F1] Workload composition mechanism from prior suites is unspecified — P2

**Section:** Plan §2.3 (Input from Prior E2E Suites), §4.1 (Concurrency Profiles)
**Issue:** The plan says "Harness composes and scales these workloads rather than inventing disconnected synthetic tests" and references Mode 3 (plan 14) and Mode 2 (plan 15) scenarios as workload templates. But it doesn't define the composition mechanism: What interface do the prior suites expose for the harness to invoke? How is a single E2E scenario parameterized for concurrent execution (different session IDs, different fixture repos)? How are Mode 2 and Mode 3 workloads mixed in a single run (separate goroutine pools? shared resource pool? independent processes?)?
**Recommendation:** Define a `Workload` interface that the harness controller uses:
```go
type Workload interface {
    Setup(ctx context.Context) error
    Run(ctx context.Context, sessionID string) (*WorkloadResult, error)
    Teardown(ctx context.Context) error
}
```
Mode 2 and Mode 3 workloads implement this interface by wrapping their existing E2E scenarios. The harness controller manages concurrency, session allocation, and result collection. Document this interface and the wrapping pattern.

### [F2] Infrastructure requirements for P-large profile are missing — P2

**Section:** Plan §4.1 (Concurrency Profiles)
**Issue:** The P-large profile runs 200+ concurrent agents. Each agent session requires a ZFS dataset and potentially gVisor containers. The plan doesn't specify: how many sandbox hosts are needed, how the harness controller connects to and distributes sessions across multiple hosts, what compute/storage requirements each host needs, or how this infrastructure is provisioned in CI. Without this, P-large is aspirational rather than implementable.
**Recommendation:** Add an infrastructure section specifying: (a) estimated hosts needed per profile (e.g., P-small: 1 host, P-medium: 2-3 hosts, P-large: 10+ hosts), (b) per-host requirements (CPU, RAM, ZFS pool size), (c) provisioning strategy (static fleet, auto-scaled, or CI-provisioned), (d) how the harness controller discovers and manages multiple hosts (static config, service discovery, or CLI flags).

### [F3] Soak test failure criteria are underspecified — P2

**Section:** Plan §8, AC2 (Soak harness detects stability regressions)
**Issue:** AC2 says "no unbounded resource growth" but doesn't define thresholds. The monitored drift dimensions (§4.2) list goroutine count, RSS, heap, fd count, RPC error rate, snapshot count — but without concrete thresholds, the soak test can't determine pass/fail programmatically. "Unbounded" is subjective — is 10% goroutine growth over 12 hours acceptable? 100%?
**Recommendation:** Define specific drift thresholds for soak tests: e.g., goroutine count growth < 5% over baseline after 1-hour warmup, RSS growth < 10% over 12 hours, fd count stable within ±2 of steady state, RPC error rate trend slope < 0.01%/hour. These can be initial targets that are refined as we gather data, but the test needs concrete pass/fail criteria.

### [F4] Baseline management workflow is underspecified — P2

**Section:** Plan §5.2 (Threshold Policy)
**Issue:** The plan mentions "versioned baseline files" and "Regressions fail CI if above tolerated envelopes unless explicitly approved" but doesn't define: (a) who creates initial baselines, (b) how baselines are updated when performance legitimately changes (e.g., after a planned architectural change), (c) what the approval process for exceptions looks like (PR comment? config file? CI flag?), (d) where baselines are stored (in-repo? external artifact store?). Without this, the regression gating will either be too strict (blocking legitimate changes) or too loose (manually overridden on every failure).
**Recommendation:** Define the baseline workflow: baselines stored in-repo under `tests/integration/runtime/reports/baselines/`. New baselines created by running a dedicated "baseline update" CI job that produces and commits updated baseline files. Exceptions require a `BASELINE_OVERRIDE=true` CI variable with a linked issue tracking the expected regression. Automatic baseline drift detection alerts when baselines haven't been updated in >30 days.

### [F5] Test harness meta-tests (companion doc) have ambitious coverage without implementation guidance — P3

**Section:** Test Harness companion doc (16-runtime-test-harness-test-harness.md)
**Issue:** The meta-harness companion doc defines 5 property tests, 5 fault injection tests, 3 oracle tests, 4 simulation tests, 4 benchmarks, 3 stress tests, and 4 security tests — 28 test categories for validating the harness itself. While thorough, this is a significant implementation effort and the companion doc provides no implementation guidance (unlike the 11-sandbox-host-service test harness which includes code examples). For a meta-harness, the risk of over-engineering is high — tests-of-tests can become their own maintenance burden.
**Recommendation:** Prioritize the meta-harness tests. Mark P1-P3 (workload generator validity, metrics aggregation, baseline comparison) and F1-F2 (telemetry loss, clock skew) as must-have. Mark the rest as stretch goals. Add implementation notes for the must-have tests so implementors know how to approach them. Consider whether some meta-harness tests (like S1 harness self-soak) could be replaced by simpler invariant checks integrated into the harness itself.

### [F6] No specification of how distributed benchmark runners coordinate — P3

**Section:** Plan §11 (Extreme Optimization), item 1
**Issue:** The plan mentions "distributed benchmark runners to parallelize high-concurrency profiles" but doesn't specify how multiple runners coordinate: shared metrics collection, synchronized test phases (ramp-up, steady state, ramp-down), result aggregation from multiple runners, clock synchronization for latency measurements.
**Recommendation:** Define the coordination model. Simplest viable approach: a single harness controller process that launches workloads across multiple sandbox hosts via RPC, collects metrics centrally via OTEL, and aggregates results in a single report. Runners don't need to coordinate with each other — only with the controller.

---

## Statistics

- Total findings: 6
- P0 (blocking): 0
- P1 (significant): 0
- P2 (moderate): 4
- P3 (minor): 2
