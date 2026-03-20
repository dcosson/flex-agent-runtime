package fly

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment"
)

func TestCreateAndLifecycle(t *testing.T) {
	var authHeader string
	var paths []string
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		authHeader = r.Header.Get("Authorization")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/apps/app-1/volumes":
			_, _ = w.Write([]byte(`{"id":"vol-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/apps/app-1/machines":
			_, _ = w.Write([]byte(`{"id":"machine-1","private_ip":"fdaa::1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/apps/app-1/machines/machine-1/suspend":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/apps/app-1/machines/machine-1/start":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/apps/app-1/machines/machine-1":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/apps/app-1/volumes/vol-1":
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	env := NewFlySandboxEnvironment("token-1", "app-1", []byte("ignored"), "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGx2", // test path; executor is mocked
		WithBaseURL(ts.URL),
		WithHTTPClient(ts.Client()),
		WithSSHExecutor(func(context.Context, string, string, func(environment.ToolProgress)) (*SSHExecResult, error) {
			return &SSHExecResult{Stdout: "ok", ExitCode: 0}, nil
		}),
	)

	if err := env.Create(context.Background(), environment.SessionConfig{
		SessionID: "sess-1",
		BaseImage: "img-1",
	}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if got := env.State(); got != environment.StateActive {
		t.Fatalf("State() after Create = %q, want active", got)
	}
	if authHeader != "Bearer token-1" {
		t.Fatalf("Authorization header = %q, want Bearer token-1", authHeader)
	}

	if err := env.Pause(context.Background()); err != nil {
		t.Fatalf("Pause() error: %v", err)
	}
	if got := env.State(); got != environment.StatePaused {
		t.Fatalf("State() after Pause = %q, want paused", got)
	}
	if err := env.Resume(context.Background()); err != nil {
		t.Fatalf("Resume() error: %v", err)
	}
	if err := env.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error: %v", err)
	}
	if got := env.State(); got != environment.StateDestroyed {
		t.Fatalf("State() after Destroy = %q, want destroyed", got)
	}
}

func TestCreate_WrongOptionsType(t *testing.T) {
	env := NewFlySandboxEnvironment("t", "a", nil, "")
	err := env.Create(context.Background(), environment.SessionConfig{
		SessionID: "s1",
		BaseImage: "img",
		Options:   struct{ Bad bool }{Bad: true},
	})
	if err == nil {
		t.Fatal("Create() succeeded, want error")
	}
	if !strings.Contains(err.Error(), "Options must be fly.FlyOptions") {
		t.Fatalf("error = %v, want options type error", err)
	}
}

func TestExecuteTool_RoutesFileAndProcessOps(t *testing.T) {
	var gotCommands []string
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/apps/app-1/volumes":
			_, _ = w.Write([]byte(`{"id":"vol-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/apps/app-1/machines":
			_, _ = w.Write([]byte(`{"id":"machine-1","private_ip":"fdaa::1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	env := NewFlySandboxEnvironment("token", "app-1", []byte("k"), "ssh-ed25519 AAAA",
		WithBaseURL(ts.URL),
		WithHTTPClient(ts.Client()),
		WithSSHExecutor(func(_ context.Context, _ string, command string, onProgress func(environment.ToolProgress)) (*SSHExecResult, error) {
			mu.Lock()
			gotCommands = append(gotCommands, command)
			mu.Unlock()
			if onProgress != nil {
				onProgress(environment.ToolProgress{Content: "line"})
			}
			return &SSHExecResult{Stdout: "out", ExitCode: 0}, nil
		}),
	)
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "s1", BaseImage: "img"}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	if _, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
		ToolName: "read_file",
		Params:   map[string]any{"path": "a.txt"},
	}, nil); err != nil {
		t.Fatalf("ExecuteTool(read_file) error: %v", err)
	}

	var progressCalled bool
	if _, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
		ToolName: "bash",
		Params:   map[string]any{"command": "echo hi"},
	}, func(environment.ToolProgress) {
		progressCalled = true
	}); err != nil {
		t.Fatalf("ExecuteTool(bash) error: %v", err)
	}
	if !progressCalled {
		t.Fatal("expected progress callback")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gotCommands) != 2 {
		t.Fatalf("commands executed = %d, want 2", len(gotCommands))
	}
	if !strings.Contains(gotCommands[0], "cat ") {
		t.Fatalf("first command = %q, want file read command", gotCommands[0])
	}
	if gotCommands[1] != "echo hi" {
		t.Fatalf("second command = %q, want echo hi", gotCommands[1])
	}
}

func TestExecuteTool_AfterDestroyReturnsNotActive(t *testing.T) {
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
	defer ts.Close()

	env := NewFlySandboxEnvironment("token", "app-1", []byte("k"), "ssh-ed25519 AAAA",
		WithBaseURL(ts.URL),
		WithHTTPClient(ts.Client()),
		WithSSHExecutor(func(_ context.Context, _ string, _ string, _ func(environment.ToolProgress)) (*SSHExecResult, error) {
			return &SSHExecResult{Stdout: "out", ExitCode: 0}, nil
		}),
	)
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "s1", BaseImage: "img"}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if err := env.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error: %v", err)
	}
	_, err := env.ExecuteTool(context.Background(), environment.ToolRequest{ToolName: "bash", Params: map[string]any{"command": "echo hi"}}, nil)
	if !errors.Is(err, environment.ErrNotActive) {
		t.Fatalf("ExecuteTool after destroy error = %v, want ErrNotActive", err)
	}
}

func TestSSHClientConfig_RequiresPinnedHostKey(t *testing.T) {
	env := NewFlySandboxEnvironment("token", "app", []byte("invalid-private"), "",
		WithSSHExecutor(func(_ context.Context, _ string, _ string, _ func(environment.ToolProgress)) (*SSHExecResult, error) {
			return nil, nil
		}))
	_, err := env.sshClientConfig()
	if err == nil {
		t.Fatal("sshClientConfig() succeeded, want error")
	}
	if !strings.Contains(err.Error(), "pinned host key is required") {
		t.Fatalf("error = %v, want pinned key error", err)
	}
}

func TestCreate_ParsesPayloadShape(t *testing.T) {
	var sawMachinePayload bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/apps/app-1/volumes":
			_, _ = w.Write([]byte(`{"id":"vol-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/apps/app-1/machines":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			if _, ok := payload["config"]; ok {
				sawMachinePayload = true
			}
			_, _ = w.Write([]byte(`{"id":"machine-1","private_ip":"fdaa::1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	env := NewFlySandboxEnvironment("token", "app-1", []byte("k"), "ssh-ed25519 AAAA",
		WithBaseURL(ts.URL),
		WithHTTPClient(ts.Client()),
		WithSSHExecutor(func(_ context.Context, _ string, _ string, _ func(environment.ToolProgress)) (*SSHExecResult, error) {
			return &SSHExecResult{Stdout: "out", ExitCode: 0}, nil
		}),
	)
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "s1", BaseImage: "img"}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if !sawMachinePayload {
		t.Fatal("expected machine create payload")
	}
}
