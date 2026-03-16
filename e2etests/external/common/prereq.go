package common

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// RequireZFS skips the test if ZFS is not available on the host.
func RequireZFS(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("zfs"); err != nil {
		t.Skip("zfs not found in PATH")
	}
	out, err := exec.Command("lsmod").Output()
	if err != nil {
		t.Skip("lsmod failed; cannot verify ZFS module")
	}
	if !strings.Contains(string(out), "zfs") {
		t.Skip("zfs kernel module not loaded")
	}
}

// RequireGVisor skips the test if gVisor (runsc) is not available.
func RequireGVisor(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("runsc"); err != nil {
		t.Skip("runsc not found in PATH")
	}
}

// RequireDocker skips the test if Docker is not available.
func RequireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not found in PATH")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not running")
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

// RequireZFSBackend skips the test if the configured backend is not ZFS.
func RequireZFSBackend(t *testing.T) {
	t.Helper()
	sb, _ := SandboxHostConfig(t)
	if sb != "zfs" {
		t.Skipf("storage_backend is %q, not zfs", sb)
	}
}

// RequireGVisorRuntime skips the test if the configured runtime is not gVisor.
func RequireGVisorRuntime(t *testing.T) {
	t.Helper()
	_, cr := SandboxHostConfig(t)
	if cr != "gvisor" {
		t.Skipf("container_runtime is %q, not gvisor", cr)
	}
}
