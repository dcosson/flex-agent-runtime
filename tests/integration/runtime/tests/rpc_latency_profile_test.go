package tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	rh "github.com/anthropics/flex-agent-runtime/tests/integration/runtime/harness"
	"github.com/anthropics/flex-agent-runtime/tests/integration/runtime/workloads"
)

func TestRPCLatencyProfiling_UnderLoad(t *testing.T) {
	ctl := rh.NewController()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	summary, err := ctl.RunProfile(ctx, rh.ProfileMedium, workloads.DefaultMixedWorkloads())
	if err != nil {
		t.Fatalf("run profile medium: %v", err)
	}
	if summary.RPCLatencyP95Ms <= 0 {
		t.Fatalf("expected positive rpc p95 latency, got %.2f", summary.RPCLatencyP95Ms)
	}
	if summary.Metrics.RPCCount <= 0 {
		t.Fatalf("expected rpc calls > 0, got %d", summary.Metrics.RPCCount)
	}

	bundle, err := rh.WriteArtifacts(t.TempDir(), summary)
	if err != nil {
		t.Fatalf("write artifacts: %v", err)
	}
	for _, p := range []string{bundle.JSONPath, bundle.CSVPath, bundle.MarkdownPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("artifact missing %s: %v", p, err)
		}
	}
}

func TestHostConfig_LoadsFleetConfig(t *testing.T) {
	cfg, err := rh.LoadHostsConfig(filepath.Join("..", "config", "hosts.yaml"))
	if err != nil {
		t.Fatalf("load hosts config: %v", err)
	}
	if len(cfg.Environments["pr-fast"]) == 0 || len(cfg.Environments["weekly"]) == 0 {
		t.Fatalf("expected hosts in pr-fast and weekly envs: %+v", cfg.Environments)
	}
}
