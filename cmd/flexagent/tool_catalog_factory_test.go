package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/flex-agent-runtime/internal/agent"
	agentapi "github.com/anthropics/flex-agent-runtime/internal/agent/api"
	"github.com/anthropics/flex-agent-runtime/internal/ai"
	"github.com/anthropics/flex-agent-runtime/internal/rpc/api"
	"github.com/anthropics/flex-agent-runtime/internal/rpc/transport"
)

func TestRuntimeToolCatalogFactoryLocal(t *testing.T) {
	root := t.TempDir()
	factory := newRuntimeToolCatalogFactory(nil)

	catalog, err := factory(agentapi.SessionConfig{
		SessionID: "local-1",
		Tools:     []string{"write_file", "read_file"},
		ToolEnvironment: agentapi.ToolEnvironmentConfig{
			Type:         agentapi.ToolEnvLocal,
			LocalRootDir: root,
		},
	})
	if err != nil {
		t.Fatalf("factory error = %v", err)
	}

	writeTool := mustFindTool(t, catalog, "write_file")
	if _, err := writeTool.Execute(context.Background(), "tc-write", map[string]any{
		"path":    "hello.txt",
		"content": "hello-local",
	}, nil); err != nil {
		t.Fatalf("write_file error = %v", err)
	}

	readTool := mustFindTool(t, catalog, "read_file")
	result, err := readTool.Execute(context.Background(), "tc-read", map[string]any{"path": "hello.txt"}, nil)
	if err != nil {
		t.Fatalf("read_file error = %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatalf("expected read content")
	}
	text, ok := result.Content[0].(*ai.TextContent)
	if !ok || !strings.Contains(text.Text, "hello-local") {
		t.Fatalf("unexpected read content: %#v", result.Content[0])
	}
}

func TestRuntimeToolCatalogFactorySandbox(t *testing.T) {
	svc := &sandboxExecOnlyService{}
	server := transport.NewServer(transport.ServerConfig{}, transport.WithSandboxService(svc))
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	factory := newRuntimeToolCatalogFactory(nil)
	catalog, err := factory(agentapi.SessionConfig{
		SessionID: "sandbox-1",
		Tools:     []string{"read_file"},
		ToolEnvironment: agentapi.ToolEnvironmentConfig{
			Type:             agentapi.ToolEnvSandbox,
			SandboxHostAddr:  ts.URL,
			SandboxSessionID: "sbx-123",
		},
	})
	if err != nil {
		t.Fatalf("factory error = %v", err)
	}

	readTool := mustFindTool(t, catalog, "read_file")
	res, err := readTool.Execute(context.Background(), "tc-1", map[string]any{"path": "x.txt"}, nil)
	if err != nil {
		t.Fatalf("read_file error = %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("expected content")
	}
	if svc.lastReq == nil || svc.lastReq.SessionID != "sbx-123" {
		t.Fatalf("sandbox session id not propagated: %+v", svc.lastReq)
	}
}

func TestRuntimeToolCatalogFactorySandboxValidation(t *testing.T) {
	factory := newRuntimeToolCatalogFactory(nil)
	_, err := factory(agentapi.SessionConfig{
		SessionID: "sandbox-1",
		Tools:     []string{"read_file"},
		ToolEnvironment: agentapi.ToolEnvironmentConfig{
			Type:             agentapi.ToolEnvSandbox,
			SandboxHostAddr:  "",
			SandboxSessionID: "sid",
		},
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func mustFindTool(t *testing.T, catalog []agent.AgentTool, name string) agent.AgentTool {
	t.Helper()
	for _, tool := range catalog {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("missing tool %q", name)
	return agent.AgentTool{}
}

type sandboxExecOnlyService struct {
	lastReq *api.ExecuteToolRequest
}

func (s *sandboxExecOnlyService) CreateSession(context.Context, *api.CreateSessionRequest) (*api.CreateSessionResponse, error) {
	return nil, errors.New("not implemented")
}
func (s *sandboxExecOnlyService) GetSession(context.Context, *api.GetSessionRequest) (*api.GetSessionResponse, error) {
	return nil, errors.New("not implemented")
}
func (s *sandboxExecOnlyService) PauseSession(context.Context, *api.PauseSessionRequest) (*api.PauseSessionResponse, error) {
	return nil, errors.New("not implemented")
}
func (s *sandboxExecOnlyService) ResumeSession(context.Context, *api.ResumeSessionRequest) (*api.ResumeSessionResponse, error) {
	return nil, errors.New("not implemented")
}
func (s *sandboxExecOnlyService) DestroySession(context.Context, *api.DestroySessionRequest) (*api.DestroySessionResponse, error) {
	return nil, errors.New("not implemented")
}
func (s *sandboxExecOnlyService) LaunchProcess(context.Context, *api.LaunchProcessRequest) (*api.LaunchProcessResponse, error) {
	return nil, errors.New("not implemented")
}
func (s *sandboxExecOnlyService) KillProcess(context.Context, *api.KillProcessRequest) (*api.KillProcessResponse, error) {
	return nil, errors.New("not implemented")
}
func (s *sandboxExecOnlyService) GetProcessStatus(context.Context, *api.GetProcessStatusRequest) (*api.GetProcessStatusResponse, error) {
	return nil, errors.New("not implemented")
}
func (s *sandboxExecOnlyService) ExecuteTool(context.Context, *api.ExecuteToolRequest) (*api.ExecuteToolResponse, error) {
	return nil, errors.New("not implemented")
}
func (s *sandboxExecOnlyService) ExecuteToolStream(_ context.Context, req *api.ExecuteToolRequest) (api.ExecuteToolStreamReceiver, error) {
	s.lastReq = req
	return api.NewExecuteToolStream(&api.ExecuteToolStreamMessage{
		Response: &api.ExecuteToolResponse{
			SessionID:  req.SessionID,
			ToolCallID: req.ToolCallID,
			ToolName:   req.ToolName,
			Content:    "sandbox-ok",
		},
	}), nil
}
func (s *sandboxExecOnlyService) TurnComplete(context.Context, *api.TurnCompleteRequest) (*api.TurnCompleteResponse, error) {
	return nil, errors.New("not implemented")
}
func (s *sandboxExecOnlyService) CreateSnapshot(context.Context, *api.CreateSnapshotRequest) (*api.CreateSnapshotResponse, error) {
	return nil, errors.New("not implemented")
}
func (s *sandboxExecOnlyService) RollbackSession(context.Context, *api.RollbackSessionRequest) (*api.RollbackSessionResponse, error) {
	return nil, errors.New("not implemented")
}
func (s *sandboxExecOnlyService) ListSnapshots(context.Context, *api.ListSnapshotsRequest) (*api.ListSnapshotsResponse, error) {
	return nil, errors.New("not implemented")
}
func (s *sandboxExecOnlyService) HealthCheck(context.Context, *api.HealthCheckRequest) (*api.HealthCheckResponse, error) {
	return nil, errors.New("not implemented")
}
