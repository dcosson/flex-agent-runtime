package tests

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	rh "flex-agent-runtime/tests/integration/runtime/harness"
	"flex-agent-runtime/tests/integration/runtime/workloads"

	"pgregory.net/rapid"
)

// =============================================================================
// P1: Workload Generator Validity
// Generated workloads always satisfy profile constraints (mode ratio, concurrency
// caps, duration bounds).
// =============================================================================

func TestP1_WorkloadGeneratorValidity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		concurrency := rapid.IntRange(2, 50).Draw(rt, "concurrency")
		mode2Weight := rapid.IntRange(1, 10).Draw(rt, "mode2Weight")
		mode3Weight := rapid.IntRange(1, 10).Draw(rt, "mode3Weight")

		profile := rh.ConcurrencyProfile{
			Name:              fmt.Sprintf("p1-c%d-m2w%d-m3w%d", concurrency, mode2Weight, mode3Weight),
			Concurrency:       concurrency,
			Mode2Weight:       mode2Weight,
			Mode3Weight:       mode3Weight,
			Tier1Share:        0.7,
			TargetSessionTime: 2 * time.Second,
			FailureRate:       0.0,
		}

		ctl := rh.NewController()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		summary, err := ctl.RunProfile(ctx, profile, workloads.DefaultMixedWorkloads())
		if err != nil {
			rt.Fatalf("RunProfile: %v", err)
		}

		// Invariant 1: Total results match concurrency
		totalWeight := mode2Weight + mode3Weight
		mode2Target := concurrency * mode2Weight / totalWeight
		if mode2Target < 1 {
			mode2Target = 1
		}
		mode3Target := concurrency - mode2Target
		if mode3Target < 1 {
			mode3Target = 1
		}
		expectedTotal := mode2Target + mode3Target

		if len(summary.Results) != expectedTotal {
			rt.Fatalf("result count %d != expected %d (m2=%d m3=%d)",
				len(summary.Results), expectedTotal, mode2Target, mode3Target)
		}

		// Invariant 2: Mode distribution matches computed targets
		if summary.Mode2Runs != mode2Target {
			rt.Fatalf("mode2 runs %d != target %d", summary.Mode2Runs, mode2Target)
		}
		if summary.Mode3Runs != mode3Target {
			rt.Fatalf("mode3 runs %d != target %d", summary.Mode3Runs, mode3Target)
		}

		// Invariant 3: Mode ratio within ±1 of ideal ratio
		idealMode2 := float64(concurrency) * float64(mode2Weight) / float64(totalWeight)
		if math.Abs(float64(summary.Mode2Runs)-idealMode2) > 1.5 {
			rt.Fatalf("mode2 ratio too far: got %d, ideal %.1f", summary.Mode2Runs, idealMode2)
		}

		// Invariant 4: All results have valid mode tags
		for i, r := range summary.Results {
			if r.Mode != "mode2" && r.Mode != "mode3" {
				rt.Fatalf("result %d has invalid mode %q", i, r.Mode)
			}
		}

		// Invariant 5: All results have non-zero timing
		for i, r := range summary.Results {
			if r.FinishedAt.Before(r.StartedAt) {
				rt.Fatalf("result %d has negative duration", i)
			}
		}
	})
}

func TestP1_WorkloadGeneratorValidity_EdgeCases(t *testing.T) {
	// Edge: minimum concurrency with extreme weight ratios
	testCases := []struct {
		name        string
		concurrency int
		m2w, m3w    int
	}{
		{"min_concurrency", 2, 1, 9},
		{"equal_weights", 10, 5, 5},
		{"extreme_m2", 10, 9, 1},
		{"extreme_m3", 10, 1, 9},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			profile := rh.ConcurrencyProfile{
				Name:              tc.name,
				Concurrency:       tc.concurrency,
				Mode2Weight:       tc.m2w,
				Mode3Weight:       tc.m3w,
				TargetSessionTime: 2 * time.Second,
			}

			ctl := rh.NewController()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			summary, err := ctl.RunProfile(ctx, profile, workloads.DefaultMixedWorkloads())
			if err != nil {
				t.Fatalf("RunProfile: %v", err)
			}

			if summary.Mode2Runs < 1 {
				t.Fatal("must have at least 1 mode2 run")
			}
			if summary.Mode3Runs < 1 {
				t.Fatal("must have at least 1 mode3 run")
			}
		})
	}
}

// =============================================================================
// P2: Metrics Aggregation Conservation
// Aggregated counters equal sum of per-session counters.
// =============================================================================

func TestP2_MetricsAggregationConservation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		numResults := rapid.IntRange(2, 30).Draw(rt, "numResults")

		tc := rh.NewTelemetryCollector()
		var totalRPCCalls int64
		var totalSnapshots int64
		var totalErrors int64

		for i := 0; i < numResults; i++ {
			rpcCalls := rapid.IntRange(0, 20).Draw(rt, fmt.Sprintf("rpc-%d", i))
			snapshots := rapid.IntRange(0, 5).Draw(rt, fmt.Sprintf("snap-%d", i))
			hasError := rapid.IntRange(0, 10).Draw(rt, fmt.Sprintf("err-%d", i)) == 0 // 10% error rate
			latencyMs := rapid.IntRange(1, 500).Draw(rt, fmt.Sprintf("lat-%d", i))

			if rpcCalls > 0 {
				tc.RecordRPCCount(rpcCalls)
				totalRPCCalls += int64(rpcCalls)
			}
			if snapshots > 0 {
				tc.RecordSnapshotDelta(int64(snapshots))
				totalSnapshots += int64(snapshots)
			}
			if hasError {
				tc.RecordRPCError(1)
				totalErrors++
			}
			tc.RecordRPCLatency(time.Duration(latencyMs) * time.Millisecond)
		}

		snap := tc.Snapshot()

		// Conservation: RPC count
		if snap.RPCCount != totalRPCCalls {
			rt.Fatalf("RPC count: got %d, want %d", snap.RPCCount, totalRPCCalls)
		}

		// Conservation: Snapshot delta
		if snap.SnapshotDelta != totalSnapshots {
			rt.Fatalf("snapshot delta: got %d, want %d", snap.SnapshotDelta, totalSnapshots)
		}

		// Conservation: RPC errors
		if snap.RPCErrors != totalErrors {
			rt.Fatalf("RPC errors: got %d, want %d", snap.RPCErrors, totalErrors)
		}
	})
}

func TestP2_MetricsAggregationConservation_ViaRunProfile(t *testing.T) {
	ctl := rh.NewController()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	summary, err := ctl.RunProfile(ctx, rh.ProfileSmall, workloads.DefaultMixedWorkloads())
	if err != nil {
		t.Fatalf("RunProfile: %v", err)
	}

	// Sum per-session RPC calls
	var totalRPC int
	var totalSnapshots int
	for _, r := range summary.Results {
		totalRPC += r.RPCCalls
		totalSnapshots += r.Snapshots
	}

	snap := summary.Metrics

	// Conservation: RPC count matches sum of results
	if snap.RPCCount != int64(totalRPC) {
		t.Fatalf("RPC count: got %d, want %d", snap.RPCCount, totalRPC)
	}

	// Conservation: Snapshot delta matches sum
	if snap.SnapshotDelta != int64(totalSnapshots) {
		t.Fatalf("snapshot delta: got %d, want %d", snap.SnapshotDelta, totalSnapshots)
	}

	// Conservation: Error count matches
	errCount := 0
	for _, r := range summary.Results {
		if r.Err != nil {
			errCount++
		}
	}
	if snap.RPCErrors != int64(errCount) {
		t.Fatalf("RPC errors: got %d, want %d", snap.RPCErrors, errCount)
	}
}

func TestP2_MetricsAggregation_ZeroResults(t *testing.T) {
	tc := rh.NewTelemetryCollector()
	snap := tc.Snapshot()

	if snap.RPCCount != 0 || snap.RPCErrors != 0 || snap.SnapshotDelta != 0 {
		t.Fatalf("empty collector should have zero counters: rpc=%d err=%d snap=%d",
			snap.RPCCount, snap.RPCErrors, snap.SnapshotDelta)
	}
	if snap.RPCLatencyP50Ms != 0 || snap.RPCLatencyP95Ms != 0 {
		t.Fatalf("empty collector should have zero latencies: p50=%.2f p95=%.2f",
			snap.RPCLatencyP50Ms, snap.RPCLatencyP95Ms)
	}
}

// =============================================================================
// P3: Baseline Comparison Determinism
// Same input metrics + same baseline => identical regression verdict.
// =============================================================================

func TestP3_BaselineComparisonDeterminism(t *testing.T) {
	current := map[string]float64{
		"rpc_latency_p95_ms":    45.123,
		"container_boot_p95_ms": 120.456,
		"errors":                3.0,
		"runs":                  50.0,
	}
	baseline := map[string]float64{
		"rpc_latency_p95_ms":    40.0,
		"container_boot_p95_ms": 100.0,
		"errors":                1.0,
		"runs":                  50.0,
	}

	tolerance := 5.0 // 5%

	// Run 100 times and verify identical results
	var referenceRegressions []rh.Regression
	for i := 0; i < 100; i++ {
		regressions := rh.CompareAgainstBaseline(current, baseline, tolerance)

		if i == 0 {
			referenceRegressions = regressions
			if len(regressions) == 0 {
				t.Fatal("expected at least one regression in synthetic test data")
			}
			continue
		}

		if len(regressions) != len(referenceRegressions) {
			t.Fatalf("iteration %d: regression count %d != reference %d",
				i, len(regressions), len(referenceRegressions))
		}

		// Build maps for order-independent comparison
		refMap := make(map[string]rh.Regression)
		for _, r := range referenceRegressions {
			refMap[r.Metric] = r
		}
		curMap := make(map[string]rh.Regression)
		for _, r := range regressions {
			curMap[r.Metric] = r
		}

		for metric, ref := range refMap {
			cur, ok := curMap[metric]
			if !ok {
				t.Fatalf("iteration %d: missing regression for %q", i, metric)
			}
			if cur.DeltaPct != ref.DeltaPct {
				t.Fatalf("iteration %d: %q delta %.6f != reference %.6f",
					i, metric, cur.DeltaPct, ref.DeltaPct)
			}
		}
	}
}

func TestP3_BaselineComparisonDeterminism_BoundaryValues(t *testing.T) {
	// Test at exact threshold boundaries — prone to floating-point nondeterminism
	baseline := map[string]float64{
		"metric_a": 100.0,
		"metric_b": 200.0,
	}
	tolerance := 10.0 // 10%

	// metric_a at exactly 10% over baseline = 110.0
	// delta = 100 * (110 - 100) / 100 = 10.0 — equal to tolerance, should NOT regress
	// metric_b at just above 10% over baseline = 220.01
	// delta = 100 * (220.01 - 200) / 200 = 10.005 — above tolerance, SHOULD regress
	current := map[string]float64{
		"metric_a": 110.0,
		"metric_b": 220.01,
	}

	for i := 0; i < 100; i++ {
		regressions := rh.CompareAgainstBaseline(current, baseline, tolerance)
		if len(regressions) != 1 {
			t.Fatalf("iteration %d: expected 1 regression, got %d", i, len(regressions))
		}
		if regressions[0].Metric != "metric_b" {
			t.Fatalf("iteration %d: expected metric_b regression, got %q", i, regressions[0].Metric)
		}
	}
}

// =============================================================================
// P4: Artifact Schema Stability
// Emitted artifacts conform to expected schema and are readable.
// =============================================================================

func TestP4_ArtifactSchemaStability(t *testing.T) {
	ctl := rh.NewController()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	summary, err := ctl.RunProfile(ctx, rh.ProfileSmall, workloads.DefaultMixedWorkloads())
	if err != nil {
		t.Fatalf("RunProfile: %v", err)
	}

	dir := t.TempDir()
	bundle, err := rh.WriteArtifacts(dir, summary)
	if err != nil {
		t.Fatalf("WriteArtifacts: %v", err)
	}

	// Verify all artifact files exist and are non-empty
	for _, path := range []string{bundle.JSONPath, bundle.CSVPath, bundle.MarkdownPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if info.Size() == 0 {
			t.Fatalf("artifact %s is empty", path)
		}
	}

	// Verify baseline roundtrip
	metrics := rh.MetricsMap(summary)
	baselinePath := fmt.Sprintf("%s/baseline.json", dir)
	if err := rh.SaveBaseline(baselinePath, metrics); err != nil {
		t.Fatalf("SaveBaseline: %v", err)
	}
	loaded, err := rh.LoadBaseline(baselinePath)
	if err != nil {
		t.Fatalf("LoadBaseline: %v", err)
	}
	for k, v := range metrics {
		if loaded[k] != v {
			t.Fatalf("baseline roundtrip mismatch for %q: %.6f != %.6f", k, loaded[k], v)
		}
	}
}

// =============================================================================
// P5: Threshold Rule Soundness
// Regression gates trigger exactly when configured threshold predicates evaluate true.
// =============================================================================

func TestP5_ThresholdRuleSoundness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		baseVal := rapid.Float64Range(1.0, 1000.0).Draw(rt, "baseVal")
		tolerance := rapid.Float64Range(1.0, 50.0).Draw(rt, "tolerance")
		deltaPct := rapid.Float64Range(-20.0, 80.0).Draw(rt, "deltaPct")

		baseline := map[string]float64{"metric": baseVal}
		currentVal := baseVal * (1 + deltaPct/100)
		current := map[string]float64{"metric": currentVal}

		regressions := rh.CompareAgainstBaseline(current, baseline, tolerance)

		// Recompute the delta the same way CompareAgainstBaseline does.
		// At the float64 boundary (|delta - tolerance| < 1e-9), FMA
		// instruction optimization can cause identical formulas to produce
		// different results at different call sites, so either answer is
		// acceptable — skip those cases.
		recomputedDelta := 100 * ((currentVal - baseVal) / baseVal)
		if math.Abs(recomputedDelta-tolerance) < 1e-9 {
			return
		}

		shouldRegress := recomputedDelta > tolerance
		didRegress := len(regressions) > 0

		if shouldRegress != didRegress {
			rt.Fatalf("threshold mismatch: deltaPct=%.4f recomputed=%.17g tolerance=%.4f shouldRegress=%v didRegress=%v",
				deltaPct, recomputedDelta, tolerance, shouldRegress, didRegress)
		}
	})
}
