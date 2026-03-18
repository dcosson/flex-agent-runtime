package orchestrator

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/codec"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
)

func TestHealthLoopMarksUnhealthyAndBlocksProxyCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	sc := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "direct:i-123", Address: "10.0.0.10:0"},
		launchResp: &control.LaunchProcessResponse{
			ProcessID: "proc-1",
			Address:   hostFromURL(t, server.URL),
			Status:    control.ProcessRunning,
		},
	}
	agentSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "remote-1", State: "idle"},
	}
	orch, err := New(OrchestratorConfig{
		DirectControl: sc,
		AgentServiceFactory: func(_ string) (agentapi.AgentService, error) {
			return agentSvc, nil
		},
		HealthInterval:  10 * time.Millisecond,
		CreateTimeout:   time.Second,
		ShutdownTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = orch.Close() })

	resp, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{Metadata: map[string]any{"placement_mode": string(PlacementAgentDirect)}},
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	waitForSessionState(t, orch, resp.SessionID, sessionUnhealthy)

	_, err = orch.SendMessage(context.Background(), &agentapi.SendMessageRequest{
		SessionID: resp.SessionID,
		Message:   "hello",
	})
	requireRPCCode(t, err, rpc.CodeUnavailable)
}

func TestHealthLoopRecoversToActive(t *testing.T) {
	var unhealthy atomic.Bool
	unhealthy.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if unhealthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	}))
	defer server.Close()

	sc := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "direct:i-123", Address: "10.0.0.10:0"},
		launchResp: &control.LaunchProcessResponse{
			ProcessID: "proc-1",
			Address:   hostFromURL(t, server.URL),
			Status:    control.ProcessRunning,
		},
	}
	agentSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "remote-1", State: "idle"},
	}
	orch := mustNewHealthOrchestrator(t, sc, agentSvc, nil)
	t.Cleanup(func() { _ = orch.Close() })

	resp, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{Metadata: map[string]any{"placement_mode": string(PlacementAgentDirect)}},
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	waitForSessionState(t, orch, resp.SessionID, sessionUnhealthy)
	unhealthy.Store(false)
	waitForSessionState(t, orch, resp.SessionID, sessionActive)

	_, err = orch.SendMessage(context.Background(), &agentapi.SendMessageRequest{
		SessionID: resp.SessionID,
		Message:   "recovered",
	})
	if err != nil {
		t.Fatalf("SendMessage() after recovery error = %v", err)
	}
}

func TestHealthLoopChecksProcessStatusAfterFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	exitCode := 137
	sc := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "direct:i-123", Address: "10.0.0.10:0"},
		launchResp: &control.LaunchProcessResponse{
			ProcessID: "proc-1",
			Address:   hostFromURL(t, server.URL),
			Status:    control.ProcessRunning,
		},
		statusResp: &control.GetProcessStatusResponse{
			Status:   control.ProcessExited,
			ExitCode: &exitCode,
		},
	}
	agentSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "remote-1", State: "idle"},
	}
	orch := mustNewHealthOrchestrator(t, sc, agentSvc, nil)
	t.Cleanup(func() { _ = orch.Close() })

	resp, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{Metadata: map[string]any{"placement_mode": string(PlacementAgentDirect)}},
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entry, entryErr := orch.getSessionEntry(resp.SessionID)
		if entryErr != nil {
			t.Fatalf("getSessionEntry() error = %v", entryErr)
		}
		entry.mu.Lock()
		statusCount := sc.statusCount
		healthErr := entry.healthErr
		entry.mu.Unlock()
		if statusCount > 0 && healthErr != nil && strings.Contains(healthErr.Error(), "exited") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected process-status probe with exited health error")
}

func TestToolsSandboxHealthUsesResolvedHostAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	nodeSC := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{
			SandboxID: "local-sess-1",
			Address:   "/zfs/sessions/local-sess-1",
		},
	}
	agentLoopSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "loop-1", State: "idle"},
	}
	orch, err := New(OrchestratorConfig{
		NodeControl:      nodeSC,
		AgentLoopService: agentLoopSvc,
		SandboxHostAddrs: []string{hostFromURL(t, server.URL)},
		HealthInterval:   10 * time.Millisecond,
		CreateTimeout:    time.Second,
		ShutdownTimeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = orch.Close() })

	resp, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			Metadata: map[string]any{"placement_mode": string(PlacementToolsSandbox)},
		},
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	waitForSessionState(t, orch, resp.SessionID, sessionUnhealthy)

	_, err = orch.SendMessage(context.Background(), &agentapi.SendMessageRequest{
		SessionID: resp.SessionID,
		Message:   "blocked",
	})
	requireRPCCode(t, err, rpc.CodeUnavailable)
}

func TestConcurrentCreateSessionAcrossModes(t *testing.T) {
	directSC := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "direct:i-123", Address: "10.0.0.1:0"},
		launchResp: &control.LaunchProcessResponse{ProcessID: "proc-direct", Address: "10.0.0.1:8081", Status: control.ProcessRunning},
	}
	nodeSC := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "node-sess-1", Address: "/zfs/sessions/1"},
		launchResp: &control.LaunchProcessResponse{ProcessID: "proc-node", Address: "10.0.0.2:8081", Status: control.ProcessRunning},
	}
	remoteSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "remote-1", State: "idle"},
	}
	loopSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "loop-1", State: "idle"},
	}
	orch, err := New(OrchestratorConfig{
		DirectControl:    directSC,
		NodeControl:      nodeSC,
		AgentLoopService: loopSvc,
		SandboxHostAddrs: []string{"10.0.0.2:8082"},
		AgentServiceFactory: func(_ string) (agentapi.AgentService, error) {
			return remoteSvc, nil
		},
		CreateTimeout:   time.Second,
		ShutdownTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = orch.Close() })

	const total = 30
	errCh := make(chan error, total)
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			mode := PlacementAgentDirect
			switch i % 3 {
			case 1:
				mode = PlacementAgentSandbox
			case 2:
				mode = PlacementToolsSandbox
			}
			_, createErr := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
				SessionConfig: agentapi.SessionConfig{Metadata: map[string]any{"placement_mode": string(mode)}},
			})
			errCh <- createErr
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("CreateSession() concurrent error = %v", err)
		}
	}

	listResp, err := orch.ListSessions(context.Background(), &agentapi.ListAgentSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(listResp.Sessions) != total {
		t.Fatalf("session count = %d, want %d", len(listResp.Sessions), total)
	}
}

func TestDestroySessionWhileStreamingDoesNotDeadlock(t *testing.T) {
	blocking := &blockingEventReceiver{
		first: &agentapi.AgentEvent{Type: agentapi.EventTurnStarted, SessionID: "remote-1"},
		done:  make(chan struct{}),
	}
	sc := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "direct:i-123", Address: "10.0.0.1:0"},
		launchResp: &control.LaunchProcessResponse{ProcessID: "proc-1", Address: "10.0.0.1:8081", Status: control.ProcessRunning},
	}
	agentSvc := &mockAgentService{
		createResp:   &agentapi.CreateAgentSessionResponse{SessionID: "remote-1", State: "idle"},
		sendReceiver: blocking,
	}
	orch := mustNewOrchestrator(t, sc, agentSvc, 0)
	t.Cleanup(func() { _ = orch.Close() })

	resp, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{Metadata: map[string]any{"placement_mode": string(PlacementAgentDirect)}},
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	recv, err := orch.SendMessage(context.Background(), &agentapi.SendMessageRequest{
		SessionID: resp.SessionID,
		Message:   "hi",
	})
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if _, err := recv.Recv(); err != nil {
		t.Fatalf("first Recv() error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, destroyErr := orch.DestroySession(context.Background(), &agentapi.DestroyAgentSessionRequest{SessionID: resp.SessionID})
		done <- destroyErr
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("DestroySession() error = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("DestroySession() blocked while stream receiver was active")
	}
	close(blocking.done)
}

func TestCreateSessionProvisioningCanceledByClose(t *testing.T) {
	createStarted := make(chan struct{})
	sc := &mockSandboxControl{
		createFn: func(ctx context.Context, _ control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
			close(createStarted)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	orch, err := New(OrchestratorConfig{
		DirectControl: sc,
		AgentServiceFactory: func(_ string) (agentapi.AgentService, error) {
			return &mockAgentService{}, nil
		},
		CreateTimeout:   10 * time.Second,
		ShutdownTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = orch.Close() })

	createDone := make(chan error, 1)
	go func() {
		_, createErr := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
			SessionConfig: agentapi.SessionConfig{Metadata: map[string]any{"placement_mode": string(PlacementAgentDirect)}},
		})
		createDone <- createErr
	}()

	select {
	case <-createStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("CreateSession() did not start provisioning")
	}
	if err := orch.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-createDone:
		if err == nil {
			t.Fatal("CreateSession() expected error after Close() cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("CreateSession() did not return after Close()")
	}
}

func TestCreateSessionSlowProvisioningBoundedByCreateTimeout(t *testing.T) {
	sc := &mockSandboxControl{
		createFn: func(ctx context.Context, _ control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	orch, err := New(OrchestratorConfig{
		DirectControl: sc,
		AgentServiceFactory: func(_ string) (agentapi.AgentService, error) {
			return &mockAgentService{}, nil
		},
		CreateTimeout:   50 * time.Millisecond,
		ShutdownTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = orch.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err = orch.CreateSession(ctx, &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{Metadata: map[string]any{"placement_mode": string(PlacementAgentDirect)}},
	})
	if err == nil {
		t.Fatal("CreateSession() expected timeout/cancel error")
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("CreateSession() took too long: %s", elapsed)
	}
}

func TestEventFidelityPreservedAfterCodecRoundTrip(t *testing.T) {
	now := time.Now().UTC().Round(time.Second)
	sc := &mockSandboxControl{
		createResp: &control.CreateSandboxResponse{SandboxID: "direct:i-123", Address: "10.0.0.1:0"},
		launchResp: &control.LaunchProcessResponse{ProcessID: "proc-1", Address: "10.0.0.1:8081", Status: control.ProcessRunning},
	}
	agentSvc := &mockAgentService{
		createResp: &agentapi.CreateAgentSessionResponse{SessionID: "remote-1", State: "idle"},
		sendReceiver: &sliceEventReceiver{events: []*agentapi.AgentEvent{{
			Type:            agentapi.EventToolCompleted,
			SessionID:       "remote-1",
			DriverSessionID: "driver-1",
			ToolName:        "bash",
			ToolCallID:      "tool-123",
			Delta:           "delta",
			ControlMessage:  "control",
			ErrorMessage:    "",
			At:              now,
			Metadata:        map[string]any{"n": 42, "s": "ok"},
		}}},
	}
	orch := mustNewOrchestrator(t, sc, agentSvc, 0)
	t.Cleanup(func() { _ = orch.Close() })

	resp, err := orch.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{Metadata: map[string]any{"placement_mode": string(PlacementAgentDirect)}},
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	recv, err := orch.SendMessage(context.Background(), &agentapi.SendMessageRequest{
		SessionID: resp.SessionID,
		Message:   "go",
	})
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}

	roundTripRecv := codec.UnwrapEventReceiver(codec.WrapEventReceiver(resp.SessionID, recv))
	got, err := roundTripRecv.Recv()
	if err != nil {
		t.Fatalf("Recv() error = %v", err)
	}

	if got.SessionID != resp.SessionID {
		t.Fatalf("SessionID = %q, want %q", got.SessionID, resp.SessionID)
	}
	if got.Type != agentapi.EventToolCompleted ||
		got.DriverSessionID != "driver-1" ||
		got.ToolName != "bash" ||
		got.ToolCallID != "tool-123" ||
		got.Delta != "delta" ||
		got.ControlMessage != "control" ||
		!got.At.Equal(now) {
		t.Fatalf("event fields changed after roundtrip: %#v", got)
	}
	if got.Metadata["n"] != float64(42) && got.Metadata["n"] != 42 {
		t.Fatalf("metadata[n] = %#v, want 42", got.Metadata["n"])
	}
	if got.Metadata["s"] != "ok" {
		t.Fatalf("metadata[s] = %#v, want ok", got.Metadata["s"])
	}
}

func mustNewHealthOrchestrator(t *testing.T, sc control.SandboxControl, agentSvc *mockAgentService, extraCfg func(*OrchestratorConfig)) *Orchestrator {
	t.Helper()
	cfg := OrchestratorConfig{
		DirectControl: sc,
		AgentServiceFactory: func(_ string) (agentapi.AgentService, error) {
			return agentSvc, nil
		},
		HealthInterval:  10 * time.Millisecond,
		CreateTimeout:   time.Second,
		ShutdownTimeout: time.Second,
	}
	if extraCfg != nil {
		extraCfg(&cfg)
	}
	orch, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return orch
}

func waitForSessionState(t *testing.T, orch *Orchestrator, sessionID string, want sessionState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entry, err := orch.getSessionEntry(sessionID)
		if err != nil {
			t.Fatalf("getSessionEntry(%q) error = %v", sessionID, err)
		}
		entry.mu.Lock()
		state := entry.state
		entry.mu.Unlock()
		if state == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session %q did not reach state %q", sessionID, want)
}

func requireRPCCode(t *testing.T, err error, want rpc.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected rpc error code %s, got nil", want)
	}
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected rpc error, got %v", err)
	}
	if rpcErr.Code != want {
		t.Fatalf("rpc code = %s, want %s", rpcErr.Code, want)
	}
}

func hostFromURL(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", raw, err)
	}
	return parsed.Host
}

type blockingEventReceiver struct {
	first *agentapi.AgentEvent
	done  chan struct{}
	sent  bool
}

func (r *blockingEventReceiver) Recv() (*agentapi.AgentEvent, error) {
	if !r.sent {
		r.sent = true
		return r.first, nil
	}
	<-r.done
	return nil, io.EOF
}

func (r *blockingEventReceiver) Close() error {
	select {
	case <-r.done:
	default:
		close(r.done)
	}
	return nil
}
