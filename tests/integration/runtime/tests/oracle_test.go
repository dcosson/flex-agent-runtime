package tests

import (
	"math"
	"testing"
	"time"

	rh "github.com/anthropics/flex-agent-runtime/tests/integration/runtime/harness"
)

// =============================================================================
// O1: Independent Statistics Oracle
// Cross-check percentile/summary stats against independent implementation.
// =============================================================================

func TestO1_IndependentStatisticsOracle(t *testing.T) {
	// Build a known dataset and verify percentile calculations match
	// an independent reference implementation.
	tc := rh.NewTelemetryCollector()

	// Record 100 known latencies: 1ms, 2ms, ..., 100ms
	latencies := make([]time.Duration, 100)
	for i := 0; i < 100; i++ {
		latencies[i] = time.Duration(i+1) * time.Millisecond
		tc.RecordRPCLatency(latencies[i])
	}

	snap := tc.Snapshot()

	// Independent P50 calculation: sorted[floor(0.50 * 99)] = sorted[49] = 50ms
	expectedP50 := 50.0
	if math.Abs(snap.RPCLatencyP50Ms-expectedP50) > 0.01 {
		t.Fatalf("P50: got %.2f, independent oracle says %.2f", snap.RPCLatencyP50Ms, expectedP50)
	}

	// Independent P95 calculation: sorted[floor(0.95 * 99)] = sorted[94] = 95ms
	expectedP95 := 95.0
	if math.Abs(snap.RPCLatencyP95Ms-expectedP95) > 0.01 {
		t.Fatalf("P95: got %.2f, independent oracle says %.2f", snap.RPCLatencyP95Ms, expectedP95)
	}
}

func TestO1_IndependentStatisticsOracle_ContainerBoot(t *testing.T) {
	tc := rh.NewTelemetryCollector()

	// Record known boot times
	bootTimes := []time.Duration{
		10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond,
		40 * time.Millisecond, 50 * time.Millisecond, 60 * time.Millisecond,
		70 * time.Millisecond, 80 * time.Millisecond, 90 * time.Millisecond,
		100 * time.Millisecond,
	}
	for _, bt := range bootTimes {
		tc.RecordContainerBoot(bt)
	}

	snap := tc.Snapshot()

	// P95 of 10 values: sorted[floor(0.95 * 9)] = sorted[8] = 90ms
	expectedP95 := 90.0
	if math.Abs(snap.ContainerBootP95Ms-expectedP95) > 0.01 {
		t.Fatalf("boot P95: got %.2f, oracle says %.2f", snap.ContainerBootP95Ms, expectedP95)
	}
}

func TestO1_IndependentStatisticsOracle_DriftGrowth(t *testing.T) {
	tc := rh.NewTelemetryCollector()
	t0 := time.Now()

	// Known growth: 100 → 150 goroutines = 50% growth
	tc.RecordDriftSample(t0, 100, 500.0, 30, 0.10)
	tc.RecordDriftSample(t0.Add(1*time.Hour), 120, 600.0, 32, 0.12)
	tc.RecordDriftSample(t0.Add(2*time.Hour), 150, 750.0, 35, 0.14)

	snap := tc.Snapshot()

	// Independent: growthPct = 100 * (last - first) / first
	expectedGoroutineGrowth := 100.0 * (150.0 - 100.0) / 100.0 // 50%
	if math.Abs(snap.Drift.GoroutineGrowthPct-expectedGoroutineGrowth) > 0.01 {
		t.Fatalf("goroutine growth: got %.2f, oracle says %.2f",
			snap.Drift.GoroutineGrowthPct, expectedGoroutineGrowth)
	}

	expectedRSSGrowth := 100.0 * (750.0 - 500.0) / 500.0 // 50%
	if math.Abs(snap.Drift.RSSGrowthPct-expectedRSSGrowth) > 0.01 {
		t.Fatalf("RSS growth: got %.2f, oracle says %.2f",
			snap.Drift.RSSGrowthPct, expectedRSSGrowth)
	}

	expectedFDDelta := 35 - 30 // 5
	if snap.Drift.FDDelta != expectedFDDelta {
		t.Fatalf("FD delta: got %d, oracle says %d", snap.Drift.FDDelta, expectedFDDelta)
	}

	// Error slope: (0.14 - 0.10) / 2 hours = 0.02/hr
	expectedSlope := (0.14 - 0.10) / 2.0
	if math.Abs(snap.Drift.RPCErrorSlopePctHr-expectedSlope) > 0.001 {
		t.Fatalf("error slope: got %.4f, oracle says %.4f",
			snap.Drift.RPCErrorSlopePctHr, expectedSlope)
	}
}

// =============================================================================
// O2: Historical Replay Oracle
// Replay known metrics and verify expected regression/non-regression outcomes.
// =============================================================================

func TestO2_HistoricalReplayOracle(t *testing.T) {
	// "Historical" baseline representing a stable system
	baseline := map[string]float64{
		"rpc_latency_p95_ms":    50.0,
		"container_boot_p95_ms": 120.0,
		"errors":                2.0,
		"runs":                  100.0,
	}

	testCases := []struct {
		name       string
		current    map[string]float64
		tolerance  float64
		wantRegr   int
		wantMetric string
	}{
		{
			name: "no_regression",
			current: map[string]float64{
				"rpc_latency_p95_ms":    52.0, // +4% — within 10% tolerance
				"container_boot_p95_ms": 125.0,
				"errors":                2.0,
				"runs":                  100.0,
			},
			tolerance: 10.0,
			wantRegr:  0,
		},
		{
			name: "latency_regression",
			current: map[string]float64{
				"rpc_latency_p95_ms":    60.0, // +20% — exceeds 10% tolerance
				"container_boot_p95_ms": 125.0,
				"errors":                2.0,
				"runs":                  100.0,
			},
			tolerance:  10.0,
			wantRegr:   1,
			wantMetric: "rpc_latency_p95_ms",
		},
		{
			name: "multiple_regressions",
			current: map[string]float64{
				"rpc_latency_p95_ms":    70.0,  // +40%
				"container_boot_p95_ms": 180.0, // +50%
				"errors":                5.0,   // +150%
				"runs":                  100.0,
			},
			tolerance: 10.0,
			wantRegr:  3,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			regressions := rh.CompareAgainstBaseline(tc.current, baseline, tc.tolerance)
			if len(regressions) != tc.wantRegr {
				t.Fatalf("expected %d regressions, got %d: %+v",
					tc.wantRegr, len(regressions), regressions)
			}
			if tc.wantMetric != "" && len(regressions) > 0 {
				found := false
				for _, r := range regressions {
					if r.Metric == tc.wantMetric {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("expected regression on %q, got %+v", tc.wantMetric, regressions)
				}
			}
		})
	}
}

// =============================================================================
// O3: Dual-Path Telemetry Oracle
// Compare metrics collected via different recording paths.
// =============================================================================

func TestO3_DualPathTelemetryOracle(t *testing.T) {
	// Path A: Record individual samples
	collectorA := rh.NewTelemetryCollector()
	latencies := []time.Duration{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	for _, l := range latencies {
		collectorA.RecordRPCLatency(l * time.Millisecond)
	}
	for i := 0; i < 5; i++ {
		collectorA.RecordRPCCount(2) // total = 10
	}
	collectorA.RecordRPCError(1)
	collectorA.RecordRPCError(1)
	collectorA.RecordSnapshotDelta(3)
	collectorA.RecordSnapshotDelta(7)

	// Path B: Record the same data in a different order/batching
	collectorB := rh.NewTelemetryCollector()
	// Reverse order latencies
	for i := len(latencies) - 1; i >= 0; i-- {
		collectorB.RecordRPCLatency(latencies[i] * time.Millisecond)
	}
	collectorB.RecordRPCCount(10) // single batch
	collectorB.RecordRPCError(2)  // single batch
	collectorB.RecordSnapshotDelta(10)

	snapA := collectorA.Snapshot()
	snapB := collectorB.Snapshot()

	// Counters must be identical
	if snapA.RPCCount != snapB.RPCCount {
		t.Fatalf("RPCCount mismatch: A=%d B=%d", snapA.RPCCount, snapB.RPCCount)
	}
	if snapA.RPCErrors != snapB.RPCErrors {
		t.Fatalf("RPCErrors mismatch: A=%d B=%d", snapA.RPCErrors, snapB.RPCErrors)
	}
	if snapA.SnapshotDelta != snapB.SnapshotDelta {
		t.Fatalf("SnapshotDelta mismatch: A=%d B=%d", snapA.SnapshotDelta, snapB.SnapshotDelta)
	}

	// Percentiles must be identical (same values, order shouldn't matter)
	if snapA.RPCLatencyP50Ms != snapB.RPCLatencyP50Ms {
		t.Fatalf("P50 mismatch: A=%.2f B=%.2f", snapA.RPCLatencyP50Ms, snapB.RPCLatencyP50Ms)
	}
	if snapA.RPCLatencyP95Ms != snapB.RPCLatencyP95Ms {
		t.Fatalf("P95 mismatch: A=%.2f B=%.2f", snapA.RPCLatencyP95Ms, snapB.RPCLatencyP95Ms)
	}
}
