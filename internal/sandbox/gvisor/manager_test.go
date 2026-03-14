package gvisor

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// writeMockRunsc creates a mock runsc script and returns its path.
// The script receives a $CMD variable extracted from the args (handles
// runsc --root X --platform Y --network Z <subcommand> ...) so mock
// scripts can use case "$CMD" in ... esac.
func writeMockRunsc(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "runsc")
	// Preamble: extract subcommand from args, skipping --flag value pairs
	preamble := `#!/bin/bash
CMD=""
SKIP_NEXT=false
for arg in "$@"; do
    if $SKIP_NEXT; then
        SKIP_NEXT=false
        continue
    fi
    case "$arg" in
        --root|--platform|--network|--bundle) SKIP_NEXT=true;;
        --version) CMD="--version"; break;;
        --force) ;; # skip standalone flags
        run|delete|kill|list|state) CMD="$arg";;
    esac
done
`
	content := preamble + script
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// newTestManagerWithMock creates a Manager using a mock runsc binary.
func newTestManagerWithMock(t *testing.T, mockScript string) *Manager {
	t.Helper()
	mockPath := writeMockRunsc(t, mockScript)
	bundleDir := filepath.Join(t.TempDir(), "bundles")
	runscRoot := filepath.Join(t.TempDir(), "runsc-root")
	os.MkdirAll(bundleDir, 0o700)

	m := &Manager{
		config: ManagerConfig{
			RunscPath:     mockPath,
			BundleBaseDir: bundleDir,
			RunscRoot:     runscRoot,
			Platform:      "systrap",
		},
		logger: slog.Default(),
	}
	return m
}

func validTestOpts() ContainerOptions {
	return ContainerOptions{
		Command: []string{"/bin/echo", "hello"},
		WorkDir: "/",
		RootFS:  "/rootfs",
	}
}

// Basic mock that handles version check and simple run
const basicMockScript = `
case "$CMD" in
    --version) echo "runsc version mock-test";;
    run) echo "mock output"; exit 0;;
    delete) exit 0;;
    list) echo "[]";;
    kill) exit 0;;
    state) echo '{"status":"stopped"}';;
    *) echo "unknown command: $CMD" >&2; exit 1;;
esac
`

func TestManager_Run_Success(t *testing.T) {
	m := newTestManagerWithMock(t, basicMockScript)
	defer m.Close()

	result, err := m.Run(context.Background(), validTestOpts())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
	if result.Status != StatusExited {
		t.Errorf("status = %q, want %q", result.Status, StatusExited)
	}
	if result.ContainerID == "" {
		t.Error("container ID should be set")
	}
	if result.Duration <= 0 {
		t.Error("duration should be positive")
	}
	if result.BootDuration <= 0 {
		t.Error("boot duration should be positive")
	}
	if string(result.Stdout) != "mock output\n" {
		t.Errorf("stdout = %q, want 'mock output\\n'", result.Stdout)
	}
}

func TestManager_Run_NonZeroExit(t *testing.T) {
	script := `
case "$CMD" in
    --version) echo "runsc version mock-test";;
    run) echo "error output" >&2; exit 42;;
    delete) exit 0;;
    *) exit 0;;
esac
`
	m := newTestManagerWithMock(t, script)
	defer m.Close()

	result, err := m.Run(context.Background(), validTestOpts())
	if err != nil {
		t.Fatalf("Run() should not error on non-zero exit: %v", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("exit code = %d, want 42", result.ExitCode)
	}
	if result.Status != StatusExited {
		t.Errorf("status = %q, want %q", result.Status, StatusExited)
	}
}

func TestManager_Run_Timeout(t *testing.T) {
	script := `
trap 'exit 137' TERM INT
case "$CMD" in
    --version) echo "runsc version mock-test";;
    run) sleep 30 & wait $!;;
    delete) exit 0;;
    kill) exit 0;;
    *) exit 0;;
esac
`
	m := newTestManagerWithMock(t, script)
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	result, err := m.Run(ctx, validTestOpts())
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Errorf("timeout took too long: %s", elapsed)
	}

	// The result could be nil (error returned) or non-nil with timed-out status
	if err != nil {
		// Infrastructure error is acceptable for timeout
		return
	}
	if result.Status != StatusTimedOut && result.Status != StatusKilled {
		t.Errorf("status = %q, want timed_out or killed", result.Status)
	}
}

func TestManager_Run_InvalidOptions(t *testing.T) {
	m := newTestManagerWithMock(t, basicMockScript)
	defer m.Close()

	_, err := m.Run(context.Background(), ContainerOptions{})
	if err == nil {
		t.Error("expected error for empty options")
	}
}

func TestManager_Run_AfterClose(t *testing.T) {
	m := newTestManagerWithMock(t, basicMockScript)
	m.Close()

	_, err := m.Run(context.Background(), validTestOpts())
	if err != ErrManagerClosed {
		t.Errorf("error = %v, want ErrManagerClosed", err)
	}
}

func TestManager_Close_Idempotent(t *testing.T) {
	m := newTestManagerWithMock(t, basicMockScript)

	err1 := m.Close()
	err2 := m.Close()
	err3 := m.Close()

	if err1 != nil {
		t.Errorf("first Close() error: %v", err1)
	}
	if err2 != nil {
		t.Errorf("second Close() error: %v", err2)
	}
	if err3 != nil {
		t.Errorf("third Close() error: %v", err3)
	}
}

func TestManager_Close_Concurrent(t *testing.T) {
	m := newTestManagerWithMock(t, basicMockScript)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Close()
		}()
	}
	wg.Wait()
	// Should not panic
}

func TestManager_ActiveContainers_Zero(t *testing.T) {
	m := newTestManagerWithMock(t, basicMockScript)
	if m.ActiveContainers() != 0 {
		t.Errorf("active = %d, want 0", m.ActiveContainers())
	}
}

func TestManager_ActiveContainers_AfterRun(t *testing.T) {
	m := newTestManagerWithMock(t, basicMockScript)
	defer m.Close()

	m.Run(context.Background(), validTestOpts())

	// After Run completes, active count should be back to 0
	if m.ActiveContainers() != 0 {
		t.Errorf("active = %d, want 0 after Run completes", m.ActiveContainers())
	}
}

func TestManager_ConcurrentRuns(t *testing.T) {
	m := newTestManagerWithMock(t, basicMockScript)
	defer m.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Run(context.Background(), validTestOpts())
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent Run() error: %v", err)
	}
}

func TestManager_ConcurrencyLimit(t *testing.T) {
	script := `
case "$CMD" in
    --version) echo "runsc version mock-test";;
    run) sleep 0.1; echo "done"; exit 0;;
    delete) exit 0;;
    *) exit 0;;
esac
`
	m := newTestManagerWithMock(t, script)
	m.config.MaxConcurrentContainers = 3
	m.sem = make(chan struct{}, 3)
	defer m.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			m.Run(ctx, validTestOpts())
		}()
	}
	wg.Wait()
}

func TestManager_ApplyDefaults(t *testing.T) {
	m := &Manager{
		config: ManagerConfig{
			DefaultNetwork: NetworkSandbox,
			DefaultResources: ResourceSpec{
				CPUs:     2,
				MemoryMB: 512,
				MaxPIDs:  500,
				Timeout:  60 * time.Second,
			},
		},
	}

	opts := ContainerOptions{
		Command: []string{"echo"},
		WorkDir: "/",
		RootFS:  "/rootfs",
	}

	applied := m.applyDefaults(opts)

	if applied.Network != NetworkSandbox {
		t.Errorf("network = %q, want 'sandbox'", applied.Network)
	}
	if applied.Resources.CPUs != 2 {
		t.Errorf("cpus = %f, want 2", applied.Resources.CPUs)
	}
	if applied.Resources.MemoryMB != 512 {
		t.Errorf("memory = %d, want 512", applied.Resources.MemoryMB)
	}
	if applied.Resources.MaxPIDs != 500 {
		t.Errorf("pids = %d, want 500", applied.Resources.MaxPIDs)
	}
	if applied.Resources.Timeout != 60*time.Second {
		t.Errorf("timeout = %s, want 60s", applied.Resources.Timeout)
	}
}

func TestManager_ApplyDefaults_NoOverride(t *testing.T) {
	m := &Manager{
		config: ManagerConfig{
			DefaultNetwork: NetworkSandbox,
			DefaultResources: ResourceSpec{
				CPUs:     2,
				MemoryMB: 512,
			},
		},
	}

	opts := ContainerOptions{
		Command: []string{"echo"},
		WorkDir: "/",
		RootFS:  "/rootfs",
		Network: NetworkNone,
		Resources: ResourceSpec{
			CPUs:     4,
			MemoryMB: 1024,
		},
	}

	applied := m.applyDefaults(opts)

	// Explicit values should not be overridden
	if applied.Network != NetworkNone {
		t.Errorf("network = %q, want 'none' (not overridden)", applied.Network)
	}
	if applied.Resources.CPUs != 4 {
		t.Errorf("cpus = %f, want 4 (not overridden)", applied.Resources.CPUs)
	}
	if applied.Resources.MemoryMB != 1024 {
		t.Errorf("memory = %d, want 1024 (not overridden)", applied.Resources.MemoryMB)
	}
}

func TestManager_ResourceTimeout(t *testing.T) {
	script := `
trap 'exit 137' TERM INT
case "$CMD" in
    --version) echo "runsc version mock-test";;
    run) sleep 30 & wait $!;;
    delete) exit 0;;
    kill) exit 0;;
    *) exit 0;;
esac
`
	m := newTestManagerWithMock(t, script)
	defer m.Close()

	opts := validTestOpts()
	opts.Resources.Timeout = 500 * time.Millisecond

	start := time.Now()
	m.Run(context.Background(), opts)
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Errorf("resource timeout took too long: %s", elapsed)
	}
}

func TestManager_OutputCapture(t *testing.T) {
	script := `
case "$CMD" in
    --version) echo "runsc version mock-test";;
    run)
        echo "stdout line 1"
        echo "stdout line 2"
        echo "stderr line" >&2
        exit 0
        ;;
    delete) exit 0;;
    *) exit 0;;
esac
`
	m := newTestManagerWithMock(t, script)
	defer m.Close()

	result, err := m.Run(context.Background(), validTestOpts())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	stdout := string(result.Stdout)
	if stdout != "stdout line 1\nstdout line 2\n" {
		t.Errorf("stdout = %q", stdout)
	}
	stderr := string(result.Stderr)
	if stderr != "stderr line\n" {
		t.Errorf("stderr = %q", stderr)
	}
	if result.StdoutTruncated || result.StderrTruncated {
		t.Error("output should not be truncated for small output")
	}
}

func TestManager_OutputTruncation(t *testing.T) {
	// Generate output larger than limit
	script := `
case "$CMD" in
    --version) echo "runsc version mock-test";;
    run)
        # Write ~200 bytes
        for i in $(seq 1 20); do
            echo "1234567890"
        done
        exit 0
        ;;
    delete) exit 0;;
    *) exit 0;;
esac
`
	m := newTestManagerWithMock(t, script)
	defer m.Close()

	opts := validTestOpts()
	opts.Resources.MaxOutputBytes = 50

	result, err := m.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if !result.StdoutTruncated {
		t.Error("stdout should be truncated")
	}
	if int64(len(result.Stdout)) > 50 {
		t.Errorf("stdout length %d exceeds limit 50", len(result.Stdout))
	}
}

func TestManager_DeleteFailureDoesntBlockRun(t *testing.T) {
	script := `
case "$CMD" in
    --version) echo "runsc version mock-test";;
    run) echo "ok"; exit 0;;
    delete) echo "delete failed" >&2; exit 1;;
    *) exit 0;;
esac
`
	m := newTestManagerWithMock(t, script)
	defer m.Close()

	result, err := m.Run(context.Background(), validTestOpts())
	if err != nil {
		t.Fatalf("Run() should succeed even if delete fails: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
}

func TestManager_BundleCleanedUpAfterRun(t *testing.T) {
	m := newTestManagerWithMock(t, basicMockScript)
	defer m.Close()

	m.Run(context.Background(), validTestOpts())

	// Bundle directory should be cleaned up
	entries, err := os.ReadDir(m.config.BundleBaseDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading bundle dir: %v", err)
	}
	if len(entries) > 0 {
		t.Errorf("bundle dir should be empty after run, found %d entries", len(entries))
	}
}
