package common

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// RequireZFS fails the test if ZFS is not available on the host.
func RequireZFS(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("zfs"); err != nil {
		t.Fatal("zfs not found in PATH")
	}
	out, err := exec.Command("lsmod").Output()
	if err != nil {
		t.Fatalf("lsmod failed; cannot verify ZFS module: %v", err)
	}
	if !strings.Contains(string(out), "zfs") {
		t.Fatal("zfs kernel module not loaded")
	}
}

// RequireGVisor fails the test if gVisor (runsc) is not available.
func RequireGVisor(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("runsc"); err != nil {
		t.Fatal("runsc not found in PATH")
	}
}

// RequireDocker fails the test if Docker is not available.
func RequireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("docker not found in PATH")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Fatalf("docker daemon not running: %v", err)
	}
}

// SandboxHostConfig returns the configured storage backend and container runtime
// from environment variables, with defaults.
func SandboxHostConfig(t *testing.T) (storageBackend, containerRuntime string) {
	t.Helper()
	storageBackend = os.Getenv("SANDBOX_STORAGE_BACKEND")
	if storageBackend == "" {
		storageBackend = "zfs"
	}
	containerRuntime = os.Getenv("SANDBOX_CONTAINER_RUNTIME")
	if containerRuntime == "" {
		containerRuntime = "gvisor"
	}
	return
}

// RequireZFSBackend fails the test if the configured backend is not ZFS.
func RequireZFSBackend(t *testing.T) {
	t.Helper()
	sb, _ := SandboxHostConfig(t)
	if sb != "zfs" {
		t.Fatalf("storage_backend is %q, not zfs", sb)
	}
}

// RequireGVisorRuntime fails the test if the configured runtime is not gVisor.
func RequireGVisorRuntime(t *testing.T) {
	t.Helper()
	_, cr := SandboxHostConfig(t)
	if cr != "gvisor" {
		t.Fatalf("container_runtime is %q, not gvisor", cr)
	}
}
