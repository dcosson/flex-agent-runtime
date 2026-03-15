package environment_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"h2-agent-runtime/internal/sandbox/environment"
	"h2-agent-runtime/internal/sandbox/environment/local"
	"h2-agent-runtime/internal/sandbox/environment/native"
)

func TestST1_ConcurrentEnvironmentLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644); err != nil {
				t.Errorf("seed file: %v", err)
				return
			}
			env := local.NewLocalEnvironment(root, slog.Default())
			if err := env.Create(ctx, environment.SessionConfig{SessionID: fmt.Sprintf("st1-%d", i)}); err != nil {
				t.Errorf("create: %v", err)
				return
			}
			_, _ = env.ExecuteTool(ctx, environment.ToolRequest{ToolName: "read_file", Params: map[string]any{"path": "f.txt"}}, nil)
			_ = env.Destroy(ctx)
		}(i)
	}
	wg.Wait()
}

func TestST2_ConcurrentToolExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	env := local.NewLocalEnvironment(root, slog.Default())
	if err := env.Create(ctx, environment.SessionConfig{SessionID: "st2"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := env.ExecuteTool(ctx, environment.ToolRequest{
				ToolName: "read_file",
				Params:   map[string]any{"path": "readme.txt"},
			}, nil); err != nil {
				t.Errorf("execute: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestST3_RapidEnvironmentSwitching(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed file: %v", err)
		}
		localEnv := local.NewLocalEnvironment(root, slog.Default())
		if err := localEnv.Create(ctx, environment.SessionConfig{SessionID: fmt.Sprintf("local-%d", i)}); err != nil {
			t.Fatalf("local create: %v", err)
		}
		if _, err := localEnv.ExecuteTool(ctx, environment.ToolRequest{ToolName: "read_file", Params: map[string]any{"path": "f.txt"}}, nil); err != nil {
			t.Fatalf("local execute: %v", err)
		}
		if err := localEnv.Destroy(ctx); err != nil {
			t.Fatalf("local destroy: %v", err)
		}

		nativeEnv := native.NewNativeSandboxEnvironment(newComplianceMockService(), slog.Default())
		if err := nativeEnv.Create(ctx, environment.SessionConfig{SessionID: fmt.Sprintf("native-%d", i)}); err != nil {
			t.Fatalf("native create: %v", err)
		}
		if _, err := nativeEnv.ExecuteTool(ctx, environment.ToolRequest{ToolName: "read_file", ToolCallID: fmt.Sprintf("tc-%d", i)}, nil); err != nil {
			t.Fatalf("native execute: %v", err)
		}
		if err := nativeEnv.Destroy(ctx); err != nil {
			t.Fatalf("native destroy: %v", err)
		}
	}
}

func TestST4_SessionLimitEnforcement(t *testing.T) {
	// Current Local and Native capability constants use ConcurrentSessions=0
	// (unlimited), so this verifies declared behavior is stable.
	if environment.LocalCapabilities.ConcurrentSessions != 0 {
		t.Fatalf("LocalCapabilities.ConcurrentSessions = %d, want 0", environment.LocalCapabilities.ConcurrentSessions)
	}
	if environment.NativeSandboxCapabilities.ConcurrentSessions != 0 {
		t.Fatalf("NativeSandboxCapabilities.ConcurrentSessions = %d, want 0", environment.NativeSandboxCapabilities.ConcurrentSessions)
	}
}
