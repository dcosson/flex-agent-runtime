# Code Review: aiag-cfy.2 (R1, reviewer-sea)

- Bead: aiag-cfy.2
- Commit range: 2f382be..9f8f3c2
- Plan doc: `docs/plans/16-runtime-test-harness-test-harness.md`
- Reviewer: reviewer-sea
- Review commit: 9f8f3c2

## Test Execution

- `go test -race ./tests/integration/runtime/... -v -count=1`: **1 FAILURE** (ST1_HarnessSelfSoak)
- All other tests (property, fault injection, oracle, simulation, security, stress ST2/ST3): PASS
- Benchmarks: B1=24ms/op, B2=8.6ms/100k, B3 p95=0ms, B4 p95=2µs — all well within targets
- Race detector: no data races detected

## Findings

### P1 - ST1 heap growth measurement has uint64 underflow bug

**Location:** `tests/integration/runtime/tests/stress_meta_test.go:94`

**Problem**
The heap growth calculation `float64(memAfter.HeapInuse - memBefore.HeapInuse)` subtracts two `uint64` values. When `memAfter.HeapInuse < memBefore.HeapInuse` (which happens normally after GC reclaims memory between runs), the subtraction wraps around to a very large uint64 value (~2^64), producing a nonsensical "17.6 TB" heap growth and causing the test to always fail:

```
stress_meta_test.go:95: heap growth: 17592186044414.43 MB
stress_meta_test.go:98: excessive heap growth during soak: 17592186044414.43 MB
--- FAIL: TestST1_HarnessSelfSoak (30.11s)
```

**Suggested fix**
Cast to `int64` before subtracting, or guard the subtraction:

```go
var heapGrowthMB float64
if memAfter.HeapInuse > memBefore.HeapInuse {
    heapGrowthMB = float64(memAfter.HeapInuse-memBefore.HeapInuse) / (1024 * 1024)
}
```

---

### P2 - F1 telemetry loss pattern is deterministic, not random as specified

**Location:** `tests/integration/runtime/tests/fault_injection_test.go:49`

**Problem**
The plan specifies F1 as "Drop random telemetry batches" but the implementation uses `shouldDrop := float64(i)/float64(numSamples) < lossRate`, which drops the first N% of samples in sequential order. At 50% loss rate, it always drops samples 0-49 and keeps 50-99. This is a deterministic head-of-stream burst pattern, not a random loss pattern.

A random loss pattern (e.g., `rand.Float64() < lossRate`) would better simulate real-world telemetry loss and could catch issues that sequential loss doesn't — for example, issues with internal state when loss happens mid-stream rather than at the start.

**Suggested fix**
Use a seeded random source for the drop decision to maintain determinism while simulating random loss:

```go
rng := rand.New(rand.NewSource(int64(lossRate * 1000)))
shouldDrop := rng.Float64() < lossRate
```

---

### P3 - S3 implements soak threshold testing, not multi-dimensional tradeoffs as planned

**Location:** `tests/integration/runtime/tests/simulation_test.go:140`

**Problem**
The plan specifies S3 as "Multi-dimensional tradeoff simulation — Improve one metric while degrading another; verify policy weighting behaves as intended." The actual S3 implementation tests individual soak drift thresholds (goroutine growth, RSS, FD, error slope) — valuable but a different test. The planned multi-dimensional scenario (e.g., latency improves while error rate worsens) is not covered.

This is a stretch goal and the implemented test is useful, so this is minor — just a labeling/coverage gap.

**Suggested fix**
Either rename the test to reflect what it actually does, or add a subtest covering the multi-dimensional tradeoff scenario described in the plan.

---

### P3 - containsStr reimplements strings.Contains

**Location:** `tests/integration/runtime/tests/security_meta_test.go:342-352`

**Problem**
The `containsStr` helper function is a manual reimplementation of `strings.Contains` from the standard library. Using the stdlib version is simpler and less error-prone.

**Suggested fix**
Replace `containsStr(content, pattern)` calls with `strings.Contains(content, pattern)` and remove the helper.

---

## Summary

4 findings: 0 P0, 1 P1, 1 P2, 2 P3

**Coverage assessment:** Excellent. All must-have categories (P1-P3, F1-F2) and all stretch goals (P4-P5, F3/F5, O1-O3, S1-S3, B1-B4, ST1-ST3, SEC1-SEC4) are implemented. SEC4 known panics are well-documented with defer/recover. The only skipped category is F4 (corrupted artifact output path), which is acceptable as a stretch goal.

**Verdict**: Approved with revisions (P1 must be fixed — ST1 currently fails on every run)
