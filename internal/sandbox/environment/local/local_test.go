package local

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropics/flex-agent-runtime/internal/ai"
	"github.com/anthropics/flex-agent-runtime/internal/sandbox/environment"
	"github.com/anthropics/flex-agent-runtime/internal/tools"
)

func TestLocalEnvironmentLifecycleAndState(t *testing.T) {
	root := t.TempDir()
	env := NewLocalEnvironment(root, slog.Default())
	ctx := context.Background()

	if err := env.Create(ctx, environment.SessionConfig{SessionID: "sess-1"}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if got := env.State(); got != environment.StateActive {
		t.Fatalf("State() = %q, want %q", got, environment.StateActive)
	}
	if err := env.Pause(ctx); err != nil {
		t.Fatalf("Pause() error: %v", err)
	}
	if err := env.Resume(ctx); err != nil {
		t.Fatalf("Resume() error: %v", err)
	}
	if err := env.Destroy(ctx); err != nil {
		t.Fatalf("Destroy() error: %v", err)
	}
	if got := env.State(); got != environment.StateDestroyed {
		t.Fatalf("State() after Destroy = %q, want %q", got, environment.StateDestroyed)
	}
}

func TestLocalEnvironmentCreateRequiresSessionID(t *testing.T) {
	env := NewLocalEnvironment(t.TempDir(), slog.Default())
	err := env.Create(context.Background(), environment.SessionConfig{})
	if err == nil {
		t.Fatal("Create() with empty SessionID succeeded, want error")
	}
}

func TestLocalEnvironmentCapabilitiesAndUnsupportedOps(t *testing.T) {
	env := NewLocalEnvironment(t.TempDir(), slog.Default())

	if got := env.Capabilities(); got != environment.LocalCapabilities {
		t.Fatalf("Capabilities() = %#v, want %#v", got, environment.LocalCapabilities)
	}
	if _, err := env.CreateSnapshot(context.Background(), "snap-1"); !errors.Is(err, environment.ErrCapabilityNotSupported) {
		t.Fatalf("CreateSnapshot() error = %v, want ErrCapabilityNotSupported", err)
	}
	if err := env.Rollback(context.Background(), "snap-1"); !errors.Is(err, environment.ErrCapabilityNotSupported) {
		t.Fatalf("Rollback() error = %v, want ErrCapabilityNotSupported", err)
	}
}

func TestLocalEnvironmentExecuteToolAndDestroyedGuard(t *testing.T) {
	root := t.TempDir()
	env := NewLocalEnvironment(root, slog.Default())
	ctx := context.Background()

	if err := env.Create(ctx, environment.SessionConfig{SessionID: "sess-1"}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	testPath := filepath.Join(root, "a.txt")
	if err := os.WriteFile(testPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	resp, err := env.ExecuteTool(ctx, environment.ToolRequest{
		ToolName: "read_file",
		Params:   map[string]any{"path": "a.txt"},
	}, nil)
	if err != nil {
		t.Fatalf("ExecuteTool(read_file) error: %v", err)
	}
	if text := firstText(resp); !strings.Contains(text, "hello") {
		t.Fatalf("read_file response missing content, got %q", text)
	}

	if err := env.Destroy(ctx); err != nil {
		t.Fatalf("Destroy() error: %v", err)
	}
	_, err = env.ExecuteTool(ctx, environment.ToolRequest{
		ToolName: "read_file",
		Params:   map[string]any{"path": "a.txt"},
	}, nil)
	if !errors.Is(err, environment.ErrNotActive) {
		t.Fatalf("ExecuteTool() after Destroy error = %v, want ErrNotActive", err)
	}
}

func TestLocalEnvironmentParityWithLocalBackend(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(root, "p.txt"), []byte("line one\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	env := NewLocalEnvironment(root, slog.Default())
	if err := env.Create(ctx, environment.SessionConfig{SessionID: "sess-1"}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	backend := tools.NewLocalBackend(root)

	req := environment.ToolRequest{
		ToolCallID: "tc-1",
		ToolName:   "read_file",
		Params:     map[string]any{"path": "p.txt"},
	}
	envResp, envErr := env.ExecuteTool(ctx, req, nil)
	beResp, beErr := backend.ExecuteTool(ctx, tools.ToolRequest(req), nil)
	if (envErr == nil) != (beErr == nil) {
		t.Fatalf("error mismatch env=%v backend=%v", envErr, beErr)
	}
	if envErr != nil {
		t.Fatalf("unexpected env error: %v", envErr)
	}
	if firstText(envResp) != firstText(beResp) {
		t.Fatalf("content mismatch env=%q backend=%q", firstText(envResp), firstText(beResp))
	}
}

func firstText(resp *environment.ToolResponse) string {
	if resp == nil || len(resp.Content) == 0 {
		return ""
	}
	if tc, ok := resp.Content[0].(*ai.TextContent); ok {
		return tc.Text
	}
	return ""
}
