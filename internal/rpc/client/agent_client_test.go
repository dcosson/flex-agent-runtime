package client

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/agent"
	agentapi "github.com/anthropics/flex-agent-runtime/internal/agent/api"
	"github.com/anthropics/flex-agent-runtime/internal/rpc"
	"github.com/anthropics/flex-agent-runtime/internal/rpc/transport"
)

func TestAgentServiceClientRoundTripCreateGetDestroy(t *testing.T) {
	svc := newMemAgentService()
	srv := transport.NewServer(transport.ServerConfig{}, transport.WithAgentService(svc))
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	client := NewAgentServiceClient(ts.Client(), ts.URL, transport.ClientConfig{APIVersion: "v1"})
	ctx := context.Background()

	resp, err := client.CreateSession(ctx, &agentapi.CreateAgentSessionRequest{SessionConfig: agentapi.SessionConfig{SessionID: "rpc-rt-1"}})
	if err != nil {
		t.Fatalf("CreateSession error = %v", err)
	}
	if resp.SessionID != "rpc-rt-1" {
		t.Fatalf("SessionID = %q, want rpc-rt-1", resp.SessionID)
	}

	getResp, err := client.GetSession(ctx, &agentapi.GetAgentSessionRequest{SessionID: "rpc-rt-1"})
	if err != nil {
		t.Fatalf("GetSession error = %v", err)
	}
	if getResp.State != "idle" {
		t.Fatalf("state = %q, want idle", getResp.State)
	}

	listResp, err := client.ListSessions(ctx, &agentapi.ListAgentSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions error = %v", err)
	}
	if len(listResp.Sessions) != 1 {
		t.Fatalf("list len = %d, want 1", len(listResp.Sessions))
	}

	if _, err := client.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{SessionID: "rpc-rt-1"}); err != nil {
		t.Fatalf("DestroySession error = %v", err)
	}
}

func TestAgentServiceClientSendMessageStream(t *testing.T) {
	svc := newMemAgentService()
	_, _ = svc.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{SessionConfig: agentapi.SessionConfig{SessionID: "rpc-stream"}})

	srv := transport.NewServer(transport.ServerConfig{}, transport.WithAgentService(svc))
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	client := NewAgentServiceClient(ts.Client(), ts.URL, transport.ClientConfig{APIVersion: "v1"})
	recv, err := client.SendMessage(context.Background(), &agentapi.SendMessageRequest{SessionID: "rpc-stream", Message: "hello over RPC"})
	if err != nil {
		t.Fatalf("SendMessage error = %v", err)
	}
	defer recv.Close()

	var events []*agentapi.AgentEvent
	for {
		evt, recvErr := recv.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			t.Fatalf("Recv error = %v", recvErr)
		}
		if evt != nil {
			events = append(events, evt)
		}
	}
	if len(events) == 0 {
		t.Fatalf("expected streamed events")
	}
	if events[0].Type != agentapi.EventTurnStarted {
		t.Fatalf("first event = %s, want %s", events[0].Type, agentapi.EventTurnStarted)
	}
	if events[len(events)-1].Type != agentapi.EventTurnCompleted {
		t.Fatalf("last event = %s, want %s", events[len(events)-1].Type, agentapi.EventTurnCompleted)
	}
}

func TestAgentServiceClientErrorMapping(t *testing.T) {
	svc := newMemAgentService()
	srv := transport.NewServer(transport.ServerConfig{}, transport.WithAgentService(svc))
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	client := NewAgentServiceClient(ts.Client(), ts.URL, transport.ClientConfig{APIVersion: "v1"})
	ctx := context.Background()

	_, err := client.GetSession(ctx, &agentapi.GetAgentSessionRequest{SessionID: "nonexistent"})
	assertAgentRPCError(t, err, rpc.CodeNotFound)

	_, _ = client.CreateSession(ctx, &agentapi.CreateAgentSessionRequest{SessionConfig: agentapi.SessionConfig{SessionID: "dup-rpc"}})
	_, err = client.CreateSession(ctx, &agentapi.CreateAgentSessionRequest{SessionConfig: agentapi.SessionConfig{SessionID: "dup-rpc"}})
	assertAgentRPCError(t, err, rpc.CodeAlreadyExists)
}

func assertAgentRPCError(t *testing.T, err error, want rpc.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error")
	}
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected rpc error, got %T", err)
	}
	if rpcErr.Code != want {
		t.Fatalf("code = %s, want %s", rpcErr.Code, want)
	}
}

type memAgentService struct {
	mu       sync.Mutex
	sessions map[string]time.Time
}

func newMemAgentService() *memAgentService {
	return &memAgentService{sessions: make(map[string]time.Time)}
}

func (m *memAgentService) CreateSession(_ context.Context, req *agentapi.CreateAgentSessionRequest) (*agentapi.CreateAgentSessionResponse, error) {
	if req == nil {
		return nil, &agent.ServiceError{Code: agent.CodeInvalidArgument, Message: "nil request"}
	}
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = "generated"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sessions[sessionID]; exists {
		return nil, &agent.ServiceError{Code: agent.CodeAlreadyExists, Message: "session exists"}
	}
	m.sessions[sessionID] = time.Now()
	return &agentapi.CreateAgentSessionResponse{SessionID: sessionID, State: "idle"}, nil
}

func (m *memAgentService) GetSession(_ context.Context, req *agentapi.GetAgentSessionRequest) (*agentapi.GetAgentSessionResponse, error) {
	if req == nil {
		return nil, &agent.ServiceError{Code: agent.CodeInvalidArgument, Message: "nil request"}
	}
	m.mu.Lock()
	_, exists := m.sessions[req.SessionID]
	m.mu.Unlock()
	if !exists {
		return nil, &agent.ServiceError{Code: agent.CodeNotFound, Message: "missing"}
	}
	return &agentapi.GetAgentSessionResponse{SessionID: req.SessionID, State: "idle"}, nil
}

func (m *memAgentService) ListSessions(_ context.Context, _ *agentapi.ListAgentSessionsRequest) (*agentapi.ListAgentSessionsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]agentapi.AgentSessionSummary, 0, len(m.sessions))
	for id, created := range m.sessions {
		out = append(out, agentapi.AgentSessionSummary{SessionID: id, State: "idle", CreatedAt: created})
	}
	return &agentapi.ListAgentSessionsResponse{Sessions: out}, nil
}

func (m *memAgentService) SendMessage(_ context.Context, req *agentapi.SendMessageRequest) (agentapi.EventReceiver, error) {
	if _, err := m.GetSession(context.Background(), &agentapi.GetAgentSessionRequest{SessionID: req.SessionID}); err != nil {
		return nil, err
	}
	return &sliceEventReceiver{events: []*agentapi.AgentEvent{
		{Type: agentapi.EventTurnStarted, SessionID: req.SessionID, Turn: 1},
		{Type: agentapi.EventTurnCompleted, SessionID: req.SessionID, Turn: 1},
	}}, nil
}

func (m *memAgentService) Continue(_ context.Context, req *agentapi.ContinueRequest) (agentapi.EventReceiver, error) {
	return &sliceEventReceiver{events: []*agentapi.AgentEvent{{Type: agentapi.EventTurnCompleted, SessionID: req.SessionID, Turn: 1}}}, nil
}

func (m *memAgentService) Steer(context.Context, *agentapi.SteerRequest) (*agentapi.SteerResponse, error) {
	return &agentapi.SteerResponse{}, nil
}

func (m *memAgentService) FollowUp(context.Context, *agentapi.FollowUpRequest) (*agentapi.FollowUpResponse, error) {
	return &agentapi.FollowUpResponse{}, nil
}

func (m *memAgentService) Abort(context.Context, *agentapi.AbortRequest) (*agentapi.AbortResponse, error) {
	return &agentapi.AbortResponse{}, nil
}

func (m *memAgentService) SubscribeEvents(_ context.Context, req *agentapi.SubscribeEventsRequest) (agentapi.EventReceiver, error) {
	return &sliceEventReceiver{events: []*agentapi.AgentEvent{{Type: agentapi.EventSessionStarted, SessionID: req.SessionID}}}, nil
}

func (m *memAgentService) ResumeSession(_ context.Context, req *agentapi.ResumeSessionRequest) (*agentapi.ResumeSessionResponse, error) {
	if _, err := m.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{SessionConfig: req.SessionConfig}); err != nil {
		return nil, err
	}
	return &agentapi.ResumeSessionResponse{SessionID: req.SessionID, State: "idle", ConversationLen: len(req.ConversationLog)}, nil
}

func (m *memAgentService) DestroySession(_ context.Context, req *agentapi.DestroyAgentSessionRequest) (*agentapi.DestroyAgentSessionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sessions[req.SessionID]; !exists {
		return nil, &agent.ServiceError{Code: agent.CodeNotFound, Message: "missing"}
	}
	delete(m.sessions, req.SessionID)
	return &agentapi.DestroyAgentSessionResponse{}, nil
}

func (m *memAgentService) Close() error { return nil }

type sliceEventReceiver struct {
	events []*agentapi.AgentEvent
}

func (r *sliceEventReceiver) Recv() (*agentapi.AgentEvent, error) {
	if len(r.events) == 0 {
		return nil, io.EOF
	}
	evt := r.events[0]
	r.events = r.events[1:]
	return evt, nil
}

func (r *sliceEventReceiver) Close() error { return nil }
