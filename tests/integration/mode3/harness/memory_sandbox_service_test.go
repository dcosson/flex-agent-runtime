package harness

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/rpc"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/api"
	"github.com/dcosson/flex-agent-runtime/internal/tools"
)

func TestMemorySandboxService_InvalidStateTransitions(t *testing.T) {
	svc, sessionID := newTestMemoryService(t)
	ctx := context.Background()

	if _, err := svc.PauseSession(ctx, &api.PauseSessionRequest{SessionID: sessionID}); err != nil {
		t.Fatalf("pause active: %v", err)
	}
	if _, err := svc.PauseSession(ctx, &api.PauseSessionRequest{SessionID: sessionID}); err == nil {
		t.Fatal("expected pause-from-paused error")
	}
	if _, err := svc.ResumeSession(ctx, &api.ResumeSessionRequest{SessionID: sessionID}); err != nil {
		t.Fatalf("resume paused: %v", err)
	}
	if _, err := svc.ResumeSession(ctx, &api.ResumeSessionRequest{SessionID: sessionID}); err == nil {
		t.Fatal("expected resume-from-active error")
	}
}

func TestMemorySandboxService_RollbackTruncatesSnapshots(t *testing.T) {
	svc, sessionID := newTestMemoryService(t)
	ctx := context.Background()

	_, err := svc.ExecuteTool(ctx, &api.ExecuteToolRequest{SessionID: sessionID, ToolCallID: "w1", ToolName: "write_file", Params: map[string]any{"path": "state.txt", "content": "v1"}})
	if err != nil {
		t.Fatalf("write v1: %v", err)
	}
	s1, err := svc.CreateSnapshot(ctx, &api.CreateSnapshotRequest{SessionID: sessionID, Name: "snap-1"})
	if err != nil {
		t.Fatalf("snapshot 1: %v", err)
	}

	_, err = svc.ExecuteTool(ctx, &api.ExecuteToolRequest{SessionID: sessionID, ToolCallID: "w2", ToolName: "write_file", Params: map[string]any{"path": "state.txt", "content": "v2"}})
	if err != nil {
		t.Fatalf("write v2: %v", err)
	}
	if _, err = svc.CreateSnapshot(ctx, &api.CreateSnapshotRequest{SessionID: sessionID, Name: "snap-2"}); err != nil {
		t.Fatalf("snapshot 2: %v", err)
	}

	if _, err := svc.RollbackSession(ctx, &api.RollbackSessionRequest{SessionID: sessionID, SnapshotID: s1.SnapshotID}); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	snaps, err := svc.ListSnapshots(ctx, &api.ListSnapshotsRequest{SessionID: sessionID})
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snaps.Snapshots) != 1 || snaps.Snapshots[0].Name != "snap-1" {
		t.Fatalf("unexpected snapshots after rollback: %+v", snaps.Snapshots)
	}
	got, ok := svc.ReadSessionFile(sessionID, "state.txt")
	if !ok || got != "v1" {
		t.Fatalf("rollback content mismatch: ok=%v got=%q", ok, got)
	}
}

func TestMemorySandboxService_ExecuteAndDestroyNoDeadlock(t *testing.T) {
	svc, sessionID := newTestMemoryService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errCh := make(chan error, 32)

	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.ExecuteTool(ctx, &api.ExecuteToolRequest{
				SessionID:  sessionID,
				ToolCallID: fmt.Sprintf("tc-%02d", i),
				ToolName:   "read_file",
				Params:     map[string]any{"path": "state.txt"},
			})
			if err != nil {
				var rpcErr *rpc.RPCError
				if !errors.As(err, &rpcErr) || (rpcErr.Code != rpc.CodeNotFound && rpcErr.Code != rpc.CodeFailedPrecondition) {
					errCh <- err
				}
			}
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		_, _ = svc.DestroySession(ctx, &api.DestroySessionRequest{SessionID: sessionID})
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("possible deadlock between execute and destroy")
	}

	close(errCh)
	for err := range errCh {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMemorySandboxService_UnknownSnapshotRollback(t *testing.T) {
	svc, sessionID := newTestMemoryService(t)
	_, err := svc.RollbackSession(context.Background(), &api.RollbackSessionRequest{SessionID: sessionID, SnapshotID: "missing"})
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeNotFound {
		t.Fatalf("expected not_found rollback error, got %v", err)
	}
}

func TestMemorySandboxService_TierClassification(t *testing.T) {
	svc, sessionID := newTestMemoryService(t)
	ctx := context.Background()

	_, err := svc.ExecuteTool(ctx, &api.ExecuteToolRequest{SessionID: sessionID, ToolCallID: "t1", ToolName: "read_file", Params: map[string]any{"path": "state.txt"}})
	if err != nil {
		t.Fatalf("tier1: %v", err)
	}
	_, err = svc.ExecuteTool(ctx, &api.ExecuteToolRequest{SessionID: sessionID, ToolCallID: "t2", ToolName: "bash", Params: map[string]any{"cmd": "echo ok"}})
	if err != nil {
		t.Fatalf("tier2: %v", err)
	}

	routes := svc.Routes()
	if len(routes) < 2 {
		t.Fatalf("expected at least 2 routes, got %d", len(routes))
	}
	if routes[0].Tier != int(tools.Tier1) {
		t.Fatalf("expected first route tier1, got %d", routes[0].Tier)
	}
	if routes[1].Tier != int(tools.Tier2) {
		t.Fatalf("expected second route tier2, got %d", routes[1].Tier)
	}
}

func TestMemorySandboxService_TurnCompleteReleasesLock(t *testing.T) {
	svc, sessionID := newTestMemoryService(t)
	ctx := context.Background()

	if _, err := svc.TurnComplete(ctx, &api.TurnCompleteRequest{SessionID: sessionID}); err != nil {
		t.Fatalf("turn complete #1: %v", err)
	}
	if _, err := svc.TurnComplete(ctx, &api.TurnCompleteRequest{SessionID: sessionID}); err != nil {
		t.Fatalf("turn complete #2: %v", err)
	}
	if _, err := svc.GetSession(ctx, &api.GetSessionRequest{SessionID: sessionID}); err != nil {
		t.Fatalf("get session after turn complete: %v", err)
	}
}

func newTestMemoryService(t *testing.T) (*MemorySandboxService, string) {
	t.Helper()
	svc := NewMemorySandboxService()
	svc.SeedBaseSnapshot("memory/base@initial", map[string]string{"state.txt": "base"})
	resp, err := svc.CreateSession(context.Background(), &api.CreateSessionRequest{SessionID: "s1", BaseSnapshot: "memory/base@initial"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return svc, resp.Session.ID
}
