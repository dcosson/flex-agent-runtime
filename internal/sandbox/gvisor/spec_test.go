package gvisor

import (
	"encoding/json"
	"sort"
	"testing"
)

func TestBuildSpec_BasicStructure(t *testing.T) {
	opts := ContainerOptions{
		Command: []string{"/bin/sh", "-c", "echo hello"},
		WorkDir: "/workspace",
		RootFS:  "/rootfs/test",
	}

	spec := BuildSpec(opts)

	if spec.Version != "1.0.2-dev" {
		t.Errorf("version = %q, want 1.0.2-dev", spec.Version)
	}
	if spec.Root == nil {
		t.Fatal("root is nil")
	}
	if spec.Root.Path != "/rootfs/test" {
		t.Errorf("root path = %q, want /rootfs/test", spec.Root.Path)
	}
	if spec.Root.Readonly {
		t.Error("root should be writable by default")
	}
	if spec.Process == nil {
		t.Fatal("process is nil")
	}
	if spec.Linux == nil {
		t.Fatal("linux is nil")
	}
}

func TestBuildSpec_ReadOnlyRoot(t *testing.T) {
	opts := ContainerOptions{
		Command:        []string{"echo"},
		WorkDir:        "/",
		RootFS:         "/rootfs",
		ReadOnlyRootFS: true,
	}

	spec := BuildSpec(opts)
	if !spec.Root.Readonly {
		t.Error("root should be read-only")
	}
}

func TestBuildSpec_Process(t *testing.T) {
	opts := ContainerOptions{
		Command: []string{"ls", "-la"},
		WorkDir: "/data",
		RootFS:  "/rootfs",
		User:    &UserSpec{UID: 1000, GID: 1000},
	}

	spec := BuildSpec(opts)
	proc := spec.Process

	if proc.Terminal {
		t.Error("terminal should be false")
	}
	if len(proc.Args) != 2 || proc.Args[0] != "ls" || proc.Args[1] != "-la" {
		t.Errorf("args = %v, want [ls -la]", proc.Args)
	}
	if proc.Cwd != "/data" {
		t.Errorf("cwd = %q, want /data", proc.Cwd)
	}
	if proc.User.UID != 1000 || proc.User.GID != 1000 {
		t.Errorf("user = %d:%d, want 1000:1000", proc.User.UID, proc.User.GID)
	}
	if !proc.NoNewPrivileges {
		t.Error("NoNewPrivileges should be true")
	}
	if proc.Capabilities == nil {
		t.Fatal("capabilities is nil")
	}
	if len(proc.Capabilities.Bounding) == 0 {
		t.Error("expected bounding capabilities")
	}
}

func TestBuildSpec_DefaultUser(t *testing.T) {
	opts := ContainerOptions{
		Command: []string{"echo"},
		WorkDir: "/",
		RootFS:  "/rootfs",
	}

	spec := BuildSpec(opts)
	if spec.Process.User.UID != 0 || spec.Process.User.GID != 0 {
		t.Errorf("default user = %d:%d, want 0:0", spec.Process.User.UID, spec.Process.User.GID)
	}
}

func TestBuildEnv_Defaults(t *testing.T) {
	env := buildEnv(nil)

	envMap := make(map[string]string)
	for _, e := range env {
		parts := splitFirst(e, '=')
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	required := []string{"PATH", "HOME", "TERM", "LANG"}
	for _, key := range required {
		if _, ok := envMap[key]; !ok {
			t.Errorf("missing default env var: %s", key)
		}
	}
}

func TestBuildEnv_UserOverride(t *testing.T) {
	env := buildEnv(map[string]string{
		"HOME":   "/custom",
		"CUSTOM": "value",
	})

	envMap := make(map[string]string)
	for _, e := range env {
		parts := splitFirst(e, '=')
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	if envMap["HOME"] != "/custom" {
		t.Errorf("HOME = %q, want /custom", envMap["HOME"])
	}
	if envMap["CUSTOM"] != "value" {
		t.Errorf("CUSTOM = %q, want value", envMap["CUSTOM"])
	}
}

func TestBuildEnv_Sorted(t *testing.T) {
	env := buildEnv(map[string]string{"Z": "1", "A": "2"})
	if !sort.StringsAreSorted(env) {
		t.Errorf("env vars not sorted: %v", env)
	}
}

func TestBuildSpec_Namespaces(t *testing.T) {
	opts := ContainerOptions{
		Command: []string{"echo"},
		WorkDir: "/",
		RootFS:  "/rootfs",
	}

	spec := BuildSpec(opts)
	nsTypes := make(map[string]bool)
	for _, ns := range spec.Linux.Namespaces {
		nsTypes[string(ns.Type)] = true
	}

	required := []string{"pid", "mount", "ipc", "uts", "network"}
	for _, ns := range required {
		if !nsTypes[ns] {
			t.Errorf("missing namespace: %s", ns)
		}
	}
}

func TestBuildSpec_DefaultMounts(t *testing.T) {
	opts := ContainerOptions{
		Command: []string{"echo"},
		WorkDir: "/",
		RootFS:  "/rootfs",
	}

	spec := BuildSpec(opts)
	mountDests := make(map[string]bool)
	for _, m := range spec.Mounts {
		mountDests[m.Destination] = true
	}

	required := []string{"/proc", "/dev", "/dev/pts", "/dev/shm", "/tmp", "/sys"}
	for _, dest := range required {
		if !mountDests[dest] {
			t.Errorf("missing mount: %s", dest)
		}
	}
}

func TestBuildSpec_ExtraMounts(t *testing.T) {
	opts := ContainerOptions{
		Command: []string{"echo"},
		WorkDir: "/",
		RootFS:  "/rootfs",
		ExtraMounts: []Mount{
			{
				Source:      "/host/cache",
				Destination: "/cache",
				Type:        "bind",
				ReadOnly:    true,
			},
			{
				Destination: "/scratch",
				Type:        "tmpfs",
				Options:     []string{"size=100m"},
			},
		},
	}

	spec := BuildSpec(opts)
	found := 0
	for _, m := range spec.Mounts {
		if m.Destination == "/cache" {
			found++
			hasRO := false
			for _, o := range m.Options {
				if o == "ro" {
					hasRO = true
				}
			}
			if !hasRO {
				t.Error("/cache mount should have ro option")
			}
		}
		if m.Destination == "/scratch" {
			found++
		}
	}
	if found != 2 {
		t.Errorf("expected 2 extra mounts, found %d", found)
	}
}

func TestBuildSpec_JSONSerializable(t *testing.T) {
	opts := ContainerOptions{
		Command:   []string{"/bin/sh"},
		WorkDir:   "/",
		RootFS:    "/rootfs",
		Resources: ResourceSpec{CPUs: 2, MemoryMB: 512, MaxPIDs: 100},
	}

	spec := BuildSpec(opts)
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("spec should be JSON serializable: %v", err)
	}
	if len(data) == 0 {
		t.Error("serialized spec is empty")
	}

	// Verify round-trip
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("deserialization failed: %v", err)
	}
	if decoded["ociVersion"] != "1.0.2-dev" {
		t.Errorf("ociVersion = %v, want 1.0.2-dev", decoded["ociVersion"])
	}
}

func TestMinimalCaps(t *testing.T) {
	caps := minimalCaps()
	if len(caps) == 0 {
		t.Fatal("no capabilities returned")
	}
	// All caps should start with CAP_
	for _, c := range caps {
		if len(c) < 4 || c[:4] != "CAP_" {
			t.Errorf("invalid capability: %q", c)
		}
	}
}

func splitFirst(s string, sep byte) []string {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}
