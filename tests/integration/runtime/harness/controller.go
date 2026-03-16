package harness

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type Workload interface {
	Name() string
	Mode() string // mode2 | mode3
	Setup(ctx context.Context) error
	Run(ctx context.Context, sessionID string) (*WorkloadResult, error)
	Teardown(ctx context.Context) error
}

type WorkloadResult struct {
	Workload   string
	Mode       string
	SessionID  string
	StartedAt  time.Time
	FinishedAt time.Time
	RPCCalls   int
	Snapshots  int
	Metadata   map[string]float64
	Err        error
}

type RunSummary struct {
	Profile            ConcurrencyProfile
	StartedAt          time.Time
	FinishedAt         time.Time
	Results            []*WorkloadResult
	Mode2Runs          int
	Mode3Runs          int
	Errors             int
	Metrics            *TelemetrySnapshot
	ContainerBootP95Ms float64
	RPCLatencyP95Ms    float64
}

type Controller struct {
	telemetry *TelemetryCollector
	nextID    atomic.Uint64
}

func NewController() *Controller {
	return &Controller{telemetry: NewTelemetryCollector()}
}

func (c *Controller) Telemetry() *TelemetryCollector { return c.telemetry }

func (c *Controller) RunProfile(ctx context.Context, profile ConcurrencyProfile, workloads []Workload) (*RunSummary, error) {
	if len(workloads) == 0 {
		return nil, fmt.Errorf("no workloads configured")
	}
	mode2 := filterWorkloads(workloads, "mode2")
	mode3 := filterWorkloads(workloads, "mode3")
	if len(mode2) == 0 || len(mode3) == 0 {
		return nil, fmt.Errorf("both mode2 and mode3 workloads are required")
	}

	summary := &RunSummary{Profile: profile, StartedAt: time.Now()}
	if err := setupAll(ctx, workloads); err != nil {
		return nil, err
	}
	defer teardownAll(context.Background(), workloads)

	totalWeight := profile.Mode2Weight + profile.Mode3Weight
	mode2Target := profile.Concurrency * profile.Mode2Weight / totalWeight
	if mode2Target < 1 {
		mode2Target = 1
	}
	mode3Target := profile.Concurrency - mode2Target
	if mode3Target < 1 {
		mode3Target = 1
	}

	out := make(chan *WorkloadResult, profile.Concurrency)
	var wg sync.WaitGroup
	launchPool := func(items []Workload, n int) {
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				w := items[i%len(items)]
				sessionID := fmt.Sprintf("%s-%d", w.Mode(), c.nextID.Add(1))
				start := time.Now()
				res, err := w.Run(ctx, sessionID)
				if res == nil {
					res = &WorkloadResult{Workload: w.Name(), Mode: w.Mode(), SessionID: sessionID}
				}
				res.StartedAt = start
				res.FinishedAt = time.Now()
				res.Err = err
				out <- res
			}(i)
		}
	}

	launchPool(mode2, mode2Target)
	launchPool(mode3, mode3Target)

	go func() {
		wg.Wait()
		close(out)
	}()

	for res := range out {
		summary.Results = append(summary.Results, res)
		if res.Mode == "mode2" {
			summary.Mode2Runs++
		} else if res.Mode == "mode3" {
			summary.Mode3Runs++
		}
		if res.Err != nil {
			summary.Errors++
			c.telemetry.RecordRPCError(1)
		}
		lat := res.FinishedAt.Sub(res.StartedAt)
		c.telemetry.RecordRPCLatency(lat)
		if boot, ok := res.Metadata["container_boot_ms"]; ok {
			c.telemetry.RecordContainerBoot(time.Duration(boot * float64(time.Millisecond)))
		}
		if res.RPCCalls > 0 {
			c.telemetry.RecordRPCCount(res.RPCCalls)
		}
		if res.Snapshots > 0 {
			c.telemetry.RecordSnapshotDelta(int64(res.Snapshots))
		}
	}

	summary.FinishedAt = time.Now()
	snap := c.telemetry.Snapshot()
	summary.Metrics = snap
	summary.ContainerBootP95Ms = snap.ContainerBootP95Ms
	summary.RPCLatencyP95Ms = snap.RPCLatencyP95Ms
	return summary, nil
}

func setupAll(ctx context.Context, workloads []Workload) error {
	for _, w := range workloads {
		if err := w.Setup(ctx); err != nil {
			return fmt.Errorf("setup %s: %w", w.Name(), err)
		}
	}
	return nil
}

func teardownAll(ctx context.Context, workloads []Workload) {
	for _, w := range workloads {
		_ = w.Teardown(ctx)
	}
}

func filterWorkloads(workloads []Workload, mode string) []Workload {
	out := make([]Workload, 0, len(workloads))
	for _, w := range workloads {
		if w.Mode() == mode {
			out = append(out, w)
		}
	}
	return out
}
