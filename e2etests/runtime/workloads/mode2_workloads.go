package workloads

import (
	"context"
	"math/rand"
	"sync"
	"time"

	rh "h2-agent-runtime/e2etests/runtime/harness"
)

type Mode2ScenarioWorkload struct {
	NameStr  string
	Delay    time.Duration
	RPCCalls int
	rng      *rand.Rand
	mu       sync.Mutex
}

func NewMode2ScenarioWorkload(name string, delay time.Duration, rpcCalls int) *Mode2ScenarioWorkload {
	return &Mode2ScenarioWorkload{NameStr: name, Delay: delay, RPCCalls: rpcCalls}
}

func NewMode2ScenarioWorkloadWithSeed(name string, delay time.Duration, rpcCalls int, seed int64) *Mode2ScenarioWorkload {
	w := NewMode2ScenarioWorkload(name, delay, rpcCalls)
	w.rng = rand.New(rand.NewSource(seed))
	return w
}

func (w *Mode2ScenarioWorkload) Name() string                   { return w.NameStr }
func (w *Mode2ScenarioWorkload) Mode() string                   { return "mode2" }
func (w *Mode2ScenarioWorkload) Setup(context.Context) error    { return nil }
func (w *Mode2ScenarioWorkload) Teardown(context.Context) error { return nil }

func (w *Mode2ScenarioWorkload) Run(ctx context.Context, sessionID string) (*rh.WorkloadResult, error) {
	start := time.Now()
	jitter := time.Duration(w.nextIntn(10)) * time.Millisecond
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

func (w *Mode2ScenarioWorkload) nextIntn(n int) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.rng != nil {
		return w.rng.Intn(n)
	}
	return rand.Intn(n)
}
