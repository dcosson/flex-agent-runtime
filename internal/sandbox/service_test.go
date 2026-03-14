package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"h2-agent-runtime/internal/sandbox/gvisor"
	"h2-agent-runtime/internal/sandbox/zfs"
)

type fakeGVisor struct {
	mu     sync.Mutex
	calls  int
	last   gvisor.ContainerOptions
	result *gvisor.ContainerResult
	runErr error
	closed bool
}

func (f *fakeGVisor) Run(_ context.Context, opts gvisor.ContainerOptions) (*gvisor.ContainerResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.last = opts
	if f.runErr != nil {
		return nil, f.runErr
	}
	if f.result != nil {
		return f.result, nil
	}
	return &gvisor.ContainerResult{ExitCode: 0}, nil
}

func (f *fakeGVisor) CleanupStale(context.Context) (int, error) { return 0, nil }
func (f *fakeGVisor) ActiveContainers() int                     { return 0 }
func (f *fakeGVisor) Close() error {
	f.closed = true
	return nil
}

func seedBase(t *testing.T, m *zfs.MockManager) string {
	t.Helper()
	ctx := context.Background()
	if err := m.CreateDataset(ctx, "tank/bases/repo-v1", zfs.DatasetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateSnapshot(ctx, "tank/bases/repo-v1", "initial"); err != nil {
		t.Fatal(err)
	}
	return "tank/bases/repo-v1@initial"
}

func newServiceForTest(t *testing.T) (*SandboxHostService, *zfs.MockManager, *fakeGVisor) {
	t.Helper()
	zm := zfs.NewMockManager()
	gm := &fakeGVisor{result: &gvisor.ContainerResult{ExitCode: 0, Stdout: []byte("ok")}}
	cfg := DefaultServiceConfig()
	cfg.PoolName = "tank"
	cfg.BasesDataset = "tank/bases"
	cfg.SessionsDataset = "tank/sessions"
	cfg.ToolTimeout = 0
	cfg.PauseDrainTimeout = 50 * time.Millisecond
	cfg.ShutdownTimeout = 50 * time.Millisecond
	svc := NewSandboxHostService(cfg, zm, gm, nil)
	return svc, zm, gm
}

func TestSessionLifecycle(t *testing.T) {
	svc, zm, _ := newServiceForTest(t)
	base := seedBase(t, zm)
	ctx := context.Background()

	info, err := svc.CreateSession(ctx, CreateSessionRequest{BaseSnapshot: base, SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if info.State != SessionActive {
		t.Fatalf("state = %s", info.State)
	}
	if err := svc.PauseSession(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExecuteTool(ctx, ExecuteToolRequest{SessionID: "s1", ToolName: "bash", Params: map[string]any{"cmd": "echo x"}}); !errors.Is(err, ErrSessionPaused) {
		t.Fatalf("expected ErrSessionPaused, got %v", err)
	}
	if err := svc.ResumeSession(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.DestroySession(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetSession(ctx, "s1"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestCreateSessionTOCTOUCapacityGuard(t *testing.T) {
	svc, zm, _ := newServiceForTest(t)
	base := seedBase(t, zm)
	svc.config.MaxSessions = 1
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.CreateSession(ctx, CreateSessionRequest{BaseSnapshot: base, SessionID: fmt.Sprintf("s%d", i)})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)

	success := 0
	maxErr := 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, ErrMaxSessionsReached) {
			maxErr++
		}
	}
	if success != 1 || maxErr != 1 {
		t.Fatalf("success=%d maxErr=%d", success, maxErr)
	}
}

func TestTierRouting(t *testing.T) {
	svc, _, gm := newServiceForTest(t)
	ctx := context.Background()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc.sessions.Store("s1", &Session{id: "s1", state: SessionActive, dataset: "tank/sessions/s1", mountpoint: tmp, created: time.Now()})

	resp1, err := svc.ExecuteTool(ctx, ExecuteToolRequest{SessionID: "s1", ToolName: "read_file", Params: map[string]any{"path": "a.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp1.Tier != int(Tier1) || resp1.Content == "" {
		t.Fatalf("unexpected tier1 response: %+v", resp1)
	}

	resp2, err := svc.ExecuteTool(ctx, ExecuteToolRequest{SessionID: "s1", ToolName: "bash", Params: map[string]any{"cmd": "echo hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp2.Tier != int(Tier2) {
		t.Fatalf("unexpected tier2 response: %+v", resp2)
	}
	if gm.calls == 0 {
		t.Fatalf("expected gvisor Run to be called")
	}
}

func TestTurnCompleteProspectivePatternAndRollbackGuard(t *testing.T) {
	svc, zm, _ := newServiceForTest(t)
	base := seedBase(t, zm)
	ctx := context.Background()
	_, err := svc.CreateSession(ctx, CreateSessionRequest{BaseSnapshot: base, SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}

	res1, err := svc.TurnComplete(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if res1.TurnNumber != 1 || res1.SnapshotID != "turn-0001" {
		t.Fatalf("unexpected turn1: %+v", res1)
	}
	res2, err := svc.TurnComplete(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if res2.TurnNumber != 2 || res2.SnapshotID != "turn-0002" {
		t.Fatalf("unexpected turn2: %+v", res2)
	}

	s, _ := svc.getSession("s1")
	s.activeTools.Store(1)
	if err := svc.RollbackSession(ctx, "s1", "turn-0001"); !errors.Is(err, ErrToolsInFlight) {
		t.Fatalf("expected ErrToolsInFlight, got %v", err)
	}
	s.activeTools.Store(0)
	if err := svc.RollbackSession(ctx, "s1", "turn-0001"); err != nil {
		t.Fatal(err)
	}
	info, _ := svc.GetSession(ctx, "s1")
	if info.TurnCount != 1 {
		t.Fatalf("turnCount = %d, want 1", info.TurnCount)
	}
}

func TestShutdownClosesGVisor(t *testing.T) {
	svc, zm, gm := newServiceForTest(t)
	base := seedBase(t, zm)
	ctx := context.Background()
	_, _ = svc.CreateSession(ctx, CreateSessionRequest{BaseSnapshot: base, SessionID: "s1"})
	if err := svc.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if !gm.closed {
		t.Fatalf("expected gvisor Close")
	}
}
