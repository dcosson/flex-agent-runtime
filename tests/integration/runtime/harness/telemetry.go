package harness

import (
	"math"
	"sort"
	"sync"
	"time"
)

type TelemetryCollector struct {
	mu            sync.Mutex
	rpcLatencies  []time.Duration
	containerBoot []time.Duration
	rpcCount      int64
	rpcErrors     int64
	snapshotDelta int64
	goroutines    []int
	rssMB         []float64
	fdCount       []int
	errorRateHist []float64
	errorRateAt   []time.Time
}

type TelemetrySnapshot struct {
	RPCLatencyP50Ms    float64
	RPCLatencyP95Ms    float64
	ContainerBootP95Ms float64
	RPCCount           int64
	RPCErrors          int64
	SnapshotDelta      int64
	Drift              DriftReport
}

type DriftReport struct {
	GoroutineGrowthPct float64
	RSSGrowthPct       float64
	FDDelta            int
	RPCErrorSlopePctHr float64
}

func NewTelemetryCollector() *TelemetryCollector { return &TelemetryCollector{} }

func (c *TelemetryCollector) RecordRPCLatency(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rpcLatencies = append(c.rpcLatencies, d)
}
func (c *TelemetryCollector) RecordContainerBoot(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.containerBoot = append(c.containerBoot, d)
}
func (c *TelemetryCollector) RecordRPCCount(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rpcCount += int64(n)
}
func (c *TelemetryCollector) RecordRPCError(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rpcErrors += int64(n)
}
func (c *TelemetryCollector) RecordSnapshotDelta(n int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshotDelta += n
}
func (c *TelemetryCollector) RecordDriftSample(at time.Time, goroutines int, rssMB float64, fdCount int, errRate float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if at.IsZero() {
		at = time.Now()
	}
	c.goroutines = append(c.goroutines, goroutines)
	c.rssMB = append(c.rssMB, rssMB)
	c.fdCount = append(c.fdCount, fdCount)
	c.errorRateHist = append(c.errorRateHist, errRate)
	c.errorRateAt = append(c.errorRateAt, at)
}

func (c *TelemetryCollector) Snapshot() *TelemetrySnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &TelemetrySnapshot{
		RPCLatencyP50Ms:    percentileMs(c.rpcLatencies, 0.50),
		RPCLatencyP95Ms:    percentileMs(c.rpcLatencies, 0.95),
		ContainerBootP95Ms: percentileMs(c.containerBoot, 0.95),
		RPCCount:           c.rpcCount,
		RPCErrors:          c.rpcErrors,
		SnapshotDelta:      c.snapshotDelta,
		Drift: DriftReport{
			GoroutineGrowthPct: growthPctInt(c.goroutines),
			RSSGrowthPct:       growthPctFloat(c.rssMB),
			FDDelta:            deltaInt(c.fdCount),
			RPCErrorSlopePctHr: slopePerHour(c.errorRateHist, c.errorRateAt),
		},
	}
}

func percentileMs(in []time.Duration, p float64) float64 {
	if len(in) == 0 {
		return 0
	}
	cp := append([]time.Duration(nil), in...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := int(math.Floor(p * float64(len(cp)-1)))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return float64(cp[idx]) / float64(time.Millisecond)
}
func growthPctInt(in []int) float64 {
	if len(in) < 2 || in[0] == 0 {
		return 0
	}
	return 100 * (float64(in[len(in)-1]-in[0]) / float64(in[0]))
}
func growthPctFloat(in []float64) float64 {
	if len(in) < 2 || in[0] == 0 {
		return 0
	}
	return 100 * ((in[len(in)-1] - in[0]) / in[0])
}
func deltaInt(in []int) int {
	if len(in) < 2 {
		return 0
	}
	return in[len(in)-1] - in[0]
}
func slopePerHour(in []float64, at []time.Time) float64 {
	if len(in) < 2 || len(at) < 2 {
		return 0
	}
	hours := at[len(at)-1].Sub(at[0]).Hours()
	if hours <= 0 {
		return 0
	}
	return (in[len(in)-1] - in[0]) / hours
}
