# Code Review: aiag-cfy.1 (R1, reviewer-sea)

- Bead: aiag-cfy.1
- Commit range: 61b2495
- Plan doc: `docs/plans/16-runtime-test-harness.md`
- Reviewer: reviewer-sea
- Review commit: fc67d31

## Findings

### P2 - slopePerHour computes per-interval slope, not per-hour

**Location:** `tests/integration/runtime/harness/telemetry.go:128-133`

**Problem**
`slopePerHour` divides `(last - first) / (len(in) - 1)`, computing slope per sample interval, not per hour. The function name and the threshold field `RPCErrorSlopePctHr` both imply per-hour normalization, but `RecordDriftSample` doesn't record timestamps, so there's no way to time-normalize.

This works in the current test (`TestSoakStability_DetectsDriftRegressions`) because the test data and threshold are co-tuned for 3 samples. But in a real 12h soak sampling every minute (720 samples), the per-interval slope would be ~60x smaller than per-hour, making the 0.01 threshold far too coarse. Anyone calibrating thresholds for a production soak run will be misled by the "per hour" naming.

**Suggested fix**
Either:
1. Add a timestamp parameter to `RecordDriftSample` and compute actual slope per hour using elapsed time, or
2. Add a `sampleInterval` field to `SoakProfile` and multiply the per-interval slope by `(time.Hour / sampleInterval)` in `slopePerHour`, or
3. Rename to `slopePerInterval` and rename `RPCErrorSlopePctHr` to `RPCErrorSlopePctInterval` to match actual semantics. Update the plan's threshold table accordingly.

Option 1 is the most robust. Option 3 is the simplest if you want to defer time-normalization to a follow-up.

---

### P3 - Dead code in MetricsMap

**Location:** `tests/integration/runtime/harness/artifacts.go:110-115`

**Problem**
```go
keys := make([]string, 0, len(m))
for k := range m {
    keys = append(keys, k)
}
sort.Strings(keys)
_ = keys
```
The `keys` slice is created, populated, sorted, and then discarded. This appears to be a leftover from an earlier version that intended to produce ordered output. It's harmless but confusing.

**Suggested fix**
Remove the dead code (lines 110-115).

---

### P3 - Workloads lack deterministic seed support

**Location:** `tests/integration/runtime/workloads/mode2_workloads.go:28`, `tests/integration/runtime/workloads/mode3_workloads.go:29`

**Problem**
Both workload types use `rand.Intn()` from the global random source for jitter. The plan specifies "Fixed random seeds for workload generation in deterministic lanes" (§9.2), but there's no way to pass a seed or `*rand.Rand` to the workloads for reproducible runs.

**Suggested fix**
Accept an optional `*rand.Rand` (or seed) in the workload constructors. When nil, use the global source (current behavior). When set, use the provided source for deterministic jitter in CI runs.

---

## Summary

3 findings: 0 P0, 0 P1, 1 P2, 2 P3

All tests pass clean with `-race`. Benchmark stable (container boot p95 ~20ms). Code structure matches plan §3 exactly. Controller orchestration, telemetry pipeline, artifact generation, baseline comparison, soak assertions, and host config loading all work as specified. Good coverage of acceptance criteria AC1-AC6 at the harness-core level.

**Verdict**: Approved with revisions
