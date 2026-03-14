package harness

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ArtifactBundle struct {
	JSONPath     string
	CSVPath      string
	MarkdownPath string
}

func WriteArtifacts(dir string, summary *RunSummary) (*ArtifactBundle, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	bundle := &ArtifactBundle{
		JSONPath:     filepath.Join(dir, "metrics.json"),
		CSVPath:      filepath.Join(dir, "metrics.csv"),
		MarkdownPath: filepath.Join(dir, "report.md"),
	}
	if err := writeJSON(bundle.JSONPath, summary); err != nil {
		return nil, err
	}
	if err := writeCSV(bundle.CSVPath, summary); err != nil {
		return nil, err
	}
	if err := writeMarkdown(bundle.MarkdownPath, summary); err != nil {
		return nil, err
	}
	return bundle, nil
}

func SaveBaseline(path string, metrics map[string]float64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeJSON(path, metrics)
}

func LoadBaseline(path string) (map[string]float64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]float64{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func writeCSV(path string, summary *RunSummary) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"workload", "mode", "session_id", "rpc_calls", "snapshots", "duration_ms", "error"}); err != nil {
		return err
	}
	for _, r := range summary.Results {
		errText := ""
		if r.Err != nil {
			errText = r.Err.Error()
		}
		if err := w.Write([]string{r.Workload, r.Mode, r.SessionID, fmt.Sprintf("%d", r.RPCCalls), fmt.Sprintf("%d", r.Snapshots), fmt.Sprintf("%.3f", r.FinishedAt.Sub(r.StartedAt).Seconds()*1000), errText}); err != nil {
			return err
		}
	}
	return nil
}

func writeMarkdown(path string, summary *RunSummary) error {
	lines := []string{
		"# Runtime Harness Report",
		"",
		fmt.Sprintf("- Profile: `%s`", summary.Profile.Name),
		fmt.Sprintf("- Runs: `%d` (mode2=%d mode3=%d)", len(summary.Results), summary.Mode2Runs, summary.Mode3Runs),
		fmt.Sprintf("- Errors: `%d`", summary.Errors),
		fmt.Sprintf("- RPC p95: `%.2fms`", summary.RPCLatencyP95Ms),
		fmt.Sprintf("- Container boot p95: `%.2fms`", summary.ContainerBootP95Ms),
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

func MetricsMap(summary *RunSummary) map[string]float64 {
	return map[string]float64{
		"rpc_latency_p95_ms":    summary.RPCLatencyP95Ms,
		"container_boot_p95_ms": summary.ContainerBootP95Ms,
		"errors":                float64(summary.Errors),
		"runs":                  float64(len(summary.Results)),
	}
}
