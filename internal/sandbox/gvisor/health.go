package gvisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// CleanupStale finds containers in runsc's state directory that aren't tracked
// by the manager and removes them. This handles containers leaked by a previous
// crash of the manager process.
func (m *Manager) CleanupStale(ctx context.Context) (int, error) {
	// List all containers known to runsc
	cmd := exec.CommandContext(ctx, m.config.RunscPath,
		"--root", m.config.RunscRoot,
		"list", "--format", "json",
	)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("runsc list: %w", err)
	}

	var containers []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(out, &containers); err != nil {
		return 0, fmt.Errorf("parse container list: %w", err)
	}

	cleaned := 0
	for _, c := range containers {
		// Skip containers we're currently tracking
		if _, ok := m.active.Load(c.ID); ok {
			continue
		}

		m.logger.Info("cleaning stale container",
			"container_id", c.ID,
			"status", c.Status,
		)

		// Kill if still running
		if c.Status == "running" || c.Status == "created" {
			killCmd := exec.CommandContext(ctx, m.config.RunscPath,
				"--root", m.config.RunscRoot,
				"kill", c.ID, "SIGKILL",
			)
			killCmd.Run() // best-effort
		}

		// Delete
		m.deleteContainer(c.ID)
		cleaned++
	}

	// Also clean up orphaned bundle directories
	entries, err := os.ReadDir(m.config.BundleBaseDir)
	if err == nil {
		for _, entry := range entries {
			if _, ok := m.active.Load(entry.Name()); !ok {
				bundlePath := filepath.Join(m.config.BundleBaseDir, entry.Name())
				m.cleanupBundle(bundlePath)
			}
		}
	}

	return cleaned, nil
}
