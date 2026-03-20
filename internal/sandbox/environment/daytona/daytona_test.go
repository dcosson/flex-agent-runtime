package daytona

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment"
)

func TestCreate_SendsPayloadAndSetsState(t *testing.T) {
	var gotAuth string
	var gotName string
	var gotImage string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/workspaces" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		gotName, _ = body["name"].(string)
		gotImage, _ = body["image"].(string)
		_, _ = w.Write([]byte(`{"id":"ws-1"}`))
	}))
	defer ts.Close()

	env := NewDaytonaSandboxEnvironment("test-key", WithBaseURL(ts.URL), WithHTTPClient(ts.Client()))
	err := env.Create(context.Background(), environment.SessionConfig{
		SessionID: "sess-1",
		BaseImage: "ubuntu:22.04",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want Bearer test-key", gotAuth)
	}
	if gotName != "sess-1" {
		t.Fatalf("name = %q, want sess-1", gotName)
	}
	if gotImage != "ubuntu:22.04" {
		t.Fatalf("image = %q, want ubuntu:22.04", gotImage)
	}
	if got := env.State(); got != environment.StateActive {
		t.Fatalf("State() = %q, want active", got)
	}
}

func TestCreate_WithOptions(t *testing.T) {
	var gotBody map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/workspaces" {
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("decode: %v", err)
			}
			_, _ = w.Write([]byte(`{"id":"ws-1"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	env := NewDaytonaSandboxEnvironment("k", WithBaseURL(ts.URL), WithHTTPClient(ts.Client()))
	err := env.Create(context.Background(), environment.SessionConfig{
		SessionID: "s1",
		BaseImage: "img",
		Options: DaytonaOptions{
			Target:  "aws",
			GitURL:  "https://github.com/example/repo",
			EnvVars: map[string]string{"FOO": "bar"},
		},
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if gotBody["target"] != "aws" {
		t.Fatalf("target = %v, want aws", gotBody["target"])
	}
	source, ok := gotBody["source"].(map[string]any)
	if !ok || source["repository"] != "https://github.com/example/repo" {
		t.Fatalf("source = %v, want repository URL", gotBody["source"])
	}
	envVars, ok := gotBody["env_vars"].(map[string]any)
	if !ok || envVars["FOO"] != "bar" {
		t.Fatalf("env_vars = %v, want FOO=bar", gotBody["env_vars"])
	}
}

func TestCreate_WrongOptionsType(t *testing.T) {
	env := NewDaytonaSandboxEnvironment("k")
	err := env.Create(context.Background(), environment.SessionConfig{
		SessionID: "s1",
		BaseImage: "img",
		Options:   struct{ Bad bool }{Bad: true},
	})
	if err == nil {
		t.Fatal("Create() succeeded, want error")
	}
	if !strings.Contains(err.Error(), "Options must be daytona.DaytonaOptions") {
		t.Fatalf("error = %v, want options type error", err)
	}
}

func TestCreate_MissingImage(t *testing.T) {
	env := NewDaytonaSandboxEnvironment("k")
	err := env.Create(context.Background(), environment.SessionConfig{
		SessionID: "s1",
	})
	if err == nil {
		t.Fatal("Create() succeeded, want error")
	}
	if !strings.Contains(err.Error(), "image is required") {
		t.Fatalf("error = %v, want image required error", err)
	}
}

func TestExecuteTool_RoutesFileOpsAndCommands(t *testing.T) {
	var mu sync.Mutex
	var seen []string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path)
		mu.Unlock()

		switch r.URL.Path {
		case "/workspaces":
			_, _ = w.Write([]byte(`{"id":"ws-1"}`))
		case "/workspaces/ws-1/filesystem/read_file":
			_, _ = w.Write([]byte(`{"content":"hello from file"}`))
		case "/workspaces/ws-1/commands/run":
			_, _ = w.Write([]byte(`{"content":"cmd output","exit_code":0,"progress":[{"content":"line1","is_error":false}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	env := NewDaytonaSandboxEnvironment("k", WithBaseURL(ts.URL), WithHTTPClient(ts.Client()))
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "s1", BaseImage: "img"}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	fileResp, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
		ToolName: "read_file",
		Params:   map[string]any{"path": "a.txt"},
	}, nil)
	if err != nil {
		t.Fatalf("ExecuteTool(read_file) error: %v", err)
	}
	if textContent(fileResp.Content) != "hello from file" {
		t.Fatalf("file response = %q, want hello from file", textContent(fileResp.Content))
	}

	var progress []string
	cmdResp, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
		ToolName: "bash",
		Params:   map[string]any{"command": "echo hi"},
	}, func(p environment.ToolProgress) {
		progress = append(progress, p.Content)
	})
	if err != nil {
		t.Fatalf("ExecuteTool(bash) error: %v", err)
	}
	if textContent(cmdResp.Content) != "cmd output" {
		t.Fatalf("command response = %q, want cmd output", textContent(cmdResp.Content))
	}
	if len(progress) != 1 || progress[0] != "line1" {
		t.Fatalf("progress = %+v, want [line1]", progress)
	}

	mu.Lock()
	defer mu.Unlock()
	if !contains(seen, "/workspaces/ws-1/filesystem/read_file") {
		t.Fatalf("missing filesystem route in seen=%v", seen)
	}
	if !contains(seen, "/workspaces/ws-1/commands/run") {
		t.Fatalf("missing commands route in seen=%v", seen)
	}
}

func TestPauseResumeReturnNotSupported(t *testing.T) {
	env := NewDaytonaSandboxEnvironment("k")
	if err := env.Pause(context.Background()); !errors.Is(err, environment.ErrCapabilityNotSupported) {
		t.Fatalf("Pause() = %v, want ErrCapabilityNotSupported", err)
	}
	if err := env.Resume(context.Background()); !errors.Is(err, environment.ErrCapabilityNotSupported) {
		t.Fatalf("Resume() = %v, want ErrCapabilityNotSupported", err)
	}
}

func TestSnapshotRollbackReturnNotSupported(t *testing.T) {
	env := NewDaytonaSandboxEnvironment("k")
	_, err := env.CreateSnapshot(context.Background(), "snap")
	if !errors.Is(err, environment.ErrCapabilityNotSupported) {
		t.Fatalf("CreateSnapshot() = %v, want ErrCapabilityNotSupported", err)
	}
	if err := env.Rollback(context.Background(), "snap-id"); !errors.Is(err, environment.ErrCapabilityNotSupported) {
		t.Fatalf("Rollback() = %v, want ErrCapabilityNotSupported", err)
	}
}

func TestDestroyLifecycle(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces":
			_, _ = w.Write([]byte(`{"id":"ws-1"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/workspaces/ws-1":
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	env := NewDaytonaSandboxEnvironment("k", WithBaseURL(ts.URL), WithHTTPClient(ts.Client()))
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "s1", BaseImage: "img"}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if err := env.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error: %v", err)
	}
	if got := env.State(); got != environment.StateDestroyed {
		t.Fatalf("State() after Destroy = %q, want destroyed", got)
	}
	// Idempotent destroy.
	if err := env.Destroy(context.Background()); err != nil {
		t.Fatalf("second Destroy() error: %v", err)
	}
}

func TestExecuteTool_AfterDestroyReturnsNotActive(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces":
			_, _ = w.Write([]byte(`{"id":"ws-1"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/workspaces/ws-1":
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	env := NewDaytonaSandboxEnvironment("k", WithBaseURL(ts.URL), WithHTTPClient(ts.Client()))
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "s1", BaseImage: "img"}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if err := env.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error: %v", err)
	}

	_, err := env.ExecuteTool(context.Background(), environment.ToolRequest{ToolName: "bash"}, nil)
	if !errors.Is(err, environment.ErrNotActive) {
		t.Fatalf("ExecuteTool() error = %v, want ErrNotActive", err)
	}
}

func TestCapabilities(t *testing.T) {
	env := NewDaytonaSandboxEnvironment("k")
	caps := env.Capabilities()
	if caps.Pause {
		t.Fatal("Daytona should not support Pause")
	}
	if caps.Snapshots {
		t.Fatal("Daytona should not support Snapshots")
	}
	if caps.Rollback {
		t.Fatal("Daytona should not support Rollback")
	}
	if !caps.StreamingProgress {
		t.Fatal("Daytona should support StreamingProgress")
	}
}

func TestCreate_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal error`))
	}))
	defer ts.Close()

	env := NewDaytonaSandboxEnvironment("k", WithBaseURL(ts.URL), WithHTTPClient(ts.Client()))
	err := env.Create(context.Background(), environment.SessionConfig{SessionID: "s1", BaseImage: "img"})
	if err == nil {
		t.Fatal("Create() succeeded, want error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error = %v, want status 500", err)
	}
	if got := env.State(); got != environment.StateCreating {
		t.Fatalf("State() after failed Create = %q, want creating", got)
	}
}

func textContent(blocks []ai.ContentBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	if t, ok := blocks[0].(*ai.TextContent); ok {
		return t.Text
	}
	return ""
}

func contains(values []string, needle string) bool {
	for _, v := range values {
		if v == needle {
			return true
		}
	}
	return false
}
