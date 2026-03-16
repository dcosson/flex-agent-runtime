package tests

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	rh "flex-agent-runtime/tests/integration/runtime/harness"
	"flex-agent-runtime/tests/integration/runtime/workloads"
)

func TestLoadScaling_PSmallAndPMedium(t *testing.T) {
	ctl := rh.NewController()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	for _, profile := range []rh.ConcurrencyProfile{rh.ProfileSmall, rh.ProfileMedium} {
		summary, err := ctl.RunProfile(ctx, profile, workloads.DefaultMixedWorkloads())
		if err != nil {
			t.Fatalf("run profile %s: %v", profile.Name, err)
		}
		if len(summary.Results) != profile.Concurrency {
			t.Fatalf("profile %s results=%d want=%d", profile.Name, len(summary.Results), profile.Concurrency)
		}
		if summary.Mode2Runs == 0 || summary.Mode3Runs == 0 {
			t.Fatalf("profile %s missing mixed mode runs m2=%d m3=%d", profile.Name, summary.Mode2Runs, summary.Mode3Runs)
		}
		if _, err := rh.WriteArtifacts(filepath.Join(t.TempDir(), profile.Name), summary); err != nil {
			t.Fatalf("write artifacts for %s: %v", profile.Name, err)
		}
	}
}

func TestLoadScaling_BaselineComparisonAndGating(t *testing.T) {
	ctl := rh.NewController()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	summary, err := ctl.RunProfile(ctx, rh.ProfileSmall, workloads.DefaultMixedWorkloads())
	if err != nil {
		t.Fatalf("run profile: %v", err)
	}
	current := rh.MetricsMap(summary)

	baselinePath := filepath.Join(t.TempDir(), "baselines", "p-small.json")
	if err := rh.SaveBaseline(baselinePath, map[string]float64{
		"rpc_latency_p95_ms":    current["rpc_latency_p95_ms"] * 0.90,
		"container_boot_p95_ms": current["container_boot_p95_ms"] * 0.90,
	}); err != nil {
		t.Fatalf("save baseline: %v", err)
	}
	baseline, err := rh.LoadBaseline(baselinePath)
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	regressions := rh.CompareAgainstBaseline(current, baseline, 5)
	if len(regressions) == 0 {
		t.Fatal("expected baseline regressions for synthetic tighter baseline")
	}
}
