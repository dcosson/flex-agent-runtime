package e2b

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment"
)

func TestCreate_SendsPayloadAndSetsState(t *testing.T) {
	var gotAuth string
	var gotTemplate string
	var gotTimeout int
	var gotSession string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/sandboxes" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		gotTemplate, _ = body["template_id"].(string)
		gotSession, _ = body["session_id"].(string)
		gotTimeout = int(body["timeout_seconds"].(float64))
		_, _ = w.Write([]byte(`{"id":"sb-1"}`))
	}))
	defer ts.Close()

	env := NewE2BSandboxEnvironment("test-key", WithBaseURL(ts.URL), WithHTTPClient(ts.Client()))
	err := env.Create(context.Background(), environment.SessionConfig{
		SessionID: "sess-1",
		BaseImage: "tmpl-abc",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want Bearer test-key", gotAuth)
	}
	if gotTemplate != "tmpl-abc" {
		t.Fatalf("template_id = %q, want tmpl-abc", gotTemplate)
	}
	if gotSession != "sess-1" {
		t.Fatalf("session_id = %q, want sess-1", gotSession)
	}
	if gotTimeout <= 0 {
		t.Fatalf("timeout_seconds = %d, want > 0", gotTimeout)
	}
	if got := env.State(); got != environment.StateActive {
		t.Fatalf("State() = %q, want active", got)
	}
}

func TestCreate_WrongOptionsType(t *testing.T) {
	env := NewE2BSandboxEnvironment("k")
	err := env.Create(context.Background(), environment.SessionConfig{
		SessionID: "sess-1",
		BaseImage: "tmpl",
		Options:   struct{ Bad bool }{Bad: true},
	})
	if err == nil {
		t.Fatal("Create() succeeded, want error")
	}
	if !strings.Contains(err.Error(), "Options must be e2b.E2BOptions") {
		t.Fatalf("error = %v, want options type error", err)
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
		case "/sandboxes":
			_, _ = w.Write([]byte(`{"id":"sb-1"}`))
		case "/sandboxes/sb-1/filesystem/read_file":
			_, _ = w.Write([]byte(`{"content":"hello from file"}`))
		case "/sandboxes/sb-1/commands/run":
			_, _ = w.Write([]byte(`{"content":"cmd output","exit_code":0,"progress":[{"content":"line1","is_error":false}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	env := NewE2BSandboxEnvironment("k", WithBaseURL(ts.URL), WithHTTPClient(ts.Client()))
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "s1", BaseImage: "tmpl"}); err != nil {
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
		t.Fatalf("file response content = %q, want hello from file", textContent(fileResp.Content))
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
		t.Fatalf("command response content = %q, want cmd output", textContent(cmdResp.Content))
	}
	if len(progress) != 1 || progress[0] != "line1" {
		t.Fatalf("progress = %+v, want [line1]", progress)
	}

	mu.Lock()
	defer mu.Unlock()
	if !contains(seen, "/sandboxes/sb-1/filesystem/read_file") {
		t.Fatalf("missing filesystem route in seen=%v", seen)
	}
	if !contains(seen, "/sandboxes/sb-1/commands/run") {
		t.Fatalf("missing commands route in seen=%v", seen)
	}
}

func TestPauseResumeDestroyLifecycle(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
			_, _ = w.Write([]byte(`{"id":"sb-1"}`))
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

	env := NewE2BSandboxEnvironment("k", WithBaseURL(ts.URL), WithHTTPClient(ts.Client()))
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "s1", BaseImage: "tmpl"}); err != nil {
		t.Fatalf("Create() error: %v", err)
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
	if got := env.State(); got != environment.StateActive {
		t.Fatalf("State() after Resume = %q, want active", got)
	}
	if err := env.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error: %v", err)
	}
	if got := env.State(); got != environment.StateDestroyed {
		t.Fatalf("State() after Destroy = %q, want destroyed", got)
	}
}

func TestExecuteTool_AfterDestroyReturnsNotActive(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
			_, _ = w.Write([]byte(`{"id":"sb-1"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/sandboxes/sb-1":
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	env := NewE2BSandboxEnvironment("k", WithBaseURL(ts.URL), WithHTTPClient(ts.Client()))
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "s1", BaseImage: "tmpl"}); err != nil {
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

func TestCreate_TimeoutCappedAt24Hours(t *testing.T) {
	var gotTimeout int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sandboxes" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			gotTimeout = int(body["timeout_seconds"].(float64))
			_, _ = w.Write([]byte(`{"id":"sb-1"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	env := NewE2BSandboxEnvironment("k", WithBaseURL(ts.URL), WithHTTPClient(ts.Client()))
	if err := env.Create(context.Background(), environment.SessionConfig{
		SessionID: "s1",
		BaseImage: "tmpl",
		Options: E2BOptions{
			Timeout: 48 * time.Hour,
		},
	}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if gotTimeout != int((24 * time.Hour).Seconds()) {
		t.Fatalf("timeout_seconds = %d, want %d", gotTimeout, int((24 * time.Hour).Seconds()))
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
