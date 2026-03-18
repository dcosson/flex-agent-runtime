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

func TestCreateSessionFactoryFailureCompensatesKillAndDestroy(t *testing.T) {
	sc := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "direct:i-123", Address: "10.0.0.10:0"},
		launchResp: &control.LaunchProcessResponse{ProcessID: "proc-1", Address: "10.0.0.10:8081", Status: control.ProcessRunning},
	}
	orch, err := New(OrchestratorConfig{
		DirectControl: sc,
		AgentServiceFactory: func(_ string) (agentapi.AgentService, error) {
			return nil, errors.New("factory failed")
		},
		CreateTimeout:   5 * time.Second,
		ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{Metadata: map[string]any{"placement_mode": string(PlacementAgentDirect)}},
	})
	if err == nil {
		t.Fatal("CreateSession() expected error")
	}
	if sc.lastKillReq.SandboxID != "direct:i-123" || sc.lastKillReq.ProcessID != "proc-1" {
		t.Fatalf("KillProcess request = %#v, want sandbox direct:i-123 and process proc-1", sc.lastKillReq)
	}
	if sc.destroyCount != 1 || sc.lastDestroyedSandboxID != "direct:i-123" {
		t.Fatalf("DestroySandbox not called as expected: count=%d sandbox=%q", sc.destroyCount, sc.lastDestroyedSandboxID)
	}
}

func TestCreateSessionBackendFailureCompensatesFullTeardown(t *testing.T) {
	calls := &[]string{}
	sc := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "direct:i-123", Address: "10.0.0.10:0"},
		launchResp: &control.LaunchProcessResponse{ProcessID: "proc-1", Address: "10.0.0.10:8081", Status: control.ProcessRunning},
		calls:      calls,
	}
	agentSvc := &mockAgentService{
		createErr: errors.New("backend create failed"),
		calls:     calls,
	}
	orch := mustNewOrchestrator(t, sc, agentSvc, 0)

	_, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{Metadata: map[string]any{"placement_mode": string(PlacementAgentDirect)}},
	})
	if err == nil {
		t.Fatal("CreateSession() expected error")
	}

	wantCalls := []string{"create-session", "agent-destroy", "agent-close", "kill-process", "destroy-sandbox"}
	for _, want := range wantCalls {
		if !containsCall(*calls, want) {
			t.Fatalf("call log missing %q: %#v", want, *calls)
		}
	}
	if callIndex(*calls, "agent-destroy") > callIndex(*calls, "agent-close") {
		t.Fatalf("expected agent-destroy before agent-close: %#v", *calls)
	}
	if callIndex(*calls, "agent-close") > callIndex(*calls, "kill-process") {
		t.Fatalf("expected agent-close before kill-process: %#v", *calls)
	}
	if callIndex(*calls, "kill-process") > callIndex(*calls, "destroy-sandbox") {
		t.Fatalf("expected kill-process before destroy-sandbox: %#v", *calls)
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
	createFn   func(context.Context, control.CreateSandboxRequest) (*control.CreateSandboxResponse, error)
	launchResp *control.LaunchProcessResponse
	launchErr  error
	launchFn   func(context.Context, control.LaunchProcessRequest) (*control.LaunchProcessResponse, error)
	killErr    error
	destroyErr error
	pauseErr   error
	resumeErr  error
	caps       *control.SandboxCapabilities
	statusResp *control.GetProcessStatusResponse
	statusErr  error
	statusFn   func(context.Context, control.GetProcessStatusRequest) (*control.GetProcessStatusResponse, error)

	lastCreateReq          control.CreateSandboxRequest
	lastLaunchReq          control.LaunchProcessRequest
	lastKillReq            control.KillProcessRequest
	lastStatusReq          control.GetProcessStatusRequest
	lastDestroyedSandboxID string
	lastPausedSandboxID    string
	lastResumedSandboxID   string

	destroyCount int
	pauseCount   int
	resumeCount  int
	statusCount  int
	calls        *[]string
	mu           sync.Mutex
}

func (m *mockSandboxControl) CreateSandbox(ctx context.Context, req control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
	if m.createFn != nil {
		return m.createFn(ctx, req)
	}
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

func (m *mockSandboxControl) LaunchProcess(ctx context.Context, req control.LaunchProcessRequest) (*control.LaunchProcessResponse, error) {
	if m.launchFn != nil {
		return m.launchFn(ctx, req)
	}
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

func (m *mockSandboxControl) GetProcessStatus(ctx context.Context, req control.GetProcessStatusRequest) (*control.GetProcessStatusResponse, error) {
	if m.statusFn != nil {
		return m.statusFn(ctx, req)
	}
	m.mu.Lock()
	m.lastStatusReq = req
	m.statusCount++
	resp := m.statusResp
	err := m.statusErr
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if resp != nil {
		out := *resp
		return &out, nil
	}
	return &control.GetProcessStatusResponse{Status: control.ProcessRunning}, nil
}

func (m *mockSandboxControl) PauseSandbox(_ context.Context, sandboxID string) error {
	m.mu.Lock()
	m.lastPausedSandboxID = sandboxID
	m.pauseCount++
	if m.calls != nil {
		*m.calls = append(*m.calls, "pause-sandbox")
	}
	m.mu.Unlock()
	return m.pauseErr
}
func (m *mockSandboxControl) ResumeSandbox(_ context.Context, sandboxID string) error {
	m.mu.Lock()
	m.lastResumedSandboxID = sandboxID
	m.resumeCount++
	if m.calls != nil {
		*m.calls = append(*m.calls, "resume-sandbox")
	}
	m.mu.Unlock()
	return m.resumeErr
}
func (m *mockSandboxControl) Capabilities() control.SandboxCapabilities {
	if m.caps != nil {
		return *m.caps
	}
	return control.SandboxCapabilities{Pause: true, LaunchProcess: true}
}

type mockAgentService struct {
	mu sync.Mutex

	createResp    *agentapi.CreateAgentSessionResponse
	createErr     error
	createFn      func(context.Context, *agentapi.CreateAgentSessionRequest) (*agentapi.CreateAgentSessionResponse, error)
	getResp       *agentapi.GetAgentSessionResponse
	getErr        error
	listResp      *agentapi.ListAgentSessionsResponse
	listErr       error
	sendReceiver  agentapi.EventReceiver
	sendErr       error
	sendFn        func(context.Context, *agentapi.SendMessageRequest) (agentapi.EventReceiver, error)
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

func (m *mockAgentService) CreateSession(ctx context.Context, req *agentapi.CreateAgentSessionRequest) (*agentapi.CreateAgentSessionResponse, error) {
	m.mu.Lock()
	cp := *req
	m.lastCreateReq = &cp
	createFn := m.createFn
	createErr := m.createErr
	createResp := m.createResp
	if m.calls != nil {
		*m.calls = append(*m.calls, "create-session")
	}
	m.mu.Unlock()
	if createFn != nil {
		return createFn(ctx, req)
	}
	if createErr != nil {
		return nil, createErr
	}
	if createResp == nil {
		return &agentapi.CreateAgentSessionResponse{SessionID: req.SessionConfig.SessionID, State: "idle"}, nil
	}
	out := *createResp
	return &out, nil
}

func (m *mockAgentService) GetSession(_ context.Context, req *agentapi.GetAgentSessionRequest) (*agentapi.GetAgentSessionResponse, error) {
	m.mu.Lock()
	getErr := m.getErr
	getResp := m.getResp
	m.mu.Unlock()
	if getErr != nil {
		return nil, getErr
	}
	if getResp == nil {
		return &agentapi.GetAgentSessionResponse{SessionID: req.SessionID, State: "idle"}, nil
	}
	out := *getResp
	return &out, nil
}

func (m *mockAgentService) ListSessions(_ context.Context, _ *agentapi.ListAgentSessionsRequest) (*agentapi.ListAgentSessionsResponse, error) {
	m.mu.Lock()
	listErr := m.listErr
	listResp := m.listResp
	m.mu.Unlock()
	if listErr != nil {
		return nil, listErr
	}
	if listResp == nil {
		return &agentapi.ListAgentSessionsResponse{}, nil
	}
	out := *listResp
	out.Sessions = append([]agentapi.AgentSessionSummary(nil), listResp.Sessions...)
	return &out, nil
}

func (m *mockAgentService) SendMessage(ctx context.Context, req *agentapi.SendMessageRequest) (agentapi.EventReceiver, error) {
	m.mu.Lock()
	cp := *req
	m.lastSendReq = &cp
	sendErr := m.sendErr
	sendReceiver := m.sendReceiver
	sendFn := m.sendFn
	m.mu.Unlock()
	if sendFn != nil {
		return sendFn(ctx, req)
	}
	if sendErr != nil {
		return nil, sendErr
	}
	if sendReceiver == nil {
		return &sliceEventReceiver{}, nil
	}
	return sendReceiver, nil
}

func (m *mockAgentService) Continue(_ context.Context, req *agentapi.ContinueRequest) (agentapi.EventReceiver, error) {
	m.mu.Lock()
	cp := *req
	m.lastContinueReq = &cp
	continueErr := m.continueErr
	continueRecv := m.continueRecv
	m.mu.Unlock()
	if continueErr != nil {
		return nil, continueErr
	}
	if continueRecv == nil {
		return &sliceEventReceiver{}, nil
	}
	return continueRecv, nil
}

func (m *mockAgentService) Steer(_ context.Context, req *agentapi.SteerRequest) (*agentapi.SteerResponse, error) {
	m.mu.Lock()
	cp := *req
	m.lastSteerReq = &cp
	steerErr := m.steerErr
	steerResp := m.steerResp
	m.mu.Unlock()
	if steerErr != nil {
		return nil, steerErr
	}
	if steerResp == nil {
		return &agentapi.SteerResponse{}, nil
	}
	out := *steerResp
	return &out, nil
}

func (m *mockAgentService) FollowUp(_ context.Context, req *agentapi.FollowUpRequest) (*agentapi.FollowUpResponse, error) {
	m.mu.Lock()
	cp := *req
	m.lastFollowReq = &cp
	followErr := m.followErr
	followResp := m.followResp
	m.mu.Unlock()
	if followErr != nil {
		return nil, followErr
	}
	if followResp == nil {
		return &agentapi.FollowUpResponse{}, nil
	}
	out := *followResp
	return &out, nil
}

func (m *mockAgentService) Abort(_ context.Context, req *agentapi.AbortRequest) (*agentapi.AbortResponse, error) {
	m.mu.Lock()
	cp := *req
	m.lastAbortReq = &cp
	abortErr := m.abortErr
	abortResp := m.abortResp
	m.mu.Unlock()
	if abortErr != nil {
		return nil, abortErr
	}
	if abortResp == nil {
		return &agentapi.AbortResponse{}, nil
	}
	out := *abortResp
	return &out, nil
}

func (m *mockAgentService) SubscribeEvents(_ context.Context, req *agentapi.SubscribeEventsRequest) (agentapi.EventReceiver, error) {
	m.mu.Lock()
	cp := *req
	m.lastSubReq = &cp
	subscribeErr := m.subscribeErr
	subscribeRecv := m.subscribeRecv
	m.mu.Unlock()
	if subscribeErr != nil {
		return nil, subscribeErr
	}
	if subscribeRecv == nil {
		return &sliceEventReceiver{}, nil
	}
	return subscribeRecv, nil
}

func (m *mockAgentService) ResumeSession(_ context.Context, req *agentapi.ResumeSessionRequest) (*agentapi.ResumeSessionResponse, error) {
	m.mu.Lock()
	cp := cloneResumeSessionRequest(req)
	m.lastResumeReq = &cp
	resumeErr := m.resumeErr
	resumeResp := m.resumeResp
	m.mu.Unlock()
	if resumeErr != nil {
		return nil, resumeErr
	}
	if resumeResp == nil {
		return &agentapi.ResumeSessionResponse{SessionID: req.SessionConfig.SessionID, State: "active"}, nil
	}
	out := *resumeResp
	return &out, nil
}

func (m *mockAgentService) DestroySession(_ context.Context, req *agentapi.DestroyAgentSessionRequest) (*agentapi.DestroyAgentSessionResponse, error) {
	m.mu.Lock()
	cp := *req
	m.lastDestroyReq = &cp
	destroyErr := m.destroyErr
	destroyResp := m.destroyResp
	if m.calls != nil {
		*m.calls = append(*m.calls, "agent-destroy")
	}
	m.mu.Unlock()
	if destroyErr != nil {
		return nil, destroyErr
	}
	if destroyResp == nil {
		return &agentapi.DestroyAgentSessionResponse{}, nil
	}
	out := *destroyResp
	return &out, nil
}

func (m *mockAgentService) Close() error {
	m.mu.Lock()
	closeErr := m.closeErr
	if m.calls != nil {
		*m.calls = append(*m.calls, "agent-close")
	}
	m.mu.Unlock()
	return closeErr
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

// --- agent-sandbox tests ---

func TestCreateSessionAgentSandboxRoutesAndRewritesIDs(t *testing.T) {
	nodeSC := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "sess-abc", Address: "/zfs/sessions/abc"},
		launchResp: &control.LaunchProcessResponse{ProcessID: "proc-1", Address: "10.0.0.5:8081", Status: control.ProcessRunning},
	}
	agentSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "remote-sb-1", State: "idle"},
	}
	orch, err := New(OrchestratorConfig{
		NodeControl:      nodeSC,
		AgentLoopService: agentSvc,
		CreateTimeout:    5 * time.Second,
		ShutdownTimeout:  2 * time.Second,
		AgentServiceFactory: func(_ string) (agentapi.AgentService, error) {
			return agentSvc, nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			Metadata: map[string]any{"placement_mode": "agent-sandbox"},
		},
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if !strings.HasPrefix(resp.SessionID, "orch-") {
		t.Fatalf("SessionID = %q, want orch-*", resp.SessionID)
	}
	if resp.SessionID == "remote-sb-1" {
		t.Fatal("SessionID should not be the backend ID")
	}
	if agentSvc.lastCreateReq == nil {
		t.Fatal("backend CreateSession not called")
	}
	if agentSvc.lastCreateReq.SessionConfig.SessionID != resp.SessionID {
		t.Fatalf("backend session ID = %q, want %q", agentSvc.lastCreateReq.SessionConfig.SessionID, resp.SessionID)
	}
}

func TestCreateSessionAgentSandboxLaunchFailureCompensates(t *testing.T) {
	nodeSC := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "sess-abc", Address: "/zfs/sessions/abc"},
		launchErr:  errors.New("launch in sandbox failed"),
	}
	orch, err := New(OrchestratorConfig{
		NodeControl:      nodeSC,
		AgentLoopService: &mockAgentService{},
		CreateTimeout:    5 * time.Second,
		ShutdownTimeout:  2 * time.Second,
		AgentServiceFactory: func(_ string) (agentapi.AgentService, error) {
			return &mockAgentService{}, nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			Metadata: map[string]any{"placement_mode": "agent-sandbox"},
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if nodeSC.destroyCount != 1 {
		t.Fatalf("DestroySandbox calls = %d, want 1", nodeSC.destroyCount)
	}
}

// --- tools-sandbox tests ---

func TestCreateSessionToolsSandboxNodeModeWiresToolEnvironment(t *testing.T) {
	nodeSC := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{
			SandboxID: "local-sess-42",
			Address:   "/zfs/sessions/42",
		},
	}
	agentLoopSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "loop-sess-1", State: "idle"},
	}
	orch, err := New(OrchestratorConfig{
		NodeControl:      nodeSC,
		AgentLoopService: agentLoopSvc,
		SandboxHostAddrs: []string{"10.0.0.5:8082"},
		CreateTimeout:    5 * time.Second,
		ShutdownTimeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			Metadata: map[string]any{"placement_mode": "tools-sandbox"},
		},
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if !strings.HasPrefix(resp.SessionID, "orch-") {
		t.Fatalf("SessionID = %q, want orch-*", resp.SessionID)
	}

	if agentLoopSvc.lastCreateReq == nil {
		t.Fatal("AgentLoopService.CreateSession not called")
	}
	toolEnv := agentLoopSvc.lastCreateReq.SessionConfig.ToolEnvironment
	if toolEnv.Type != agentapi.ToolEnvSandbox {
		t.Fatalf("ToolEnvironment.Type = %q, want %q", toolEnv.Type, agentapi.ToolEnvSandbox)
	}
	if toolEnv.SandboxHostAddr != "10.0.0.5:8082" {
		t.Fatalf("ToolEnvironment.SandboxHostAddr = %q, want 10.0.0.5:8082", toolEnv.SandboxHostAddr)
	}
	if toolEnv.SandboxSessionID != "local-sess-42" {
		t.Fatalf("ToolEnvironment.SandboxSessionID = %q, want local-sess-42", toolEnv.SandboxSessionID)
	}
}

func TestCreateSessionToolsSandboxFleetModeExtractsHostLocalID(t *testing.T) {
	nodeSC := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{
			SandboxID: "fleet:i-abc123:session-xyz",
			Address:   "10.0.1.5:8082",
		},
	}
	agentLoopSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "loop-sess-2", State: "idle"},
	}
	orch, err := New(OrchestratorConfig{
		NodeControl:      nodeSC,
		AgentLoopService: agentLoopSvc,
		SandboxHostAddrs: []string{"10.0.0.5:8082"},
		CreateTimeout:    5 * time.Second,
		ShutdownTimeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			Metadata: map[string]any{"placement_mode": "tools-sandbox"},
		},
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	if agentLoopSvc.lastCreateReq == nil {
		t.Fatal("AgentLoopService.CreateSession not called")
	}
	toolEnv := agentLoopSvc.lastCreateReq.SessionConfig.ToolEnvironment
	if toolEnv.SandboxHostAddr != "10.0.1.5:8082" {
		t.Fatalf("ToolEnvironment.SandboxHostAddr = %q, want 10.0.1.5:8082", toolEnv.SandboxHostAddr)
	}
	if toolEnv.SandboxSessionID != "session-xyz" {
		t.Fatalf("ToolEnvironment.SandboxSessionID = %q, want session-xyz", toolEnv.SandboxSessionID)
	}
	if resp.SessionID == "loop-sess-2" {
		t.Fatal("SessionID should not be the backend ID")
	}
}

func TestCreateSessionToolsSandboxCompensatesOnLoopFailure(t *testing.T) {
	nodeSC := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "local-sess-42", Address: "/zfs/sessions/42"},
	}
	agentLoopSvc := &mockAgentService{
		createErr: errors.New("max sessions exceeded"),
	}
	orch, err := New(OrchestratorConfig{
		NodeControl:      nodeSC,
		AgentLoopService: agentLoopSvc,
		SandboxHostAddrs: []string{"10.0.0.5:8082"},
		CreateTimeout:    5 * time.Second,
		ShutdownTimeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			Metadata: map[string]any{"placement_mode": "tools-sandbox"},
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if nodeSC.destroyCount != 1 {
		t.Fatalf("DestroySandbox calls = %d, want 1", nodeSC.destroyCount)
	}
	if nodeSC.lastDestroyedSandboxID != "local-sess-42" {
		t.Fatalf("destroyed sandbox ID = %q, want local-sess-42", nodeSC.lastDestroyedSandboxID)
	}
}

func TestCreateSessionToolsSandboxDualIDTracking(t *testing.T) {
	nodeSC := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{
			SandboxID: "fleet:i-abc:sess-local-1",
			Address:   "10.0.1.5:8082",
		},
	}
	agentLoopSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "loop-id-1", State: "idle"},
	}
	orch, err := New(OrchestratorConfig{
		NodeControl:      nodeSC,
		AgentLoopService: agentLoopSvc,
		SandboxHostAddrs: []string{"10.0.0.5:8082"},
		CreateTimeout:    5 * time.Second,
		ShutdownTimeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			Metadata: map[string]any{"placement_mode": "tools-sandbox"},
		},
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	entry, err := orch.getSessionEntry(resp.SessionID)
	if err != nil {
		t.Fatalf("getSessionEntry() error = %v", err)
	}
	if entry.sandboxID != "fleet:i-abc:sess-local-1" {
		t.Fatalf("sandboxID = %q, want fleet:i-abc:sess-local-1", entry.sandboxID)
	}
	if entry.toolSessionID != "sess-local-1" {
		t.Fatalf("toolSessionID = %q, want sess-local-1", entry.toolSessionID)
	}
}

// --- extractHostLocalSessionID tests ---

func TestExtractHostLocalSessionIDNode(t *testing.T) {
	got := extractHostLocalSessionID("abc-123")
	if got != "abc-123" {
		t.Fatalf("got %q, want abc-123", got)
	}
}

func TestExtractHostLocalSessionIDFleet(t *testing.T) {
	got := extractHostLocalSessionID("fleet:i-abc:session-xyz")
	if got != "session-xyz" {
		t.Fatalf("got %q, want session-xyz", got)
	}
}

func TestExtractHostLocalSessionIDFleetColonInSession(t *testing.T) {
	got := extractHostLocalSessionID("fleet:i-abc:sess:with:colons")
	if got != "sess:with:colons" {
		t.Fatalf("got %q, want sess:with:colons", got)
	}
}

// --- Pause/Resume tests ---

func TestPauseSessionCallsSandboxControl(t *testing.T) {
	nodeSC := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "sess-abc", Address: "/zfs/sessions/abc"},
	}
	agentLoopSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "loop-1", State: "idle"},
	}
	orch, err := New(OrchestratorConfig{
		NodeControl:      nodeSC,
		AgentLoopService: agentLoopSvc,
		SandboxHostAddrs: []string{"10.0.0.5:8082"},
		CreateTimeout:    5 * time.Second,
		ShutdownTimeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			Metadata: map[string]any{"placement_mode": "tools-sandbox"},
		},
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	if err := orch.PauseSession(context.Background(), resp.SessionID); err != nil {
		t.Fatalf("PauseSession() error = %v", err)
	}
	if nodeSC.pauseCount != 1 {
		t.Fatalf("PauseSandbox calls = %d, want 1", nodeSC.pauseCount)
	}
	if nodeSC.lastPausedSandboxID != "sess-abc" {
		t.Fatalf("paused sandbox ID = %q, want sess-abc", nodeSC.lastPausedSandboxID)
	}
	entry, _ := orch.getSessionEntry(resp.SessionID)
	if entry.state != sessionPaused {
		t.Fatalf("state = %q, want %q", entry.state, sessionPaused)
	}
}

func TestResumeSessionSandboxRestoresActiveState(t *testing.T) {
	nodeSC := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "sess-abc", Address: "/zfs/sessions/abc"},
	}
	agentLoopSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "loop-1", State: "idle"},
	}
	orch, err := New(OrchestratorConfig{
		NodeControl:      nodeSC,
		AgentLoopService: agentLoopSvc,
		SandboxHostAddrs: []string{"10.0.0.5:8082"},
		CreateTimeout:    5 * time.Second,
		ShutdownTimeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, _ := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			Metadata: map[string]any{"placement_mode": "tools-sandbox"},
		},
	})
	_ = orch.PauseSession(context.Background(), resp.SessionID)
	if err := orch.ResumeSessionSandbox(context.Background(), resp.SessionID); err != nil {
		t.Fatalf("ResumeSessionSandbox() error = %v", err)
	}
	if nodeSC.resumeCount != 1 {
		t.Fatalf("ResumeSandbox calls = %d, want 1", nodeSC.resumeCount)
	}
	entry, _ := orch.getSessionEntry(resp.SessionID)
	if entry.state != sessionActive {
		t.Fatalf("state = %q, want %q", entry.state, sessionActive)
	}
}

func TestPauseSessionAgentDirectAbortsFirst(t *testing.T) {
	directSC := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "direct:i-123", Address: "10.0.0.10:0"},
		launchResp: &control.LaunchProcessResponse{ProcessID: "proc-1", Address: "10.0.0.10:8081", Status: control.ProcessRunning},
	}
	agentSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "remote-1", State: "idle"},
	}
	orch := mustNewOrchestrator(t, directSC, agentSvc, 0)

	resp, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			Metadata: map[string]any{"placement_mode": "agent-direct"},
		},
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	if err := orch.PauseSession(context.Background(), resp.SessionID); err != nil {
		t.Fatalf("PauseSession() error = %v", err)
	}
	if agentSvc.lastAbortReq == nil {
		t.Fatal("Abort was not called before pause")
	}
	if agentSvc.lastAbortReq.SessionID != "remote-1" {
		t.Fatalf("Abort session ID = %q, want remote-1", agentSvc.lastAbortReq.SessionID)
	}
	if directSC.pauseCount != 1 {
		t.Fatalf("PauseSandbox calls = %d, want 1", directSC.pauseCount)
	}
}

func TestResumeSessionAgentDirectRelaunchesAgent(t *testing.T) {
	directSC := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "direct:i-123", Address: "10.0.0.10:0"},
		launchResp: &control.LaunchProcessResponse{ProcessID: "proc-1", Address: "10.0.0.10:8081", Status: control.ProcessRunning},
	}
	agentSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "remote-1", State: "idle"},
	}
	var agentFactoryCalls int
	orch, err := New(OrchestratorConfig{
		DirectControl:   directSC,
		CreateTimeout:   5 * time.Second,
		ShutdownTimeout: 2 * time.Second,
		AgentServiceFactory: func(_ string) (agentapi.AgentService, error) {
			agentFactoryCalls++
			return agentSvc, nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, _ := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			Metadata: map[string]any{"placement_mode": "agent-direct"},
		},
	})
	initialFactoryCalls := agentFactoryCalls

	_ = orch.PauseSession(context.Background(), resp.SessionID)

	directSC.launchResp = &control.LaunchProcessResponse{
		ProcessID: "proc-2",
		Address:   "10.0.0.10:8081",
		Status:    control.ProcessRunning,
	}
	if err := orch.ResumeSessionSandbox(context.Background(), resp.SessionID); err != nil {
		t.Fatalf("ResumeSessionSandbox() error = %v", err)
	}
	if directSC.resumeCount != 1 {
		t.Fatalf("ResumeSandbox calls = %d, want 1", directSC.resumeCount)
	}
	if agentFactoryCalls != initialFactoryCalls+1 {
		t.Fatalf("AgentServiceFactory calls = %d, want %d", agentFactoryCalls, initialFactoryCalls+1)
	}
	entry, _ := orch.getSessionEntry(resp.SessionID)
	if entry.processID != "proc-2" {
		t.Fatalf("processID = %q, want proc-2", entry.processID)
	}
	if entry.state != sessionActive {
		t.Fatalf("state = %q, want active", entry.state)
	}
}

func TestPauseNonActiveFails(t *testing.T) {
	nodeSC := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "sess-abc", Address: "/zfs/sessions/abc"},
	}
	agentLoopSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "loop-1", State: "idle"},
	}
	orch, err := New(OrchestratorConfig{
		NodeControl:      nodeSC,
		AgentLoopService: agentLoopSvc,
		SandboxHostAddrs: []string{"10.0.0.5:8082"},
		CreateTimeout:    5 * time.Second,
		ShutdownTimeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, _ := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			Metadata: map[string]any{"placement_mode": "tools-sandbox"},
		},
	})
	_ = orch.PauseSession(context.Background(), resp.SessionID)

	err = orch.PauseSession(context.Background(), resp.SessionID)
	if err == nil {
		t.Fatal("expected error on double-pause")
	}
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeFailedPrecondition {
		t.Fatalf("expected CodeFailedPrecondition, got %v", err)
	}
}

func TestValidatePlacementToolsSandboxRequiresBothBackends(t *testing.T) {
	orch, err := New(OrchestratorConfig{
		NodeControl:     &mockSandboxControl{},
		CreateTimeout:   5 * time.Second,
		ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	err = orch.validatePlacement(PlacementToolsSandbox)
	if err == nil {
		t.Fatal("expected error for tools-sandbox without AgentLoopService")
	}
}

func TestResumeSessionAgentDirectFactoryFailureKillsOrphanProcess(t *testing.T) {
	directSC := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "direct:i-123", Address: "10.0.0.10:0"},
		launchResp: &control.LaunchProcessResponse{ProcessID: "proc-1", Address: "10.0.0.10:8081", Status: control.ProcessRunning},
	}
	agentSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "remote-1", State: "idle"},
	}
	var factoryCallCount int
	orch, err := New(OrchestratorConfig{
		DirectControl:   directSC,
		CreateTimeout:   5 * time.Second,
		ShutdownTimeout: 2 * time.Second,
		AgentServiceFactory: func(_ string) (agentapi.AgentService, error) {
			factoryCallCount++
			if factoryCallCount > 1 {
				return nil, errors.New("factory failed on reconnect")
			}
			return agentSvc, nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, _ := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			Metadata: map[string]any{"placement_mode": "agent-direct"},
		},
	})
	_ = orch.PauseSession(context.Background(), resp.SessionID)

	directSC.launchResp = &control.LaunchProcessResponse{
		ProcessID: "proc-orphan",
		Address:   "10.0.0.10:8081",
		Status:    control.ProcessRunning,
	}

	err = orch.ResumeSessionSandbox(context.Background(), resp.SessionID)
	if err == nil {
		t.Fatal("expected error on factory failure")
	}
	if !strings.Contains(err.Error(), "reconnect agent after resume") {
		t.Fatalf("error = %v, want reconnect failure", err)
	}
	// Verify the orphaned process was killed.
	if directSC.lastKillReq.ProcessID != "proc-orphan" {
		t.Fatalf("KillProcess processID = %q, want proc-orphan", directSC.lastKillReq.ProcessID)
	}
	if directSC.lastKillReq.SandboxID != "direct:i-123" {
		t.Fatalf("KillProcess sandboxID = %q, want direct:i-123", directSC.lastKillReq.SandboxID)
	}
	// Verify original processID is preserved (not updated to orphan).
	entry, _ := orch.getSessionEntry(resp.SessionID)
	if entry.processID != "proc-1" {
		t.Fatalf("processID = %q, want proc-1 (original, not orphan)", entry.processID)
	}
}

func TestValidatePlacementAgentSandboxRequiresNodeControl(t *testing.T) {
	orch, err := New(OrchestratorConfig{
		DirectControl:   &mockSandboxControl{},
		CreateTimeout:   5 * time.Second,
		ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	err = orch.validatePlacement(PlacementAgentSandbox)
	if err == nil {
		t.Fatal("expected error for agent-sandbox without NodeControl")
	}
}
