package rpctest

import (
	"context"
	"errors"
	"sync"
	"testing"

	"flex-agent-runtime/internal/rpc"
	"flex-agent-runtime/internal/rpc/api"
	"flex-agent-runtime/internal/rpc/client"
	"flex-agent-runtime/internal/rpc/server"
	"flex-agent-runtime/internal/sandbox"
	"flex-agent-runtime/internal/sandbox/gvisor"
	"flex-agent-runtime/internal/sandbox/zfs"
)

const baseSnapshot = "tank/bases/repo@initial"

// testGVisor provides a simple gVisor mock for RPC harness tests.
type testGVisor struct {
	mu   sync.Mutex
	fail error
}

func (g *testGVisor) Run(_ context.Context, opts gvisor.ContainerOptions) (*gvisor.ContainerResult, error) {
	g.mu.Lock()
	err := g.fail
	g.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if opts.StdoutWriter != nil {
		_, _ = opts.StdoutWriter.Write([]byte("ok"))
	}
	exit := 0
	return &gvisor.ContainerResult{ExitCode: exit, Stdout: []byte("ok")}, nil
}
func (g *testGVisor) CleanupStale(context.Context) (int, error) { return 0, nil }
func (g *testGVisor) ActiveContainers() int                     { return 0 }
func (g *testGVisor) Close() error                              { return nil }

func (g *testGVisor) SetError(err error) {
	g.mu.Lock()
	g.fail = err
	g.mu.Unlock()
}

// testStack wires a full server+client stack over in-process calls.
type testStack struct {
	Host   *sandbox.SandboxHostService
	ZFS    *zfs.MockManager
	GVisor *testGVisor
	Server *server.SandboxServer
	Events *server.AgentEventServer
	Client *client.SandboxClient
}

func newTestStack(t *testing.T) *testStack {
	t.Helper()
	zm := zfs.NewMockManager()
	gm := &testGVisor{}
	cfg := sandbox.DefaultServiceConfig()
	cfg.PoolName = "tank"
	cfg.BasesDataset = "tank/bases"
	cfg.SessionsDataset = "tank/sessions"
	cfg.ToolTimeout = 0
	host, err := sandbox.NewSandboxHostService(cfg, zm, gm, nil)
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

	srv := server.NewSandboxServer(host)
	t.Cleanup(func() { _ = srv.Close() })
	eventSrv := server.NewAgentEventServer()
	cl := client.NewSandboxClient(srv)

	return &testStack{Host: host, ZFS: zm, GVisor: gm, Server: srv, Events: eventSrv, Client: cl}
}

// createRPCSession creates a session through the RPC server.
func createRPCSession(t *testing.T, srv api.SandboxService, id string) *api.Session {
	t.Helper()
	resp, err := srv.CreateSession(context.Background(), &api.CreateSessionRequest{
		SessionID:    id,
		BaseSnapshot: baseSnapshot,
	})
	if err != nil {
		t.Fatalf("CreateSession(%q) error: %v", id, err)
	}
	return resp.Session
}

// assertRPCError checks that err is an RPCError with the given code.
func assertRPCError(t *testing.T, err error, code rpc.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected rpc error with code %s, got nil", code)
	}
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != code {
		t.Fatalf("rpc code = %s, want %s (msg: %s)", rpcErr.Code, code, rpcErr.Message)
	}
}
