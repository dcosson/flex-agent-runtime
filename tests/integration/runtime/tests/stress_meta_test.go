package tests

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	rh "flex-agent-runtime/tests/integration/runtime/harness"
	"flex-agent-runtime/tests/integration/runtime/workloads"
)

// =============================================================================
// ST1: Harness Self-Soak
// Continuous profiling cycles to validate harness stability itself.
// Shortened for CI (30s default, 24h for weekly tier).
// =============================================================================

func TestST1_HarnessSelfSoak(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping soak test in short mode")
	}

	soakDuration := 30 * time.Second
	tickInterval := 2 * time.Second

	if tier := os.Getenv("RUNTIME_HARNESS_TIER"); tier == "weekly" {
		soakDuration = 24 * time.Hour
		tickInterval = 30 * time.Second
		t.Logf("RUNTIME_HARNESS_TIER=weekly: running full 24h soak")
	}

	profile := rh.ConcurrencyProfile{
		Name:              "st1-soak",
		Concurrency:       5,
		Mode2Weight:       3,
		Mode3Weight:       2,
		TargetSessionTime: 2 * time.Second,
	}

	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	goroutinesBefore := runtime.NumGoroutine()

	deadline := time.After(soakDuration)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	cycles := 0
	var totalErrors int

soak:
	for {
		select {
		case <-deadline:
			break soak
		case <-ticker.C:
			cycles++
			ctl := rh.NewController()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			summary, err := ctl.RunProfile(ctx, profile, workloads.DefaultMixedWorkloads())
			cancel()
			if err != nil {
				t.Fatalf("cycle %d: RunProfile failed: %v", cycles, err)
			}
			totalErrors += summary.Errors
		}
	}

	if cycles == 0 {
		t.Fatal("no soak cycles completed")
	}

	// Check for goroutine leaks
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	goroutinesAfter := runtime.NumGoroutine()
	goroutineGrowth := goroutinesAfter - goroutinesBefore
	t.Logf("soak: %d cycles, %d total errors, goroutine growth: %d (%d → %d)",
		cycles, totalErrors, goroutineGrowth, goroutinesBefore, goroutinesAfter)

	// Goroutine growth should be bounded (allow some for GC, etc.)
	if goroutineGrowth > 20 {
		t.Fatalf("excessive goroutine growth during soak: %d", goroutineGrowth)
	}

	// Check heap growth (guard uint64 underflow — GC can reclaim between reads)
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	var heapGrowthMB float64
	if memAfter.HeapInuse > memBefore.HeapInuse {
		heapGrowthMB = float64(memAfter.HeapInuse-memBefore.HeapInuse) / (1024 * 1024)
	}
	t.Logf("heap growth: %.2f MB", heapGrowthMB)

	if heapGrowthMB > 100 {
		t.Fatalf("excessive heap growth during soak: %.2f MB", heapGrowthMB)
	}
}

// =============================================================================
// ST2: High-Cardinality Telemetry Stress
// Simulate high label cardinality and ensure bounded resource usage.
// =============================================================================

func TestST2_HighCardinalityTelemetryStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	tc := rh.NewTelemetryCollector()

	// Record a very large number of distinct metrics
	numEvents := 100_000
	for i := 0; i < numEvents; i++ {
		tc.RecordRPCLatency(time.Duration(i%1000) * time.Microsecond)
		if i%100 == 0 {
			tc.RecordRPCCount(1)
		}
		if i%500 == 0 {
			tc.RecordDriftSample(
				time.Now().Add(time.Duration(i)*time.Millisecond),
				100+i%50, 1000.0+float64(i%100), 30+i%10, 0.1+float64(i%20)*0.001,
			)
		}
	}

	// Snapshot should complete without excessive memory or time
	start := time.Now()
	snap := tc.Snapshot()
	elapsed := time.Since(start)

	if snap == nil {
		t.Fatal("nil snapshot after high cardinality ingest")
	}

	t.Logf("high cardinality: %d events, snapshot in %v, rpc_count=%d",
		numEvents, elapsed, snap.RPCCount)

	// Snapshot should complete in reasonable time
	if elapsed > 5*time.Second {
		t.Fatalf("snapshot took too long: %v", elapsed)
	}
}

// =============================================================================
// ST3: Concurrent Run Scheduler Stress
// Queue overlapping profiles/runs and verify isolation.
// =============================================================================

func TestST3_ConcurrentRunSchedulerStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrent scheduler stress test in short mode")
	}

	numConcurrent := 5
	var wg sync.WaitGroup
	errors := make(chan error, numConcurrent)
	var totalRuns atomic.Int64

	for i := 0; i < numConcurrent; i++ {
		wg.Add(1)
		go func(runID int) {
			defer wg.Done()

			profile := rh.ConcurrencyProfile{
				Name:              fmt.Sprintf("st3-run-%d", runID),
				Concurrency:       5,
				Mode2Weight:       3,
				Mode3Weight:       2,
				TargetSessionTime: 2 * time.Second,
			}

			ctl := rh.NewController()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			summary, err := ctl.RunProfile(ctx, profile, workloads.DefaultMixedWorkloads())
			if err != nil {
				errors <- fmt.Errorf("run %d: %v", runID, err)
				return
			}

			// Verify isolation: each controller should have its own metrics
			if len(summary.Results) != 5 {
				errors <- fmt.Errorf("run %d: expected 5 results, got %d", runID, len(summary.Results))
				return
			}

			totalRuns.Add(int64(len(summary.Results)))
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}

	// Total runs across all controllers should equal numConcurrent * concurrency
	expected := int64(numConcurrent * 5)
	if totalRuns.Load() != expected {
		t.Fatalf("total runs %d != expected %d", totalRuns.Load(), expected)
	}
}
