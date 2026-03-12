# 16: Runtime Cross-Cutting Test Harness — Test Harness

**Companion to:** [16-runtime-test-harness.md](./16-runtime-test-harness.md)
**Scope:** Meta-harness validation ensuring the load/soak/performance framework itself is correct, stable, and trustworthy.

---

## Priority Summary

**Must-Have (implement first):**
- P1 (Workload Generator Validity), P2 (Metrics Aggregation Conservation), P3 (Baseline Comparison Determinism) — core correctness of the harness
- F1 (Telemetry loss bursts), F2 (Clock skew simulation) — fault tolerance for distributed operation

**Stretch Goals (implement if time permits):**
- All remaining tests (P4, P5, F3–F5, O1–O3, S1–S3, B1–B4, ST1–ST3, SEC1–SEC4)
- Some stretch tests (notably S1 harness self-soak and ST1 24h self-soak) may be replaceable by simpler invariant checks integrated into the harness itself

---

## 1. Property-Based Tests

### P1. Workload Generator Validity — **Must-Have (P1)**

Invariant:
- Generated workloads always satisfy profile constraints (mode ratio, concurrency caps, duration bounds).

**Implementation notes:** Use `testing/quick` or a property-based testing library to generate random profile configs and verify that the workload generator always produces workloads respecting mode mix ratios (within ±1 session of target), concurrency never exceeds caps, and total duration stays within bounds. Test against both Mode 2 and Mode 3 workload wrappers using the `Workload` interface.

### P2. Metrics Aggregation Conservation — **Must-Have (P1)**

Invariant:
- Aggregated counters equal sum of per-session counters within tolerance for dropped/timeout-marked samples.

**Implementation notes:** Construct synthetic `WorkloadResult` sets with known totals (including edge cases: zero results, results with dropped samples, timeout-marked samples). Feed them through the aggregation pipeline and assert that aggregated values match expected sums within the documented tolerance. Verify that dropped/timeout samples are accounted for explicitly in the aggregation metadata.

### P3. Baseline Comparison Determinism — **Must-Have (P1)**

Invariant:
- Same input metrics + same baseline => identical regression verdict.

**Implementation notes:** Serialize a known metrics bundle and baseline file. Run the comparison engine N times (N >= 100) and assert identical verdict and identical threshold breach details each time. Also test with metrics at exact threshold boundaries to verify no floating-point nondeterminism.

### P4. Artifact Schema Stability — Stretch Goal

Invariant:
- Emitted report/metric artifacts conform to versioned schema and are backwards-readable.

### P5. Threshold Rule Soundness — Stretch Goal

Invariant:
- Regression gates trigger exactly when configured threshold predicates evaluate true.

---

## 2. Fault Injection / Chaos Tests

### F1. Telemetry loss bursts — **Must-Have (P1)**

- Drop random telemetry batches.
- Verify graceful degradation and explicit data-quality flags.

**Implementation notes:** Wrap the telemetry collector with a lossy proxy that drops batches at configurable rates (e.g., 1%, 10%, 50%). Run a short synthetic workload through the proxy. Assert that: (a) the harness does not crash or hang, (b) emitted reports include a data-quality flag indicating the drop rate, (c) aggregated metrics are explicitly marked as approximate when loss exceeds a configured threshold (e.g., > 5%).

### F2. Clock skew simulation — **Must-Have (P1)**

- Inject timestamp skew across generators/collectors.
- Verify timeline normalization and robust percentile computation.

**Implementation notes:** Inject monotonically increasing offsets (e.g., +50ms, +200ms, +1s) into event timestamps from a subset of generators. Verify that the timeline normalization layer detects and corrects skew, and that percentile computations remain within expected bounds compared to a zero-skew baseline. This validates the harness's resilience to the NTP-based synchronization approach described in the main plan §11.

### F3. Partial host outages — Stretch Goal

- Remove one or more sandbox hosts mid-run.
- Verify harness continues and reports scoped impact.

### F4. Corrupted artifact output path — Stretch Goal

- Simulate write failures/partial writes.
- Verify atomic artifact handling and explicit failure surfaces.

### F5. Baseline drift misconfiguration — Stretch Goal

- Inject malformed baseline files.
- Verify validation failure before benchmark execution proceeds.

---

## 3. Comparison / Oracle Tests

### O1. Independent statistics oracle — Stretch Goal

- Cross-check percentile/summary stats against independent statistical library implementation.

### O2. Historical replay oracle — Stretch Goal

- Replay known historical runs and verify expected regression/non-regression outcomes.

### O3. Dual-path telemetry oracle — Stretch Goal

- Compare direct raw-event-derived metrics vs pre-aggregated metrics pipeline outputs.

---

## 4. Deterministic Simulation Tests

### S1. Synthetic stable workload simulation — Stretch Goal

- Simulate ideal stable system; verify no false-positive regressions.

> **Note:** This test could be replaced by simpler invariant checks integrated into the harness itself — e.g., assert that a zero-drift synthetic workload never triggers a regression verdict, checked as part of the harness's own startup self-test.

### S2. Synthetic degradation simulation — Stretch Goal

- Inject controlled latency/resource degradation; verify detection sensitivity.

### S3. Multi-dimensional tradeoff simulation — Stretch Goal

- Improve one metric while degrading another; verify policy weighting behaves as intended.

---

## 5. Benchmarks and Performance Targets

### B1. Harness overhead — Stretch Goal

Target:
- Harness CPU overhead <= 5% of runtime-under-test CPU during medium profile.

### B2. Telemetry ingest throughput — Stretch Goal

Target:
- Sustain >= 100k metric events/minute without backlog growth.

### B3. Report generation latency — Stretch Goal

Target:
- End-of-run report generation p95 < 30s for nightly profile size.

### B4. Baseline compare latency — Stretch Goal

Target:
- Regression evaluation p95 < 5s per run.

---

## 6. Stress and Soak Tests

### ST1. Harness 24h self-soak — Stretch Goal

- Run continuous profiling cycles to validate harness stability itself.

> **Note:** Consider replacing this full self-soak with simpler invariant checks integrated into the harness: e.g., assert bounded goroutine/memory growth within the harness process itself at the end of every standard run. This provides ongoing self-monitoring without a dedicated 24h meta-test.

### ST2. High-cardinality telemetry stress — Stretch Goal

- Simulate extremely high label cardinality and ensure bounded resource usage.

### ST3. Concurrent run scheduler stress — Stretch Goal

- Queue overlapping profiles/runs and verify scheduling/isolation guarantees.

---

## 7. Security Tests

### SEC1. Artifact integrity — Stretch Goal

- Verify artifact checksums and tamper detection for stored reports/baselines.

### SEC2. Secret redaction in telemetry — Stretch Goal

- Ensure traces/logs/reports redact credentials and sensitive payload fields.

### SEC3. Multi-tenant run isolation — Stretch Goal

- Verify run IDs and artifacts are isolated across concurrent executions.

### SEC4. Input validation for profile configs — Stretch Goal

- Fuzz profile definitions; reject dangerous/invalid config values safely.

---

## 8. Manual QA Plan

1. Run small and medium profiles and inspect generated reports for clarity and correctness.
2. Trigger a synthetic regression and verify gate failure + diagnostics.
3. Replay a historical benchmark bundle and confirm reproducible verdict.
4. Manually inspect snapshot growth and RPC latency charts for expected behavior.
5. Validate baseline update workflow and approval process usability.

---

## 9. CI Tier Mapping

| Tier | Runs | Contents |
|------|------|----------|
| PR-fast | every PR | harness unit/property tests + synthetic simulations |
| PR-standard | every PR | component fault-injection checks + baseline-compare tests |
| Nightly | nightly | medium-profile harness benchmarks + replay oracles |
| Weekly | weekly | harness self-soak + high-cardinality stress |

---

## 10. Exit Criteria

1. Property tests P1-P5 pass consistently.
2. Fault injection tests F1-F5 pass and diagnostics are actionable.
3. Oracle tests O1-O3 pass with documented tolerances.
4. Deterministic simulations S1-S3 pass.
5. Benchmark targets B1-B4 are met or approved exceptions recorded.
6. Stress/soak tests ST1-ST3 pass in scheduled CI.
7. Security tests SEC1-SEC4 pass.
8. Manual QA checklist completed for release-candidate commit.
