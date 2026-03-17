package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anthropics/flex-agent-runtime/internal/agent"
	"github.com/anthropics/flex-agent-runtime/internal/rpc/api"
	rpcserver "github.com/anthropics/flex-agent-runtime/internal/rpc/server"
	"github.com/anthropics/flex-agent-runtime/internal/sandbox"
	"github.com/anthropics/flex-agent-runtime/internal/sandbox/gvisor"
	"github.com/anthropics/flex-agent-runtime/internal/sandbox/zfs"
	"github.com/anthropics/flex-agent-runtime/internal/termmux"
)

type fakeGVisor struct{}

func (f *fakeGVisor) Run(context.Context, gvisor.ContainerOptions) (*gvisor.ContainerResult, error) {
	exit := 0
	return &gvisor.ContainerResult{ExitCode: exit, Stdout: []byte("ok")}, nil
}
func (f *fakeGVisor) CleanupStale(context.Context) (int, error) { return 0, nil }
func (f *fakeGVisor) ActiveContainers() int                     { return 0 }
func (f *fakeGVisor) Close() error                              { return nil }

func newSandboxRPCForTransport(t *testing.T) *rpcserver.SandboxServer {
	t.Helper()
	zm := zfs.NewMockManager()
	cfg := sandbox.DefaultServiceConfig()
	cfg.PoolName = "tank"
	cfg.BasesDataset = "tank/bases"
	cfg.SessionsDataset = "tank/sessions"
	cfg.ToolTimeout = 0
	host, err := sandbox.NewSandboxHostService(cfg, zm, &fakeGVisor{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := zm.CreateDataset(ctx, "tank/bases/repo", zfs.DatasetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := zm.CreateSnapshot(ctx, "tank/bases/repo", "initial"); err != nil {
		t.Fatal(err)
	}
	return rpcserver.NewSandboxServer(host)
}

func TestTransportUnaryVersionAndAuth(t *testing.T) {
	sandboxRPC := newSandboxRPCForTransport(t)
	t.Cleanup(func() { _ = sandboxRPC.Close() })
	events := rpcserver.NewAgentEventServer()
	srv := NewServer(
		ServerConfig{
			APIVersion:    "v2",
			MinAPIVersion: "v2",
			AuthHook: func(_ context.Context, _ string, headers http.Header) error {
				if headers.Get("authorization") != "Bearer good" {
					return errors.New("unauthenticated")
				}
				return nil
			},
		},
		WithSandboxService(sandboxRPC),
		WithAgentEventService(events),
	)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client := NewSandboxClient(ts.Client(), ts.URL, ClientConfig{
		APIVersion:     "v2",
		HeaderInjector: HeaderTokenAuth("authorization", "Bearer good"),
	})
	resp, err := client.HealthCheck.CallUnary(context.Background(), connect.NewRequest(&api.HealthCheckRequest{}))
	if err != nil {
		t.Fatalf("HealthCheck call failed: %v", err)
	}
	if resp.Msg.Status == "" {
		t.Fatalf("expected health status")
	}
	if got := resp.Header().Get("x-api-version"); got != "v2" {
		t.Fatalf("x-api-version = %q, want v2", got)
	}
}

func TestTransportAuthRejected(t *testing.T) {
	sandboxRPC := newSandboxRPCForTransport(t)
	t.Cleanup(func() { _ = sandboxRPC.Close() })
	srv := NewServer(
		ServerConfig{
			APIVersion:    "v1",
			MinAPIVersion: "v1",
			AuthHook: func(_ context.Context, _ string, headers http.Header) error {
				if headers.Get("authorization") != "Bearer expected" {
					return errors.New("unauthenticated")
				}
				return nil
			},
		},
		WithSandboxService(sandboxRPC),
		WithAgentEventService(rpcserver.NewAgentEventServer()),
	)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client := NewSandboxClient(ts.Client(), ts.URL, ClientConfig{APIVersion: "v1"})
	_, err := client.HealthCheck.CallUnary(context.Background(), connect.NewRequest(&api.HealthCheckRequest{}))
	if err == nil {
		t.Fatalf("expected auth error")
	}
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want unauthenticated", connect.CodeOf(err))
	}
}

func TestTransportVersionRejected(t *testing.T) {
	sandboxRPC := newSandboxRPCForTransport(t)
	t.Cleanup(func() { _ = sandboxRPC.Close() })
	srv := NewServer(
		ServerConfig{
			APIVersion:    "v2",
			MinAPIVersion: "v2",
		},
		WithSandboxService(sandboxRPC),
		WithAgentEventService(rpcserver.NewAgentEventServer()),
	)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client := NewSandboxClient(ts.Client(), ts.URL, ClientConfig{APIVersion: "v1"})
	_, err := client.HealthCheck.CallUnary(context.Background(), connect.NewRequest(&api.HealthCheckRequest{}))
	if err == nil {
		t.Fatalf("expected version error")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want failed_precondition", connect.CodeOf(err))
	}
}

func TestTransportExecuteToolStream(t *testing.T) {
	sandboxRPC := newSandboxRPCForTransport(t)
	t.Cleanup(func() { _ = sandboxRPC.Close() })
	srv := NewServer(ServerConfig{}, WithSandboxService(sandboxRPC), WithAgentEventService(rpcserver.NewAgentEventServer()))
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	client := NewSandboxClient(ts.Client(), ts.URL, ClientConfig{APIVersion: "v1"})

	_, err := client.CreateSession.CallUnary(context.Background(), connect.NewRequest(&api.CreateSessionRequest{
		SessionID:    "s1",
		BaseSnapshot: "tank/bases/repo@initial",
	}))
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	stream, err := client.ExecuteStream.CallServerStream(context.Background(), connect.NewRequest(&api.ExecuteToolRequest{
		SessionID:  "s1",
		ToolCallID: "tc1",
		ToolName:   "bash",
		Params:     map[string]any{"cmd": "echo ok"},
	}))
	if err != nil {
		t.Fatalf("CallServerStream failed: %v", err)
	}

	if !stream.Receive() {
		t.Fatalf("ExecuteToolStream recv failed: %v", stream.Err())
	}
	msg := stream.Msg()
	if msg.Response == nil {
		t.Fatalf("expected final response message")
	}
	if msg.Response.ToolCallID != "tc1" {
		t.Fatalf("tool_call_id = %q, want tc1", msg.Response.ToolCallID)
	}

	if stream.Receive() {
		t.Fatalf("expected stream to finish")
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
}

func TestTransportProcessRPCs(t *testing.T) {
	cfg := sandbox.DefaultServiceConfig()
	cfg.StorageBackend = sandbox.StorageBackendLocalDisk
	cfg.ContainerRuntime = sandbox.ContainerRuntimeNone
	cfg.AdvertiseAddr = "127.0.0.1"
	cfg.SessionsRootDir = t.TempDir()
	host, err := sandbox.NewSandboxHostService(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewSandboxHostService: %v", err)
	}
	ctx := context.Background()
	sandboxRPC := rpcserver.NewSandboxServer(host)
	t.Cleanup(func() { _ = sandboxRPC.Close() })

	srv := NewServer(ServerConfig{}, WithSandboxService(sandboxRPC), WithAgentEventService(rpcserver.NewAgentEventServer()))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	client := NewSandboxClient(ts.Client(), ts.URL, ClientConfig{APIVersion: "v1"})

	if _, err := client.CreateSession.CallUnary(ctx, connect.NewRequest(&api.CreateSessionRequest{SessionID: "s1"})); err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	launchResp, err := client.LaunchProcess.CallUnary(ctx, connect.NewRequest(&api.LaunchProcessRequest{
		SessionID: "s1",
		Binary:    "/bin/sh",
		Args:      []string{"-c", "sleep 30"},
	}))
	if err != nil {
		t.Fatalf("LaunchProcess failed: %v", err)
	}
	if launchResp.Msg.ProcessID == "" {
		t.Fatalf("expected process ID")
	}

	statusResp, err := client.GetProcessStatus.CallUnary(ctx, connect.NewRequest(&api.GetProcessStatusRequest{
		SessionID: "s1",
		ProcessID: launchResp.Msg.ProcessID,
	}))
	if err != nil {
		t.Fatalf("GetProcessStatus failed: %v", err)
	}
	if statusResp.Msg.Status != api.ProcessStatusRunning {
		t.Fatalf("status = %q, want %q", statusResp.Msg.Status, api.ProcessStatusRunning)
	}

	if _, err := client.KillProcess.CallUnary(ctx, connect.NewRequest(&api.KillProcessRequest{
		SessionID: "s1",
		ProcessID: launchResp.Msg.ProcessID,
	})); err != nil {
		t.Fatalf("KillProcess failed: %v", err)
	}
}

func TestTransportAgentEventStream(t *testing.T) {
	sandboxRPC := newSandboxRPCForTransport(t)
	t.Cleanup(func() { _ = sandboxRPC.Close() })
	eventRPC := rpcserver.NewAgentEventServer()
	srv := NewServer(ServerConfig{}, WithSandboxService(sandboxRPC), WithAgentEventService(eventRPC))
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	client := NewEventClient(ts.Client(), ts.URL, ClientConfig{APIVersion: "v1"})
	want := agent.AgentEvent{Type: agent.EventTurnStarted, SessionID: "s1"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	type result struct {
		msg *api.AgentEventEnvelope
		err error
	}
	ch := make(chan result, 1)
	go func() {
		stream, err := client.Stream.CallServerStream(ctx, connect.NewRequest(&api.StreamAgentEventsRequest{SessionID: "s1"}))
		if err != nil {
			ch <- result{err: err}
			return
		}
		if !stream.Receive() {
			ch <- result{err: stream.Err()}
			return
		}
		ch <- result{msg: stream.Msg()}
	}()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	select {
	case <-ctx.Done():
		t.Fatalf("timed out waiting for event")
	case <-ticker.C:
		eventRPC.Publish("s1", want)
		for {
			select {
			case <-ctx.Done():
				t.Fatalf("timed out waiting for event")
			case got := <-ch:
				if got.err != nil {
					t.Fatalf("receive event failed: %v", got.err)
				}
				if got.msg == nil || got.msg.Event.Type != want.Type {
					t.Fatalf("unexpected event: %#v", got.msg)
				}
				return
			case <-ticker.C:
				eventRPC.Publish("s1", want)
			}
		}
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("receive event failed: %v", got.err)
		}
		if got.msg == nil || got.msg.Event.Type != want.Type {
			t.Fatalf("unexpected event: %#v", got.msg)
		}
	}
}

func TestTransportTerminalBidiAttach(t *testing.T) {
	sandboxRPC := newSandboxRPCForTransport(t)
	t.Cleanup(func() { _ = sandboxRPC.Close() })
	sm := termmux.NewSessionManager()
	_, err := sm.Create("s1", termmux.SessionConfig{Command: "cat"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	srv := NewServer(
		ServerConfig{},
		WithSandboxService(sandboxRPC),
		WithAgentEventService(rpcserver.NewAgentEventServer()),
		WithSessionManager(sm),
	)
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	tc := NewTerminalClient(ts.Client(), ts.URL, ClientConfig{APIVersion: "v1"})
	stream := tc.Stream.CallBidiStream(context.Background())
	if err := stream.Send(&termmux.TerminalClientMessage{
		Attach: &termmux.StreamTerminalRequest{SessionID: "s1"},
	}); err != nil {
		t.Fatalf("send attach: %v", err)
	}
	msg, err := stream.Receive()
	if err != nil {
		t.Fatalf("receive attached: %v", err)
	}
	if msg == nil || msg.Attached == nil {
		t.Fatalf("expected attached message")
	}
	_ = stream.CloseRequest()
	_ = stream.CloseResponse()
}
