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
	"h2-agent-runtime/internal/sandbox/environment/native"
)

func newLocalHarnessEnv(t *testing.T) (environment.ExecutionEnvironment, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "input.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	env := local.NewLocalEnvironment(root, slog.Default())
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "local-harness"}); err != nil {
		t.Fatalf("create local env: %v", err)
	}
	return env, root
}

func newNativeHarnessEnv(t *testing.T) environment.ExecutionEnvironment {
	t.Helper()
	svc := newComplianceMockService()
	env := native.NewNativeSandboxEnvironment(svc, native.DefaultConfig(), slog.Default())
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "native-harness"}); err != nil {
		t.Fatalf("create native env: %v", err)
	}
	return env
}

func TestP1_InterfaceCompleteness(t *testing.T) {
	ctx := context.Background()
	envs := []environment.ExecutionEnvironment{
		func() environment.ExecutionEnvironment { e, _ := newLocalHarnessEnv(t); return e }(),
		newNativeHarnessEnv(t),
	}
	for _, env := range envs {
		caps := env.Capabilities()
		if !caps.Snapshots {
			if _, err := env.CreateSnapshot(ctx, "any"); !errors.Is(err, environment.ErrCapabilityNotSupported) {
				t.Fatalf("CreateSnapshot error = %v, want ErrCapabilityNotSupported", err)
			}
		}
		if !caps.Rollback {
			if err := env.Rollback(ctx, "any"); !errors.Is(err, environment.ErrCapabilityNotSupported) {
				t.Fatalf("Rollback error = %v, want ErrCapabilityNotSupported", err)
			}
		}
		if caps.Pause {
			if err := env.Pause(ctx); err != nil {
				t.Fatalf("Pause error: %v", err)
			}
			if err := env.Resume(ctx); err != nil {
				t.Fatalf("Resume error: %v", err)
			}
		} else {
			if err := env.Pause(ctx); !errors.Is(err, environment.ErrCapabilityNotSupported) {
				t.Fatalf("Pause error = %v, want ErrCapabilityNotSupported", err)
			}
			if err := env.Resume(ctx); !errors.Is(err, environment.ErrCapabilityNotSupported) {
				t.Fatalf("Resume error = %v, want ErrCapabilityNotSupported", err)
			}
		}
	}
}

func TestP2_StateMachineConsistency(t *testing.T) {
	ctx := context.Background()

	localEnv, _ := newLocalHarnessEnv(t)
	if got := localEnv.State(); got != environment.StateActive {
		t.Fatalf("local state after create = %q, want active", got)
	}
	if err := localEnv.Pause(ctx); err != nil {
		t.Fatalf("local pause: %v", err)
	}
	if got := localEnv.State(); got != environment.StateActive {
		t.Fatalf("local pause should keep active state, got %q", got)
	}
	if err := localEnv.Destroy(ctx); err != nil {
		t.Fatalf("local destroy: %v", err)
	}
	if got := localEnv.State(); got != environment.StateDestroyed {
		t.Fatalf("local state after destroy = %q, want destroyed", got)
	}
	if _, err := localEnv.ExecuteTool(ctx, environment.ToolRequest{ToolName: "read_file", Params: map[string]any{"path": "input.txt"}}, nil); !errors.Is(err, environment.ErrNotActive) {
		t.Fatalf("local execute after destroy = %v, want ErrNotActive", err)
	}

	nativeEnv := newNativeHarnessEnv(t)
	if got := nativeEnv.State(); got != environment.StateActive {
		t.Fatalf("native state after create = %q, want active", got)
	}
	if err := nativeEnv.Pause(ctx); err != nil {
		t.Fatalf("native pause: %v", err)
	}
	if got := nativeEnv.State(); got != environment.StatePaused {
		t.Fatalf("native state after pause = %q, want paused", got)
	}
	if err := nativeEnv.Resume(ctx); err != nil {
		t.Fatalf("native resume: %v", err)
	}
	if got := nativeEnv.State(); got != environment.StateActive {
		t.Fatalf("native state after resume = %q, want active", got)
	}
}

func TestP3_ToolExecutionDeterminism(t *testing.T) {
	ctx := context.Background()

	localEnv, _ := newLocalHarnessEnv(t)
	req := environment.ToolRequest{ToolName: "read_file", Params: map[string]any{"path": "input.txt"}}
	resp1, err1 := localEnv.ExecuteTool(ctx, req, nil)
	resp2, err2 := localEnv.ExecuteTool(ctx, req, nil)
	if err1 != nil || err2 != nil {
		t.Fatalf("local execute errs: %v / %v", err1, err2)
	}
	if len(resp1.Content) != len(resp2.Content) {
		t.Fatalf("local content length mismatch: %d != %d", len(resp1.Content), len(resp2.Content))
	}

	nativeEnv := newNativeHarnessEnv(t)
	resp3, err3 := nativeEnv.ExecuteTool(ctx, environment.ToolRequest{ToolName: "read_file", ToolCallID: "tc-1"}, nil)
	resp4, err4 := nativeEnv.ExecuteTool(ctx, environment.ToolRequest{ToolName: "read_file", ToolCallID: "tc-1"}, nil)
	if err3 != nil || err4 != nil {
		t.Fatalf("native execute errs: %v / %v", err3, err4)
	}
	if len(resp3.Content) != len(resp4.Content) {
		t.Fatalf("native content length mismatch: %d != %d", len(resp3.Content), len(resp4.Content))
	}
}

func TestP4_CapabilitiesStatic(t *testing.T) {
	ctx := context.Background()
	envs := []environment.ExecutionEnvironment{
		func() environment.ExecutionEnvironment { e, _ := newLocalHarnessEnv(t); return e }(),
		newNativeHarnessEnv(t),
	}

	for _, env := range envs {
		c1 := env.Capabilities()
		_, _ = env.ExecuteTool(ctx, environment.ToolRequest{ToolName: "read_file", Params: map[string]any{"path": "input.txt"}}, nil)
		_ = env.Pause(ctx)
		_ = env.Resume(ctx)
		c2 := env.Capabilities()
		if c1 != c2 {
			t.Fatalf("Capabilities changed: before=%+v after=%+v", c1, c2)
		}
	}
}

func TestP5_NativeSandboxParityWithDirectServicePath(t *testing.T) {
	ctx := context.Background()
	svc := newComplianceMockService()
	env := native.NewNativeSandboxEnvironment(svc, native.DefaultConfig(), slog.Default())
	if err := env.Create(ctx, environment.SessionConfig{SessionID: "native-parity"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	req := environment.ToolRequest{
		ToolCallID: "tc-parity",
		ToolName:   "read_file",
		Params:     map[string]any{"path": "parity.txt"},
	}
	envResp, envErr := env.ExecuteTool(ctx, req, nil)

	stream, directErr := svc.ExecuteToolStream(ctx, svc.lastExecReq)
	if (envErr == nil) != (directErr == nil) {
		t.Fatalf("error mismatch env=%v direct=%v", envErr, directErr)
	}
	if directErr != nil {
		t.Fatalf("unexpected direct execute error: %v", directErr)
	}
	msg, recvErr := stream.Recv()
	if recvErr != nil {
		t.Fatalf("direct stream recv: %v", recvErr)
	}
	directResp := msg.Response
	if directResp == nil {
		t.Fatal("direct path returned nil response")
	}

	if len(envResp.Content) == 0 || directResp.Content == "" {
		t.Fatalf("expected non-empty responses env=%+v direct=%+v", envResp, directResp)
	}
}
