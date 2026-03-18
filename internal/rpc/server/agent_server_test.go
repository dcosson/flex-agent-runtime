package server

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/agent"
	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc"
)

func TestAgentRPCServerGetSession(t *testing.T) {
	service := &fakeAgentService{
		getSessionResp: &agentapi.GetAgentSessionResponse{
			SessionID: "s1",
			State:     "idle",
			Metrics: agentapi.SessionMetrics{
				TurnsStarted:      1,
				TurnsCompleted:    2,
				MessagesAppended:  3,
				ToolCallsStarted:  4,
				ToolCallsFinished: 5,
				Errors:            6,
			},
			ConversationLen: 9,
		},
	}
	srv := NewAgentRPCServer(service)

	resp, err := srv.GetSession(context.Background(), &agentapi.GetAgentSessionRequest{SessionID: "s1"})
	if err != nil {
		t.Fatalf("GetSession error = %v", err)
	}
	if resp == nil || resp.SessionID != "s1" || resp.ConversationLen != 9 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Metrics.TurnsCompleted != 2 {
		t.Fatalf("metrics mismatch: %+v", resp.Metrics)
	}
}

func TestAgentRPCServerSendMessageStream(t *testing.T) {
	service := &fakeAgentService{
		sendMessageRecv: &stubAgentReceiver{events: []*agentapi.AgentEvent{{
			Type: agentapi.EventTurnStarted,
			Turn: 1,
			At:   time.Unix(1, 0),
		}}},
	}
	srv := NewAgentRPCServer(service)

	recv, err := srv.SendMessage(context.Background(), &agentapi.SendMessageRequest{SessionID: "s1", Message: "hello"})
	if err != nil {
		t.Fatalf("SendMessage error = %v", err)
	}
	env, err := recv.Recv()
	if err != nil {
		t.Fatalf("Recv error = %v", err)
	}
	if env == nil {
		t.Fatalf("expected envelope")
	}
	if env.SessionID != "s1" {
		t.Fatalf("session_id = %q, want s1", env.SessionID)
	}
	if env.Event.Type != agent.EventTurnStarted {
		t.Fatalf("event type = %q, want %q", env.Event.Type, agent.EventTurnStarted)
	}
	if env.Event.SessionID != "s1" {
		t.Fatalf("event session_id = %q, want s1", env.Event.SessionID)
	}
	if err := recv.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestAgentRPCServerMapsErrors(t *testing.T) {
	service := &fakeAgentService{
		getSessionErr: &agent.ServiceError{Code: agent.CodeNotFound, Message: "missing"},
	}
	srv := NewAgentRPCServer(service)

	_, err := srv.GetSession(context.Background(), &agentapi.GetAgentSessionRequest{SessionID: "missing"})
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected rpc error, got %T", err)
	}
	if rpcErr.Code != rpc.CodeNotFound {
		t.Fatalf("code = %s, want %s", rpcErr.Code, rpc.CodeNotFound)
	}
}

func TestAgentRPCServerNilService(t *testing.T) {
	srv := NewAgentRPCServer(nil)
	_, err := srv.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{})
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected rpc error, got %T", err)
	}
	if rpcErr.Code != rpc.CodeUnavailable {
		t.Fatalf("code = %s, want %s", rpcErr.Code, rpc.CodeUnavailable)
	}
}

type fakeAgentService struct {
	createSessionResp   *agentapi.CreateAgentSessionResponse
	createSessionErr    error
	getSessionResp      *agentapi.GetAgentSessionResponse
	getSessionErr       error
	listSessionsResp    *agentapi.ListAgentSessionsResponse
	listSessionsErr     error
	sendMessageRecv     agentapi.EventReceiver
	sendMessageErr      error
	continueRecv        agentapi.EventReceiver
	continueErr         error
	steerResp           *agentapi.SteerResponse
	steerErr            error
	followUpResp        *agentapi.FollowUpResponse
	followUpErr         error
	abortResp           *agentapi.AbortResponse
	abortErr            error
	subscribeEventsRecv agentapi.EventReceiver
	subscribeEventsErr  error
	resumeResp          *agentapi.ResumeSessionResponse
	resumeErr           error
	destroyResp         *agentapi.DestroyAgentSessionResponse
	destroyErr          error
	closeErr            error
}

func (f *fakeAgentService) CreateSession(context.Context, *agentapi.CreateAgentSessionRequest) (*agentapi.CreateAgentSessionResponse, error) {
	return f.createSessionResp, f.createSessionErr
}

func (f *fakeAgentService) GetSession(context.Context, *agentapi.GetAgentSessionRequest) (*agentapi.GetAgentSessionResponse, error) {
	return f.getSessionResp, f.getSessionErr
}

func (f *fakeAgentService) ListSessions(context.Context, *agentapi.ListAgentSessionsRequest) (*agentapi.ListAgentSessionsResponse, error) {
	return f.listSessionsResp, f.listSessionsErr
}

func (f *fakeAgentService) SendMessage(context.Context, *agentapi.SendMessageRequest) (agentapi.EventReceiver, error) {
	return f.sendMessageRecv, f.sendMessageErr
}

func (f *fakeAgentService) Continue(context.Context, *agentapi.ContinueRequest) (agentapi.EventReceiver, error) {
	return f.continueRecv, f.continueErr
}

func (f *fakeAgentService) Steer(context.Context, *agentapi.SteerRequest) (*agentapi.SteerResponse, error) {
	return f.steerResp, f.steerErr
}

func (f *fakeAgentService) FollowUp(context.Context, *agentapi.FollowUpRequest) (*agentapi.FollowUpResponse, error) {
	return f.followUpResp, f.followUpErr
}

func (f *fakeAgentService) Abort(context.Context, *agentapi.AbortRequest) (*agentapi.AbortResponse, error) {
	return f.abortResp, f.abortErr
}

func (f *fakeAgentService) SubscribeEvents(context.Context, *agentapi.SubscribeEventsRequest) (agentapi.EventReceiver, error) {
	return f.subscribeEventsRecv, f.subscribeEventsErr
}

func (f *fakeAgentService) ResumeSession(context.Context, *agentapi.ResumeSessionRequest) (*agentapi.ResumeSessionResponse, error) {
	return f.resumeResp, f.resumeErr
}

func (f *fakeAgentService) DestroySession(context.Context, *agentapi.DestroyAgentSessionRequest) (*agentapi.DestroyAgentSessionResponse, error) {
	return f.destroyResp, f.destroyErr
}

func (f *fakeAgentService) Close() error {
	return f.closeErr
}

type stubAgentReceiver struct {
	events []*agentapi.AgentEvent
	closed bool
}

func (r *stubAgentReceiver) Recv() (*agentapi.AgentEvent, error) {
	if len(r.events) == 0 {
		return nil, io.EOF
	}
	evt := r.events[0]
	r.events = r.events[1:]
	return evt, nil
}

func (r *stubAgentReceiver) Close() error {
	r.closed = true
	return nil
}
