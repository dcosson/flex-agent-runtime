package workloads

import (
	"time"

	rh "flex-agent-runtime/tests/integration/runtime/harness"
)

func DefaultMixedWorkloads() []rh.Workload {
	return []rh.Workload{
		NewMode2ScenarioWorkloadWithSeed("mode2/driver-launch", 15*time.Millisecond, 4, 101),
		NewMode2ScenarioWorkloadWithSeed("mode2/pause-resume", 12*time.Millisecond, 3, 102),
		NewMode3ScenarioWorkloadWithSeed("mode3/happy-path", 10*time.Millisecond, 5, 2, 201),
		NewMode3ScenarioWorkloadWithSeed("mode3/rollback", 14*time.Millisecond, 6, 3, 202),
	}
}
