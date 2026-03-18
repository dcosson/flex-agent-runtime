package orchestrator

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
)

func TestCreateSessionAgentDirectRoutesAndRewritesIDs(t *testing.T) {
	sc := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "direct:i-123", Address: "10.0.0.10:0"},
		launchResp: &control.LaunchProcessResponse{ProcessID: "proc-1", Address: "10.0.0.10:8081", Status: control.ProcessRunning},
	}
	agentSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "remote-1", State: "idle"},
		sendReceiver: &sliceEventReceiver{events: []*agentapi.AgentEvent{{
			Type:      agentapi.EventAgentMessageDelta,
			SessionID: "remote-1",
			Delta:     "hello",
			ToolName:  "x",
		}}},
	}
	orch := mustNewOrchestrator(t, sc, agentSvc, 0)

	resp, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{SessionConfig: agentapi.SessionConfig{Metadata: map[string]any{"placement_mode": string(PlacementAgentDirect)}}})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if resp.SessionID == "remote-1" {
		t.Fatalf("CreateSession() returned backend session ID, want orchestrator-owned ID: %q", resp.SessionID)
	}
	if !strings.HasPrefix(resp.SessionID, "orch-") {
		t.Fatalf("CreateSession() SessionID = %q, want orch-*", resp.SessionID)
	}
	if agentSvc.lastCreateReq == nil {
		t.Fatal("backend CreateSession was not called")
	}
	if agentSvc.lastCreateReq.SessionConfig.SessionID != resp.SessionID {
		t.Fatalf("backend session id = %q, want %q", agentSvc.lastCreateReq.SessionConfig.SessionID, resp.SessionID)
	}

	recv, err := orch.SendMessage(context.Background(), &agentapi.SendMessageRequest{SessionID: resp.SessionID, Message: "hi"})
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if agentSvc.lastSendReq == nil {
		t.Fatal("backend SendMessage was not called")
	}
	if agentSvc.lastSendReq.SessionID != "remote-1" {
		t.Fatalf("backend SendMessage sessionID = %q, want remote-1", agentSvc.lastSendReq.SessionID)
	}
	evt, err := recv.Recv()
	if err != nil {
		t.Fatalf("recv error = %v", err)
	}
	if evt.SessionID != resp.SessionID {
		t.Fatalf("proxied event sessionID = %q, want %q", evt.SessionID, resp.SessionID)
	}
	if evt.Delta != "hello" || evt.ToolName != "x" || evt.Type != agentapi.EventAgentMessageDelta {
		t.Fatalf("non-session fields changed in event: %#v", evt)
	}
}

func TestCreateSessionLaunchFailureCompensatesWithDestroySandbox(t *testing.T) {
	sc := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "direct:i-123", Address: "10.0.0.10:0"},
		launchErr:  errors.New("launch failed"),
	}
	orch := mustNewOrchestrator(t, sc, &mockAgentService{}, 0)

	_, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{SessionConfig: agentapi.SessionConfig{Metadata: map[string]any{"placement_mode": string(PlacementAgentDirect)}}})
	if err == nil {
		t.Fatal("CreateSession() expected error")
	}
	if sc.destroyCount != 1 {
		t.Fatalf("DestroySandbox calls = %d, want 1", sc.destroyCount)
	}
	if sc.lastDestroyedSandboxID != "direct:i-123" {
		t.Fatalf("DestroySandbox sandboxID = %q, want direct:i-123", sc.lastDestroyedSandboxID)
	}
}

func TestDestroySessionOrdersAgentThenProcessThenSandbox(t *testing.T) {
	calls := &[]string{}
	sc := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "direct:i-123", Address: "10.0.0.10:0"},
		launchResp: &control.LaunchProcessResponse{ProcessID: "proc-1", Address: "10.0.0.10:8081", Status: control.ProcessRunning},
		calls:      calls,
	}
	agentSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "remote-1", State: "idle"},
		calls:      calls,
		destroyErr: errors.New("kill downstream failed"),
	}
	orch := mustNewOrchestrator(t, sc, agentSvc, 0)

	createResp, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{SessionConfig: agentapi.SessionConfig{Metadata: map[string]any{"placement_mode": string(PlacementAgentDirect)}}})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	sc.killErr = errors.New("kill failed")

	_, err = orch.DestroySession(context.Background(), &agentapi.DestroyAgentSessionRequest{SessionID: createResp.SessionID})
	if err == nil {
		t.Fatal("DestroySession() expected aggregated error")
	}
	if !strings.Contains(err.Error(), "kill failed") {
		t.Fatalf("DestroySession() error = %v, want kill failure", err)
	}

	wantOrder := []string{"agent-destroy", "kill-process", "destroy-sandbox", "agent-close"}
	for _, want := range wantOrder {
		if !containsCall(*calls, want) {
			t.Fatalf("call log missing %q: %#v", want, *calls)
		}
	}
	if callIndex(*calls, "agent-destroy") > callIndex(*calls, "kill-process") {
		t.Fatalf("expected agent-destroy before kill-process: %#v", *calls)
	}
	if callIndex(*calls, "kill-process") > callIndex(*calls, "destroy-sandbox") {
		t.Fatalf("expected kill-process before destroy-sandbox: %#v", *calls)
	}

	listResp, err := orch.ListSessions(context.Background(), &agentapi.ListAgentSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(listResp.Sessions) != 0 {
		t.Fatalf("ListSessions count = %d, want 0", len(listResp.Sessions))
	}
}

func TestMaxSessionsEnforced(t *testing.T) {
	sc := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "direct:i-123", Address: "10.0.0.10:0"},
		launchResp: &control.LaunchProcessResponse{ProcessID: "proc-1", Address: "10.0.0.10:8081", Status: control.ProcessRunning},
	}
	agentSvc := &mockAgentService{createResp: &agentapi.CreateAgentSessionResponse{SessionID: "remote-1"}}
	orch := mustNewOrchestrator(t, sc, agentSvc, 1)

	_, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{SessionConfig: agentapi.SessionConfig{Metadata: map[string]any{"placement_mode": string(PlacementAgentDirect)}}})
	if err != nil {
		t.Fatalf("first CreateSession() error = %v", err)
	}

	_, err = orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{SessionConfig: agentapi.SessionConfig{Metadata: map[string]any{"placement_mode": string(PlacementAgentDirect)}}})
	if err == nil {
		t.Fatal("second CreateSession() expected max sessions error")
	}
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected rpc error, got %v", err)
	}
	if rpcErr.Code != rpc.CodeResourceExhausted {
		t.Fatalf("rpc code = %s, want %s", rpcErr.Code, rpc.CodeResourceExhausted)
	}
}

func TestCloseRejectsCreateSession(t *testing.T) {
	sc := &mockSandboxControl{}
	orch := mustNewOrchestrator(t, sc, &mockAgentService{}, 0)
	if err := orch.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	_, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{SessionConfig: agentapi.SessionConfig{Metadata: map[string]any{"placement_mode": string(PlacementAgentDirect)}}})
	if err == nil {
		t.Fatal("CreateSession() expected unavailable error after close")
	}
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected rpc error, got %v", err)
	}
	if rpcErr.Code != rpc.CodeUnavailable {
		t.Fatalf("rpc code = %s, want %s", rpcErr.Code, rpc.CodeUnavailable)
	}
}

func TestResumeSessionRewritesIDs(t *testing.T) {
	sc := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "direct:i-123", Address: "10.0.0.10:0"},
		launchResp: &control.LaunchProcessResponse{ProcessID: "proc-1", Address: "10.0.0.10:8081", Status: control.ProcessRunning},
	}
	agentSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "remote-1", State: "idle"},
		resumeResp: &agentapi.ResumeSessionResponse{SessionID: "remote-1", State: "active"},
	}
	orch := mustNewOrchestrator(t, sc, agentSvc, 0)

	createResp, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{SessionConfig: agentapi.SessionConfig{Metadata: map[string]any{"placement_mode": string(PlacementAgentDirect)}}})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	resumeResp, err := orch.ResumeSession(context.Background(), &agentapi.ResumeSessionRequest{
		SessionConfig: agentapi.SessionConfig{SessionID: createResp.SessionID},
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	if agentSvc.lastResumeReq == nil {
		t.Fatal("backend ResumeSession was not called")
	}
	if agentSvc.lastResumeReq.SessionConfig.SessionID != "remote-1" {
		t.Fatalf("backend resume SessionID = %q, want remote-1", agentSvc.lastResumeReq.SessionConfig.SessionID)
	}
	if resumeResp.SessionID != createResp.SessionID {
		t.Fatalf("resume response SessionID = %q, want %q", resumeResp.SessionID, createResp.SessionID)
	}
}

func TestGetSessionNotFoundReturnsRPCCode(t *testing.T) {
	orch := mustNewOrchestrator(t, &mockSandboxControl{}, &mockAgentService{}, 0)
	_, err := orch.GetSession(context.Background(), &agentapi.GetAgentSessionRequest{SessionID: "missing"})
	if err == nil {
		t.Fatal("GetSession() expected error")
	}
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected rpc error, got %v", err)
	}
	if rpcErr.Code != rpc.CodeNotFound {
		t.Fatalf("rpc code = %s, want %s", rpcErr.Code, rpc.CodeNotFound)
	}
}

func mustNewOrchestrator(t *testing.T, sc control.SandboxControl, agentSvc *mockAgentService, maxSessions int) *Orchestrator {
	t.Helper()
	orch, err := New(OrchestratorConfig{
		DirectControl: sc,
		MaxSessions:   maxSessions,
		AgentServiceFactory: func(_ string) (agentapi.AgentService, error) {
			return agentSvc, nil
		},
		CreateTimeout:   5 * time.Second,
		ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return orch
}

type mockSandboxControl struct {
	createResp *control.CreateSandboxResponse
	createErr  error
	launchResp *control.LaunchProcessResponse
	launchErr  error
	killErr    error
	destroyErr error

	lastCreateReq          control.CreateSandboxRequest
	lastLaunchReq          control.LaunchProcessRequest
	lastKillReq            control.KillProcessRequest
	lastDestroyedSandboxID string

	destroyCount int
	calls        *[]string
	mu           sync.Mutex
}

func (m *mockSandboxControl) CreateSandbox(_ context.Context, req control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
	m.mu.Lock()
	m.lastCreateReq = req
	if m.calls != nil {
		*m.calls = append(*m.calls, "create-sandbox")
	}
	m.mu.Unlock()
	if m.createErr != nil {
		return nil, m.createErr
	}
	if m.createResp == nil {
		return &control.CreateSandboxResponse{SandboxID: "direct:i-default", Address: "127.0.0.1:0"}, nil
	}
	out := *m.createResp
	return &out, nil
}

func (m *mockSandboxControl) DestroySandbox(_ context.Context, sandboxID string) error {
	m.mu.Lock()
	m.lastDestroyedSandboxID = sandboxID
	m.destroyCount++
	if m.calls != nil {
		*m.calls = append(*m.calls, "destroy-sandbox")
	}
	m.mu.Unlock()
	return m.destroyErr
}

func (m *mockSandboxControl) LaunchProcess(_ context.Context, req control.LaunchProcessRequest) (*control.LaunchProcessResponse, error) {
	m.mu.Lock()
	m.lastLaunchReq = req
	if m.calls != nil {
		*m.calls = append(*m.calls, "launch-process")
	}
	m.mu.Unlock()
	if m.launchErr != nil {
		return nil, m.launchErr
	}
	if m.launchResp == nil {
		return &control.LaunchProcessResponse{ProcessID: "proc-default", Address: "127.0.0.1:8081", Status: control.ProcessRunning}, nil
	}
	out := *m.launchResp
	return &out, nil
}

func (m *mockSandboxControl) KillProcess(_ context.Context, req control.KillProcessRequest) error {
	m.mu.Lock()
	m.lastKillReq = req
	if m.calls != nil {
		*m.calls = append(*m.calls, "kill-process")
	}
	m.mu.Unlock()
	return m.killErr
}

func (m *mockSandboxControl) GetProcessStatus(_ context.Context, _ control.GetProcessStatusRequest) (*control.GetProcessStatusResponse, error) {
	return &control.GetProcessStatusResponse{Status: control.ProcessRunning}, nil
}

func (m *mockSandboxControl) PauseSandbox(_ context.Context, _ string) error  { return nil }
func (m *mockSandboxControl) ResumeSandbox(_ context.Context, _ string) error { return nil }
func (m *mockSandboxControl) Capabilities() control.SandboxCapabilities {
	return control.SandboxCapabilities{Pause: true, LaunchProcess: true}
}

type mockAgentService struct {
	createResp    *agentapi.CreateAgentSessionResponse
	createErr     error
	getResp       *agentapi.GetAgentSessionResponse
	getErr        error
	listResp      *agentapi.ListAgentSessionsResponse
	listErr       error
	sendReceiver  agentapi.EventReceiver
	sendErr       error
	continueRecv  agentapi.EventReceiver
	continueErr   error
	steerResp     *agentapi.SteerResponse
	steerErr      error
	followResp    *agentapi.FollowUpResponse
	followErr     error
	abortResp     *agentapi.AbortResponse
	abortErr      error
	subscribeRecv agentapi.EventReceiver
	subscribeErr  error
	resumeResp    *agentapi.ResumeSessionResponse
	resumeErr     error
	destroyResp   *agentapi.DestroyAgentSessionResponse
	destroyErr    error
	closeErr      error

	lastCreateReq   *agentapi.CreateAgentSessionRequest
	lastSendReq     *agentapi.SendMessageRequest
	lastContinueReq *agentapi.ContinueRequest
	lastSteerReq    *agentapi.SteerRequest
	lastFollowReq   *agentapi.FollowUpRequest
	lastAbortReq    *agentapi.AbortRequest
	lastSubReq      *agentapi.SubscribeEventsRequest
	lastResumeReq   *agentapi.ResumeSessionRequest
	lastDestroyReq  *agentapi.DestroyAgentSessionRequest

	calls *[]string
}

func (m *mockAgentService) CreateSession(_ context.Context, req *agentapi.CreateAgentSessionRequest) (*agentapi.CreateAgentSessionResponse, error) {
	cp := *req
	m.lastCreateReq = &cp
	if m.calls != nil {
		*m.calls = append(*m.calls, "create-session")
	}
	if m.createErr != nil {
		return nil, m.createErr
	}
	if m.createResp == nil {
		return &agentapi.CreateAgentSessionResponse{SessionID: req.SessionConfig.SessionID, State: "idle"}, nil
	}
	out := *m.createResp
	return &out, nil
}

func (m *mockAgentService) GetSession(_ context.Context, req *agentapi.GetAgentSessionRequest) (*agentapi.GetAgentSessionResponse, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.getResp == nil {
		return &agentapi.GetAgentSessionResponse{SessionID: req.SessionID, State: "idle"}, nil
	}
	out := *m.getResp
	return &out, nil
}

func (m *mockAgentService) ListSessions(_ context.Context, _ *agentapi.ListAgentSessionsRequest) (*agentapi.ListAgentSessionsResponse, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	if m.listResp == nil {
		return &agentapi.ListAgentSessionsResponse{}, nil
	}
	out := *m.listResp
	out.Sessions = append([]agentapi.AgentSessionSummary(nil), m.listResp.Sessions...)
	return &out, nil
}

func (m *mockAgentService) SendMessage(_ context.Context, req *agentapi.SendMessageRequest) (agentapi.EventReceiver, error) {
	cp := *req
	m.lastSendReq = &cp
	if m.sendErr != nil {
		return nil, m.sendErr
	}
	if m.sendReceiver == nil {
		return &sliceEventReceiver{}, nil
	}
	return m.sendReceiver, nil
}

func (m *mockAgentService) Continue(_ context.Context, req *agentapi.ContinueRequest) (agentapi.EventReceiver, error) {
	cp := *req
	m.lastContinueReq = &cp
	if m.continueErr != nil {
		return nil, m.continueErr
	}
	if m.continueRecv == nil {
		return &sliceEventReceiver{}, nil
	}
	return m.continueRecv, nil
}

func (m *mockAgentService) Steer(_ context.Context, req *agentapi.SteerRequest) (*agentapi.SteerResponse, error) {
	cp := *req
	m.lastSteerReq = &cp
	if m.steerErr != nil {
		return nil, m.steerErr
	}
	if m.steerResp == nil {
		return &agentapi.SteerResponse{}, nil
	}
	out := *m.steerResp
	return &out, nil
}

func (m *mockAgentService) FollowUp(_ context.Context, req *agentapi.FollowUpRequest) (*agentapi.FollowUpResponse, error) {
	cp := *req
	m.lastFollowReq = &cp
	if m.followErr != nil {
		return nil, m.followErr
	}
	if m.followResp == nil {
		return &agentapi.FollowUpResponse{}, nil
	}
	out := *m.followResp
	return &out, nil
}

func (m *mockAgentService) Abort(_ context.Context, req *agentapi.AbortRequest) (*agentapi.AbortResponse, error) {
	cp := *req
	m.lastAbortReq = &cp
	if m.abortErr != nil {
		return nil, m.abortErr
	}
	if m.abortResp == nil {
		return &agentapi.AbortResponse{}, nil
	}
	out := *m.abortResp
	return &out, nil
}

func (m *mockAgentService) SubscribeEvents(_ context.Context, req *agentapi.SubscribeEventsRequest) (agentapi.EventReceiver, error) {
	cp := *req
	m.lastSubReq = &cp
	if m.subscribeErr != nil {
		return nil, m.subscribeErr
	}
	if m.subscribeRecv == nil {
		return &sliceEventReceiver{}, nil
	}
	return m.subscribeRecv, nil
}

func (m *mockAgentService) ResumeSession(_ context.Context, req *agentapi.ResumeSessionRequest) (*agentapi.ResumeSessionResponse, error) {
	cp := cloneResumeSessionRequest(req)
	m.lastResumeReq = &cp
	if m.resumeErr != nil {
		return nil, m.resumeErr
	}
	if m.resumeResp == nil {
		return &agentapi.ResumeSessionResponse{SessionID: req.SessionConfig.SessionID, State: "active"}, nil
	}
	out := *m.resumeResp
	return &out, nil
}

func (m *mockAgentService) DestroySession(_ context.Context, req *agentapi.DestroyAgentSessionRequest) (*agentapi.DestroyAgentSessionResponse, error) {
	cp := *req
	m.lastDestroyReq = &cp
	if m.calls != nil {
		*m.calls = append(*m.calls, "agent-destroy")
	}
	if m.destroyErr != nil {
		return nil, m.destroyErr
	}
	if m.destroyResp == nil {
		return &agentapi.DestroyAgentSessionResponse{}, nil
	}
	out := *m.destroyResp
	return &out, nil
}

func (m *mockAgentService) Close() error {
	if m.calls != nil {
		*m.calls = append(*m.calls, "agent-close")
	}
	return m.closeErr
}

type sliceEventReceiver struct {
	events []*agentapi.AgentEvent
	index  int
	closed bool
}

func (r *sliceEventReceiver) Recv() (*agentapi.AgentEvent, error) {
	if r.index >= len(r.events) {
		return nil, io.EOF
	}
	evt := r.events[r.index]
	r.index++
	return evt, nil
}

func (r *sliceEventReceiver) Close() error {
	r.closed = true
	return nil
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

func callIndex(calls []string, want string) int {
	for i, call := range calls {
		if call == want {
			return i
		}
	}
	return -1
}
