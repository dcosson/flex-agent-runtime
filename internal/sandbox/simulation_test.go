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

// DS1. Full Session Lifecycle Simulation
func TestDS1_FullLifecycleSimulation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	sess := createTestSession(t, svc, "lifecycle")

	// 10 turns, 5 tools each
	for turn := 0; turn < 10; turn++ {
		for tool := 0; tool < 5; tool++ {
			_, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
				SessionID: sess.ID,
				ToolName:  "read_file",
				Params:    map[string]any{"path": fmt.Sprintf("file%d.txt", tool)},
			})
			if err != nil {
				t.Fatalf("turn %d, tool %d: %v", turn, tool, err)
			}
		}
		result, err := svc.TurnComplete(ctx, sess.ID)
		if err != nil {
			t.Fatalf("TurnComplete %d: %v", turn, err)
		}
		if result.TurnNumber != turn+1 {
			t.Fatalf("TurnNumber = %d, want %d", result.TurnNumber, turn+1)
		}
	}

	// Verify 10 snapshots
	snaps, err := svc.ListSnapshots(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 10 {
		t.Fatalf("snapshot count = %d, want 10", len(snaps))
	}

	// Rollback to turn 5
	err = svc.RollbackSession(ctx, sess.ID, snaps[4].Name)
	if err != nil {
		t.Fatal(err)
	}

	snapsAfter, err := svc.ListSnapshots(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapsAfter) != 5 {
		t.Fatalf("after rollback: snapshot count = %d, want 5", len(snapsAfter))
	}

	info, _ := svc.GetSession(ctx, sess.ID)
	if info.TurnCount != 5 {
		t.Fatalf("turnCount = %d, want 5", info.TurnCount)
	}

	// 10 more tool calls
	for i := 0; i < 10; i++ {
		_, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
			SessionID: sess.ID,
			ToolName:  "read_file",
			Params:    map[string]any{"path": "test.txt"},
		})
		if err != nil {
			t.Fatalf("post-rollback tool %d: %v", i, err)
		}
	}

	// Pause / Resume
	if err := svc.PauseSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	_, err = svc.ExecuteTool(ctx, ExecuteToolRequest{
		SessionID: sess.ID, ToolName: "read_file",
		Params: map[string]any{"path": "test.txt"},
	})
	if !errors.Is(err, ErrSessionPaused) {
		t.Fatalf("expected ErrSessionPaused, got %v", err)
	}

	if err := svc.ResumeSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	_, err = svc.ExecuteTool(ctx, ExecuteToolRequest{
		SessionID: sess.ID, ToolName: "read_file",
		Params: map[string]any{"path": "test.txt"},
	})
	if err != nil {
		t.Fatalf("after resume: %v", err)
	}

	// Destroy
	if err := svc.DestroySession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	_, err = svc.GetSession(ctx, sess.ID)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

// DS2. Concurrent Multi-Session Simulation
func TestDS2_ConcurrentMultiSession(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	const sessions = 10
	var wg sync.WaitGroup
	errs := make(chan error, sessions)

	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			sess := createTestSession(t, svc, fmt.Sprintf("multi-%03d", id))

			for turn := 0; turn < 5; turn++ {
				_, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
					SessionID: sess.ID,
					ToolName:  "read_file",
					Params:    map[string]any{"path": "test.txt"},
				})
				if err != nil {
					errs <- fmt.Errorf("session %d, turn %d, tool: %v", id, turn, err)
					return
				}
				_, err = svc.TurnComplete(ctx, sess.ID)
				if err != nil {
					errs <- fmt.Errorf("session %d, turn %d, complete: %v", id, turn, err)
					return
				}
			}

			if err := svc.DestroySession(ctx, sess.ID); err != nil {
				errs <- fmt.Errorf("session %d destroy: %v", id, err)
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// All sessions should be gone
	list, _ := svc.ListSessions(ctx)
	if len(list) != 0 {
		t.Fatalf("leaked sessions: %d", len(list))
	}
}

// DS3. Graceful Shutdown Simulation
func TestDS3_GracefulShutdownSimulation(t *testing.T) {
	gm := &mockGVisor{}
	toolStarted := make(chan struct{}, 5)
	gm.runFn = func(ctx context.Context, _ gvisor.ContainerOptions) (*gvisor.ContainerResult, error) {
		select {
		case toolStarted <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
			return &gvisor.ContainerResult{ExitCode: 0}, nil
		}
	}

	svc := newTestServiceWith(t, zfs.NewMockManager(), gm)
	svc.config.ShutdownTimeout = 200 * time.Millisecond
	ctx := context.Background()

	// Create 3 sessions
	for i := 0; i < 3; i++ {
		createTestSession(t, svc, fmt.Sprintf("shutdown-%d", i))
	}

	// Start tools on each session
	var toolWg sync.WaitGroup
	for i := 0; i < 3; i++ {
		toolWg.Add(1)
		go func(id int) {
			defer toolWg.Done()
			svc.ExecuteTool(ctx, ExecuteToolRequest{
				SessionID: fmt.Sprintf("shutdown-%d", id),
				ToolName:  "bash",
				Params:    map[string]any{"cmd": "work"},
			})
		}(i)
	}

	// Wait for at least some tools to start
	<-toolStarted

	// Shutdown should drain and destroy all sessions
	if err := svc.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	toolWg.Wait()

	// All sessions should be gone
	list, _ := svc.ListSessions(ctx)
	if len(list) != 0 {
		t.Fatalf("leaked sessions after shutdown: %d", len(list))
	}

	// gVisor should be closed
	if !gm.IsClosed() {
		t.Fatal("expected gVisor to be closed after shutdown")
	}
}
