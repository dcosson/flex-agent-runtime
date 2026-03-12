# 16: Runtime Cross-Cutting Test Harness — Test Harness

**Companion to:** [16-runtime-test-harness.md](./16-runtime-test-harness.md)
**Scope:** Meta-harness validation ensuring the load/soak/performance framework itself is correct, stable, and trustworthy.

---

## 1. Property-Based Tests

### P1. Workload Generator Validity

Invariant:
- Generated workloads always satisfy profile constraints (mode ratio, concurrency caps, duration bounds).

### P2. Metrics Aggregation Conservation

Invariant:
- Aggregated counters equal sum of per-session counters within tolerance for dropped/timeout-marked samples.

### P3. Baseline Comparison Determinism

Invariant:
- Same input metrics + same baseline => identical regression verdict.

### P4. Artifact Schema Stability

Invariant:
- Emitted report/metric artifacts conform to versioned schema and are backwards-readable.

### P5. Threshold Rule Soundness

Invariant:
- Regression gates trigger exactly when configured threshold predicates evaluate true.

---

## 2. Fault Injection / Chaos Tests

### F1. Telemetry loss bursts

- Drop random telemetry batches.
- Verify graceful degradation and explicit data-quality flags.

### F2. Clock skew simulation

- Inject timestamp skew across generators/collectors.
- Verify timeline normalization and robust percentile computation.

### F3. Partial host outages

- Remove one or more sandbox hosts mid-run.
- Verify harness continues and reports scoped impact.

### F4. Corrupted artifact output path

- Simulate write failures/partial writes.
- Verify atomic artifact handling and explicit failure surfaces.

### F5. Baseline drift misconfiguration

- Inject malformed baseline files.
- Verify validation failure before benchmark execution proceeds.

---

## 3. Comparison / Oracle Tests

### O1. Independent statistics oracle

- Cross-check percentile/summary stats against independent statistical library implementation.

### O2. Historical replay oracle

- Replay known historical runs and verify expected regression/non-regression outcomes.

### O3. Dual-path telemetry oracle

- Compare direct raw-event-derived metrics vs pre-aggregated metrics pipeline outputs.

---

## 4. Deterministic Simulation Tests

### S1. Synthetic stable workload simulation

- Simulate ideal stable system; verify no false-positive regressions.

### S2. Synthetic degradation simulation

- Inject controlled latency/resource degradation; verify detection sensitivity.

### S3. Multi-dimensional tradeoff simulation

- Improve one metric while degrading another; verify policy weighting behaves as intended.

---

## 5. Benchmarks and Performance Targets

### B1. Harness overhead

Target:
- Harness CPU overhead <= 5% of runtime-under-test CPU during medium profile.

### B2. Telemetry ingest throughput

Target:
- Sustain >= 100k metric events/minute without backlog growth.

### B3. Report generation latency

Target:
- End-of-run report generation p95 < 30s for nightly profile size.

### B4. Baseline compare latency

Target:
- Regression evaluation p95 < 5s per run.

---

## 6. Stress and Soak Tests

### ST1. Harness 24h self-soak

- Run continuous profiling cycles to validate harness stability itself.

### ST2. High-cardinality telemetry stress

- Simulate extremely high label cardinality and ensure bounded resource usage.

### ST3. Concurrent run scheduler stress

- Queue overlapping profiles/runs and verify scheduling/isolation guarantees.

---

## 7. Security Tests

### SEC1. Artifact integrity

- Verify artifact checksums and tamper detection for stored reports/baselines.

### SEC2. Secret redaction in telemetry

- Ensure traces/logs/reports redact credentials and sensitive payload fields.

### SEC3. Multi-tenant run isolation

- Verify run IDs and artifacts are isolated across concurrent executions.

### SEC4. Input validation for profile configs

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
