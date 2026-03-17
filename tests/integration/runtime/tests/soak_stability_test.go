package tests

import (
	"testing"
	"time"

	rh "github.com/anthropics/flex-agent-runtime/tests/integration/runtime/harness"
)

func TestSoakStability_DetectsDriftRegressions(t *testing.T) {
	soak := rh.DefaultSoak12h()
	soak.Duration = 2 * time.Second
	soak.Warmup = 200 * time.Millisecond

	telemetry := rh.NewTelemetryCollector()
	// Simulate a healthy progression after warmup.
	t0 := time.Now()
	telemetry.RecordDriftSample(t0, 100, 1000, 30, 0.10)
	telemetry.RecordDriftSample(t0.Add(1*time.Hour), 102, 1035, 31, 0.105)
	telemetry.RecordDriftSample(t0.Add(2*time.Hour), 103, 1050, 31, 0.108)
	err := rh.AssertSoakThresholds(soak, telemetry.Snapshot())
	if err != nil {
		t.Fatalf("healthy soak should pass: %v", err)
	}

	// Simulate breach in RPC error-rate slope.
	telemetry = rh.NewTelemetryCollector()
	telemetry.RecordDriftSample(t0, 100, 1000, 30, 0.10)
	telemetry.RecordDriftSample(t0.Add(1*time.Hour), 103, 1080, 31, 0.13)
	telemetry.RecordDriftSample(t0.Add(2*time.Hour), 104, 1090, 31, 0.16)
	if err := rh.AssertSoakThresholds(soak, telemetry.Snapshot()); err == nil {
		t.Fatal("expected soak threshold failure for rpc error slope")
	}
}
