package environment_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment/daytona"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment/e2b"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment/fly"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment/local"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment/native"
)

func TestE2E3_E2BFullCycleWithMockServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
			_, _ = w.Write([]byte(`{"id":"sb-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/sb-1/filesystem/read_file":
			_, _ = w.Write([]byte(`{"content":"hello-e2b"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/sb-1/commands/run":
			_, _ = w.Write([]byte(`{"content":"bash-ok","exit_code":0,"progress":[{"content":"line-1","is_error":false}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/sb-1/pause":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/sb-1/resume":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/sandboxes/sb-1":
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	env := e2b.NewE2BSandboxEnvironment("key", e2b.WithBaseURL(ts.URL), e2b.WithHTTPClient(ts.Client()))
	ctx := context.Background()
	if err := env.Create(ctx, environment.SessionConfig{SessionID: "e2e3", BaseImage: "tmpl"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	readResp, err := env.ExecuteTool(ctx, environment.ToolRequest{ToolName: "read_file", Params: map[string]any{"path": "a.txt"}}, nil)
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if got := textFromContent(readResp.Content); got != "hello-e2b" {
		t.Fatalf("read_file content = %q, want hello-e2b", got)
	}
	var progress []string
	bashResp, err := env.ExecuteTool(ctx, environment.ToolRequest{ToolName: "bash", Params: map[string]any{"command": "echo hi"}}, func(p environment.ToolProgress) {
		progress = append(progress, p.Content)
	})
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	if got := textFromContent(bashResp.Content); got != "bash-ok" {
		t.Fatalf("bash content = %q, want bash-ok", got)
	}
	if len(progress) != 1 || progress[0] != "line-1" {
		t.Fatalf("progress = %v, want [line-1]", progress)
	}
	if _, err := env.CreateSnapshot(ctx, "snap"); !errors.Is(err, environment.ErrCapabilityNotSupported) {
		t.Fatalf("CreateSnapshot err = %v, want ErrCapabilityNotSupported", err)
	}
	if err := env.Pause(ctx); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if err := env.Resume(ctx); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if err := env.Destroy(ctx); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
}

func TestE2E4_EnvironmentFallbackBehavior(t *testing.T) {
	var createCalls int
	var destroyCalls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
			createCalls++
			_, _ = w.Write([]byte(fmt.Sprintf(`{"id":"sb-%d"}`, createCalls)))
		case r.Method == http.MethodDelete && (r.URL.Path == "/sandboxes/sb-1" || r.URL.Path == "/sandboxes/sb-2"):
			destroyCalls++
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	env := e2b.NewE2BSandboxEnvironment("key", e2b.WithBaseURL(ts.URL), e2b.WithHTTPClient(ts.Client()))
	ctx := context.Background()
	if err := env.Create(ctx, environment.SessionConfig{SessionID: "e2e4", BaseImage: "tmpl"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := env.CreateSnapshot(ctx, "missing"); !errors.Is(err, environment.ErrCapabilityNotSupported) {
		t.Fatalf("CreateSnapshot err = %v, want ErrCapabilityNotSupported", err)
	}
	if err := env.Rollback(ctx, "missing"); !errors.Is(err, environment.ErrCapabilityNotSupported) {
		t.Fatalf("Rollback err = %v, want ErrCapabilityNotSupported", err)
	}

	if err := env.Destroy(ctx); err != nil {
		t.Fatalf("Destroy fallback path: %v", err)
	}
	if err := env.Create(ctx, environment.SessionConfig{SessionID: "e2e4-recreate", BaseImage: "tmpl"}); err != nil {
		t.Fatalf("Create recreate fallback path: %v", err)
	}
	if createCalls != 2 {
		t.Fatalf("createCalls = %d, want 2", createCalls)
	}
	if destroyCalls != 1 {
		t.Fatalf("destroyCalls = %d, want 1", destroyCalls)
	}
}

func TestE2E5_EnvironmentTransparency(t *testing.T) {
	ctx := context.Background()

	localFactory := func(t *testing.T) environment.ExecutionEnvironment {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "input.txt"), []byte("hello-local"), 0o644); err != nil {
			t.Fatalf("seed local file: %v", err)
		}
		env := local.NewLocalEnvironment(root, slog.Default())
		if err := env.Create(ctx, environment.SessionConfig{SessionID: "e2e5-local"}); err != nil {
			t.Fatalf("local create: %v", err)
		}
		return env
	}

	nativeFactory := func(t *testing.T) environment.ExecutionEnvironment {
		env := native.NewNativeSandboxEnvironment(newComplianceMockService(), native.DefaultConfig(), slog.Default())
		if err := env.Create(ctx, environment.SessionConfig{SessionID: "e2e5-native"}); err != nil {
			t.Fatalf("native create: %v", err)
		}
		return env
	}

	e2bFactory := func(t *testing.T) environment.ExecutionEnvironment {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
				_, _ = w.Write([]byte(`{"id":"sb-1"}`))
			case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/sb-1/filesystem/read_file":
				_, _ = w.Write([]byte(`{"content":"hello-remote"}`))
			case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/sb-1/commands/run":
				_, _ = w.Write([]byte(`{"content":"ok","exit_code":0}`))
			case r.Method == http.MethodDelete && r.URL.Path == "/sandboxes/sb-1":
				_, _ = w.Write([]byte(`{}`))
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(ts.Close)
		env := e2b.NewE2BSandboxEnvironment("key", e2b.WithBaseURL(ts.URL), e2b.WithHTTPClient(ts.Client()))
		if err := env.Create(ctx, environment.SessionConfig{SessionID: "e2e5-e2b", BaseImage: "tmpl"}); err != nil {
			t.Fatalf("e2b create: %v", err)
		}
		return env
	}

	daytonaFactory := func(t *testing.T) environment.ExecutionEnvironment {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/workspaces":
				_, _ = w.Write([]byte(`{"id":"ws-1"}`))
			case r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-1/filesystem/read_file":
				_, _ = w.Write([]byte(`{"content":"hello-remote"}`))
			case r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-1/commands/run":
				_, _ = w.Write([]byte(`{"content":"ok","exit_code":0}`))
			case r.Method == http.MethodDelete && r.URL.Path == "/workspaces/ws-1":
				_, _ = w.Write([]byte(`{}`))
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(ts.Close)
		env := daytona.NewDaytonaSandboxEnvironment("key", daytona.WithBaseURL(ts.URL), daytona.WithHTTPClient(ts.Client()))
		if err := env.Create(ctx, environment.SessionConfig{SessionID: "e2e5-daytona", BaseImage: "img"}); err != nil {
			t.Fatalf("daytona create: %v", err)
		}
		return env
	}

	flyFactory := func(t *testing.T) environment.ExecutionEnvironment {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/apps/app-1/volumes":
				_, _ = w.Write([]byte(`{"id":"vol-1"}`))
			case r.Method == http.MethodPost && r.URL.Path == "/apps/app-1/machines":
				_, _ = w.Write([]byte(`{"id":"machine-1","private_ip":"fdaa::1"}`))
			case r.Method == http.MethodDelete && r.URL.Path == "/apps/app-1/machines/machine-1":
				_, _ = w.Write([]byte(`{}`))
			case r.Method == http.MethodDelete && r.URL.Path == "/apps/app-1/volumes/vol-1":
				_, _ = w.Write([]byte(`{}`))
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(ts.Close)
		env := fly.NewFlySandboxEnvironment("token", "app-1", []byte("ignored"), "ssh-ed25519 AAAA",
			fly.WithBaseURL(ts.URL),
			fly.WithHTTPClient(ts.Client()),
			fly.WithSSHExecutor(func(_ context.Context, _ string, command string, _ func(environment.ToolProgress)) (*fly.SSHExecResult, error) {
				if command == "echo hi" {
					return &fly.SSHExecResult{Stdout: "ok", ExitCode: 0}, nil
				}
				return &fly.SSHExecResult{Stdout: "hello-remote", ExitCode: 0}, nil
			}),
		)
		if err := env.Create(ctx, environment.SessionConfig{SessionID: "e2e5-fly", BaseImage: "img"}); err != nil {
			t.Fatalf("fly create: %v", err)
		}
		return env
	}

	tests := []struct {
		name string
		mk   func(t *testing.T) environment.ExecutionEnvironment
	}{
		{name: "local", mk: localFactory},
		{name: "native", mk: nativeFactory},
		{name: "e2b", mk: e2bFactory},
		{name: "daytona", mk: daytonaFactory},
		{name: "fly", mk: flyFactory},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := tc.mk(t)
			t.Cleanup(func() { _ = env.Destroy(ctx) })

			readReq := environment.ToolRequest{ToolName: "read_file", Params: map[string]any{"path": "input.txt"}}
			if tc.name == "native" {
				readReq.ToolCallID = "native-read"
			}
			readResp, err := env.ExecuteTool(ctx, readReq, nil)
			if err != nil {
				t.Fatalf("read_file: %v", err)
			}
			if len(readResp.Content) == 0 {
				t.Fatal("read_file returned empty content")
			}

			bashReq := environment.ToolRequest{ToolName: "bash", Params: map[string]any{"command": "echo hi"}}
			if tc.name == "native" {
				bashReq.ToolCallID = "native-bash"
			}
			bashResp, err := env.ExecuteTool(ctx, bashReq, nil)
			if err != nil {
				t.Fatalf("bash: %v", err)
			}
			if len(bashResp.Content) == 0 {
				t.Fatal("bash returned empty content")
			}
			if tc.name == "e2b" || tc.name == "daytona" || tc.name == "fly" {
				if bashResp.ExitCode == nil {
					t.Fatal("remote bash missing exit code")
				}
			}
		})
	}
}

func textFromContent(blocks []ai.ContentBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	txt, ok := blocks[0].(*ai.TextContent)
	if !ok {
		return ""
	}
	return txt.Text
}
