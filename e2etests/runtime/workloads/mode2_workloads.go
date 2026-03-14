package workloads

import (
	"context"
	"math/rand"
	"time"

	rh "h2-agent-runtime/e2etests/runtime/harness"
)

type Mode2ScenarioWorkload struct {
	NameStr  string
	Delay    time.Duration
	RPCCalls int
}

func NewMode2ScenarioWorkload(name string, delay time.Duration, rpcCalls int) *Mode2ScenarioWorkload {
	return &Mode2ScenarioWorkload{NameStr: name, Delay: delay, RPCCalls: rpcCalls}
}

func (w *Mode2ScenarioWorkload) Name() string                   { return w.NameStr }
func (w *Mode2ScenarioWorkload) Mode() string                   { return "mode2" }
func (w *Mode2ScenarioWorkload) Setup(context.Context) error    { return nil }
func (w *Mode2ScenarioWorkload) Teardown(context.Context) error { return nil }

func (w *Mode2ScenarioWorkload) Run(ctx context.Context, sessionID string) (*rh.WorkloadResult, error) {
	start := time.Now()
	jitter := time.Duration(rand.Intn(10)) * time.Millisecond
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(w.Delay + jitter):
	}
	return &rh.WorkloadResult{
		Workload:   w.Name(),
		Mode:       w.Mode(),
		SessionID:  sessionID,
		StartedAt:  start,
		FinishedAt: time.Now(),
		RPCCalls:   w.RPCCalls,
		Metadata: map[string]float64{
			"container_boot_ms": float64((w.Delay + jitter).Milliseconds()),
		},
	}, nil
}
