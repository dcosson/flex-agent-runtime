package termmux

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConfigDirManager manages per-session config directories for 3rd party agent drivers.
// Config directories contain authentication tokens, settings, and injected files
// (like CLAUDE.md) that drivers need at launch time.
type ConfigDirManager struct {
	BaseDir string
}

// NewConfigDirManager creates a new config directory manager.
func NewConfigDirManager(baseDir string) *ConfigDirManager {
	return &ConfigDirManager{BaseDir: baseDir}
}

// StablePath returns the deterministic config directory path for a session.
// The same session ID always resolves to the same path, which is critical
// for auth token persistence across pause/resume cycles.
func (m *ConfigDirManager) StablePath(sessionID string) string {
	return filepath.Join(m.BaseDir, sessionID)
}

// EnsureDir creates the config directory if it doesn't exist and returns the path.
func (m *ConfigDirManager) EnsureDir(sessionID string) (string, error) {
	path := m.StablePath(sessionID)
	if err := os.MkdirAll(path, 0700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	return path, nil
}

// Cleanup removes the config directory for a session.
func (m *ConfigDirManager) Cleanup(sessionID string) error {
	path := m.StablePath(sessionID)
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("cleanup config dir: %w", err)
	}
	return nil
}

// InjectFile writes a file into the session's config directory.
// This is used by orchestrators to inject CLAUDE.md, agents.md, MCP configs, etc.
func (m *ConfigDirManager) InjectFile(sessionID, relativePath string, data []byte) error {
	dir := m.StablePath(sessionID)
	fullPath := filepath.Clean(filepath.Join(dir, relativePath))

	// Prevent path traversal outside the session directory
	if !strings.HasPrefix(fullPath, filepath.Clean(dir)+string(os.PathSeparator)) {
		return fmt.Errorf("relative path %q escapes config directory", relativePath)
	}

	// Ensure parent directories exist
	if err := os.MkdirAll(filepath.Dir(fullPath), 0700); err != nil {
		return fmt.Errorf("create parent dirs: %w", err)
	}

	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return fmt.Errorf("write injected file: %w", err)
	}
	return nil
}
