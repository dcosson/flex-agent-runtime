package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/gvisor"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/zfs"
	"github.com/dcosson/flex-agent-runtime/internal/tools"
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
	svc, err := NewSandboxHostService(cfg, zm, gm, nil)
	if err != nil {
		t.Fatal(err)
	}
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

func TestCreateSessionQuotaApplied(t *testing.T) {
	svc, zm, _ := newServiceForTest(t)
	base := seedBase(t, zm)
	ctx := context.Background()
	_, err := svc.CreateSession(ctx, CreateSessionRequest{BaseSnapshot: base, SessionID: "s1", Quota: 4096})
	if err != nil {
		t.Fatal(err)
	}
	info, err := zm.GetDatasetInfo(ctx, "tank/sessions/s1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Available != 4096 {
		t.Fatalf("quota not applied: available=%d", info.Available)
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
	if resp1.Tier != int(tools.Tier1) || resp1.Content == "" {
		t.Fatalf("unexpected tier1 response: %+v", resp1)
	}

	resp2, err := svc.ExecuteTool(ctx, ExecuteToolRequest{SessionID: "s1", ToolName: "bash", Params: map[string]any{"cmd": "echo hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp2.Tier != int(tools.Tier2) {
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

func TestRollbackFailureMarksSessionFailed(t *testing.T) {
	svc, zm, _ := newServiceForTest(t)
	base := seedBase(t, zm)
	ctx := context.Background()
	_, err := svc.CreateSession(ctx, CreateSessionRequest{BaseSnapshot: base, SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = svc.TurnComplete(ctx, "s1")
	zm.SetError("Rollback", errors.New("boom"))
	if err := svc.RollbackSession(ctx, "s1", "turn-0001"); err == nil {
		t.Fatalf("expected rollback error")
	}
	info, err := svc.GetSession(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if info.State != SessionFailed {
		t.Fatalf("expected failed state, got %s", info.State)
	}
}

func TestExecuteToolRollbackInProgress(t *testing.T) {
	svc, zm, _ := newServiceForTest(t)
	base := seedBase(t, zm)
	ctx := context.Background()
	_, err := svc.CreateSession(ctx, CreateSessionRequest{BaseSnapshot: base, SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := svc.getSession("s1")
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.rollingBack = true
	s.mu.Unlock()
	_, err = svc.ExecuteTool(ctx, ExecuteToolRequest{SessionID: "s1", ToolName: "bash", Params: map[string]any{"cmd": "echo x"}})
	if !errors.Is(err, ErrRollbackInProgress) {
		t.Fatalf("expected ErrRollbackInProgress, got %v", err)
	}
}

func TestHealthCheckReturnsUnhealthyStatusOnPoolErrors(t *testing.T) {
	svc, zm, _ := newServiceForTest(t)
	ctx := context.Background()
	zm.SetError("PoolSpace", errors.New("pool unavailable"))
	health, err := svc.HealthCheck(ctx)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if health.Status != "unhealthy" || len(health.Errors) == 0 {
		t.Fatalf("unexpected health response: %+v", health)
	}
}

func TestValidatePathBlocksEscapes(t *testing.T) {
	root := t.TempDir()
	if err := validatePath(root, "../escape"); err == nil {
		t.Fatalf("expected traversal error")
	}
	if err := validatePath(root, "ok/file.txt"); err != nil {
		t.Fatalf("expected valid relative path, got %v", err)
	}
	if err := validatePath(root, filepath.Join(root, "sub", "file.txt")); err != nil {
		t.Fatalf("expected valid absolute path in root, got %v", err)
	}
	if err := validatePath(root, "/tmp/outside-root"); err == nil {
		t.Fatalf("expected absolute outside path to fail")
	}
}

func TestBlocksToTextIncludesNonTextContent(t *testing.T) {
	out := blocksToText([]ai.ContentBlock{
		&ai.TextContent{Text: "hello"},
		&ai.ThinkingContent{Thinking: "thinking"},
		&ai.ToolCall{Name: "grep", Arguments: map[string]any{"path": "x"}},
		&ai.ImageContent{MimeType: "image/png", Data: "abc"},
	})
	if out == "" {
		t.Fatalf("expected non-empty output")
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "thinking") || !strings.Contains(out, "tool_call:grep") || !strings.Contains(out, "image:image/png") {
		t.Fatalf("unexpected output: %q", out)
	}
	// Ensure JSON encoding of tool args is valid content.
	lines := strings.Split(out, "\n")
	for _, ln := range lines {
		if !strings.HasPrefix(ln, "tool_call:") {
			continue
		}
		idx := strings.IndexByte(ln, ' ')
		if idx <= 0 || idx+1 >= len(ln) {
			t.Fatalf("unexpected tool_call format: %q", ln)
		}
		var tmp map[string]any
		if err := json.Unmarshal([]byte(ln[idx+1:]), &tmp); err != nil {
			t.Fatalf("tool_call args not valid json: %v", err)
		}
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
