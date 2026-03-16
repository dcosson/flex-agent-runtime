package environment_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2-agent-runtime/internal/ai"
	"h2-agent-runtime/internal/sandbox/environment"
	"h2-agent-runtime/internal/sandbox/environment/local"
	"h2-agent-runtime/internal/sandbox/environment/native"
)

func TestF1_NativeRPCFailureHandling(t *testing.T) {
	t.Run("stream open failure", func(t *testing.T) {
		svc := newComplianceMockService()
		svc.streamErr = fmt.Errorf("connection refused")
		env := native.NewNativeSandboxEnvironment(svc, native.DefaultConfig(), slog.Default())
		if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "f1-open"}); err != nil {
			t.Fatalf("create env: %v", err)
		}
		_, err := env.ExecuteTool(context.Background(), environment.ToolRequest{ToolName: "bash", ToolCallID: "tc"}, nil)
		if err == nil || !strings.Contains(err.Error(), "native sandbox: execute tool") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("recv failure", func(t *testing.T) {
		svc := newComplianceMockService()
		svc.streamRecvErr = fmt.Errorf("stream interrupted")
		svc.streamProgressOnly = true
		env := native.NewNativeSandboxEnvironment(svc, native.DefaultConfig(), slog.Default())
		if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "f1-recv"}); err != nil {
			t.Fatalf("create env: %v", err)
		}
		_, err := env.ExecuteTool(context.Background(), environment.ToolRequest{ToolName: "bash", ToolCallID: "tc"}, nil)
		if err == nil || !strings.Contains(err.Error(), "native sandbox: stream recv") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestF2_CreateFailureRecovery(t *testing.T) {
	svc := newComplianceMockService()
	svc.streamErr = fmt.Errorf("unavailable")
	env := native.NewNativeSandboxEnvironment(svc, native.DefaultConfig(), slog.Default())
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "f2"}); err != nil {
		t.Fatalf("create should succeed with compliance service: %v", err)
	}
	_, err := env.ExecuteTool(context.Background(), environment.ToolRequest{ToolName: "bash", ToolCallID: "tc"}, nil)
	if err == nil {
		t.Fatal("expected execute failure after injected stream open error")
	}
}

func TestF3_ContextCancellationMidExecution(t *testing.T) {
	svc := newComplianceMockService()
	svc.streamErr = context.Canceled
	env := native.NewNativeSandboxEnvironment(svc, native.DefaultConfig(), slog.Default())
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "f3"}); err != nil {
		t.Fatalf("create env: %v", err)
	}
	_, err := env.ExecuteTool(context.Background(), environment.ToolRequest{ToolName: "bash", ToolCallID: "tc"}, nil)
	if err == nil {
		t.Fatal("expected canceled error")
	}
}

func TestF4_ResponseTypeMappingErrorPath(t *testing.T) {
	svc := newComplianceMockService()
	svc.streamRecvErr = fmt.Errorf("eof")
	svc.streamProgressOnly = true
	env := native.NewNativeSandboxEnvironment(svc, native.DefaultConfig(), slog.Default())
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "f4"}); err != nil {
		t.Fatalf("create env: %v", err)
	}
	_, err := env.ExecuteTool(context.Background(), environment.ToolRequest{ToolName: "bash", ToolCallID: "tc"}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestO1_O2_NativeTypeMappingGolden(t *testing.T) {
	svc := newComplianceMockService()
	env := native.NewNativeSandboxEnvironment(svc, native.DefaultConfig(), slog.Default())
	if err := env.Create(context.Background(), environment.SessionConfig{
		SessionID: "golden",
		BaseImage: "pool/base@v1",
		Labels:    map[string]string{"lane": "oracle"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if svc.lastCreateReq == nil || svc.lastCreateReq.BaseSnapshot != "pool/base@v1" || svc.lastCreateReq.SessionID != "golden" {
		t.Fatalf("unexpected create request mapping: %+v", svc.lastCreateReq)
	}
	resp, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
		ToolCallID: "tc-golden",
		ToolName:   "read_file",
		Params:     map[string]any{"path": "x.txt"},
	}, nil)
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if svc.lastExecReq == nil || svc.lastExecReq.SessionID != "golden" || svc.lastExecReq.ToolCallID != "tc-golden" {
		t.Fatalf("unexpected execute mapping: %+v", svc.lastExecReq)
	}
	if len(resp.Content) == 0 {
		t.Fatal("expected mapped response content")
	}
}

func TestSEC1_SessionIDValidation(t *testing.T) {
	localEnv := local.NewLocalEnvironment(t.TempDir(), slog.Default())
	if err := localEnv.Create(context.Background(), environment.SessionConfig{}); err == nil {
		t.Fatal("local Create() should reject empty SessionID")
	}

	nativeEnv := native.NewNativeSandboxEnvironment(newComplianceMockService(), native.DefaultConfig(), slog.Default())
	if err := nativeEnv.Create(context.Background(), environment.SessionConfig{}); err == nil {
		t.Fatal("native Create() should reject empty SessionID")
	}
}

func TestSEC2_LocalPathTraversalSanitization(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "safe.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	env := local.NewLocalEnvironment(root, slog.Default())
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "sec-path"}); err != nil {
		t.Fatalf("create env: %v", err)
	}
	tests := []string{
		"",
		"../../../etc/passwd",
		`..\\windows\\system32`,
	}
	for _, p := range tests {
		resp, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
			ToolName: "read_file",
			Params:   map[string]any{"path": p},
		}, nil)
		if err != nil {
			t.Fatalf("unexpected execution error for path %q: %v", p, err)
		}
		if len(resp.Content) == 0 {
			t.Fatalf("expected validation/error content for path %q", p)
		}
		tc, ok := resp.Content[0].(*ai.TextContent)
		if !ok {
			t.Fatalf("unexpected content type for %q: %T", p, resp.Content[0])
		}
		if p == "" && !strings.Contains(tc.Text, "missing required parameter") {
			t.Fatalf("expected missing param error for empty path, got %q", tc.Text)
		}
		if p != "" && !strings.Contains(tc.Text, "escapes workspace root") && !strings.Contains(tc.Text, "file not found") {
			t.Fatalf("expected traversal-safe rejection for %q, got %q", p, tc.Text)
		}
	}
}
