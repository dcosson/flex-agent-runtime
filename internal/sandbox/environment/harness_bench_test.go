package environment_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/dcosson/flex-agent-runtime/internal/rpc/api"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment/local"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment/native"
)

func BenchmarkB1_EnvironmentSelectionLatency(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < b.N; i++ {
		_ = local.NewLocalEnvironment(root, slog.Default())
	}
}

func BenchmarkB2_NativeAdapterOverhead(b *testing.B) {
	svc := newComplianceMockService()
	env := native.NewNativeSandboxEnvironment(svc, native.DefaultConfig(), slog.Default())
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "bench-native"}); err != nil {
		b.Fatalf("create env: %v", err)
	}
	req := &api.ExecuteToolRequest{
		SessionID:  "bench-native",
		ToolCallID: "tc",
		ToolName:   "read_file",
		Params:     map[string]any{"path": "x.txt"},
	}

	b.Run("direct-service", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			stream, err := svc.ExecuteToolStream(context.Background(), req)
			if err != nil {
				b.Fatalf("execute stream: %v", err)
			}
			if _, err := stream.Recv(); err != nil {
				b.Fatalf("recv: %v", err)
			}
			_ = stream.Close()
		}
	})
	b.Run("via-environment", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
				ToolCallID: "tc",
				ToolName:   "read_file",
				Params:     map[string]any{"path": "x.txt"},
			}, nil); err != nil {
				b.Fatalf("execute tool: %v", err)
			}
		}
	})
}

func BenchmarkB3_CapabilityCheckLatency(b *testing.B) {
	root := b.TempDir()
	env := local.NewLocalEnvironment(root, slog.Default())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = env.Capabilities()
	}
}

func BenchmarkB4_TypeConversionOverhead(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "bench.txt"), []byte("hello\n"), 0o644); err != nil {
		b.Fatalf("seed file: %v", err)
	}
	env := local.NewLocalEnvironment(root, slog.Default())
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "bench-local"}); err != nil {
		b.Fatalf("create env: %v", err)
	}
	req := environment.ToolRequest{
		ToolCallID: "tc",
		ToolName:   "read_file",
		Params:     map[string]any{"path": "bench.txt"},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := env.ExecuteTool(context.Background(), req, nil); err != nil {
			b.Fatalf("execute: %v", err)
		}
	}
}
