package client

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/rpc"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/transport"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox"
)

func TestSandboxServiceClientRoundTripCreateGet(t *testing.T) {
	svc := newMemSandboxService()
	srv := transport.NewServer(transport.ServerConfig{}, transport.WithSandboxService(svc))
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	client := NewSandboxServiceClient(ts.Client(), ts.URL, transport.ClientConfig{APIVersion: "v1"})
	ctx := context.Background()

	createResp, err := client.CreateSession(ctx, &api.CreateSessionRequest{SessionID: "sess-rt"})
	if err != nil {
		t.Fatalf("CreateSession error = %v", err)
	}
	if createResp.Session.ID != "sess-rt" {
		t.Fatalf("session id = %q, want sess-rt", createResp.Session.ID)
	}

	getResp, err := client.GetSession(ctx, &api.GetSessionRequest{SessionID: "sess-rt"})
	if err != nil {
		t.Fatalf("GetSession error = %v", err)
	}
	if getResp.Session.ID != "sess-rt" {
		t.Fatalf("get id = %q, want sess-rt", getResp.Session.ID)
	}
}

func TestSandboxServiceClientExecuteToolStream(t *testing.T) {
	svc := newMemSandboxService()
	_, _ = svc.CreateSession(context.Background(), &api.CreateSessionRequest{SessionID: "stream"})

	srv := transport.NewServer(transport.ServerConfig{}, transport.WithSandboxService(svc))
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	client := NewSandboxServiceClient(ts.Client(), ts.URL, transport.ClientConfig{APIVersion: "v1"})
	stream, err := client.ExecuteToolStream(context.Background(), &api.ExecuteToolRequest{
		SessionID:  "stream",
		ToolCallID: "tc-1",
		ToolName:   "read_file",
		Params:     map[string]any{"path": "README.md"},
	})
	if err != nil {
		t.Fatalf("ExecuteToolStream error = %v", err)
	}
	defer stream.Close()

	msg, err := stream.Recv()
	if err != nil {
		t.Fatalf("first Recv error = %v", err)
	}
	if msg.Progress == nil || msg.Progress.Content == "" {
		t.Fatalf("expected progress payload, got %+v", msg)
	}

	final, err := stream.Recv()
	if err != nil {
		t.Fatalf("second Recv error = %v", err)
	}
	if final.Response == nil || final.Response.ToolCallID != "tc-1" {
		t.Fatalf("expected final response for tc-1, got %+v", final)
	}

	_, err = stream.Recv()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestSandboxServiceClientErrorMapping(t *testing.T) {
	svc := newMemSandboxService()
	srv := transport.NewServer(transport.ServerConfig{}, transport.WithSandboxService(svc))
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	client := NewSandboxServiceClient(ts.Client(), ts.URL, transport.ClientConfig{APIVersion: "v1"})
	_, err := client.GetSession(context.Background(), &api.GetSessionRequest{SessionID: "missing"})
	if err == nil {
		t.Fatalf("expected error")
	}
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected rpc error, got %T", err)
	}
	if rpcErr.Code != rpc.CodeNotFound {
		t.Fatalf("code = %s, want %s", rpcErr.Code, rpc.CodeNotFound)
	}
}

type memSandboxService struct {
	mu       sync.Mutex
	sessions map[string]*api.Session
}

func newMemSandboxService() *memSandboxService {
	return &memSandboxService{sessions: make(map[string]*api.Session)}
}

func (m *memSandboxService) CreateSession(_ context.Context, req *api.CreateSessionRequest) (*api.CreateSessionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := req.SessionID
	if id == "" {
		id = "generated"
	}
	if _, exists := m.sessions[id]; exists {
		return nil, rpc.MapError(sandbox.ErrSessionExists)
	}
	now := time.Now().UTC()
	session := &api.Session{ID: id, State: "active", Created: now}
	m.sessions[id] = session
	return &api.CreateSessionResponse{
		Session: session,
		ServerCapabilities: api.Capabilities{
			Snapshots:         true,
			Rollback:          true,
			Pause:             true,
			StreamingProgress: true,
			TierRouting:       false,
		},
	}, nil
}

func (m *memSandboxService) GetSession(_ context.Context, req *api.GetSessionRequest) (*api.GetSessionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[req.SessionID]
	if !ok {
		return nil, rpc.MapError(sandbox.ErrSessionNotFound)
	}
	cp := *s
	return &api.GetSessionResponse{Session: &cp}, nil
}

func (m *memSandboxService) PauseSession(context.Context, *api.PauseSessionRequest) (*api.PauseSessionResponse, error) {
	return &api.PauseSessionResponse{}, nil
}

func (m *memSandboxService) ResumeSession(context.Context, *api.ResumeSessionRequest) (*api.ResumeSessionResponse, error) {
	return &api.ResumeSessionResponse{}, nil
}

func (m *memSandboxService) DestroySession(_ context.Context, req *api.DestroySessionRequest) (*api.DestroySessionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[req.SessionID]; !ok {
		return nil, rpc.MapError(sandbox.ErrSessionNotFound)
	}
	delete(m.sessions, req.SessionID)
	return &api.DestroySessionResponse{}, nil
}

func (m *memSandboxService) LaunchProcess(context.Context, *api.LaunchProcessRequest) (*api.LaunchProcessResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *memSandboxService) KillProcess(context.Context, *api.KillProcessRequest) (*api.KillProcessResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *memSandboxService) GetProcessStatus(context.Context, *api.GetProcessStatusRequest) (*api.GetProcessStatusResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *memSandboxService) ExecuteTool(_ context.Context, req *api.ExecuteToolRequest) (*api.ExecuteToolResponse, error) {
	return &api.ExecuteToolResponse{
		SessionID:  req.SessionID,
		ToolCallID: req.ToolCallID,
		ToolName:   req.ToolName,
		Content:    "ok",
	}, nil
}

func (m *memSandboxService) ExecuteToolStream(_ context.Context, req *api.ExecuteToolRequest) (api.ExecuteToolStreamReceiver, error) {
	return api.NewExecuteToolStream(
		&api.ExecuteToolStreamMessage{Progress: &api.ToolProgress{Content: "running", IsError: false}},
		&api.ExecuteToolStreamMessage{Response: &api.ExecuteToolResponse{
			SessionID:  req.SessionID,
			ToolCallID: req.ToolCallID,
			ToolName:   req.ToolName,
			Content:    "done",
		}},
	), nil
}

func (m *memSandboxService) TurnComplete(context.Context, *api.TurnCompleteRequest) (*api.TurnCompleteResponse, error) {
	return &api.TurnCompleteResponse{}, nil
}

func (m *memSandboxService) CreateSnapshot(context.Context, *api.CreateSnapshotRequest) (*api.CreateSnapshotResponse, error) {
	return &api.CreateSnapshotResponse{}, nil
}

func (m *memSandboxService) RollbackSession(context.Context, *api.RollbackSessionRequest) (*api.RollbackSessionResponse, error) {
	return &api.RollbackSessionResponse{}, nil
}

func (m *memSandboxService) ListSnapshots(context.Context, *api.ListSnapshotsRequest) (*api.ListSnapshotsResponse, error) {
	return &api.ListSnapshotsResponse{}, nil
}

func (m *memSandboxService) HealthCheck(context.Context, *api.HealthCheckRequest) (*api.HealthCheckResponse, error) {
	return &api.HealthCheckResponse{Status: "ok"}, nil
}
