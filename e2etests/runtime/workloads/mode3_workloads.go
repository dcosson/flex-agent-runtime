package workloads

import (
	"context"
	"math/rand"
	"time"

	rh "h2-agent-runtime/e2etests/runtime/harness"
)

type Mode3ScenarioWorkload struct {
	NameStr   string
	Delay     time.Duration
	RPCCalls  int
	Snapshots int
}

func NewMode3ScenarioWorkload(name string, delay time.Duration, rpcCalls, snapshots int) *Mode3ScenarioWorkload {
	return &Mode3ScenarioWorkload{NameStr: name, Delay: delay, RPCCalls: rpcCalls, Snapshots: snapshots}
}

func (w *Mode3ScenarioWorkload) Name() string                   { return w.NameStr }
func (w *Mode3ScenarioWorkload) Mode() string                   { return "mode3" }
func (w *Mode3ScenarioWorkload) Setup(context.Context) error    { return nil }
func (w *Mode3ScenarioWorkload) Teardown(context.Context) error { return nil }

func (w *Mode3ScenarioWorkload) Run(ctx context.Context, sessionID string) (*rh.WorkloadResult, error) {
	start := time.Now()
	jitter := time.Duration(rand.Intn(12)) * time.Millisecond
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
		Snapshots:  w.Snapshots,
		Metadata: map[string]float64{
			"container_boot_ms": float64((w.Delay + jitter).Milliseconds()) * 0.6,
		},
	}, nil
}
