package tests

import (
	"context"
	"testing"
	"time"

	rh "flex-agent-runtime/tests/integration/runtime/harness"
	"flex-agent-runtime/tests/integration/runtime/workloads"
)

func TestSnapshotGrowth_AutomatedAnalysis(t *testing.T) {
	ctl := rh.NewController()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	ws := []rh.Workload{
		workloads.NewMode3ScenarioWorkload("mode3/snapshot-heavy-a", 8*time.Millisecond, 5, 3),
		workloads.NewMode3ScenarioWorkload("mode3/snapshot-heavy-b", 10*time.Millisecond, 6, 4),
		workloads.NewMode2ScenarioWorkload("mode2/light", 9*time.Millisecond, 3),
	}
	summary, err := ctl.RunProfile(ctx, rh.ProfileSmall, ws)
	if err != nil {
		t.Fatalf("run profile: %v", err)
	}
	if summary.Metrics.SnapshotDelta <= 0 {
		t.Fatalf("expected positive snapshot growth, got %d", summary.Metrics.SnapshotDelta)
	}
	if summary.Metrics.RPCCount == 0 {
		t.Fatal("expected rpc metrics to be captured")
	}
}
