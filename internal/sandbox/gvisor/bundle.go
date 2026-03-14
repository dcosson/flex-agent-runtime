package gvisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	ocispec "github.com/opencontainers/runtime-spec/specs-go"
)

// createBundle creates a temporary OCI bundle directory with the spec.
// Returns the bundle path on success. On error, any partial state is cleaned up.
func (m *Manager) createBundle(containerID string, spec *ocispec.Spec) (string, error) {
	bundlePath := filepath.Join(m.config.BundleBaseDir, containerID)

	if err := os.MkdirAll(bundlePath, 0o700); err != nil {
		return "", fmt.Errorf("mkdir bundle: %w", err)
	}

	specJSON, err := json.Marshal(spec)
	if err != nil {
		os.RemoveAll(bundlePath)
		return "", fmt.Errorf("marshal spec: %w", err)
	}

	configPath := filepath.Join(bundlePath, "config.json")
	if err := os.WriteFile(configPath, specJSON, 0o600); err != nil {
		os.RemoveAll(bundlePath)
		return "", fmt.Errorf("write config.json: %w", err)
	}

	return bundlePath, nil
}

// cleanupBundle removes the bundle directory. Best-effort.
func (m *Manager) cleanupBundle(bundlePath string) {
	if err := os.RemoveAll(bundlePath); err != nil {
		m.logger.Warn("bundle cleanup failed",
			"path", bundlePath,
			"error", err,
		)
	}
}
