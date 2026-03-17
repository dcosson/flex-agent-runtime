package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/sandbox/gvisor"
	"github.com/anthropics/flex-agent-runtime/internal/sandbox/zfs"
)

// mockGVisor is an enhanced mock for the GVisorManager interface.
// Supports fault injection, slow execution, and call tracking.
type mockGVisor struct {
	mu       sync.Mutex
	calls    int
	lastOpts gvisor.ContainerOptions
	result   *gvisor.ContainerResult
	runErr   error
	failMode string        // "crash", "oom", "timeout"
	delay    time.Duration // simulated execution time
	closed   bool
	closedMu sync.Mutex
	runFn    func(ctx context.Context, opts gvisor.ContainerOptions) (*gvisor.ContainerResult, error)
}

func newMockGVisor() *mockGVisor {
	return &mockGVisor{
		result: &gvisor.ContainerResult{ExitCode: 0, Stdout: []byte("ok"), Status: gvisor.StatusExited},
	}
}

func newSlowMockGVisor(delay time.Duration) *mockGVisor {
	m := newMockGVisor()
	m.delay = delay
	return m
}

func (m *mockGVisor) InjectFailure(mode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failMode = mode
}

func (m *mockGVisor) ClearFailure() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failMode = ""
	m.runErr = nil
}

func (m *mockGVisor) SetRunError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runErr = err
}

func (m *mockGVisor) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *mockGVisor) Run(ctx context.Context, opts gvisor.ContainerOptions) (*gvisor.ContainerResult, error) {
	m.mu.Lock()
	m.calls++
	failMode := m.failMode
	runErr := m.runErr
	delay := m.delay
	result := m.result
	runFn := m.runFn
	m.lastOpts = opts
	m.mu.Unlock()

	if runFn != nil {
		return runFn(ctx, opts)
	}

	if delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}

	if runErr != nil {
		return nil, runErr
	}

	switch failMode {
	case "crash":
		return nil, fmt.Errorf("gvisor: container crashed unexpectedly")
	case "oom":
		return nil, fmt.Errorf("gvisor: container killed by OOM")
	case "timeout":
		return nil, fmt.Errorf("gvisor: container execution timed out")
	}

	if result != nil {
		r := *result
		return &r, nil
	}
	return &gvisor.ContainerResult{ExitCode: 0, Status: gvisor.StatusExited}, nil
}

func (m *mockGVisor) CleanupStale(_ context.Context) (int, error) { return 0, nil }
func (m *mockGVisor) ActiveContainers() int                       { return 0 }
func (m *mockGVisor) Close() error {
	m.closedMu.Lock()
	defer m.closedMu.Unlock()
	m.closed = true
	return nil
}

func (m *mockGVisor) IsClosed() bool {
	m.closedMu.Lock()
	defer m.closedMu.Unlock()
	return m.closed
}

// newTestService creates a SandboxHostService with mock ZFS and gVisor,
// pre-seeded with a base snapshot at "pool/bases/test@v1".
func newTestService(t testing.TB) *SandboxHostService {
	t.Helper()
	zm := zfs.NewMockManager()
	gm := newMockGVisor()
	return newTestServiceWith(t, zm, gm)
}

// newTestServiceWith creates a SandboxHostService with the given ZFS and gVisor mocks,
// pre-seeded with a base snapshot at "pool/bases/test@v1".
func newTestServiceWith(t testing.TB, zm zfs.ZFSManager, gm gvisor.GVisorManager) *SandboxHostService {
	t.Helper()
	ctx := context.Background()

	// Seed the base dataset and snapshot (only if zm is a MockManager).
	if mock, ok := zm.(*zfs.MockManager); ok {
		if err := mock.CreateDataset(ctx, "pool/bases/test", zfs.DatasetOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err := mock.CreateSnapshot(ctx, "pool/bases/test", "v1"); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultServiceConfig()
	cfg.PoolName = "pool"
	cfg.BasesDataset = "pool/bases"
	cfg.SessionsDataset = "pool/sessions"
	cfg.ToolTimeout = 0
	cfg.PauseDrainTimeout = 50 * time.Millisecond
	cfg.ShutdownTimeout = 50 * time.Millisecond
	svc, err := NewSandboxHostService(cfg, zm, gm, nil)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// createTestSession creates a session with a real temp directory containing
// test files, so Tier 1 tool execution works against real files.
func createTestSession(t testing.TB, svc *SandboxHostService, id string) *SessionInfo {
	t.Helper()
	ctx := context.Background()

	reqID := id
	if reqID == "" {
		reqID = fmt.Sprintf("test-%d", time.Now().UnixNano())
	}

	info, err := svc.CreateSession(ctx, CreateSessionRequest{
		BaseSnapshot: "pool/bases/test@v1",
		SessionID:    reqID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Patch mountpoint to a real temp dir with test files.
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("test content"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		fname := fmt.Sprintf("file%d.txt", i)
		if err := os.WriteFile(filepath.Join(tmpDir, fname), []byte(fmt.Sprintf("content %d", i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sess, err := svc.getSession(reqID)
	if err != nil {
		t.Fatal(err)
	}
	sess.mu.Lock()
	sess.mountpoint = tmpDir
	sess.mu.Unlock()
	info.Mountpoint = tmpDir
	return info
}
