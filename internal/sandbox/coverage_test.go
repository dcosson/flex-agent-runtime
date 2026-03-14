package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"h2-agent-runtime/internal/sandbox/gvisor"
	"h2-agent-runtime/internal/sandbox/zfs"
)

// Coverage tests for functions/branches missed by the main test suite.

func TestMergeDefaultConfig(t *testing.T) {
	// mergeDefaultConfig is called when SnapshotPrefix is empty
	cfg := ServiceConfig{} // all zero values
	svc := NewSandboxHostService(cfg, zfs.NewMockManager(), newMockGVisor(), nil)

	if svc.config.SnapshotPrefix != "turn" {
		t.Errorf("SnapshotPrefix = %q, want 'turn'", svc.config.SnapshotPrefix)
	}
	if svc.config.ToolTimeout != 5*time.Minute {
		t.Errorf("ToolTimeout = %v, want 5m", svc.config.ToolTimeout)
	}
	if svc.config.PoolSpaceWarnThreshold != 0.85 {
		t.Errorf("PoolSpaceWarnThreshold = %f, want 0.85", svc.config.PoolSpaceWarnThreshold)
	}
	if svc.config.PoolSpaceCritThreshold != 0.95 {
		t.Errorf("PoolSpaceCritThreshold = %f, want 0.95", svc.config.PoolSpaceCritThreshold)
	}
	if svc.config.HealthCheckInterval != 30*time.Second {
		t.Errorf("HealthCheckInterval = %v, want 30s", svc.config.HealthCheckInterval)
	}
	if svc.config.PauseDrainTimeout != 30*time.Second {
		t.Errorf("PauseDrainTimeout = %v, want 30s", svc.config.PauseDrainTimeout)
	}
	if svc.config.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 30s", svc.config.ShutdownTimeout)
	}
}

func TestHealthCheck(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Create some sessions
	for i := 0; i < 3; i++ {
		createTestSession(t, svc, "hc-"+string(rune('a'+i)))
	}

	hs, err := svc.HealthCheck(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hs.Status != "healthy" {
		t.Errorf("status = %q, want healthy", hs.Status)
	}
	if hs.SessionCount != 3 {
		t.Errorf("sessionCount = %d, want 3", hs.SessionCount)
	}
	if hs.Uptime <= 0 {
		t.Error("uptime should be positive")
	}
}

func TestHealthCheckDegraded(t *testing.T) {
	zm := zfs.NewMockManager()
	gm := newMockGVisor()
	svc := newTestServiceWith(t, zm, gm)

	// PoolSpace failure returns unhealthy status (not an error)
	zm.SetError("PoolSpace", errors.New("zfs: pool I/O error"))
	hs, err := svc.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck should not return error, got %v", err)
	}
	if hs.Status != "unhealthy" {
		t.Errorf("status = %q, want unhealthy", hs.Status)
	}
	if len(hs.Errors) == 0 {
		t.Error("expected errors in health status")
	}
	zm.ClearError("PoolSpace")

	// PoolStatus failure also returns unhealthy status
	zm.SetError("PoolStatus", errors.New("zfs: pool status error"))
	hs, err = svc.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck should not return error, got %v", err)
	}
	if hs.Status != "unhealthy" {
		t.Errorf("status = %q, want unhealthy", hs.Status)
	}
	if len(hs.Errors) == 0 {
		t.Error("expected errors in health status")
	}
	zm.ClearError("PoolStatus")
}

func TestGetActiveSession(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	sess := createTestSession(t, svc, "active-test")

	// Active session
	s, err := svc.getActiveSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if s.id != sess.ID {
		t.Fatalf("id = %s, want %s", s.id, sess.ID)
	}

	// Paused session
	if err := svc.PauseSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	_, err = svc.getActiveSession(sess.ID)
	if !errors.Is(err, ErrSessionPaused) {
		t.Fatalf("expected ErrSessionPaused, got %v", err)
	}

	// Resume and destroy
	if err := svc.ResumeSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}

	// Set to destroying state manually
	sessObj, _ := svc.getSession(sess.ID)
	sessObj.mu.Lock()
	sessObj.state = SessionDestroying
	sessObj.mu.Unlock()

	_, err = svc.getActiveSession(sess.ID)
	if !errors.Is(err, ErrSessionDestroying) {
		t.Fatalf("expected ErrSessionDestroying, got %v", err)
	}

	// Non-existent session
	_, err = svc.getActiveSession("nonexistent")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}

	// Failed state
	sessObj.mu.Lock()
	sessObj.state = SessionFailed
	sessObj.mu.Unlock()
	_, err = svc.getActiveSession(sess.ID)
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState, got %v", err)
	}
}

func TestCreateSnapshotExplicit(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	sess := createTestSession(t, svc, "snap-explicit")

	result, err := svc.CreateSnapshot(ctx, sess.ID, "manual-checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	if result.SnapshotID != "manual-checkpoint" {
		t.Errorf("snapshotID = %q, want 'manual-checkpoint'", result.SnapshotID)
	}

	// Verify it shows up in list
	snaps, err := svc.ListSnapshots(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range snaps {
		if s.Name == "manual-checkpoint" {
			found = true
		}
	}
	if !found {
		t.Fatal("manual-checkpoint not found in snapshot list")
	}

	// Non-existent session
	_, err = svc.CreateSnapshot(ctx, "nonexistent", "x")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestBuildTier2Command_AllTools(t *testing.T) {
	tests := []struct {
		tool   string
		params map[string]any
		want   []string
	}{
		{"bash", map[string]any{"cmd": "echo hello"}, []string{"/bin/bash", "-c", "echo hello"}},
		{"git_add", map[string]any{"path": "file.txt"}, []string{"git", "add", "file.txt"}},
		{"git_add", map[string]any{}, []string{"git", "add", "."}},
		{"git_commit", map[string]any{"message": "fix bug"}, []string{"git", "commit", "-m", "fix bug"}},
		{"git_push", map[string]any{"args": []any{"origin", "main"}}, []string{"git", "push", "origin", "main"}},
		{"git_clone", map[string]any{"args": []any{"https://example.com/repo.git"}}, []string{"git", "clone", "https://example.com/repo.git"}},
		{"git_fetch", map[string]any{}, []string{"git", "fetch"}},
		{"git_pull", map[string]any{}, []string{"git", "pull"}},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			cmd, err := buildTier2Command(tt.tool, tt.params)
			if err != nil {
				t.Fatal(err)
			}
			if len(cmd) != len(tt.want) {
				t.Fatalf("cmd = %v, want %v", cmd, tt.want)
			}
			for i := range cmd {
				if cmd[i] != tt.want[i] {
					t.Fatalf("cmd[%d] = %q, want %q", i, cmd[i], tt.want[i])
				}
			}
		})
	}
}

func TestBuildTier2Command_Errors(t *testing.T) {
	// bash with empty cmd
	_, err := buildTier2Command("bash", map[string]any{"cmd": ""})
	if err == nil {
		t.Fatal("expected error for empty bash cmd")
	}

	// bash with whitespace cmd
	_, err = buildTier2Command("bash", map[string]any{"cmd": "   "})
	if err == nil {
		t.Fatal("expected error for whitespace bash cmd")
	}

	// git_commit with empty message
	_, err = buildTier2Command("git_commit", map[string]any{"message": ""})
	if err == nil {
		t.Fatal("expected error for empty commit message")
	}

	// unknown tool
	_, err = buildTier2Command("delete_everything", map[string]any{})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestCopyLabels(t *testing.T) {
	// nil input
	result := copyLabels(nil)
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}

	// empty map
	result = copyLabels(map[string]string{})
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}

	// non-empty map
	original := map[string]string{"key": "val"}
	result = copyLabels(original)
	if result["key"] != "val" {
		t.Fatalf("expected val, got %q", result["key"])
	}
	// Modify original shouldn't affect copy
	original["key"] = "changed"
	if result["key"] != "val" {
		t.Fatal("copy was mutated by original")
	}
}

func TestBlocksToText(t *testing.T) {
	// empty blocks
	if result := blocksToText(nil); result != "" {
		t.Fatalf("expected empty, got %q", result)
	}
}

func TestTotalActiveTools(t *testing.T) {
	svc := newTestService(t)

	// No sessions — should be 0
	total := svc.totalActiveTools()
	if total != 0 {
		t.Fatalf("totalActiveTools = %d, want 0", total)
	}

	// Create sessions and set active tools
	sess1 := createTestSession(t, svc, "active1")
	sess2 := createTestSession(t, svc, "active2")

	s1, _ := svc.getSession(sess1.ID)
	s2, _ := svc.getSession(sess2.ID)
	s1.activeTools.Store(3)
	s2.activeTools.Store(2)

	total = svc.totalActiveTools()
	if total != 5 {
		t.Fatalf("totalActiveTools = %d, want 5", total)
	}
}

func TestExecuteToolRollingBackGuard(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	sess := createTestSession(t, svc, "rollback-guard")

	// Set rollingBack flag
	s, _ := svc.getSession(sess.ID)
	s.mu.Lock()
	s.rollingBack = true
	s.mu.Unlock()

	_, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
		SessionID: sess.ID,
		ToolName:  "read_file",
		Params:    map[string]any{"path": "test.txt"},
	})
	if !errors.Is(err, ErrRollbackInProgress) {
		t.Fatalf("expected ErrRollbackInProgress during rollback, got %v", err)
	}
}

func TestExecuteToolPerToolSnapshots(t *testing.T) {
	svc := newTestService(t)
	svc.config.PerToolSnapshots = true
	ctx := context.Background()

	sess := createTestSession(t, svc, "per-tool-snap")

	resp, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
		SessionID:  sess.ID,
		ToolName:   "read_file",
		ToolCallID: "call-001",
		Params:     map[string]any{"path": "test.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.SnapshotID == "" {
		t.Fatal("expected per-tool snapshot ID")
	}
}

func TestCreateSessionBaseSnapshotRequired(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.CreateSession(context.Background(), CreateSessionRequest{})
	if err == nil {
		t.Fatal("expected error when base snapshot is empty")
	}
}

func TestCreateSessionDuplicate(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	createTestSession(t, svc, "dup")

	_, err := svc.CreateSession(ctx, CreateSessionRequest{
		BaseSnapshot: "pool/bases/test@v1",
		SessionID:    "dup",
	})
	if !errors.Is(err, ErrSessionExists) {
		t.Fatalf("expected ErrSessionExists, got %v", err)
	}
}

func TestTurnCompleteOnPausedSession(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	sess := createTestSession(t, svc, "tc-paused")
	if err := svc.PauseSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}

	_, err := svc.TurnComplete(ctx, sess.ID)
	if err == nil {
		t.Fatal("expected error on TurnComplete for paused session")
	}
}

func TestListSnapshotsNonexistent(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.ListSnapshots(context.Background(), "nonexistent")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestRollbackNonexistent(t *testing.T) {
	svc := newTestService(t)
	err := svc.RollbackSession(context.Background(), "nonexistent", "snap")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestResumeNonPausedSession(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	sess := createTestSession(t, svc, "resume-active")

	err := svc.ResumeSession(ctx, sess.ID)
	if err == nil {
		t.Fatal("expected error resuming active session")
	}
}

func TestPauseNonActiveSession(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	sess := createTestSession(t, svc, "pause-paused")
	if err := svc.PauseSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}

	err := svc.PauseSession(ctx, sess.ID)
	if err == nil {
		t.Fatal("expected error pausing already-paused session")
	}
}

func TestDestroyNonexistentSession(t *testing.T) {
	svc := newTestService(t)
	err := svc.DestroySession(context.Background(), "nonexistent")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestExecuteToolTier2WithCustomResources(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	sess := createTestSession(t, svc, "custom-res")

	customRes := gvisor.ResourceSpec{CPUs: 2.0, MemoryMB: 512}
	gm := svc.gvisor.(*mockGVisor)

	_, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
		SessionID: sess.ID,
		ToolName:  "bash",
		Params:    map[string]any{"cmd": "echo hi"},
		Resources: &customRes,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify the custom resources were passed through
	gm.mu.Lock()
	lastOpts := gm.lastOpts
	gm.mu.Unlock()
	if lastOpts.Resources.CPUs != 2.0 {
		t.Errorf("CPUs = %f, want 2.0", lastOpts.Resources.CPUs)
	}
	if lastOpts.Resources.MemoryMB != 512 {
		t.Errorf("MemoryMB = %d, want 512", lastOpts.Resources.MemoryMB)
	}
}

func TestExecuteToolDestroyingSession(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	sess := createTestSession(t, svc, "destroying")

	s, _ := svc.getSession(sess.ID)
	s.mu.Lock()
	s.state = SessionDestroying
	s.mu.Unlock()

	_, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
		SessionID: sess.ID,
		ToolName:  "read_file",
		Params:    map[string]any{"path": "test.txt"},
	})
	if err == nil {
		t.Fatal("expected error on destroying session")
	}
}

func TestShutdownWithNilGVisor(t *testing.T) {
	zm := zfs.NewMockManager()
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
	svc := NewSandboxHostService(cfg, zm, nil, nil)

	err := svc.Shutdown(ctx)
	if err != nil {
		t.Fatalf("shutdown with nil gvisor: %v", err)
	}
}
