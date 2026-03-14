package workloads

import (
	"time"

	rh "h2-agent-runtime/e2etests/runtime/harness"
)

func DefaultMixedWorkloads() []rh.Workload {
	return []rh.Workload{
		NewMode2ScenarioWorkload("mode2/driver-launch", 15*time.Millisecond, 4),
		NewMode2ScenarioWorkload("mode2/pause-resume", 12*time.Millisecond, 3),
		NewMode3ScenarioWorkload("mode3/happy-path", 10*time.Millisecond, 5, 2),
		NewMode3ScenarioWorkload("mode3/rollback", 14*time.Millisecond, 6, 3),
	}
}
