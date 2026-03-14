package gvisor

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/runtime-spec/specs-go"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	bundleDir := filepath.Join(t.TempDir(), "bundles")
	return &Manager{
		config: ManagerConfig{
			BundleBaseDir: bundleDir,
			RunscRoot:     filepath.Join(t.TempDir(), "runsc"),
			Platform:      "systrap",
		},
		logger: slog.Default(),
	}
}

func TestCreateBundle_Success(t *testing.T) {
	m := testManager(t)
	spec := BuildSpec(ContainerOptions{
		Command: []string{"echo", "hello"},
		WorkDir: "/",
		RootFS:  "/rootfs",
	})

	bundlePath, err := m.createBundle("test-container", spec)
	if err != nil {
		t.Fatalf("createBundle failed: %v", err)
	}

	// Verify bundle directory exists
	info, err := os.Stat(bundlePath)
	if err != nil {
		t.Fatalf("bundle dir not found: %v", err)
	}
	if !info.IsDir() {
		t.Error("bundle path should be a directory")
	}

	// Verify config.json exists and is valid
	configPath := filepath.Join(bundlePath, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.json not found: %v", err)
	}

	var decoded ocispec.Spec
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("config.json is not valid JSON: %v", err)
	}
	if decoded.Version != "1.0.2-dev" {
		t.Errorf("version = %q, want '1.0.2-dev'", decoded.Version)
	}
	if decoded.Root.Path != "/rootfs" {
		t.Errorf("rootfs = %q, want '/rootfs'", decoded.Root.Path)
	}
}

func TestCreateBundle_DirectoryPermissions(t *testing.T) {
	m := testManager(t)
	spec := BuildSpec(ContainerOptions{
		Command: []string{"echo"},
		WorkDir: "/",
		RootFS:  "/rootfs",
	})

	bundlePath, err := m.createBundle("perm-test", spec)
	if err != nil {
		t.Fatalf("createBundle failed: %v", err)
	}

	info, err := os.Stat(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm != 0o700 {
		t.Errorf("bundle dir permissions = %o, want 700", perm)
	}
}

func TestCreateBundle_ConfigPermissions(t *testing.T) {
	m := testManager(t)
	spec := BuildSpec(ContainerOptions{
		Command: []string{"echo"},
		WorkDir: "/",
		RootFS:  "/rootfs",
	})

	bundlePath, err := m.createBundle("config-perm", spec)
	if err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(bundlePath, "config.json")
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("config.json permissions = %o, want 600", perm)
	}
}

func TestCleanupBundle(t *testing.T) {
	m := testManager(t)
	spec := BuildSpec(ContainerOptions{
		Command: []string{"echo"},
		WorkDir: "/",
		RootFS:  "/rootfs",
	})

	bundlePath, err := m.createBundle("cleanup-test", spec)
	if err != nil {
		t.Fatal(err)
	}

	// Verify it exists
	if _, err := os.Stat(bundlePath); err != nil {
		t.Fatalf("bundle should exist: %v", err)
	}

	m.cleanupBundle(bundlePath)

	// Verify it's removed
	if _, err := os.Stat(bundlePath); !os.IsNotExist(err) {
		t.Error("bundle should be removed after cleanup")
	}
}

func TestCleanupBundle_NonExistent(t *testing.T) {
	m := testManager(t)
	// Should not panic or error on non-existent path
	m.cleanupBundle("/nonexistent/path/that/does/not/exist")
}

func TestCreateBundle_UniquePerContainer(t *testing.T) {
	m := testManager(t)
	spec := BuildSpec(ContainerOptions{
		Command: []string{"echo"},
		WorkDir: "/",
		RootFS:  "/rootfs",
	})

	path1, err := m.createBundle("container-1", spec)
	if err != nil {
		t.Fatal(err)
	}
	path2, err := m.createBundle("container-2", spec)
	if err != nil {
		t.Fatal(err)
	}

	if path1 == path2 {
		t.Error("different containers should have different bundle paths")
	}
}
