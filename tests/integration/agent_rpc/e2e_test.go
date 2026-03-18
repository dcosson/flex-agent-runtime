package agentrpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/agent"
	"github.com/dcosson/flex-agent-runtime/internal/agent/agenttest"
	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/api"
	rpcclient "github.com/dcosson/flex-agent-runtime/internal/rpc/client"
	rpcserver "github.com/dcosson/flex-agent-runtime/internal/rpc/server"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/transport"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/node"
	"github.com/dcosson/flex-agent-runtime/internal/termmux/driver"
	"github.com/dcosson/flex-agent-runtime/internal/termmux/driver/claudecode"
)

const helperProcessEnv = "AGENT_RPC_HELPER_PROCESS"

func TestAgentRPCServeAgentHelperProcess(t *testing.T) {
	if os.Getenv(helperProcessEnv) != "1" {
		return
	}
	listenAddr := helperListenAddr()
	if listenAddr == "" {
		fmt.Fprintln(os.Stderr, "missing --listen for helper process")
		os.Exit(2)
	}
	if err := runHelperAgentServer(listenAddr); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(2)
	}
	os.Exit(0)
}

func TestE2E_OrchestratorAgentSandbox(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	sandboxSvc, ctrl := newSandboxHostControl(t)

	sandboxResp, err := ctrl.CreateSandbox(ctx, control.CreateSandboxRequest{
		Template:  "",
		Labels:    map[string]string{"suite": "agent-rpc-e2e"},
		Resources: control.ResourceSpec{CPUs: 1, MemMB: 512},
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	t.Cleanup(func() {
		_ = ctrl.DestroySandbox(context.Background(), sandboxResp.SandboxID)
	})

	// Verify real tool execution inside the created sandbox filesystem.
	writeReq := &api.ExecuteToolRequest{
		SessionID:  sandboxResp.SandboxID,
		ToolCallID: "tc-write-e2e",
		ToolName:   "write_file",
		Params:     map[string]any{"path": "state.txt", "content": "hello from sandbox"},
	}
	if _, err := sandboxSvc.ExecuteTool(ctx, writeReq); err != nil {
		t.Fatalf("sandbox ExecuteTool(write_file): %v", err)
	}
	readResp, err := sandboxSvc.ExecuteTool(ctx, &api.ExecuteToolRequest{
		SessionID:  sandboxResp.SandboxID,
		ToolCallID: "tc-read-e2e",
		ToolName:   "read_file",
		Params:     map[string]any{"path": "state.txt"},
	})
	if err != nil {
		t.Fatalf("sandbox ExecuteTool(read_file): %v", err)
	}
	if !strings.Contains(readResp.Content, "hello from sandbox") {
		t.Fatalf("sandbox read content mismatch: %q", readResp.Content)
	}

	processID, address := launchHelperProcess(t, ctx, ctrl, sandboxResp.SandboxID)
	agentClient := rpcclient.NewAgentServiceClient(
		&http.Client{Timeout: 10 * time.Second},
		control.AddressToURL(address),
		transport.ClientConfig{APIVersion: "v1"},
	)

	provider, model := pickAnyModel(t)
	sessionID := "e2e-main"
	_, err = agentClient.CreateSession(ctx, &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			SessionID: sessionID,
			Driver:    "mock",
			Provider:  provider,
			Model:     model,
			Tools:     []string{"read_file", "write_file"},
			ToolEnvironment: agentapi.ToolEnvironmentConfig{
				Type: agentapi.ToolEnvLocal,
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateSession(agent): %v", err)
	}

	recv, err := agentClient.SendMessage(ctx, &agentapi.SendMessageRequest{
		SessionID: sessionID,
		Message:   "perform one turn with tool activity",
	})
	if err != nil {
		t.Fatalf("SendMessage(agent): %v", err)
	}
	events, err := collectEvents(recv)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}
	assertEventSequence(t, events, agentapi.EventTurnStarted, agentapi.EventToolStarted, agentapi.EventToolCompleted, agentapi.EventTurnCompleted)

	// Session forking from a common conversation log.
	resumeLog := buildResumeLog(t, 3)
	for i := 0; i < 3; i++ {
		forkID := fmt.Sprintf("fork-%d", i)
		_, err := agentClient.ResumeSession(ctx, &agentapi.ResumeSessionRequest{
			SessionConfig: agentapi.SessionConfig{
				SessionID: forkID,
				Driver:    "mock",
				Provider:  provider,
				Model:     model,
				ToolEnvironment: agentapi.ToolEnvironmentConfig{
					Type: agentapi.ToolEnvLocal,
				},
			},
			SchemaVersion:   agentapi.CurrentConversationSchemaVersion,
			ConversationLog: resumeLog,
		})
		if err != nil {
			t.Fatalf("ResumeSession(%s): %v", forkID, err)
		}
		recv, err := agentClient.SendMessage(ctx, &agentapi.SendMessageRequest{
			SessionID: forkID,
			Message:   fmt.Sprintf("fork prompt %d", i),
		})
		if err != nil {
			t.Fatalf("SendMessage(%s): %v", forkID, err)
		}
		if _, err := collectEvents(recv); err != nil {
			t.Fatalf("collect events(%s): %v", forkID, err)
		}
	}

	// Cross-agent resume conversion chain: AgentMessageRecord -> ConversationEntry -> session.jsonl round-trip.
	verifyCrossAgentRoundTrip(t)

	// Connection drop recovery: kill process, relaunch, and resume.
	if err := ctrl.KillProcess(ctx, control.KillProcessRequest{SandboxID: sandboxResp.SandboxID, ProcessID: processID, Signal: int(syscall.SIGKILL)}); err != nil {
		t.Fatalf("KillProcess(primary): %v", err)
	}
	waitProcessExited(t, ctx, ctrl, sandboxResp.SandboxID, processID)

	replacementID, replacementAddr := launchHelperProcess(t, ctx, ctrl, sandboxResp.SandboxID)
	replacement := rpcclient.NewAgentServiceClient(
		&http.Client{Timeout: 10 * time.Second},
		control.AddressToURL(replacementAddr),
		transport.ClientConfig{APIVersion: "v1"},
	)

	_, err = replacement.ResumeSession(ctx, &agentapi.ResumeSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			SessionID: "recovered",
			Driver:    "mock",
			Provider:  provider,
			Model:     model,
			ToolEnvironment: agentapi.ToolEnvironmentConfig{
				Type: agentapi.ToolEnvLocal,
			},
		},
		SchemaVersion:   agentapi.CurrentConversationSchemaVersion,
		ConversationLog: resumeLog,
	})
	if err != nil {
		t.Fatalf("ResumeSession(recovered): %v", err)
	}
	recoveredRecv, err := replacement.SendMessage(ctx, &agentapi.SendMessageRequest{
		SessionID: "recovered",
		Message:   "continue after process restart",
	})
	if err != nil {
		t.Fatalf("SendMessage(recovered): %v", err)
	}
	if _, err := collectEvents(recoveredRecv); err != nil {
		t.Fatalf("collect events(recovered): %v", err)
	}

	// Graceful shutdown path via SIGTERM, then verify exited state.
	if err := ctrl.KillProcess(ctx, control.KillProcessRequest{SandboxID: sandboxResp.SandboxID, ProcessID: replacementID, Signal: int(syscall.SIGTERM)}); err != nil {
		t.Fatalf("KillProcess(replacement): %v", err)
	}
	waitProcessExited(t, ctx, ctrl, sandboxResp.SandboxID, replacementID)
}

func newSandboxHostControl(t *testing.T) (*rpcserver.SandboxServer, *node.NodeSandboxControl) {
	t.Helper()
	cfg := sandbox.DefaultServiceConfig()
	cfg.StorageBackend = sandbox.StorageBackendLocalDisk
	cfg.ContainerRuntime = sandbox.ContainerRuntimeNone
	cfg.SessionsRootDir = t.TempDir()
	cfg.AdvertiseAddr = "127.0.0.1"
	host, err := sandbox.NewSandboxHostService(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewSandboxHostService: %v", err)
	}
	svc := rpcserver.NewSandboxServer(host)
	t.Cleanup(func() { _ = svc.Close() })
	return svc, node.NewNodeSandboxControl(svc)
}

func launchHelperProcess(t *testing.T, ctx context.Context, ctrl control.SandboxControl, sandboxID string) (string, string) {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	const maxAttempts = 4
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Reserve+bind is inherently TOCTOU. Mitigate flake risk by retrying with a
		// fresh port when the helper cannot come up quickly on the selected port.
		port := reservePort(t)
		launch, launchErr := ctrl.LaunchProcess(ctx, control.LaunchProcessRequest{
			SandboxID:  sandboxID,
			Binary:     bin,
			Args:       []string{"-test.run", "TestAgentRPCServeAgentHelperProcess", "--", "-listen", ":" + strconv.Itoa(port)},
			Env:        map[string]string{helperProcessEnv: "1"},
			ExposePort: port,
		})
		if launchErr != nil {
			lastErr = launchErr
			continue
		}
		if launch.ProcessID == "" || launch.Address == "" {
			lastErr = fmt.Errorf("invalid launch response: %+v", launch)
			continue
		}
		client := rpcclient.NewAgentServiceClient(
			&http.Client{Timeout: 2 * time.Second},
			control.AddressToURL(launch.Address),
			transport.ClientConfig{APIVersion: "v1"},
		)
		readyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		readyErr := waitAgentReady(readyCtx, client)
		cancel()
		if readyErr == nil {
			return launch.ProcessID, launch.Address
		}
		lastErr = readyErr
		_ = ctrl.KillProcess(context.Background(), control.KillProcessRequest{
			SandboxID: sandboxID,
			ProcessID: launch.ProcessID,
			Signal:    int(syscall.SIGKILL),
		})
		_, _ = ctrl.GetProcessStatus(context.Background(), control.GetProcessStatusRequest{
			SandboxID: sandboxID,
			ProcessID: launch.ProcessID,
		})
	}
	t.Fatalf("launch helper process failed after %d attempts: %v", maxAttempts, lastErr)
	return "", ""
}

func waitAgentReady(ctx context.Context, c *rpcclient.AgentServiceClient) error {
	for {
		_, err := c.ListSessions(ctx, &agentapi.ListAgentSessionsRequest{})
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("agent service not ready before deadline: %w", err)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func waitProcessExited(t *testing.T, ctx context.Context, ctrl control.SandboxControl, sandboxID, processID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, err := ctrl.GetProcessStatus(ctx, control.GetProcessStatusRequest{
			SandboxID: sandboxID,
			ProcessID: processID,
		})
		if err != nil {
			t.Fatalf("GetProcessStatus(%s): %v", processID, err)
		}
		if status.Status == control.ProcessExited {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %s did not exit in time (status=%q)", processID, status.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func collectEvents(recv agentapi.EventReceiver) ([]agentapi.AgentEvent, error) {
	defer recv.Close()
	events := make([]agentapi.AgentEvent, 0, 16)
	for {
		evt, err := recv.Recv()
		if errors.Is(err, io.EOF) {
			return events, nil
		}
		if err != nil {
			return nil, err
		}
		if evt != nil {
			events = append(events, *evt)
		}
	}
}

func assertEventSequence(t *testing.T, events []agentapi.AgentEvent, want ...agentapi.AgentEventType) {
	t.Helper()
	idx := 0
	for _, evt := range events {
		if idx < len(want) && evt.Type == want[idx] {
			idx++
		}
	}
	if idx != len(want) {
		t.Fatalf("missing event sequence, matched=%d/%d events=%v want=%v", idx, len(want), eventTypes(events), want)
	}
}

func eventTypes(events []agentapi.AgentEvent) []agentapi.AgentEventType {
	out := make([]agentapi.AgentEventType, 0, len(events))
	for _, evt := range events {
		out = append(out, evt.Type)
	}
	return out
}

func pickAnyModel(t *testing.T) (string, string) {
	t.Helper()
	for _, provider := range ai.GetModelProviders() {
		models := ai.GetModels(provider)
		if len(models) > 0 {
			return provider, models[0].ID
		}
	}
	t.Fatal("no models registered")
	return "", ""
}

func buildResumeLog(t *testing.T, turns int) []agentapi.AgentMessageRecord {
	t.Helper()
	out := make([]agentapi.AgentMessageRecord, 0, turns*2)
	for turn := 1; turn <= turns; turn++ {
		user, err := agentapi.AgentMessageToRecord(agentapi.AgentMessage{
			Turn:      turn,
			CreatedAt: time.Now(),
			Message: &ai.UserMessage{
				Content: []ai.ContentBlock{&ai.TextContent{Text: fmt.Sprintf("prompt-%d", turn)}},
			},
		})
		if err != nil {
			t.Fatalf("user record: %v", err)
		}
		assistant, err := agentapi.AgentMessageToRecord(agentapi.AgentMessage{
			Turn:      turn,
			CreatedAt: time.Now(),
			Message: &ai.AssistantMessage{
				Content: []ai.ContentBlock{&ai.TextContent{Text: fmt.Sprintf("reply-%d", turn)}},
			},
		})
		if err != nil {
			t.Fatalf("assistant record: %v", err)
		}
		out = append(out, user, assistant)
	}
	return out
}

func verifyCrossAgentRoundTrip(t *testing.T) {
	t.Helper()
	base := time.Date(2026, time.March, 17, 12, 0, 0, 0, time.UTC)
	records := []agentapi.AgentMessageRecord{
		mustRecord(t, agentapi.AgentMessage{
			Turn:      1,
			CreatedAt: base,
			Message: &ai.UserMessage{
				Content: []ai.ContentBlock{&ai.TextContent{Text: "Fix main.go"}},
			},
		}),
		mustRecord(t, agentapi.AgentMessage{
			Turn:      1,
			CreatedAt: base.Add(time.Second),
			Message: &ai.AssistantMessage{
				Content: []ai.ContentBlock{
					&ai.TextContent{Text: "I will inspect main.go"},
					&ai.ToolCall{ID: "tc-1", Name: "read_file", Arguments: map[string]any{"path": "main.go"}},
				},
			},
		}),
		mustRecord(t, agentapi.AgentMessage{
			Turn:      1,
			CreatedAt: base.Add(2 * time.Second),
			Message: &ai.ToolResultMessage{
				ToolCallID: "tc-1",
				ToolName:   "read_file",
				Content: []ai.ContentBlock{
					&ai.TextContent{Text: "package main"},
				},
			},
		}),
	}

	entries := make([]driver.ConversationEntry, 0, len(records)*2)
	for _, rec := range records {
		msg, err := agentapi.RecordToAgentMessage(rec)
		if err != nil {
			t.Fatalf("RecordToAgentMessage: %v", err)
		}
		entries = append(entries, agentapi.AgentMessageToConversationEntries(msg)...)
	}
	var b strings.Builder
	if err := claudecode.WriteSessionLog(entries, &b); err != nil {
		t.Fatalf("WriteSessionLog: %v", err)
	}
	parsed, err := claudecode.ParseSessionLog(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("ParseSessionLog: %v", err)
	}
	if len(parsed) == 0 {
		t.Fatalf("expected parsed entries")
	}
	if parsed[0].Role != driver.RoleUser {
		t.Fatalf("first parsed role = %q, want %q", parsed[0].Role, driver.RoleUser)
	}
	var hasToolUse, hasToolResult bool
	for _, entry := range parsed {
		switch entry.Role {
		case driver.RoleToolUse:
			if entry.ToolCall != nil && entry.ToolCall.CallID == "tc-1" && entry.ToolCall.Name == "read_file" {
				hasToolUse = true
			}
		case driver.RoleToolResult:
			if entry.ToolCall != nil && entry.ToolCall.CallID == "tc-1" && entry.ToolCall.Result != "" {
				hasToolResult = true
			}
		}
	}
	if !hasToolUse {
		t.Fatalf("parsed session log missing tool_use for tc-1/read_file")
	}
	if !hasToolResult {
		t.Fatalf("parsed session log missing tool_result for tc-1")
	}
}

func mustRecord(t *testing.T, msg agentapi.AgentMessage) agentapi.AgentMessageRecord {
	t.Helper()
	rec, err := agentapi.AgentMessageToRecord(msg)
	if err != nil {
		t.Fatalf("AgentMessageToRecord: %v", err)
	}
	return rec
}

func helperListenAddr() string {
	for i := 0; i < len(os.Args)-1; i++ {
		if os.Args[i] == "-listen" {
			return os.Args[i+1]
		}
	}
	return ""
}

func runHelperAgentServer(listenAddr string) error {
	service := agent.NewAgentLoopService(&agenttest.MockEventPublisher{}, agent.WithCloseDrainTimeout(5*time.Second))
	drivers := agenttest.NewMockDriverFactory()
	drivers.SetDriver("default", &agenttest.MockAgentDriver{
		TurnScript: []agent.AgentEvent{
			{Type: agent.EventTurnStarted, Turn: 1},
			{Type: agent.EventToolStarted, Turn: 1, ToolName: "read_file", ToolCallID: "tc-1"},
			{Type: agent.EventToolCompleted, Turn: 1, ToolName: "read_file", ToolCallID: "tc-1"},
			{Type: agent.EventAgentMessageCompleted, Turn: 1},
			{Type: agent.EventTurnCompleted, Turn: 1},
		},
	})
	service.SetDriverFactory(drivers.Create)

	server := transport.NewServer(transport.ServerConfig{}, transport.WithAgentService(service))
	mux := http.NewServeMux()
	mux.Handle("/", server.Handler())
	httpServer := &http.Server{Addr: listenAddr, Handler: mux}
	go func() {
		_ = httpServer.ListenAndServe()
	}()

	// Block until signal, then drain and shutdown.
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()
	_ = service.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

func reservePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestHelperArgParsing(t *testing.T) {
	// Coverage for helper-side parsing/utility paths.
	orig := os.Args
	t.Cleanup(func() { os.Args = orig })
	os.Args = []string{"testbin", "-test.run", "X", "--", "-listen", ":12345"}
	if got := helperListenAddr(); got != ":12345" {
		t.Fatalf("helperListenAddr() = %q, want :12345", got)
	}
}
