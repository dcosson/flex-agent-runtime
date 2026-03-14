package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"h2-agent-runtime/internal/rpc/api"
	rpcserver "h2-agent-runtime/internal/rpc/server"
	"h2-agent-runtime/internal/sandbox"
	"h2-agent-runtime/internal/sandbox/gvisor"
	"h2-agent-runtime/internal/sandbox/zfs"
	"h2-agent-runtime/internal/termmux"
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
	host := sandbox.NewSandboxHostService(cfg, zm, &fakeGVisor{}, nil)
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
	srv := NewServer(sandboxRPC, events, nil, ServerConfig{
		APIVersion:    "v2",
		MinAPIVersion: "v2",
		AuthHook: func(_ context.Context, _ string, headers http.Header) error {
			if headers.Get("authorization") != "Bearer good" {
				return errors.New("unauthenticated")
			}
			return nil
		},
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client := NewSandboxClient(ts.Client(), ts.URL, ClientConfig{
		APIVersion: "v2",
		AuthHook:   HeaderTokenAuth("authorization", "Bearer good"),
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

func TestTransportVersionRejected(t *testing.T) {
	sandboxRPC := newSandboxRPCForTransport(t)
	t.Cleanup(func() { _ = sandboxRPC.Close() })
	srv := NewServer(sandboxRPC, rpcserver.NewAgentEventServer(), nil, ServerConfig{
		APIVersion:    "v2",
		MinAPIVersion: "v2",
	})
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

func TestTransportTerminalBidiAttach(t *testing.T) {
	sandboxRPC := newSandboxRPCForTransport(t)
	t.Cleanup(func() { _ = sandboxRPC.Close() })
	sm := termmux.NewSessionManager()
	_, err := sm.Create("s1", termmux.SessionConfig{Command: "cat"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	srv := NewServer(sandboxRPC, rpcserver.NewAgentEventServer(), sm, ServerConfig{})
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
