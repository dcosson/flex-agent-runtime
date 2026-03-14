package tests

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"

	rh "h2-agent-runtime/e2etests/runtime/harness"
	"h2-agent-runtime/e2etests/runtime/workloads"
)

// =============================================================================
// F1: Telemetry Loss Bursts
// Drop random telemetry batches. Verify graceful degradation and explicit
// data-quality flags.
// =============================================================================

func TestF1_TelemetryLossBursts_GracefulDegradation(t *testing.T) {
	// Simulate different loss rates and verify the harness doesn't crash
	// and produces valid (if approximate) metrics.
	for _, lossRate := range []float64{0.01, 0.10, 0.50} {
		t.Run(fmt.Sprintf("loss_%.0fpct", lossRate*100), func(t *testing.T) {
			fullCollector := rh.NewTelemetryCollector()
			lossyCollector := rh.NewTelemetryCollector()

			// Seeded RNG for deterministic but random-pattern loss
			rng := rand.New(rand.NewSource(int64(lossRate * 1000)))

			numSamples := 100
			t0 := time.Now()
			dropped := 0

			for i := 0; i < numSamples; i++ {
				latency := time.Duration(10+i) * time.Millisecond
				rpcCalls := 3 + (i % 5)
				goroutines := 100 + i
				rssMB := 1000.0 + float64(i)*0.5
				fdCount := 30
				errRate := 0.1

				// Full collector gets everything
				fullCollector.RecordRPCLatency(latency)
				fullCollector.RecordRPCCount(rpcCalls)
				fullCollector.RecordDriftSample(
					t0.Add(time.Duration(i)*time.Minute),
					goroutines, rssMB, fdCount, errRate,
				)

				// Lossy collector drops random batches (seeded for determinism)
				if rng.Float64() < lossRate {
					dropped++
					continue
				}
				lossyCollector.RecordRPCLatency(latency)
				lossyCollector.RecordRPCCount(rpcCalls)
				lossyCollector.RecordDriftSample(
					t0.Add(time.Duration(i)*time.Minute),
					goroutines, rssMB, fdCount, errRate,
				)
			}

			// Both snapshots must be valid (no panics, no NaN)
			fullSnap := fullCollector.Snapshot()
			lossySnap := lossyCollector.Snapshot()

			if math.IsNaN(fullSnap.RPCLatencyP95Ms) || math.IsNaN(lossySnap.RPCLatencyP95Ms) {
				t.Fatal("NaN in latency metrics")
			}

			// Lossy collector should have fewer RPC counts
			if lossySnap.RPCCount >= fullSnap.RPCCount && dropped > 0 {
				t.Fatalf("lossy collector should have fewer RPCs: lossy=%d full=%d dropped=%d",
					lossySnap.RPCCount, fullSnap.RPCCount, dropped)
			}

			// Data quality: explicit accounting of what was recorded
			actualLossRate := float64(dropped) / float64(numSamples)
			t.Logf("loss_rate=%.2f%% dropped=%d/%d lossy_rpc=%d full_rpc=%d",
				actualLossRate*100, dropped, numSamples,
				lossySnap.RPCCount, fullSnap.RPCCount)
		})
	}
}

func TestF1_TelemetryLossBursts_EmptyCollector(t *testing.T) {
	// Total loss: 100% dropped — collector must still produce valid snapshot
	tc := rh.NewTelemetryCollector()
	snap := tc.Snapshot()

	if snap == nil {
		t.Fatal("nil snapshot from empty collector")
	}
	if math.IsNaN(snap.RPCLatencyP50Ms) || math.IsNaN(snap.RPCLatencyP95Ms) {
		t.Fatal("NaN in empty snapshot latencies")
	}
	if snap.RPCCount != 0 || snap.RPCErrors != 0 {
		t.Fatalf("non-zero counters in empty snapshot: rpc=%d err=%d",
			snap.RPCCount, snap.RPCErrors)
	}
}

func TestF1_TelemetryLossBursts_PartialDrift(t *testing.T) {
	// Only one drift sample (need >= 2 for growth calculation)
	tc := rh.NewTelemetryCollector()
	tc.RecordDriftSample(time.Now(), 100, 1000, 30, 0.1)

	snap := tc.Snapshot()
	if snap.Drift.GoroutineGrowthPct != 0 {
		t.Fatalf("single sample should yield 0 growth, got %.2f%%", snap.Drift.GoroutineGrowthPct)
	}
	if snap.Drift.FDDelta != 0 {
		t.Fatalf("single sample should yield 0 FD delta, got %d", snap.Drift.FDDelta)
	}
}

// =============================================================================
// F2: Clock Skew Simulation
// Inject timestamp skew across generators/collectors. Verify timeline
// normalization and robust percentile computation.
// =============================================================================

func TestF2_ClockSkewSimulation(t *testing.T) {
	// Normal: evenly spaced timestamps over 2 hours
	normalTC := rh.NewTelemetryCollector()
	t0 := time.Now()
	normalTC.RecordDriftSample(t0, 100, 1000, 30, 0.10)
	normalTC.RecordDriftSample(t0.Add(1*time.Hour), 105, 1050, 31, 0.12)
	normalTC.RecordDriftSample(t0.Add(2*time.Hour), 110, 1100, 32, 0.14)
	normalSnap := normalTC.Snapshot()

	// Skewed: same values but timestamps compressed (simulating clock skew)
	skewedTC := rh.NewTelemetryCollector()
	skewedTC.RecordDriftSample(t0, 100, 1000, 30, 0.10)
	skewedTC.RecordDriftSample(t0.Add(30*time.Minute), 105, 1050, 31, 0.12) // compressed
	skewedTC.RecordDriftSample(t0.Add(1*time.Hour), 110, 1100, 32, 0.14)    // compressed
	skewedSnap := skewedTC.Snapshot()

	// Growth percentages should be identical (same values, different timing)
	if normalSnap.Drift.GoroutineGrowthPct != skewedSnap.Drift.GoroutineGrowthPct {
		t.Fatalf("goroutine growth should be timing-independent: normal=%.2f skewed=%.2f",
			normalSnap.Drift.GoroutineGrowthPct, skewedSnap.Drift.GoroutineGrowthPct)
	}
	if normalSnap.Drift.RSSGrowthPct != skewedSnap.Drift.RSSGrowthPct {
		t.Fatalf("RSS growth should be timing-independent: normal=%.2f skewed=%.2f",
			normalSnap.Drift.RSSGrowthPct, skewedSnap.Drift.RSSGrowthPct)
	}
	if normalSnap.Drift.FDDelta != skewedSnap.Drift.FDDelta {
		t.Fatalf("FD delta should be timing-independent: normal=%d skewed=%d",
			normalSnap.Drift.FDDelta, skewedSnap.Drift.FDDelta)
	}

	// Error slope should scale inversely with time window
	// normal: 2 hours, skewed: 1 hour — slope should be ~2x for skewed
	if normalSnap.Drift.RPCErrorSlopePctHr == 0 {
		t.Fatal("normal slope should be non-zero")
	}
	slopeRatio := skewedSnap.Drift.RPCErrorSlopePctHr / normalSnap.Drift.RPCErrorSlopePctHr
	if math.Abs(slopeRatio-2.0) > 0.1 {
		t.Fatalf("slope ratio should be ~2.0 (compressed time window): got %.2f (normal=%.4f skewed=%.4f)",
			slopeRatio, normalSnap.Drift.RPCErrorSlopePctHr, skewedSnap.Drift.RPCErrorSlopePctHr)
	}
}

func TestF2_ClockSkew_ZeroDuration(t *testing.T) {
	// Edge: all timestamps identical (zero duration window)
	tc := rh.NewTelemetryCollector()
	now := time.Now()
	tc.RecordDriftSample(now, 100, 1000, 30, 0.10)
	tc.RecordDriftSample(now, 110, 1100, 32, 0.15) // same timestamp

	snap := tc.Snapshot()

	// Slope should be 0 (not Inf or NaN) when duration is zero
	if math.IsNaN(snap.Drift.RPCErrorSlopePctHr) || math.IsInf(snap.Drift.RPCErrorSlopePctHr, 0) {
		t.Fatalf("zero-duration slope should be 0, got %v", snap.Drift.RPCErrorSlopePctHr)
	}
	if snap.Drift.RPCErrorSlopePctHr != 0 {
		t.Fatalf("zero-duration slope should be 0, got %.4f", snap.Drift.RPCErrorSlopePctHr)
	}
}

func TestF2_ClockSkew_LargeOffset(t *testing.T) {
	// Large clock offset: timestamps far apart
	tc := rh.NewTelemetryCollector()
	t0 := time.Now()
	tc.RecordDriftSample(t0, 100, 1000, 30, 0.10)
	tc.RecordDriftSample(t0.Add(1000*time.Hour), 110, 1100, 32, 0.14)

	snap := tc.Snapshot()

	// Slope should be very small (spread over 1000 hours)
	if snap.Drift.RPCErrorSlopePctHr > 0.001 {
		t.Fatalf("large time window should yield tiny slope: got %.6f", snap.Drift.RPCErrorSlopePctHr)
	}

	// Growth percentages should still be correct
	expectedGrowth := 10.0 // (110-100)/100 * 100
	if math.Abs(snap.Drift.GoroutineGrowthPct-expectedGrowth) > 0.01 {
		t.Fatalf("goroutine growth: got %.2f want %.2f", snap.Drift.GoroutineGrowthPct, expectedGrowth)
	}
}

// =============================================================================
// F3: Partial Host Outage
// Verify harness continues when a workload fails.
// =============================================================================

func TestF3_PartialWorkloadFailure(t *testing.T) {
	// Create workloads where one fails
	failingWorkload := &failingMode2Workload{}
	wl := []rh.Workload{
		workloads.NewMode2ScenarioWorkload("mode2/healthy", 5*time.Millisecond, 3),
		failingWorkload,
		workloads.NewMode3ScenarioWorkload("mode3/healthy", 5*time.Millisecond, 3, 1),
	}

	ctl := rh.NewController()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	profile := rh.ConcurrencyProfile{
		Name:              "f3-partial-failure",
		Concurrency:       6,
		Mode2Weight:       3,
		Mode3Weight:       3,
		TargetSessionTime: 2 * time.Second,
	}

	summary, err := ctl.RunProfile(ctx, profile, wl)
	if err != nil {
		t.Fatalf("RunProfile should not fail even with failing workloads: %v", err)
	}

	// Some errors expected
	if summary.Errors == 0 {
		t.Fatal("expected at least one error from failing workload")
	}

	// But we should still have results
	if len(summary.Results) == 0 {
		t.Fatal("should have some results despite failures")
	}
}

// =============================================================================
// F5: Baseline Drift Misconfiguration
// Inject malformed baseline files.
// =============================================================================

func TestF5_MalformedBaseline(t *testing.T) {
	// Load non-existent baseline
	_, err := rh.LoadBaseline("/nonexistent/path/baseline.json")
	if err == nil {
		t.Fatal("expected error loading non-existent baseline")
	}

	// Load malformed JSON
	dir := t.TempDir()
	malformedPath := fmt.Sprintf("%s/malformed.json", dir)
	if err := writeFile(malformedPath, []byte("{not valid json")); err != nil {
		t.Fatalf("write malformed: %v", err)
	}
	_, err = rh.LoadBaseline(malformedPath)
	if err == nil {
		t.Fatal("expected error loading malformed baseline")
	}

	// Empty baseline: compare should produce no regressions
	emptyBaseline := map[string]float64{}
	current := map[string]float64{"rpc_latency_p95_ms": 50.0}
	regressions := rh.CompareAgainstBaseline(current, emptyBaseline, 5.0)
	if len(regressions) != 0 {
		t.Fatalf("empty baseline should produce no regressions, got %d", len(regressions))
	}

	// Zero-value baseline: should not cause division by zero
	zeroBaseline := map[string]float64{"metric": 0.0}
	current = map[string]float64{"metric": 100.0}
	regressions = rh.CompareAgainstBaseline(current, zeroBaseline, 5.0)
	// b==0 is skipped in CompareAgainstBaseline, so no regression
	if len(regressions) != 0 {
		t.Fatalf("zero baseline should be skipped, got %d regressions", len(regressions))
	}
}

// --- Helpers ---

type failingMode2Workload struct{}

func (w *failingMode2Workload) Name() string                   { return "mode2/failing" }
func (w *failingMode2Workload) Mode() string                   { return "mode2" }
func (w *failingMode2Workload) Setup(context.Context) error    { return nil }
func (w *failingMode2Workload) Teardown(context.Context) error { return nil }
func (w *failingMode2Workload) Run(context.Context, string) (*rh.WorkloadResult, error) {
	return nil, fmt.Errorf("simulated workload failure")
}

func writeFile(path string, data []byte) error {
	return writeFileWithPerm(path, data, 0o644)
}
