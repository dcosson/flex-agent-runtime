package common

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// E2EReport captures the results of an E2E test run for a given tier.
type E2EReport struct {
	Tier      string           `json:"tier"`
	StartedAt time.Time        `json:"started_at"`
	Duration  time.Duration    `json:"duration"`
	Scenarios []ScenarioReport `json:"scenarios"`
	Summary   ReportSummary    `json:"summary"`
}

// ScenarioReport captures the result of a single test scenario.
type ScenarioReport struct {
	Name       string        `json:"name"`
	Mode       string        `json:"mode"`
	Passed     bool          `json:"passed"`
	Duration   time.Duration `json:"duration"`
	EventCount int           `json:"event_count"`
	Error      string        `json:"error,omitempty"`
}

// ReportSummary provides aggregate counts for a test run.
type ReportSummary struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// WriteReport writes the report to a JSON file in the given directory.
func (r *E2EReport) WriteReport(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create report dir: %w", err)
	}
	filename := fmt.Sprintf("%s-%s.json", r.Tier, r.StartedAt.Format("20060102-150405"))
	path := filepath.Join(dir, filename)
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}
