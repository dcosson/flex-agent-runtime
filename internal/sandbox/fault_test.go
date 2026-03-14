package sandbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"h2-agent-runtime/internal/sandbox/gvisor"
	"h2-agent-runtime/internal/sandbox/zfs"
)

// FI1. ZFS Failure During Session Creation
func TestFI1_ZFSFailureDuringCreate(t *testing.T) {
	zm := zfs.NewMockManager()
	gm := newMockGVisor()
	svc := newTestServiceWith(t, zm, gm)

	zm.SetError("CloneFromSnapshot", fmt.Errorf("zfs: no space left on device"))

	_, err := svc.CreateSession(context.Background(), CreateSessionRequest{
		BaseSnapshot: "pool/bases/test@v1",
	})
	if err == nil {
		t.Fatal("expected error from ZFS failure")
	}

	// Verify no orphaned session
	sessions, err := svc.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions after failed create, got %d", len(sessions))
	}
}

// FI1b. ZFS GetMountpoint failure during create
func TestFI1b_ZFSMountpointFailureDuringCreate(t *testing.T) {
	zm := zfs.NewMockManager()
	gm := newMockGVisor()
	svc := newTestServiceWith(t, zm, gm)

	// Clone succeeds but GetMountpoint fails
	zm.SetError("GetMountpoint", fmt.Errorf("zfs: dataset not mounted"))

	_, err := svc.CreateSession(context.Background(), CreateSessionRequest{
		BaseSnapshot: "pool/bases/test@v1",
	})
	if err == nil {
		t.Fatal("expected error from mountpoint failure")
	}

	// Verify cleanup happened
	sessions, _ := svc.ListSessions(context.Background())
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions after failed create, got %d", len(sessions))
	}
}

// FI2. gVisor Failure During Tool Execution
func TestFI2_GVisorFailureDuringExec(t *testing.T) {
	for _, failMode := range []string{"crash", "oom", "timeout"} {
		t.Run(failMode, func(t *testing.T) {
			gm := newMockGVisor()
			gm.InjectFailure(failMode)

			svc := newTestServiceWith(t, zfs.NewMockManager(), gm)
			ctx := context.Background()

			sess := createTestSession(t, svc, "s1")

			_, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
				SessionID: sess.ID,
				ToolName:  "bash",
				Params:    map[string]any{"cmd": "echo hello"},
			})
			if err == nil {
				t.Fatal("expected error from gVisor failure")
			}

			// Session should still be active
			info, err := svc.GetSession(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			if info.State != SessionActive {
				t.Fatalf("state = %s, want active", info.State)
			}

			// ActiveTools should be 0
			s, _ := svc.getSession(sess.ID)
			if active := s.activeTools.Load(); active != 0 {
				t.Fatalf("activeTools = %d, want 0", active)
			}
		})
	}
}

// FI3. Snapshot Failure After Tool Execution
func TestFI3_SnapshotFailureDuringTurnComplete(t *testing.T) {
	zm := zfs.NewMockManager()
	gm := newMockGVisor()
	svc := newTestServiceWith(t, zm, gm)
	ctx := context.Background()

	sess := createTestSession(t, svc, "s1")

	// Successful turn first
	res1, err := svc.TurnComplete(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res1.TurnNumber != 1 {
		t.Fatalf("turn = %d, want 1", res1.TurnNumber)
	}

	// Inject snapshot failure
	zm.SetError("CreateSnapshot", fmt.Errorf("zfs: no space for snapshot"))

	_, err = svc.TurnComplete(ctx, sess.ID)
	if err == nil {
		t.Fatal("expected error from snapshot failure")
	}

	// Session should still work
	zm.ClearError("CreateSnapshot")
	_, err = svc.ExecuteTool(ctx, ExecuteToolRequest{
		SessionID: sess.ID,
		ToolName:  "read_file",
		Params:    map[string]any{"path": "test.txt"},
	})
	if err != nil {
		t.Fatalf("tool after snapshot failure should work: %v", err)
	}

	// Turn count should not have incremented on failure
	info, _ := svc.GetSession(ctx, sess.ID)
	if info.TurnCount != 1 {
		t.Fatalf("turnCount = %d, want 1 (should not increment on failure)", info.TurnCount)
	}
}

// FI4. Context Cancellation During Tool Execution
func TestFI4_ContextCancelDuringExec(t *testing.T) {
	gm := newSlowMockGVisor(5 * time.Second)
	svc := newTestServiceWith(t, zfs.NewMockManager(), gm)
	ctx := context.Background()

	sess := createTestSession(t, svc, "s1")

	execCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	_, err := svc.ExecuteTool(execCtx, ExecuteToolRequest{
		SessionID: sess.ID,
		ToolName:  "bash",
		Params:    map[string]any{"cmd": "sleep 100"},
	})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}

	// Session should still work with a fresh context
	gm.ClearFailure()
	gm.mu.Lock()
	gm.delay = 0
	gm.mu.Unlock()

	_, err = svc.ExecuteTool(ctx, ExecuteToolRequest{
		SessionID: sess.ID,
		ToolName:  "read_file",
		Params:    map[string]any{"path": "test.txt"},
	})
	if err != nil {
		t.Fatalf("tool after cancellation should work: %v", err)
	}

	// ActiveTools should be 0
	s, _ := svc.getSession(sess.ID)
	if active := s.activeTools.Load(); active != 0 {
		t.Fatalf("activeTools = %d, want 0", active)
	}
}

// FI5. Destroy During In-Flight Tool
func TestFI5_DestroyDuringInFlightTool(t *testing.T) {
	// Use a slow mock so we can destroy while a tool is running
	gm := &mockGVisor{}
	started := make(chan struct{})
	gm.runFn = func(ctx context.Context, _ gvisor.ContainerOptions) (*gvisor.ContainerResult, error) {
		close(started) // signal that the tool has started
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
			return &gvisor.ContainerResult{ExitCode: 0}, nil
		}
	}

	svc := newTestServiceWith(t, zfs.NewMockManager(), gm)
	svc.config.ShutdownTimeout = 200 * time.Millisecond
	ctx := context.Background()

	sess := createTestSession(t, svc, "s1")

	// Start a tool in the background
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		svc.ExecuteTool(ctx, ExecuteToolRequest{
			SessionID: sess.ID,
			ToolName:  "bash",
			Params:    map[string]any{"cmd": "sleep 100"},
		})
	}()

	// Wait for tool to start
	<-started

	// Verify activeTools > 0
	s, _ := svc.getSession(sess.ID)
	if active := s.activeTools.Load(); active == 0 {
		t.Fatal("expected activeTools > 0 while tool is running")
	}

	// Destroy should wait for drain (with timeout) then proceed
	err := svc.DestroySession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("destroy during in-flight: %v", err)
	}

	wg.Wait()

	// Session should be gone
	_, err = svc.GetSession(ctx, sess.ID)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

// FI6. Pool Exhaustion During Operations
func TestFI6_PoolExhaustionDuringOps(t *testing.T) {
	zm := zfs.NewMockManager()
	gm := newMockGVisor()
	svc := newTestServiceWith(t, zm, gm)
	ctx := context.Background()

	sess := createTestSession(t, svc, "s1")

	// Inject snapshot failure to simulate pool exhaustion
	zm.SetError("CreateSnapshot", fmt.Errorf("zfs: no space left on device"))

	_, err := svc.TurnComplete(ctx, sess.ID)
	if err == nil {
		t.Fatal("expected error from pool exhaustion")
	}

	// Existing session should still work for reads (Tier 1)
	zm.ClearError("CreateSnapshot")
	_, err = svc.ExecuteTool(ctx, ExecuteToolRequest{
		SessionID: sess.ID,
		ToolName:  "read_file",
		Params:    map[string]any{"path": "test.txt"},
	})
	if err != nil {
		t.Fatalf("tier 1 tool should still work: %v", err)
	}
}
