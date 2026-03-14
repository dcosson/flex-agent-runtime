package harness

import "fmt"

func AssertSoakThresholds(soak SoakProfile, snap *TelemetrySnapshot) error {
	if snap == nil {
		return fmt.Errorf("nil telemetry snapshot")
	}
	d := snap.Drift
	if d.GoroutineGrowthPct > soak.Drift.GoroutineGrowthPct {
		return fmt.Errorf("goroutine drift %.2f%% exceeds %.2f%%", d.GoroutineGrowthPct, soak.Drift.GoroutineGrowthPct)
	}
	if d.RSSGrowthPct > soak.Drift.RSSGrowthPct {
		return fmt.Errorf("rss drift %.2f%% exceeds %.2f%%", d.RSSGrowthPct, soak.Drift.RSSGrowthPct)
	}
	if d.FDDelta > soak.Drift.FDDeltaAbs || d.FDDelta < -soak.Drift.FDDeltaAbs {
		return fmt.Errorf("fd delta %d exceeds ±%d", d.FDDelta, soak.Drift.FDDeltaAbs)
	}
	if d.RPCErrorSlopePctHr > soak.Drift.RPCErrorSlopePctHr {
		return fmt.Errorf("rpc error slope %.4f%%/hr exceeds %.4f%%/hr", d.RPCErrorSlopePctHr, soak.Drift.RPCErrorSlopePctHr)
	}
	return nil
}

type Regression struct {
	Metric   string
	Baseline float64
	Current  float64
	DeltaPct float64
}

func CompareAgainstBaseline(current map[string]float64, baseline map[string]float64, tolerancePct float64) []Regression {
	out := []Regression{}
	for k, b := range baseline {
		c, ok := current[k]
		if !ok || b == 0 {
			continue
		}
		delta := 100 * ((c - b) / b)
		if delta > tolerancePct {
			out = append(out, Regression{Metric: k, Baseline: b, Current: c, DeltaPct: delta})
		}
	}
	return out
}
