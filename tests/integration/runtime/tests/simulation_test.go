package tests

import (
	"context"
	"testing"
	"time"

	rh "github.com/dcosson/flex-agent-runtime/tests/integration/runtime/harness"
	"github.com/dcosson/flex-agent-runtime/tests/integration/runtime/workloads"
)

// =============================================================================
// S1: Synthetic Stable Workload Simulation
// Simulate ideal stable system; verify no false-positive regressions.
// =============================================================================

func TestS1_SyntheticStableWorkload_NoFalsePositives(t *testing.T) {
	// Run the same profile twice with seeded workloads (deterministic).
	// Baseline from first run should not trigger regressions in second run
	// within a reasonable tolerance.

	wl := func() []rh.Workload {
		return []rh.Workload{
			workloads.NewMode2ScenarioWorkloadWithSeed("mode2/stable", 10*time.Millisecond, 3, 42),
			workloads.NewMode3ScenarioWorkloadWithSeed("mode3/stable", 10*time.Millisecond, 3, 2, 43),
		}
	}

	profile := rh.ConcurrencyProfile{
		Name:              "s1-stable",
		Concurrency:       10,
		Mode2Weight:       5,
		Mode3Weight:       5,
		TargetSessionTime: 2 * time.Second,
	}

	// Run 1: establish baseline
	ctl1 := rh.NewController()
	ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel1()
	summary1, err := ctl1.RunProfile(ctx1, profile, wl())
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	baseline := rh.MetricsMap(summary1)

	// Run multiple subsequent runs — none should regress
	falsePositives := 0
	runs := 10
	for i := 0; i < runs; i++ {
		ctl := rh.NewController()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		summary, err := ctl.RunProfile(ctx, profile, wl())
		cancel()
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		current := rh.MetricsMap(summary)
		regressions := rh.CompareAgainstBaseline(current, baseline, 50.0) // generous 50% tolerance
		if len(regressions) > 0 {
			falsePositives++
			t.Logf("run %d: false positive regressions: %+v", i, regressions)
		}
	}

	// Should have zero or very few false positives
	if falsePositives > 1 {
		t.Fatalf("too many false positives: %d/%d", falsePositives, runs)
	}
}

// =============================================================================
// S2: Synthetic Degradation Simulation
// Inject controlled latency degradation; verify detection sensitivity.
// =============================================================================

func TestS2_SyntheticDegradation_DetectionSensitivity(t *testing.T) {
	// Establish baseline with fast workloads
	fastWL := []rh.Workload{
		workloads.NewMode2ScenarioWorkloadWithSeed("mode2/fast", 5*time.Millisecond, 3, 100),
		workloads.NewMode3ScenarioWorkloadWithSeed("mode3/fast", 5*time.Millisecond, 3, 1, 101),
	}

	profile := rh.ConcurrencyProfile{
		Name:              "s2-degrade",
		Concurrency:       10,
		Mode2Weight:       5,
		Mode3Weight:       5,
		TargetSessionTime: 2 * time.Second,
	}

	ctl1 := rh.NewController()
	ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel1()
	summary1, err := ctl1.RunProfile(ctx1, profile, fastWL)
	if err != nil {
		t.Fatalf("baseline run: %v", err)
	}
	baseline := rh.MetricsMap(summary1)

	// Run with slow workloads (3x latency) — should detect regression
	slowWL := []rh.Workload{
		workloads.NewMode2ScenarioWorkloadWithSeed("mode2/slow", 50*time.Millisecond, 3, 200),
		workloads.NewMode3ScenarioWorkloadWithSeed("mode3/slow", 50*time.Millisecond, 3, 1, 201),
	}

	ctl2 := rh.NewController()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	summary2, err := ctl2.RunProfile(ctx2, profile, slowWL)
	if err != nil {
		t.Fatalf("degraded run: %v", err)
	}
	current := rh.MetricsMap(summary2)

	regressions := rh.CompareAgainstBaseline(current, baseline, 10.0) // 10% tolerance
	if len(regressions) == 0 {
		t.Fatal("expected regression detection for 10x slower workloads")
	}

	// The rpc_latency_p95_ms metric should be the primary regression
	found := false
	for _, r := range regressions {
		if r.Metric == "rpc_latency_p95_ms" {
			found = true
			t.Logf("detected latency regression: baseline=%.2f current=%.2f delta=%.1f%%",
				r.Baseline, r.Current, r.DeltaPct)
		}
	}
	if !found {
		t.Fatalf("expected rpc_latency_p95_ms regression, got %+v", regressions)
	}
}

// =============================================================================
// S3: Soak Drift Threshold Simulation
// Verify that each individual drift dimension (goroutine growth, RSS, FDs,
// error slope) correctly triggers threshold failures when breached.
// (The planned multi-dimensional tradeoff scenario is not applicable to the
// current single-threshold comparison model in CompareAgainstBaseline.)
// =============================================================================

func TestS3_SoakDriftThresholdSimulation(t *testing.T) {
	soak := rh.SoakProfile{
		Name:     "s3-soak-sim",
		Duration: 1 * time.Second,
		Warmup:   100 * time.Millisecond,
		Drift: rh.DriftThresholds{
			GoroutineGrowthPct: 10.0,
			RSSGrowthPct:       15.0,
			FDDeltaAbs:         5,
			RPCErrorSlopePctHr: 0.05,
		},
	}

	// Healthy: all within bounds
	t.Run("healthy", func(t *testing.T) {
		tc := rh.NewTelemetryCollector()
		t0 := time.Now()
		tc.RecordDriftSample(t0, 100, 1000, 30, 0.10)
		tc.RecordDriftSample(t0.Add(1*time.Hour), 105, 1080, 33, 0.12)

		if err := rh.AssertSoakThresholds(soak, tc.Snapshot()); err != nil {
			t.Fatalf("healthy should pass: %v", err)
		}
	})

	// Goroutine leak
	t.Run("goroutine_leak", func(t *testing.T) {
		tc := rh.NewTelemetryCollector()
		t0 := time.Now()
		tc.RecordDriftSample(t0, 100, 1000, 30, 0.10)
		tc.RecordDriftSample(t0.Add(1*time.Hour), 115, 1050, 31, 0.11) // 15% growth

		err := rh.AssertSoakThresholds(soak, tc.Snapshot())
		if err == nil {
			t.Fatal("goroutine leak should fail threshold")
		}
	})

	// RSS growth
	t.Run("rss_growth", func(t *testing.T) {
		tc := rh.NewTelemetryCollector()
		t0 := time.Now()
		tc.RecordDriftSample(t0, 100, 1000, 30, 0.10)
		tc.RecordDriftSample(t0.Add(1*time.Hour), 105, 1200, 31, 0.11) // 20% RSS growth

		err := rh.AssertSoakThresholds(soak, tc.Snapshot())
		if err == nil {
			t.Fatal("RSS growth should fail threshold")
		}
	})

	// FD leak
	t.Run("fd_leak", func(t *testing.T) {
		tc := rh.NewTelemetryCollector()
		t0 := time.Now()
		tc.RecordDriftSample(t0, 100, 1000, 30, 0.10)
		tc.RecordDriftSample(t0.Add(1*time.Hour), 102, 1050, 40, 0.11) // +10 FDs

		err := rh.AssertSoakThresholds(soak, tc.Snapshot())
		if err == nil {
			t.Fatal("FD leak should fail threshold")
		}
	})

	// Error rate slope
	t.Run("error_slope", func(t *testing.T) {
		tc := rh.NewTelemetryCollector()
		t0 := time.Now()
		tc.RecordDriftSample(t0, 100, 1000, 30, 0.10)
		tc.RecordDriftSample(t0.Add(1*time.Hour), 102, 1050, 31, 0.20) // 0.10/hr slope

		err := rh.AssertSoakThresholds(soak, tc.Snapshot())
		if err == nil {
			t.Fatal("error slope should fail threshold")
		}
	})

	// Nil snapshot
	t.Run("nil_snapshot", func(t *testing.T) {
		err := rh.AssertSoakThresholds(soak, nil)
		if err == nil {
			t.Fatal("nil snapshot should fail")
		}
	})
}

func TestS3_SoakThreshold_NegativeDrift(t *testing.T) {
	// Negative FD delta (FDs being closed) should also be caught if abs > threshold
	soak := rh.SoakProfile{
		Name: "s3-negative-drift",
		Drift: rh.DriftThresholds{
			GoroutineGrowthPct: 50.0,
			RSSGrowthPct:       50.0,
			FDDeltaAbs:         3,
			RPCErrorSlopePctHr: 1.0,
		},
	}

	tc := rh.NewTelemetryCollector()
	t0 := time.Now()
	tc.RecordDriftSample(t0, 100, 1000, 40, 0.10)
	tc.RecordDriftSample(t0.Add(1*time.Hour), 100, 1000, 30, 0.10) // -10 FDs

	err := rh.AssertSoakThresholds(soak, tc.Snapshot())
	if err == nil {
		t.Fatal("large negative FD delta should fail threshold")
	}

	// But small negative should pass
	tc2 := rh.NewTelemetryCollector()
	tc2.RecordDriftSample(t0, 100, 1000, 32, 0.10)
	tc2.RecordDriftSample(t0.Add(1*time.Hour), 100, 1000, 30, 0.10) // -2 FDs

	if err := rh.AssertSoakThresholds(soak, tc2.Snapshot()); err != nil {
		t.Fatalf("small negative FD delta should pass: %v", err)
	}
}
