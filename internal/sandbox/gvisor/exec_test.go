package gvisor

import (
	"crypto/rand"
	"strings"
	"sync"
	"testing"
)

func TestGenerateContainerID_Format(t *testing.T) {
	id := generateContainerID()
	if !strings.HasPrefix(id, "gv-") {
		t.Errorf("container ID should start with 'gv-', got %q", id)
	}
	parts := strings.SplitN(id, "-", 3)
	if len(parts) != 3 {
		t.Errorf("container ID should have format gv-<ts>-<hex>, got %q", id)
	}
	// Random hex part should be 12 chars (6 bytes)
	if len(parts[2]) != 12 {
		t.Errorf("random hex should be 12 chars, got %d in %q", len(parts[2]), id)
	}
}

func TestGenerateContainerID_Unique(t *testing.T) {
	n := 10000
	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		id := generateContainerID()
		if seen[id] {
			t.Fatalf("duplicate container ID after %d iterations: %s", i, id)
		}
		seen[id] = true
	}
}

func TestGenerateContainerID_ConcurrentUnique(t *testing.T) {
	n := 1000
	goroutines := 10
	ids := make(chan string, n*goroutines)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < n; i++ {
				ids <- generateContainerID()
			}
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[string]bool, n*goroutines)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate container ID in concurrent test: %s", id)
		}
		seen[id] = true
	}
}

func TestLimitedBuffer_UnderLimit(t *testing.T) {
	buf := newLimitedBuffer(100)
	n, err := buf.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("wrote %d bytes, want 5", n)
	}
	if string(buf.Bytes()) != "hello" {
		t.Errorf("bytes = %q, want 'hello'", buf.Bytes())
	}
	if buf.Truncated() {
		t.Error("should not be truncated")
	}
}

func TestLimitedBuffer_ExactLimit(t *testing.T) {
	buf := newLimitedBuffer(5)
	n, err := buf.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("wrote %d bytes, want 5", n)
	}
	if buf.Truncated() {
		t.Error("should not be truncated at exact limit")
	}
}

func TestLimitedBuffer_OverLimit(t *testing.T) {
	buf := newLimitedBuffer(5)
	n, err := buf.Write([]byte("hello world"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should report full write to preserve io.Writer compatibility
	if n != 11 {
		t.Errorf("wrote %d bytes, want 11 (reported)", n)
	}
	if !buf.Truncated() {
		t.Error("should be truncated")
	}
	if len(buf.Bytes()) != 5 {
		t.Errorf("stored %d bytes, want 5", len(buf.Bytes()))
	}
	if string(buf.Bytes()) != "hello" {
		t.Errorf("bytes = %q, want 'hello'", buf.Bytes())
	}
}

func TestLimitedBuffer_MultipleWrites(t *testing.T) {
	buf := newLimitedBuffer(10)
	buf.Write([]byte("hello"))
	buf.Write([]byte("world"))
	if buf.Truncated() {
		t.Error("should not be truncated at exactly 10 bytes")
	}
	if string(buf.Bytes()) != "helloworld" {
		t.Errorf("bytes = %q, want 'helloworld'", buf.Bytes())
	}

	// Now exceed it
	n, err := buf.Write([]byte("!"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("wrote %d bytes, want 1 (reported)", n)
	}
	if !buf.Truncated() {
		t.Error("should be truncated after exceeding limit")
	}
	if len(buf.Bytes()) != 10 {
		t.Errorf("stored %d bytes, want 10", len(buf.Bytes()))
	}
}

func TestLimitedBuffer_ZeroLimit(t *testing.T) {
	buf := newLimitedBuffer(0)
	n, err := buf.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("wrote %d bytes, want 5 (reported)", n)
	}
	if !buf.Truncated() {
		t.Error("should be truncated with 0 limit")
	}
	if len(buf.Bytes()) != 0 {
		t.Errorf("stored %d bytes, want 0", len(buf.Bytes()))
	}
}

func TestLimitedBuffer_ConcurrentWrites(t *testing.T) {
	buf := newLimitedBuffer(1024)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data := make([]byte, 20)
			rand.Read(data)
			buf.Write(data)
		}()
	}
	wg.Wait()

	// 100 * 20 = 2000 bytes, limit is 1024
	if !buf.Truncated() {
		t.Error("should be truncated")
	}
	if int64(len(buf.Bytes())) > 1024 {
		t.Errorf("stored %d bytes, exceeds limit 1024", len(buf.Bytes()))
	}
}

func TestBuildRunscArgs(t *testing.T) {
	m := &Manager{
		config: ManagerConfig{
			RunscPath: "/usr/bin/runsc",
			RunscRoot: "/run/runsc",
			Platform:  "systrap",
		},
	}

	opts := ContainerOptions{
		Command: []string{"echo", "hello"},
		WorkDir: "/",
		RootFS:  "/rootfs",
		Network: NetworkSandbox,
	}

	args := m.buildRunscArgs(opts, "/tmp/bundle-123", "gv-123-abc")

	expected := []string{
		"--root", "/run/runsc",
		"--platform", "systrap",
		"--network", "sandbox",
		"run",
		"--bundle", "/tmp/bundle-123",
		"gv-123-abc",
	}

	if len(args) != len(expected) {
		t.Fatalf("args length = %d, want %d: %v", len(args), len(expected), args)
	}
	for i, arg := range args {
		if arg != expected[i] {
			t.Errorf("args[%d] = %q, want %q", i, arg, expected[i])
		}
	}
}

func TestBuildRunscArgs_DefaultNetwork(t *testing.T) {
	m := &Manager{
		config: ManagerConfig{
			RunscRoot:      "/run/runsc",
			Platform:       "systrap",
			DefaultNetwork: NetworkHost,
		},
	}

	opts := ContainerOptions{
		Command: []string{"echo"},
		WorkDir: "/",
		RootFS:  "/rootfs",
		// Network not set — should use manager default
	}

	args := m.buildRunscArgs(opts, "/tmp/bundle", "gv-test")
	for i, arg := range args {
		if arg == "--network" && i+1 < len(args) {
			if args[i+1] != "host" {
				t.Errorf("network = %q, want 'host' (manager default)", args[i+1])
			}
			return
		}
	}
	t.Error("--network flag not found in args")
}

func TestBuildRunscArgs_FallbackNetwork(t *testing.T) {
	m := &Manager{
		config: ManagerConfig{
			RunscRoot: "/run/runsc",
			Platform:  "systrap",
			// No DefaultNetwork set
		},
	}

	opts := ContainerOptions{
		Command: []string{"echo"},
		WorkDir: "/",
		RootFS:  "/rootfs",
	}

	args := m.buildRunscArgs(opts, "/tmp/bundle", "gv-test")
	for i, arg := range args {
		if arg == "--network" && i+1 < len(args) {
			if args[i+1] != "none" {
				t.Errorf("network = %q, want 'none' (fallback default)", args[i+1])
			}
			return
		}
	}
	t.Error("--network flag not found in args")
}
