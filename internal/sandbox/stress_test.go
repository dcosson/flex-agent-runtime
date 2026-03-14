package sandbox

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// ST1. High-Concurrency Session Churn
func TestST1_SessionChurnStress(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}

	svc := newTestService(t)
	ctx := context.Background()

	const goroutines = 50
	const opsPerGoroutine = 200

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				id := fmt.Sprintf("stress-%d-%d", gid, i)
				sess, err := svc.CreateSession(ctx, CreateSessionRequest{
					BaseSnapshot: "pool/bases/test@v1", SessionID: id,
				})
				if err != nil {
					continue
				}
				svc.ExecuteTool(ctx, ExecuteToolRequest{
					SessionID: sess.ID, ToolName: "read_file",
					Params: map[string]any{"path": "test.txt"},
				})
				svc.TurnComplete(ctx, sess.ID)
				svc.DestroySession(ctx, sess.ID)
			}
		}(g)
	}
	wg.Wait()

	list, _ := svc.ListSessions(ctx)
	if len(list) != 0 {
		t.Fatalf("leaked sessions: %d", len(list))
	}
}

// ST2. Long-Running Session Soak
func TestST2_LongRunningSessionSoak(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}

	svc := newTestService(t)
	svc.config.MaxSnapshotsPerSession = 20 // Enable snapshot cleanup
	ctx := context.Background()

	sess := createTestSession(t, svc, "soak")

	// 500 turns, 5 tool calls per turn, periodic rollbacks
	for turn := 0; turn < 500; turn++ {
		for tool := 0; tool < 5; tool++ {
			_, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
				SessionID: sess.ID,
				ToolName:  "read_file",
				Params:    map[string]any{"path": "test.txt"},
			})
			if err != nil {
				t.Fatalf("turn %d, tool %d: %v", turn, tool, err)
			}
		}

		_, err := svc.TurnComplete(ctx, sess.ID)
		if err != nil {
			t.Fatalf("TurnComplete %d: %v", turn, err)
		}

		// Periodic rollback every 50 turns (to turn - 5)
		if turn > 0 && turn%50 == 0 {
			snaps, err := svc.ListSnapshots(ctx, sess.ID)
			if err != nil {
				t.Fatalf("ListSnapshots at turn %d: %v", turn, err)
			}
			if len(snaps) > 5 {
				target := snaps[len(snaps)-6] // rollback 5 turns
				if err := svc.RollbackSession(ctx, sess.ID, target.Name); err != nil {
					t.Fatalf("rollback at turn %d: %v", turn, err)
				}
			}
		}
	}

	// Verify snapshot count is bounded by MaxSnapshotsPerSession
	snaps, _ := svc.ListSnapshots(ctx, sess.ID)
	if len(snaps) > svc.config.MaxSnapshotsPerSession {
		t.Fatalf("snapshot count %d exceeds max %d", len(snaps), svc.config.MaxSnapshotsPerSession)
	}

	// Session should still be active
	info, _ := svc.GetSession(ctx, sess.ID)
	if info.State != SessionActive {
		t.Fatalf("state = %s, want active after soak", info.State)
	}
}

// ST3. Burst Tool Execution
func TestST3_BurstToolExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}

	svc := newTestService(t)
	ctx := context.Background()

	sess := createTestSession(t, svc, "burst")

	const burstSize = 100
	var wg sync.WaitGroup
	results := make(chan error, burstSize)

	for i := 0; i < burstSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
				SessionID: sess.ID,
				ToolName:  "read_file",
				Params:    map[string]any{"path": "test.txt"},
			})
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	successCount := 0
	for err := range results {
		if err == nil {
			successCount++
		}
	}
	if successCount == 0 {
		t.Fatal("all burst tool calls failed")
	}

	// ActiveTools should be back to 0
	s, _ := svc.getSession(sess.ID)
	if active := s.activeTools.Load(); active != 0 {
		t.Fatalf("activeTools = %d after burst, want 0", active)
	}

	// Session should still be active
	info, _ := svc.GetSession(ctx, sess.ID)
	if info.State != SessionActive {
		t.Fatalf("state = %s after burst, want active", info.State)
	}
}
