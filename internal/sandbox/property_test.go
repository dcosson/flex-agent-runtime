package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"h2-agent-runtime/internal/sandbox/zfs"

	"pgregory.net/rapid"
)

// shadowState tracks expected session state for property-based validation.
type shadowState struct {
	state     SessionState
	turnCount int
	snapCount int
	destroyed bool
}

func (ss *shadowState) expectResult(op int) (expectErr bool) {
	switch op {
	case 0: // ExecuteTool
		return ss.state != SessionActive
	case 1: // TurnComplete
		return ss.state != SessionActive
	case 2: // Pause
		return ss.state != SessionActive
	case 3: // Resume
		return ss.state != SessionPaused
	case 4: // Rollback
		return ss.state != SessionActive || ss.snapCount == 0
	case 5: // Destroy
		return ss.destroyed
	}
	return false
}

func (ss *shadowState) applySuccess(op int) {
	switch op {
	case 1: // TurnComplete
		ss.turnCount++
		ss.snapCount++
	case 2: // Pause
		ss.state = SessionPaused
	case 3: // Resume
		ss.state = SessionActive
	case 4: // Rollback — snapshot count is updated based on rollback target
		// handled externally since we need to know the target index
	case 5: // Destroy
		ss.destroyed = true
	}
}

var rapidSessionCounter atomic.Uint64

// rapidCreateSession creates a service and session for use inside rapid.Check.
// It avoids testing.TB methods that rapid.T doesn't have (like TempDir).
func rapidCreateSession(t *rapid.T) (svc *SandboxHostService, info *SessionInfo, cleanup func()) {
	zm := zfs.NewMockManager()
	gm := newMockGVisor()
	ctx := context.Background()
	if err := zm.CreateDataset(ctx, "pool/bases/test", zfs.DatasetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := zm.CreateSnapshot(ctx, "pool/bases/test", "v1"); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultServiceConfig()
	cfg.PoolName = "pool"
	cfg.BasesDataset = "pool/bases"
	cfg.SessionsDataset = "pool/sessions"
	cfg.ToolTimeout = 0
	cfg.PauseDrainTimeout = 50 * time.Millisecond
	cfg.ShutdownTimeout = 50 * time.Millisecond
	svc = NewSandboxHostService(cfg, zm, gm, nil)

	id := fmt.Sprintf("rapid-%d", rapidSessionCounter.Add(1))
	var err error
	info, err = svc.CreateSession(ctx, CreateSessionRequest{
		BaseSnapshot: "pool/bases/test@v1",
		SessionID:    id,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Patch mountpoint to a real temp dir with test files.
	tmpDir, err := os.MkdirTemp("", "rapid-session-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("test content"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := svc.getSession(id)
	if err != nil {
		t.Fatal(err)
	}
	sess.mu.Lock()
	sess.mountpoint = tmpDir
	sess.mu.Unlock()
	info.Mountpoint = tmpDir

	return svc, info, func() { os.RemoveAll(tmpDir) }
}

// P1. Session State Machine Validity
func TestPropertySessionStateMachine(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		svc, sess, cleanup := rapidCreateSession(t)
		defer cleanup()
		ctx := context.Background()
		shadow := &shadowState{state: SessionActive}

		ops := rapid.IntRange(1, 30).Draw(t, "opCount")
		for i := 0; i < ops; i++ {
			if shadow.destroyed {
				break
			}
			op := rapid.IntRange(0, 5).Draw(t, fmt.Sprintf("op-%d", i))
			expectErr := shadow.expectResult(op)

			var opErr error
			switch op {
			case 0: // ExecuteTool
				_, opErr = svc.ExecuteTool(ctx, ExecuteToolRequest{
					SessionID: sess.ID, ToolName: "read_file",
					Params: map[string]any{"path": "test.txt"},
				})
			case 1: // TurnComplete
				_, opErr = svc.TurnComplete(ctx, sess.ID)
			case 2: // Pause
				opErr = svc.PauseSession(ctx, sess.ID)
			case 3: // Resume
				opErr = svc.ResumeSession(ctx, sess.ID)
			case 4: // Rollback
				snaps, _ := svc.ListSnapshots(ctx, sess.ID)
				if len(snaps) == 0 {
					// Shadow model predicts error for rollback with no snapshots.
					// We skip the actual call because RollbackSession transitions
					// to SessionFailed on ZFS errors (even for nonexistent targets),
					// which would make the session unusable for subsequent ops.
					continue
				}
				targetIdx := rapid.IntRange(0, len(snaps)-1).Draw(t, fmt.Sprintf("rollback-target-%d", i))
				opErr = svc.RollbackSession(ctx, sess.ID, snaps[targetIdx].Name)
				if opErr == nil {
					shadow.snapCount = targetIdx + 1
					s, _ := svc.getSession(sess.ID)
					s.mu.RLock()
					shadow.turnCount = s.turnCount
					s.mu.RUnlock()
				}
				continue // skip generic applySuccess
			case 5: // Destroy
				opErr = svc.DestroySession(ctx, sess.ID)
				if opErr == nil {
					shadow.applySuccess(op)
				}
				return // session is gone
			}

			if expectErr {
				if opErr == nil && op != 0 {
					t.Fatalf("op %d (type %d) should fail in state %s but succeeded",
						i, op, shadow.state)
				}
			} else {
				if opErr == nil {
					shadow.applySuccess(op)
				}
				// Tool calls may fail for non-state reasons (file not found)
				if op != 0 && opErr != nil {
					t.Fatalf("op %d (type %d) should succeed in state %s: %v",
						i, op, shadow.state, opErr)
				}
			}

			// Verify actual state matches shadow
			info, err := svc.GetSession(ctx, sess.ID)
			if err == nil {
				if info.State != shadow.state {
					t.Fatalf("state mismatch after op %d (type %d): got %s, want %s",
						i, op, info.State, shadow.state)
				}
			}
		}
	})
}

// P2. Tier Classification Completeness
func TestPropertyTierClassificationCompleteness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.String().Draw(t, "toolName")
		tier := ClassifyTool(name)
		if tier != Tier1 && tier != Tier2 {
			t.Fatalf("invalid tier %d for tool %q", tier, name)
		}
	})
}

// P2 supplement: known tools have expected tiers.
func TestTierClassificationKnownTools(t *testing.T) {
	tier1Tools := []string{"read_file", "write_file", "edit_file", "grep", "glob", "git_status", "git_diff", "git_log", "git_show"}
	tier2Tools := []string{"bash", "git_push", "git_clone", "git_fetch", "git_pull", "git_add", "git_commit"}

	for _, tool := range tier1Tools {
		if tier := ClassifyTool(tool); tier != Tier1 {
			t.Errorf("ClassifyTool(%q) = %d, want Tier1", tool, tier)
		}
	}
	for _, tool := range tier2Tools {
		if tier := ClassifyTool(tool); tier != Tier2 {
			t.Errorf("ClassifyTool(%q) = %d, want Tier2", tool, tier)
		}
	}
	if tier := ClassifyTool("unknown_tool"); tier != Tier2 {
		t.Errorf("ClassifyTool(unknown_tool) = %d, want Tier2", tier)
	}
}

// P3. Snapshot Ordering Consistency
func TestPropertySnapshotOrderingConsistency(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		svc, sess, cleanup := rapidCreateSession(t)
		defer cleanup()
		ctx := context.Background()

		n := rapid.IntRange(1, 50).Draw(t, "turns")

		// Track snapshot names in creation order
		snapNames := make([]string, 0, n)
		for i := 0; i < n; i++ {
			result, err := svc.TurnComplete(ctx, sess.ID)
			if err != nil {
				t.Fatalf("TurnComplete %d: %v", i, err)
			}
			if result.TurnNumber != i+1 {
				t.Fatalf("TurnNumber = %d, want %d", result.TurnNumber, i+1)
			}
			snapNames = append(snapNames, result.SnapshotID)
		}

		snaps, err := svc.ListSnapshots(ctx, sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(snaps) != n {
			t.Fatalf("snapshot count = %d, want %d", len(snaps), n)
		}

		// Verify non-decreasing creation order
		for i := 1; i < len(snaps); i++ {
			if snaps[i].Creation.Before(snaps[i-1].Creation) {
				t.Fatalf("snapshot %d (%s) created before snapshot %d (%s)",
					i, snaps[i].Creation, i-1, snaps[i-1].Creation)
			}
		}

		// Rollback to a random snapshot by name (avoids index/sort-order mismatch)
		if n > 2 {
			k := rapid.IntRange(0, n-2).Draw(t, "rollbackTarget")
			targetSnap := snapNames[k]
			if err := svc.RollbackSession(ctx, sess.ID, targetSnap); err != nil {
				t.Fatalf("rollback to %s: %v", targetSnap, err)
			}

			// Verify session turnCount matches
			info, _ := svc.GetSession(ctx, sess.ID)
			if info.TurnCount != k+1 {
				t.Fatalf("after rollback: turnCount = %d, want %d", info.TurnCount, k+1)
			}

			// Verify target snapshot still exists in ZFS
			snapsAfter, err := svc.ListSnapshots(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, s := range snapsAfter {
				if s.Name == targetSnap {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("target snapshot %s not found after rollback", targetSnap)
			}

			// All remaining ZFS snapshots should have been created at or before the target
			if len(snapsAfter) > k+1 {
				t.Fatalf("after rollback to index %d: ZFS snapshot count = %d, want <= %d",
					k, len(snapsAfter), k+1)
			}
		}
	})
}

// P4. Concurrent Tool Execution Safety
func TestPropertyConcurrentToolSafety(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		svc, sess, cleanup := rapidCreateSession(t)
		defer cleanup()
		ctx := context.Background()

		concurrency := rapid.IntRange(2, 20).Draw(t, "concurrency")

		var wg sync.WaitGroup
		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				svc.ExecuteTool(ctx, ExecuteToolRequest{
					SessionID: sess.ID,
					ToolName:  "read_file",
					Params:    map[string]any{"path": "test.txt"},
				})
			}()
		}
		wg.Wait()

		// ActiveTools should be 0 after all complete
		s, err := svc.getSession(sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		if active := s.activeTools.Load(); active != 0 {
			t.Fatalf("activeTools = %d after all tools completed, want 0", active)
		}

		info, err := svc.GetSession(ctx, sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		if info.State != SessionActive {
			t.Fatalf("state = %s, want active", info.State)
		}
	})
}

// P5. Session Capacity Enforcement
func TestPropertySessionCapacityEnforcement(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		zm := zfs.NewMockManager()
		gm := newMockGVisor()
		ctx := context.Background()
		if err := zm.CreateDataset(ctx, "pool/bases/test", zfs.DatasetOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err := zm.CreateSnapshot(ctx, "pool/bases/test", "v1"); err != nil {
			t.Fatal(err)
		}
		cfg := DefaultServiceConfig()
		cfg.PoolName = "pool"
		cfg.BasesDataset = "pool/bases"
		cfg.SessionsDataset = "pool/sessions"
		cfg.ToolTimeout = 0
		svc := NewSandboxHostService(cfg, zm, gm, nil)

		maxSessions := rapid.IntRange(1, 10).Draw(t, "maxSessions")
		svc.config.MaxSessions = maxSessions

		ids := make([]string, 0, maxSessions)
		for i := 0; i < maxSessions; i++ {
			id := fmt.Sprintf("cap-%d-%d", rapidSessionCounter.Add(1), i)
			_, err := svc.CreateSession(ctx, CreateSessionRequest{
				BaseSnapshot: "pool/bases/test@v1",
				SessionID:    id,
			})
			if err != nil {
				t.Fatalf("session %d should succeed: %v", i, err)
			}
			ids = append(ids, id)
		}

		// Next creation should fail
		_, err := svc.CreateSession(ctx, CreateSessionRequest{
			BaseSnapshot: "pool/bases/test@v1",
			SessionID:    fmt.Sprintf("overflow-%d", rapidSessionCounter.Add(1)),
		})
		if err == nil {
			t.Fatal("expected ErrMaxSessionsReached")
		}

		// Destroy one, should free a slot
		destroyIdx := rapid.IntRange(0, maxSessions-1).Draw(t, "destroyIdx")
		if err := svc.DestroySession(ctx, ids[destroyIdx]); err != nil {
			t.Fatalf("destroy: %v", err)
		}

		_, err = svc.CreateSession(ctx, CreateSessionRequest{
			BaseSnapshot: "pool/bases/test@v1",
			SessionID:    fmt.Sprintf("freed-%d", rapidSessionCounter.Add(1)),
		})
		if err != nil {
			t.Fatalf("session after destroy should succeed: %v", err)
		}
	})
}
