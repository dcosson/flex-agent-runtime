package harness

import "time"

type ConcurrencyProfile struct {
	Name              string
	Concurrency       int
	Mode2Weight       int
	Mode3Weight       int
	Tier1Share        float64
	TargetSessionTime time.Duration
	FailureRate       float64
}

type SoakProfile struct {
	Name     string
	Duration time.Duration
	Warmup   time.Duration
	Drift    DriftThresholds
}

type BenchmarkProfile struct {
	Name             string
	Concurrency      int
	Iterations       int
	DatasetSizeBytes int64
}

type DriftThresholds struct {
	GoroutineGrowthPct float64
	RSSGrowthPct       float64
	FDDeltaAbs         int
	RPCErrorSlopePctHr float64
}

var (
	ProfileSmall = ConcurrencyProfile{
		Name:              "P-small",
		Concurrency:       10,
		Mode2Weight:       4,
		Mode3Weight:       6,
		Tier1Share:        0.7,
		TargetSessionTime: 5 * time.Second,
		FailureRate:       0.01,
	}
	ProfileMedium = ConcurrencyProfile{
		Name:              "P-medium",
		Concurrency:       50,
		Mode2Weight:       4,
		Mode3Weight:       6,
		Tier1Share:        0.65,
		TargetSessionTime: 8 * time.Second,
		FailureRate:       0.02,
	}
	ProfileLarge = ConcurrencyProfile{
		Name:              "P-large",
		Concurrency:       200,
		Mode2Weight:       4,
		Mode3Weight:       6,
		Tier1Share:        0.60,
		TargetSessionTime: 12 * time.Second,
		FailureRate:       0.03,
	}
)

func DefaultSoak12h() SoakProfile {
	return SoakProfile{
		Name:     "Soak-12h",
		Duration: 12 * time.Hour,
		Warmup:   time.Hour,
		Drift: DriftThresholds{
			GoroutineGrowthPct: 5,
			RSSGrowthPct:       10,
			FDDeltaAbs:         2,
			RPCErrorSlopePctHr: 0.01,
		},
	}
}

func DefaultSoak24h() SoakProfile {
	s := DefaultSoak12h()
	s.Name = "Soak-24h"
	s.Duration = 24 * time.Hour
	return s
}
