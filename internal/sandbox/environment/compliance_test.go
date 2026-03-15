package environment_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"h2-agent-runtime/internal/sandbox/environment"
	"h2-agent-runtime/internal/sandbox/environment/local"
)

type envFactory func(t *testing.T) environment.ExecutionEnvironment

func runEnvironmentComplianceSuite(t *testing.T, mkEnv envFactory, config environment.SessionConfig, readReq environment.ToolRequest, writeReq environment.ToolRequest) {
	t.Helper()
	ctx := context.Background()

	t.Run("Capabilities", func(t *testing.T) {
		caps := mkEnv(t).Capabilities()
		_ = caps.Snapshots
		_ = caps.Pause
	})

	t.Run("FullLifecycle", func(t *testing.T) {
		env := mkEnv(t)
		if err := env.Create(ctx, config); err != nil {
			t.Fatalf("Create() error: %v", err)
		}
		resp, err := env.ExecuteTool(ctx, readReq, nil)
		if err != nil {
			t.Fatalf("ExecuteTool() error: %v", err)
		}
		if resp == nil {
			t.Fatal("ExecuteTool() returned nil response")
		}
		if err := env.Destroy(ctx); err != nil {
			t.Fatalf("Destroy() error: %v", err)
		}
	})

	t.Run("PauseResume", func(t *testing.T) {
		env := mkEnv(t)
		if err := env.Create(ctx, config); err != nil {
			t.Fatalf("Create() error: %v", err)
		}
		if !env.Capabilities().Pause {
			if err := env.Pause(ctx); !errors.Is(err, environment.ErrCapabilityNotSupported) {
				t.Fatalf("Pause() error = %v, want ErrCapabilityNotSupported", err)
			}
			if err := env.Resume(ctx); !errors.Is(err, environment.ErrCapabilityNotSupported) {
				t.Fatalf("Resume() error = %v, want ErrCapabilityNotSupported", err)
			}
			return
		}
		if err := env.Pause(ctx); err != nil {
			t.Fatalf("Pause() error: %v", err)
		}
		if err := env.Resume(ctx); err != nil {
			t.Fatalf("Resume() error: %v", err)
		}
	})

	t.Run("SnapshotRollback", func(t *testing.T) {
		env := mkEnv(t)
		if err := env.Create(ctx, config); err != nil {
			t.Fatalf("Create() error: %v", err)
		}
		if !env.Capabilities().Rollback {
			if err := env.Rollback(ctx, "any"); !errors.Is(err, environment.ErrCapabilityNotSupported) {
				t.Fatalf("Rollback() error = %v, want ErrCapabilityNotSupported", err)
			}
			return
		}
		if _, err := env.ExecuteTool(ctx, writeReq, nil); err != nil {
			t.Fatalf("ExecuteTool(write) error: %v", err)
		}
		snap, err := env.CreateSnapshot(ctx, "pre-change")
		if err != nil {
			t.Fatalf("CreateSnapshot() error: %v", err)
		}
		if snap == nil || snap.ID == "" {
			t.Fatal("CreateSnapshot() returned empty snapshot")
		}
		if err := env.Rollback(ctx, snap.ID); err != nil {
			t.Fatalf("Rollback() error: %v", err)
		}
	})

	t.Run("DestroyedEnvironmentErrors", func(t *testing.T) {
		env := mkEnv(t)
		if err := env.Create(ctx, config); err != nil {
			t.Fatalf("Create() error: %v", err)
		}
		if err := env.Destroy(ctx); err != nil {
			t.Fatalf("Destroy() error: %v", err)
		}
		_, err := env.ExecuteTool(ctx, readReq, nil)
		if err == nil {
			t.Fatal("ExecuteTool() after destroy succeeded, want error")
		}
	})
}

func TestLocalEnvironmentComplianceSuite(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "input.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	factory := func(_ *testing.T) environment.ExecutionEnvironment {
		return local.NewLocalEnvironment(root, slog.Default())
	}
	config := environment.SessionConfig{SessionID: "local-compliance"}
	readReq := environment.ToolRequest{
		ToolName: "read_file",
		Params:   map[string]any{"path": "input.txt"},
	}
	writeReq := environment.ToolRequest{
		ToolName: "write_file",
		Params:   map[string]any{"path": "written.txt", "content": "hi"},
	}

	runEnvironmentComplianceSuite(t, factory, config, readReq, writeReq)
}
