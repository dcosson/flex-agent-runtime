package environment_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment/daytona"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment/e2b"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment/fly"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment/local"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment/native"
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

type recordedHTTPCall struct {
	Method string
	Path   string
	Body   map[string]any
}

func TestO1_E2BWireFormatGolden(t *testing.T) {
	var mu sync.Mutex
	var calls []recordedHTTPCall

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		mu.Lock()
		calls = append(calls, recordedHTTPCall{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   body,
		})
		mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
			_, _ = w.Write([]byte(`{"id":"sb-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/sb-1/commands/run":
			_, _ = w.Write([]byte(`{"content":"ok","exit_code":0}`))
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/sb-1/filesystem/read_file":
			_, _ = w.Write([]byte(`{"content":"hello"}`))
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

	env := e2b.NewE2BSandboxEnvironment("e2b-key", e2b.WithBaseURL(ts.URL), e2b.WithHTTPClient(ts.Client()))
	if err := env.Create(context.Background(), environment.SessionConfig{
		SessionID: "o1",
		BaseImage: "tmpl-1",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
		ToolName: "bash",
		Params:   map[string]any{"command": "echo hi"},
	}, nil); err != nil {
		t.Fatalf("ExecuteTool bash: %v", err)
	}
	if _, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
		ToolName: "read_file",
		Params:   map[string]any{"path": "README.md"},
	}, nil); err != nil {
		t.Fatalf("ExecuteTool read_file: %v", err)
	}
	if err := env.Pause(context.Background()); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if err := env.Resume(context.Background()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if err := env.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) < 6 {
		t.Fatalf("captured %d calls, want >= 6", len(calls))
	}

	var createBody map[string]any
	var runBody map[string]any
	var readBody map[string]any
	for _, c := range calls {
		if c.Path == "/sandboxes" && c.Method == http.MethodPost {
			createBody = c.Body
		}
		if c.Path == "/sandboxes/sb-1/commands/run" && c.Method == http.MethodPost {
			runBody = c.Body
		}
		if c.Path == "/sandboxes/sb-1/filesystem/read_file" && c.Method == http.MethodPost {
			readBody = c.Body
		}
	}
	if createBody["template_id"] != "tmpl-1" || createBody["session_id"] != "o1" {
		t.Fatalf("unexpected create payload: %+v", createBody)
	}
	if createBody["timeout_seconds"] == nil {
		t.Fatalf("create payload missing timeout_seconds: %+v", createBody)
	}
	if runBody["tool_name"] != "bash" {
		t.Fatalf("unexpected command payload: %+v", runBody)
	}
	if params, ok := runBody["params"].(map[string]any); !ok || params["command"] != "echo hi" {
		t.Fatalf("unexpected command params payload: %+v", runBody)
	}
	if readBody["tool_name"] != "read_file" {
		t.Fatalf("unexpected read payload: %+v", readBody)
	}
}

func TestO2_DaytonaWireFormatGolden(t *testing.T) {
	var mu sync.Mutex
	var calls []recordedHTTPCall

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		mu.Lock()
		calls = append(calls, recordedHTTPCall{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   body,
		})
		mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces":
			_, _ = w.Write([]byte(`{"id":"ws-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-1/commands/run":
			_, _ = w.Write([]byte(`{"content":"ok","exit_code":0}`))
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-1/filesystem/read_file":
			_, _ = w.Write([]byte(`{"content":"hello"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/workspaces/ws-1":
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	env := daytona.NewDaytonaSandboxEnvironment("daytona-key", daytona.WithBaseURL(ts.URL), daytona.WithHTTPClient(ts.Client()))
	if err := env.Create(context.Background(), environment.SessionConfig{
		SessionID: "o2",
		BaseImage: "ubuntu:22.04",
		Labels:    map[string]string{"tier": "golden"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
		ToolName: "bash",
		Params:   map[string]any{"command": "pwd"},
	}, nil); err != nil {
		t.Fatalf("ExecuteTool bash: %v", err)
	}
	if _, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
		ToolName: "read_file",
		Params:   map[string]any{"path": "README.md"},
	}, nil); err != nil {
		t.Fatalf("ExecuteTool read_file: %v", err)
	}
	if err := env.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	var createBody map[string]any
	var runBody map[string]any
	var readBody map[string]any
	for _, c := range calls {
		if c.Path == "/workspaces" && c.Method == http.MethodPost {
			createBody = c.Body
		}
		if c.Path == "/workspaces/ws-1/commands/run" && c.Method == http.MethodPost {
			runBody = c.Body
		}
		if c.Path == "/workspaces/ws-1/filesystem/read_file" && c.Method == http.MethodPost {
			readBody = c.Body
		}
	}
	if createBody["name"] != "o2" || createBody["image"] != "ubuntu:22.04" {
		t.Fatalf("unexpected create payload: %+v", createBody)
	}
	if runBody["tool_name"] != "bash" {
		t.Fatalf("unexpected command payload: %+v", runBody)
	}
	if readBody["tool_name"] != "read_file" {
		t.Fatalf("unexpected read payload: %+v", readBody)
	}
}

func TestO3_FlyWireFormatGolden(t *testing.T) {
	var mu sync.Mutex
	var calls []recordedHTTPCall

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		mu.Lock()
		calls = append(calls, recordedHTTPCall{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   body,
		})
		mu.Unlock()
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

	env := fly.NewFlySandboxEnvironment("fly-token", "app-1", []byte("ignored"), "ssh-ed25519 AAAA",
		fly.WithBaseURL(ts.URL),
		fly.WithHTTPClient(ts.Client()),
		fly.WithSSHExecutor(func(_ context.Context, _ string, _ string, _ func(environment.ToolProgress)) (*fly.SSHExecResult, error) {
			return &fly.SSHExecResult{Stdout: "ok", ExitCode: 0}, nil
		}),
	)
	if err := env.Create(context.Background(), environment.SessionConfig{
		SessionID: "o3",
		BaseImage: "ghcr.io/flex/base:latest",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := env.Pause(context.Background()); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if err := env.Resume(context.Background()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if err := env.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) < 6 {
		t.Fatalf("captured %d calls, want >= 6", len(calls))
	}

	var machineCreate map[string]any
	var sawSuspend bool
	var sawStart bool
	var sawMachineDelete bool
	var sawVolumeDelete bool
	for _, c := range calls {
		switch {
		case c.Method == http.MethodPost && c.Path == "/apps/app-1/machines":
			machineCreate = c.Body
		case c.Method == http.MethodPost && c.Path == "/apps/app-1/machines/machine-1/suspend":
			sawSuspend = true
		case c.Method == http.MethodPost && c.Path == "/apps/app-1/machines/machine-1/start":
			sawStart = true
		case c.Method == http.MethodDelete && c.Path == "/apps/app-1/machines/machine-1":
			sawMachineDelete = true
		case c.Method == http.MethodDelete && c.Path == "/apps/app-1/volumes/vol-1":
			sawVolumeDelete = true
		}
	}
	if machineCreate == nil {
		t.Fatal("missing machine create request")
	}
	if machineCreate["name"] != "sess-o3" {
		t.Fatalf("unexpected machine name in payload: %+v", machineCreate)
	}
	cfg, ok := machineCreate["config"].(map[string]any)
	if !ok || cfg["image"] != "ghcr.io/flex/base:latest" {
		t.Fatalf("unexpected machine config payload: %+v", machineCreate)
	}
	if !sawSuspend || !sawStart || !sawMachineDelete || !sawVolumeDelete {
		t.Fatalf("missing lifecycle calls: suspend=%v start=%v machineDelete=%v volumeDelete=%v", sawSuspend, sawStart, sawMachineDelete, sawVolumeDelete)
	}
}

func TestF5_FlySSHFailureScenarios(t *testing.T) {
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

	cases := []struct {
		name    string
		errText string
	}{
		{name: "connection refused", errText: "connection refused"},
		{name: "auth failed", errText: "unable to authenticate"},
		{name: "timeout", errText: "i/o timeout"},
		{name: "connection dropped", errText: "connection reset by peer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := fly.NewFlySandboxEnvironment("fly-token", "app-1", []byte("ignored"), "ssh-ed25519 AAAA",
				fly.WithBaseURL(ts.URL),
				fly.WithHTTPClient(ts.Client()),
				fly.WithSSHExecutor(func(_ context.Context, _ string, _ string, _ func(environment.ToolProgress)) (*fly.SSHExecResult, error) {
					return nil, fmt.Errorf("%s", tc.errText)
				}),
			)
			if err := env.Create(context.Background(), environment.SessionConfig{
				SessionID: "f5",
				BaseImage: "img",
			}); err != nil {
				t.Fatalf("Create: %v", err)
			}
			_, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
				ToolName: "bash",
				Params:   map[string]any{"command": "echo hi"},
			}, nil)
			if err == nil {
				t.Fatal("ExecuteTool succeeded, want error")
			}
			if !strings.Contains(err.Error(), "fly: execute bash") || !strings.Contains(err.Error(), tc.errText) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestSEC3_RemoteToolParameterSanitization(t *testing.T) {
	t.Run("Fly file path is shell-quoted", func(t *testing.T) {
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

		var command string
		env := fly.NewFlySandboxEnvironment("token", "app-1", []byte("ignored"), "ssh-ed25519 AAAA",
			fly.WithBaseURL(ts.URL),
			fly.WithHTTPClient(ts.Client()),
			fly.WithSSHExecutor(func(_ context.Context, _ string, cmd string, _ func(environment.ToolProgress)) (*fly.SSHExecResult, error) {
				command = cmd
				return &fly.SSHExecResult{Stdout: "ok", ExitCode: 0}, nil
			}),
		)
		if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "sec3-fly", BaseImage: "img"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		_, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
			ToolName: "read_file",
			Params:   map[string]any{"path": "unsafe'; touch /tmp/pwned; echo '"},
		}, nil)
		if err != nil {
			t.Fatalf("ExecuteTool: %v", err)
		}
		if !strings.HasPrefix(command, "cat '") || !strings.HasSuffix(command, "'") {
			t.Fatalf("unexpected quoted command: %q", command)
		}
	})

	t.Run("Remote bash command is passed through without env-side mutation", func(t *testing.T) {
		for _, provider := range []string{"e2b", "daytona"} {
			provider := provider
			t.Run(provider, func(t *testing.T) {
				var gotPath string
				var gotToolName string
				var gotCommand string
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if r.Body != nil {
						_ = json.NewDecoder(r.Body).Decode(&body)
					}
					if params, ok := body["params"].(map[string]any); ok {
						if cmd, ok := params["command"].(string); ok {
							gotCommand = cmd
						}
					}
					gotPath = r.URL.Path
					if name, ok := body["tool_name"].(string); ok {
						gotToolName = name
					}
					switch {
					case provider == "e2b" && r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
						_, _ = w.Write([]byte(`{"id":"sb-1"}`))
					case provider == "e2b" && r.Method == http.MethodPost && r.URL.Path == "/sandboxes/sb-1/commands/run":
						_, _ = w.Write([]byte(`{"content":"ok","exit_code":0}`))
					case provider == "daytona" && r.Method == http.MethodPost && r.URL.Path == "/workspaces":
						_, _ = w.Write([]byte(`{"id":"ws-1"}`))
					case provider == "daytona" && r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-1/commands/run":
						_, _ = w.Write([]byte(`{"content":"ok","exit_code":0}`))
					default:
						http.NotFound(w, r)
					}
				}))
				defer ts.Close()

				const cmd = "printf 'a;b|c'"
				if provider == "e2b" {
					env := e2b.NewE2BSandboxEnvironment("key", e2b.WithBaseURL(ts.URL), e2b.WithHTTPClient(ts.Client()))
					if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "sec3-e2b", BaseImage: "tmpl"}); err != nil {
						t.Fatalf("Create: %v", err)
					}
					if _, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
						ToolName: "bash",
						Params:   map[string]any{"command": cmd},
					}, nil); err != nil {
						t.Fatalf("ExecuteTool: %v", err)
					}
					if gotPath != "/sandboxes/sb-1/commands/run" {
						t.Fatalf("unexpected path: %s", gotPath)
					}
				} else {
					env := daytona.NewDaytonaSandboxEnvironment("key", daytona.WithBaseURL(ts.URL), daytona.WithHTTPClient(ts.Client()))
					if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "sec3-daytona", BaseImage: "img"}); err != nil {
						t.Fatalf("Create: %v", err)
					}
					if _, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
						ToolName: "bash",
						Params:   map[string]any{"command": cmd},
					}, nil); err != nil {
						t.Fatalf("ExecuteTool: %v", err)
					}
					if gotPath != "/workspaces/ws-1/commands/run" {
						t.Fatalf("unexpected path: %s", gotPath)
					}
				}
				if gotToolName != "bash" {
					t.Fatalf("tool_name = %q, want bash", gotToolName)
				}
				if gotCommand != cmd {
					t.Fatalf("command = %q, want %q", gotCommand, cmd)
				}
			})
		}
	})
}

func TestSEC4_FlySSHKeyHandling(t *testing.T) {
	t.Run("invalid private key parse does not leak key material", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/apps/app-1/volumes":
				_, _ = w.Write([]byte(`{"id":"vol-1"}`))
			case r.Method == http.MethodPost && r.URL.Path == "/apps/app-1/machines":
				_, _ = w.Write([]byte(`{"id":"machine-1","private_ip":"127.0.0.1"}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer ts.Close()

		privateKey := "-----BEGIN RSA PRIVATE KEY-----\nTOPSECRET-KEY-MATERIAL\n-----END RSA PRIVATE KEY-----"
		env := fly.NewFlySandboxEnvironment("token", "app-1", []byte(privateKey), "ssh-ed25519 AAAA",
			fly.WithBaseURL(ts.URL),
			fly.WithHTTPClient(ts.Client()),
		)
		if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "sec4", BaseImage: "img"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		_, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
			ToolName: "bash",
			Params:   map[string]any{"command": "echo hi"},
		}, nil)
		if err == nil {
			t.Fatal("ExecuteTool succeeded, want parse key error")
		}
		if !strings.Contains(err.Error(), "parse ssh private key") {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(err.Error(), "TOPSECRET-KEY-MATERIAL") {
			t.Fatalf("error leaked private key material: %v", err)
		}
	})

	t.Run("missing pinned host key is rejected", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/apps/app-1/volumes":
				_, _ = w.Write([]byte(`{"id":"vol-1"}`))
			case r.Method == http.MethodPost && r.URL.Path == "/apps/app-1/machines":
				_, _ = w.Write([]byte(`{"id":"machine-1","private_ip":"127.0.0.1"}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer ts.Close()

		env := fly.NewFlySandboxEnvironment("token", "app-1", []byte("invalid-key"), "",
			fly.WithBaseURL(ts.URL),
			fly.WithHTTPClient(ts.Client()),
		)
		if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "sec4-host", BaseImage: "img"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		_, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
			ToolName: "bash",
			Params:   map[string]any{"command": "echo hi"},
		}, nil)
		if err == nil {
			t.Fatal("ExecuteTool succeeded, want pinned host key error")
		}
		if !strings.Contains(err.Error(), "pinned host key is required") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestSEC1_RemoteAPIKeyHandling(t *testing.T) {
	for _, provider := range []string{"e2b", "daytona", "fly"} {
		provider := provider
		t.Run(provider, func(t *testing.T) {
			var authHeader string
			var requestURI string

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				authHeader = r.Header.Get("Authorization")
				requestURI = r.URL.String()
				switch {
				case provider == "e2b" && r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
					http.Error(w, "upstream failed", http.StatusUnauthorized)
				case provider == "daytona" && r.Method == http.MethodPost && r.URL.Path == "/workspaces":
					http.Error(w, "upstream failed", http.StatusUnauthorized)
				case provider == "fly" && r.Method == http.MethodPost && r.URL.Path == "/apps/app-1/volumes":
					http.Error(w, "upstream failed", http.StatusUnauthorized)
				default:
					http.NotFound(w, r)
				}
			}))
			defer ts.Close()

			const key = "sensitive-api-key"
			var err error
			switch provider {
			case "e2b":
				env := e2b.NewE2BSandboxEnvironment(key, e2b.WithBaseURL(ts.URL), e2b.WithHTTPClient(ts.Client()))
				err = env.Create(context.Background(), environment.SessionConfig{SessionID: "sec1-e2b", BaseImage: "tmpl"})
			case "daytona":
				env := daytona.NewDaytonaSandboxEnvironment(key, daytona.WithBaseURL(ts.URL), daytona.WithHTTPClient(ts.Client()))
				err = env.Create(context.Background(), environment.SessionConfig{SessionID: "sec1-daytona", BaseImage: "img"})
			default:
				env := fly.NewFlySandboxEnvironment(key, "app-1", []byte("ignored"), "ssh-ed25519 AAAA",
					fly.WithBaseURL(ts.URL),
					fly.WithHTTPClient(ts.Client()),
					fly.WithSSHExecutor(func(_ context.Context, _ string, _ string, _ func(environment.ToolProgress)) (*fly.SSHExecResult, error) {
						return &fly.SSHExecResult{Stdout: "ok", ExitCode: 0}, nil
					}))
				err = env.Create(context.Background(), environment.SessionConfig{SessionID: "sec1-fly", BaseImage: "img"})
			}
			if err == nil {
				t.Fatal("Create succeeded, want auth error")
			}
			if authHeader != "Bearer "+key {
				t.Fatalf("Authorization header = %q, want Bearer token", authHeader)
			}
			if strings.Contains(requestURI, key) {
				t.Fatalf("API key leaked in URL: %s", requestURI)
			}
			if strings.Contains(err.Error(), key) {
				t.Fatalf("API key leaked in error: %v", err)
			}
		})
	}
}

func TestF5_FlySSHTimeoutContext(t *testing.T) {
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

	env := fly.NewFlySandboxEnvironment("fly-token", "app-1", []byte("ignored"), "ssh-ed25519 AAAA",
		fly.WithBaseURL(ts.URL),
		fly.WithHTTPClient(ts.Client()),
		fly.WithSSHExecutor(func(ctx context.Context, _ string, _ string, _ func(environment.ToolProgress)) (*fly.SSHExecResult, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(2 * time.Second):
				return &fly.SSHExecResult{Stdout: "late", ExitCode: 0}, nil
			}
		}),
	)
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "f5-timeout", BaseImage: "img"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := env.ExecuteTool(ctx, environment.ToolRequest{
		ToolName: "bash",
		Params:   map[string]any{"command": "sleep 5"},
	}, nil)
	if err == nil {
		t.Fatal("ExecuteTool succeeded, want timeout error")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("unexpected timeout error: %v", err)
	}
}
