package harness

import (
	"os"
	"path/filepath"
	"testing"
)

// SandboxEnv provides workspace and session lifecycle helpers for Mode 2 E2E tests.
// It manages workspace directories that simulate ZFS-backed session storage.
type SandboxEnv struct {
	SessionID    string
	WorkspaceDir string
	DataDir      string // sandbox-data-dir (parent of configs/)
	ConfigDir    string // <DataDir>/configs/<SessionID>/

	t *testing.T
}

// SandboxEnvConfig controls SandboxEnv setup.
type SandboxEnvConfig struct {
	SessionID string
}

// NewSandboxEnv creates a SandboxEnv with isolated temp directories.
func NewSandboxEnv(t *testing.T, cfg SandboxEnvConfig) *SandboxEnv {
	t.Helper()

	if cfg.SessionID == "" {
		cfg.SessionID = "mode2-sandbox-" + t.Name()
	}

	base := t.TempDir()
	workspaceDir := filepath.Join(base, "workspace")
	dataDir := filepath.Join(base, "sandbox-data")
	configDir := filepath.Join(dataDir, "configs", cfg.SessionID)

	for _, dir := range []string{workspaceDir, configDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create dir %s: %v", dir, err)
		}
	}

	env := &SandboxEnv{
		SessionID:    cfg.SessionID,
		WorkspaceDir: workspaceDir,
		DataDir:      dataDir,
		ConfigDir:    configDir,
		t:            t,
	}

	return env
}

// WriteWorkspaceFile creates a file in the workspace directory.
func (e *SandboxEnv) WriteWorkspaceFile(relPath, content string) {
	e.t.Helper()
	abs := filepath.Join(e.WorkspaceDir, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// ReadWorkspaceFile reads a file from the workspace directory.
func (e *SandboxEnv) ReadWorkspaceFile(relPath string) string {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.WorkspaceDir, relPath))
	if err != nil {
		e.t.Fatalf("read workspace file %s: %v", relPath, err)
	}
	return string(data)
}

// WorkspaceFileExists checks if a file exists in the workspace.
func (e *SandboxEnv) WorkspaceFileExists(relPath string) bool {
	_, err := os.Stat(filepath.Join(e.WorkspaceDir, relPath))
	return err == nil
}

// WriteConfigFile creates a file in the config directory.
func (e *SandboxEnv) WriteConfigFile(relPath, content string) {
	e.t.Helper()
	abs := filepath.Join(e.ConfigDir, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// ReadConfigFile reads a file from the config directory.
func (e *SandboxEnv) ReadConfigFile(relPath string) string {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.ConfigDir, relPath))
	if err != nil {
		e.t.Fatalf("read config file %s: %v", relPath, err)
	}
	return string(data)
}

// ConfigFileExists checks if a file exists in the config directory.
func (e *SandboxEnv) ConfigFileExists(relPath string) bool {
	_, err := os.Stat(filepath.Join(e.ConfigDir, relPath))
	return err == nil
}

// ConfigPath returns the config directory path for the session.
// This validates the path scheme: <sandbox-data-dir>/configs/<session-id>/
func (e *SandboxEnv) ConfigPath() string {
	return e.ConfigDir
}

// SimulateSnapshot simulates a ZFS snapshot by creating a copy marker.
func (e *SandboxEnv) SimulateSnapshot(name string) string {
	e.t.Helper()
	snapDir := filepath.Join(e.DataDir, "snapshots")
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	marker := filepath.Join(snapDir, name)
	if err := os.WriteFile(marker, []byte(name), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return marker
}

// SnapshotExists checks if a simulated snapshot exists.
func (e *SandboxEnv) SnapshotExists(name string) bool {
	_, err := os.Stat(filepath.Join(e.DataDir, "snapshots", name))
	return err == nil
}
