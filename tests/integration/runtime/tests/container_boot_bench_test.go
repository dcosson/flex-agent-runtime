package tests

import (
	"context"
	"testing"
	"time"

	rh "flex-agent-runtime/tests/integration/runtime/harness"
	"flex-agent-runtime/tests/integration/runtime/workloads"
)

func BenchmarkContainerBoot_ProfileMedium(b *testing.B) {
	ctl := rh.NewController()
	w := []rh.Workload{
		workloads.NewMode2ScenarioWorkload("mode2/container-boot", 12*time.Millisecond, 3),
		workloads.NewMode3ScenarioWorkload("mode3/container-boot", 10*time.Millisecond, 4, 1),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		summary, err := ctl.RunProfile(context.Background(), rh.ProfileMedium, w)
		if err != nil {
			b.Fatalf("run profile: %v", err)
		}
		b.ReportMetric(summary.ContainerBootP95Ms, "container_boot_p95_ms")
	}
}

func TestContainerBootBenchmark_RepeatableSignal(t *testing.T) {
	ctl := rh.NewController()
	w := []rh.Workload{
		workloads.NewMode2ScenarioWorkload("mode2/container-boot", 12*time.Millisecond, 3),
		workloads.NewMode3ScenarioWorkload("mode3/container-boot", 10*time.Millisecond, 4, 1),
	}
	first, err := ctl.RunProfile(context.Background(), rh.ProfileSmall, w)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	second, err := ctl.RunProfile(context.Background(), rh.ProfileSmall, w)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	delta := second.ContainerBootP95Ms - first.ContainerBootP95Ms
	if delta < 0 {
		delta = -delta
	}
	if delta > 20 {
		t.Fatalf("container boot signal too unstable: first=%.2f second=%.2f", first.ContainerBootP95Ms, second.ContainerBootP95Ms)
	}
}
