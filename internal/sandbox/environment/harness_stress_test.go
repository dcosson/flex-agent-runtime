package environment_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"flex-agent-runtime/internal/sandbox/environment"
	"flex-agent-runtime/internal/sandbox/environment/local"
	"flex-agent-runtime/internal/sandbox/environment/native"
)

func TestST1_ConcurrentEnvironmentLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
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
	for i := 0; i < 100; i++ {
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
	const perType = 10
	envs := make([]environment.ExecutionEnvironment, 0, perType*2)

	for i := 0; i < perType; i++ {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed file: %v", err)
		}
		localEnv := local.NewLocalEnvironment(root, slog.Default())
		if err := localEnv.Create(ctx, environment.SessionConfig{SessionID: fmt.Sprintf("local-%d", i)}); err != nil {
			t.Fatalf("local create: %v", err)
		}
		envs = append(envs, localEnv)
	}
	for i := 0; i < perType; i++ {
		nativeEnv := native.NewNativeSandboxEnvironment(newComplianceMockService(), native.DefaultConfig(), slog.Default())
		if err := nativeEnv.Create(ctx, environment.SessionConfig{SessionID: fmt.Sprintf("native-%d", i)}); err != nil {
			t.Fatalf("native create: %v", err)
		}
		envs = append(envs, nativeEnv)
	}

	var wg sync.WaitGroup
	for i, env := range envs {
		wg.Add(1)
		go func(i int, env environment.ExecutionEnvironment) {
			defer wg.Done()
			req := environment.ToolRequest{ToolName: "read_file", ToolCallID: fmt.Sprintf("tc-%d", i)}
			// local envs need a concrete file path; native mock ignores params.
			if i < perType {
				req.Params = map[string]any{"path": "f.txt"}
			}
			if _, err := env.ExecuteTool(ctx, req, nil); err != nil {
				t.Errorf("execute[%d]: %v", i, err)
			}
		}(i, env)
	}
	wg.Wait()
	for i, env := range envs {
		if err := env.Destroy(ctx); err != nil {
			t.Fatalf("destroy[%d]: %v", i, err)
		}
	}
}

func TestST4_SessionLimitEnforcement(t *testing.T) {
	// Current Local and Native capability constants use ConcurrentSessions=0
	// (unlimited), so this verifies declared behavior is stable.
	if environment.LocalCapabilities.ConcurrentSessions != 0 {
		t.Fatalf("LocalCapabilities.ConcurrentSessions = %d, want 0", environment.LocalCapabilities.ConcurrentSessions)
	}
	nativeEnv := native.NewNativeSandboxEnvironment(newComplianceMockService(), native.DefaultConfig(), slog.Default())
	if nativeEnv.Capabilities().ConcurrentSessions != 0 {
		t.Fatalf("native capabilities ConcurrentSessions = %d, want 0", nativeEnv.Capabilities().ConcurrentSessions)
	}
}
