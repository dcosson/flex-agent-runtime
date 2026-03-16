package workloads

import (
	"context"
	"math/rand"
	"sync"
	"time"

	rh "flex-agent-runtime/tests/integration/runtime/harness"
)

type Mode3ScenarioWorkload struct {
	NameStr   string
	Delay     time.Duration
	RPCCalls  int
	Snapshots int
	rng       *rand.Rand
	mu        sync.Mutex
}

func NewMode3ScenarioWorkload(name string, delay time.Duration, rpcCalls, snapshots int) *Mode3ScenarioWorkload {
	return &Mode3ScenarioWorkload{NameStr: name, Delay: delay, RPCCalls: rpcCalls, Snapshots: snapshots}
}

func NewMode3ScenarioWorkloadWithSeed(name string, delay time.Duration, rpcCalls, snapshots int, seed int64) *Mode3ScenarioWorkload {
	w := NewMode3ScenarioWorkload(name, delay, rpcCalls, snapshots)
	w.rng = rand.New(rand.NewSource(seed))
	return w
}

func (w *Mode3ScenarioWorkload) Name() string                   { return w.NameStr }
func (w *Mode3ScenarioWorkload) Mode() string                   { return "mode3" }
func (w *Mode3ScenarioWorkload) Setup(context.Context) error    { return nil }
func (w *Mode3ScenarioWorkload) Teardown(context.Context) error { return nil }

func (w *Mode3ScenarioWorkload) Run(ctx context.Context, sessionID string) (*rh.WorkloadResult, error) {
	start := time.Now()
	jitter := time.Duration(w.nextIntn(12)) * time.Millisecond
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

func (w *Mode3ScenarioWorkload) nextIntn(n int) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.rng != nil {
		return w.rng.Intn(n)
	}
	return rand.Intn(n)
}
