package tests

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	rh "flex-agent-runtime/tests/integration/runtime/harness"
	"flex-agent-runtime/tests/integration/runtime/workloads"
)

// =============================================================================
// B1: Harness Overhead
// Target: harness CPU overhead <= 5% of runtime-under-test CPU.
// Measured as ratio of harness bookkeeping time to total workload time.
// =============================================================================

func BenchmarkB1_HarnessOverhead(b *testing.B) {
	b.ReportAllocs()

	profile := rh.ConcurrencyProfile{
		Name:              "b1-overhead",
		Concurrency:       20,
		Mode2Weight:       4,
		Mode3Weight:       6,
		TargetSessionTime: 2 * time.Second,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctl := rh.NewController()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := ctl.RunProfile(ctx, profile, workloads.DefaultMixedWorkloads())
		cancel()
		if err != nil {
			b.Fatalf("RunProfile: %v", err)
		}
	}
}

// =============================================================================
// B2: Telemetry Ingest Throughput
// Target: sustain >= 100k metric events/minute without backlog growth.
// =============================================================================

func BenchmarkB2_TelemetryIngestThroughput(b *testing.B) {
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tc := rh.NewTelemetryCollector()

		// Ingest 100k events
		for j := 0; j < 100_000; j++ {
			tc.RecordRPCLatency(time.Duration(j%100) * time.Millisecond)
			if j%10 == 0 {
				tc.RecordRPCCount(3)
			}
			if j%50 == 0 {
				tc.RecordRPCError(1)
			}
			if j%100 == 0 {
				tc.RecordSnapshotDelta(1)
			}
		}

		_ = tc.Snapshot()
	}
}

// =============================================================================
// B3: Report Generation Latency
// Target: end-of-run report generation p95 < 30s for nightly profile size.
// =============================================================================

func BenchmarkB3_ReportGenerationLatency(b *testing.B) {
	b.ReportAllocs()

	// Build a realistic summary
	ctl := rh.NewController()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	summary, err := ctl.RunProfile(ctx, rh.ProfileSmall, workloads.DefaultMixedWorkloads())
	if err != nil {
		b.Fatalf("RunProfile: %v", err)
	}

	var latencies []time.Duration
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dir := b.TempDir()
		start := time.Now()
		_, err := rh.WriteArtifacts(dir, summary)
		elapsed := time.Since(start)
		if err != nil {
			b.Fatalf("WriteArtifacts: %v", err)
		}
		latencies = append(latencies, elapsed)
	}

	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		p95idx := int(float64(len(latencies)) * 0.95)
		if p95idx >= len(latencies) {
			p95idx = len(latencies) - 1
		}
		p95 := latencies[p95idx]
		b.ReportMetric(float64(p95.Milliseconds()), "p95_ms")

		if p95 > 30*time.Second {
			b.Fatalf("p95 report generation %v exceeds 30s target", p95)
		}
	}
}

// =============================================================================
// B4: Baseline Compare Latency
// Target: regression evaluation p95 < 5s per run.
// =============================================================================

func BenchmarkB4_BaselineCompareLatency(b *testing.B) {
	b.ReportAllocs()

	// Build realistic metrics maps
	baseline := map[string]float64{
		"rpc_latency_p95_ms":    50.0,
		"container_boot_p95_ms": 120.0,
		"errors":                2.0,
		"runs":                  100.0,
	}
	// Add more metrics to simulate realistic cardinality
	for i := 0; i < 100; i++ {
		baseline[fmt.Sprintf("custom_metric_%d", i)] = float64(i) * 1.5
	}

	current := make(map[string]float64)
	for k, v := range baseline {
		current[k] = v * 1.05 // 5% higher
	}

	var latencies []time.Duration

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		_ = rh.CompareAgainstBaseline(current, baseline, 10.0)
		latencies = append(latencies, time.Since(start))
	}

	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		p95idx := int(float64(len(latencies)) * 0.95)
		if p95idx >= len(latencies) {
			p95idx = len(latencies) - 1
		}
		p95 := latencies[p95idx]
		b.ReportMetric(float64(p95.Microseconds()), "p95_us")

		if p95 > 5*time.Second {
			b.Fatalf("p95 baseline compare %v exceeds 5s target", p95)
		}
	}
}
