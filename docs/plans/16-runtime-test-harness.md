# 16: Runtime Cross-Cutting Test Harness

**Status:** Draft
**Depends on:** 14-mode3-e2e, 15-mode2-e2e
**Depended on by:** —
**Implements:** Cross-cutting runtime assurance suite for load, soak, performance, durability, and scalability across Mode 2 and Mode 3 deployments.

---

## 1. Overview

This plan defines the final integration/performance harness for the entire runtime. It moves beyond correctness of individual flows and validates the system under scale, long duration, and adverse operating conditions.

Primary goals:
- Load testing with many concurrent agent sessions.
- Soak testing for long-running stability and leak detection.
- Snapshot space growth characterization under realistic mutation patterns.
- Container boot-time and execution-path benchmarking.
- RPC latency and reliability profiling under distributed load.

Non-goals:
- Redefining correctness scenarios already covered in plans 14 and 15.
- Feature development in agent/tools/sandbox/rpc components.

---

## 2. Architecture

### 2.1 Harness Topology

```mermaid
graph TB
    subgraph "Harness Controller"
        ORCH[Scenario orchestrator\nload profiles + schedules]
        GEN[Workload generator\nagent/task mixes]
        COL[Metrics/trace collector]
        ANA[Analysis + report generator]

        ORCH --> GEN
        GEN --> COL
        COL --> ANA
    end

    subgraph "Runtime Under Test"
        M2[Mode 2 stack\ntermmux + sandbox host + 3rd party drivers]
        M3[Mode 3 stack\nagent loop + RPC + sandbox host]
        SB[Sandbox hosts\nZFS + gVisor]
    end

    GEN --> M2
    GEN --> M3
    M2 --> SB
    M3 --> SB
    SB --> COL
```

### 2.2 Test Families

| Family | Purpose |
|--------|---------|
| Load tests | concurrency scaling and throughput limits |
| Soak tests | long-duration stability and resource drift |
| Snapshot-space analysis | storage growth, retention efficiency, rollback behavior |
| Container benchmarks | Tier 2 cold-start/exec performance characterization |
| RPC profiling | latency distribution, error/retry behavior, saturation points |

### 2.3 Input from Prior E2E Suites

- Mode 3 scenarios (plan 14) provide remote-dispatch workload templates.
- Mode 2 scenarios (plan 15) provide driver/PTY lifecycle workload templates.
- Harness composes and scales these workloads rather than inventing disconnected synthetic tests.

---

## 3. Suite Structure

```text
e2etests/runtime/
├── harness/
│   ├── controller.go          # orchestrates run plans and phases
│   ├── profiles.go            # load/soak/benchmark profile definitions
│   ├── telemetry.go           # metrics/trace ingestion and tags
│   ├── artifacts.go           # report + raw-data artifact emission
│   └── assertions.go          # SLA/threshold checks
├── workloads/
│   ├── mode2_workloads.go     # based on plan 15 scenarios
│   ├── mode3_workloads.go     # based on plan 14 scenarios
│   └── mixed_workloads.go
├── tests/
│   ├── load_scaling_test.go
│   ├── soak_stability_test.go
│   ├── snapshot_growth_test.go
│   ├── container_boot_bench_test.go
│   └── rpc_latency_profile_test.go
└── reports/
    ├── templates/
    └── baselines/
```

---

## 4. Workload Models

### 4.1 Concurrency Profiles

- `P-small`: 10 concurrent agents (CI quick baseline)
- `P-medium`: 50 concurrent agents (daily performance signal)
- `P-large`: 200+ concurrent agents (weekly stress ceiling)

Each profile defines:
- Mode mix ratio (e.g., 60% Mode 3, 40% Mode 2)
- Tool mix (Tier 1 vs Tier 2)
- Session duration and turn cadence
- Failure injection frequency

### 4.2 Soak Profiles

- 12h baseline soak (nightly/weekly)
- 24h extended soak (weekly/monthly)

Monitored drift dimensions:
- goroutine count
- RSS / heap / fd count
- RPC error rate trend
- snapshot count and space deltas
- PTY session stability metrics

### 4.3 Benchmark Profiles

- Container cold-start benchmark (Tier 2 commands)
- RPC unary + stream latency benchmark under variable concurrency
- Snapshot creation/rollback latency under varying dataset sizes

---

## 5. Metrics and SLAs

### 5.1 Core Metrics

- Throughput: completed turns/min, completed sessions/hour
- Latency: p50/p95/p99 tool latency, RPC latency, container boot latency
- Reliability: error rates by class, retry success ratios
- Durability: rollback success rate, snapshot integrity checks
- Efficiency: snapshot space growth per turn, CPU/memory per active session

### 5.2 Threshold Policy

- Thresholds stored in versioned baseline files.
- Regressions fail CI if above tolerated envelopes unless explicitly approved.
- Trend-based alerts on monotonic degradation across recent runs.

---

## 6. Analysis Outputs

Per run produce:
- machine-readable metrics bundle (JSON/CSV)
- summarized markdown report with deltas vs baseline
- flamegraphs/traces for slowest scenarios
- storage growth report by session/profile
- retry/error-class histogram by RPC method

---

## 7. Connected Components (Seams)

| Component | Seam | Contract |
|-----------|------|----------|
| `e2etests/mode3` | workload source | remote dispatch scenarios scaled for load/soak |
| `e2etests/mode2` | workload source | driver/PTY lifecycle scenarios scaled for load/soak |
| `internal/sandbox` | durability and execution metrics | snapshots, rollback, tiered execution timings |
| `internal/rpc` | transport profiling | method-level latency/retry/error telemetry |
| `internal/termmux` | mode2 stability metrics | PTY health and normalized event throughput |

---

## 8. Acceptance Criteria

1. **Load harness executes mixed-mode concurrency profiles**
- Steps: run `P-small` and `P-medium` mixed Mode 2/3 profiles.
- Expected: harness completes and produces comparable metrics artifacts.

2. **Soak harness detects stability regressions**
- Steps: run 12h soak profile with drift monitoring.
- Expected: no unbounded resource growth; failures emit actionable diagnostics.

3. **Snapshot growth analysis is automated**
- Steps: run mutation-heavy workloads and collect snapshot/storage metrics.
- Expected: report quantifies growth per turn/session and retention effects.

4. **Container boot benchmarks are repeatable**
- Steps: execute Tier 2 benchmark profile repeatedly.
- Expected: stable boot-time distributions with baseline comparison.

5. **RPC latency profiling under load works**
- Steps: run RPC profile across concurrency levels.
- Expected: method-level p50/p95/p99 and retry/error breakdowns captured.

6. **Cross-run baselines and regression gating work**
- Steps: compare run outputs to stored baselines.
- Expected: statistically meaningful regressions are flagged automatically.

---

## 9. Testing Strategy

### 9.1 Execution Cadence

- PR fast lane: `P-small` smoke + minimal benchmark checks.
- Nightly lane: `P-medium` load + targeted soak + full profiling.
- Weekly lane: `P-large` stress + 24h soak + full report generation.

### 9.2 Reproducibility

- Fixed random seeds for workload generation in deterministic lanes.
- Versioned fixture snapshots and workload profile manifests.
- Artifact retention policy for failed and baseline runs.

### 9.3 Failure Forensics

On threshold breach, auto-capture:
- top N slow traces
- per-host resource snapshots
- RPC retry/error timelines
- sandbox snapshot growth timeline

---

## 10. URP (Unreasonably Robust Programming)

1. **Continuous synthetic canaries:** always-running low-rate mixed-mode sessions for early regression detection.
2. **Automated bisect integration:** performance regression detector triggers commit-range bisect workflows.
3. **Predictive capacity modeling:** fit queueing/capacity models from observed telemetry to forecast safe concurrency envelopes.

---

## 11. Extreme Optimization

1. Distributed benchmark runners to parallelize high-concurrency profiles.
2. Zero-copy telemetry ingestion path for high-cardinality event streams.
3. Adaptive sampling for traces to preserve hot-path visibility at scale.

---

## 12. Alien Artifacts

1. **Bayesian baseline modeling:** probabilistic regression detection resilient to natural variance.
2. **Causal performance attribution:** infer dominant bottleneck contributors across RPC, container, snapshot, and PTY subsystems.
3. **Automated workload mutation search:** generate worst-case workload patterns via adversarial search.

---

## 13. Dependencies

| Dependency | Purpose |
|-----------|---------|
| `e2etests/mode3` | remote-dispatch workload primitives |
| `e2etests/mode2` | 3rd-party-driver workload primitives |
| `internal/sandbox` | snapshot/container metrics and lifecycle hooks |
| `internal/rpc` | transport profiling hooks |
| `internal/termmux` | PTY/session stability metrics |

---

## 14. Exit Criteria

1. Runtime harness exists under `e2etests/runtime/` with load, soak, snapshot, container, and RPC profiling suites.
2. Mixed-mode concurrency profiles execute successfully and emit standardized artifacts.
3. 12h soak lane is operational with drift assertions.
4. Snapshot space growth reporting is automated and baseline-compared.
5. Container boot and RPC latency benchmark lanes are operational.
6. Baseline regression gating is wired into CI with documented override process.
