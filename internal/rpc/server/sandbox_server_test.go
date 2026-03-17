package server

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/rpc"
	"github.com/anthropics/flex-agent-runtime/internal/rpc/api"
	"github.com/anthropics/flex-agent-runtime/internal/sandbox"
	"github.com/anthropics/flex-agent-runtime/internal/sandbox/gvisor"
	"github.com/anthropics/flex-agent-runtime/internal/sandbox/zfs"
)

type testGVisor struct{}

func (g *testGVisor) Run(_ context.Context, opts gvisor.ContainerOptions) (*gvisor.ContainerResult, error) {
	if opts.StdoutWriter != nil {
		_, _ = opts.StdoutWriter.Write([]byte("ok"))
	}
	exit := 0
	return &gvisor.ContainerResult{ExitCode: exit, Stdout: []byte("ok")}, nil
}
func (g *testGVisor) CleanupStale(context.Context) (int, error) { return 0, nil }
func (g *testGVisor) ActiveContainers() int                     { return 0 }
func (g *testGVisor) Close() error                              { return nil }

func newHostForRPC(t *testing.T) (*sandbox.SandboxHostService, *zfs.MockManager) {
	t.Helper()
	zm := zfs.NewMockManager()
	cfg := sandbox.DefaultServiceConfig()
	cfg.PoolName = "tank"
	cfg.BasesDataset = "tank/bases"
	cfg.SessionsDataset = "tank/sessions"
	cfg.ToolTimeout = 0
	host, err := sandbox.NewSandboxHostService(cfg, zm, &testGVisor{}, nil)
	if err != nil {
		t.Fatalf("NewSandboxHostService: %v", err)
	}
	ctx := context.Background()
	if err := zm.CreateDataset(ctx, "tank/bases/repo", zfs.DatasetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := zm.CreateSnapshot(ctx, "tank/bases/repo", "initial"); err != nil {
		t.Fatal(err)
	}
	return host, zm
}

func TestSandboxServerSessionCRUDAndTool(t *testing.T) {
	host, _ := newHostForRPC(t)
	srv := NewSandboxServer(host)
	t.Cleanup(func() { _ = srv.Close() })
	ctx := context.Background()

	create, err := srv.CreateSession(ctx, &api.CreateSessionRequest{SessionID: "s1", BaseSnapshot: "tank/bases/repo@initial"})
	if err != nil {
		t.Fatalf("CreateSession error = %v", err)
	}
	if create.Session == nil || create.Session.ID != "s1" {
		t.Fatalf("unexpected create response: %+v", create)
	}
	get, err := srv.GetSession(ctx, &api.GetSessionRequest{SessionID: "s1"})
	if err != nil || get.Session == nil {
		t.Fatalf("GetSession = (%+v, %v)", get, err)
	}

	stream, err := srv.ExecuteToolStream(ctx, &api.ExecuteToolRequest{SessionID: "s1", ToolCallID: "tc1", ToolName: "bash", Params: map[string]any{"cmd": "echo hi"}})
	if err != nil {
		t.Fatalf("ExecuteToolStream error = %v", err)
	}
	seenResp := false
	seenProgress := false
	for {
		msg, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			t.Fatalf("stream recv error = %v", recvErr)
		}
		if msg.Response != nil {
			seenResp = true
			if msg.Response.ToolCallID != "tc1" || msg.Response.SnapshotID != "" {
				t.Fatalf("unexpected response: %+v", msg.Response)
			}
		}
		if msg.Progress != nil && msg.Progress.Content != "" {
			seenProgress = true
		}
	}
	if !seenResp {
		t.Fatalf("expected final response message")
	}
	if !seenProgress {
		t.Fatalf("expected streamed progress")
	}

	if _, err := srv.DestroySession(ctx, &api.DestroySessionRequest{SessionID: "s1"}); err != nil {
		t.Fatalf("DestroySession error = %v", err)
	}
}

func TestSandboxServerExecuteToolIdempotency(t *testing.T) {
	host, _ := newHostForRPC(t)
	srv := NewSandboxServer(host)
	t.Cleanup(func() { _ = srv.Close() })
	ctx := context.Background()
	_, _ = srv.CreateSession(ctx, &api.CreateSessionRequest{SessionID: "s1", BaseSnapshot: "tank/bases/repo@initial"})

	first, err := srv.ExecuteTool(ctx, &api.ExecuteToolRequest{SessionID: "s1", ToolCallID: "tc1", ToolName: "bash", Params: map[string]any{"cmd": "echo 1"}})
	if err != nil {
		t.Fatalf("first ExecuteTool error = %v", err)
	}
	second, err := srv.ExecuteTool(ctx, &api.ExecuteToolRequest{SessionID: "s1", ToolCallID: "tc1", ToolName: "bash", Params: map[string]any{"cmd": "echo 1"}})
	if err != nil {
		t.Fatalf("second ExecuteTool error = %v", err)
	}
	if first.Content != second.Content || first.ToolCallID != second.ToolCallID {
		t.Fatalf("idempotent mismatch: first=%+v second=%+v", first, second)
	}
}

func TestSandboxServerMapsErrors(t *testing.T) {
	host, _ := newHostForRPC(t)
	srv := NewSandboxServer(host)
	t.Cleanup(func() { _ = srv.Close() })
	_, err := srv.GetSession(context.Background(), &api.GetSessionRequest{SessionID: "missing"})
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected rpc error, got %T", err)
	}
	if rpcErr.Code != rpc.CodeNotFound {
		t.Fatalf("code = %s, want %s", rpcErr.Code, rpc.CodeNotFound)
	}
}

func TestSandboxServerIdempotencySweepEvictsExpired(t *testing.T) {
	host, _ := newHostForRPC(t)
	srv := NewSandboxServer(host)
	t.Cleanup(func() { _ = srv.Close() })
	srv.mu.Lock()
	srv.idempotency["s1:tc1"] = idempotencyEntry{
		response: &api.ExecuteToolResponse{ToolCallID: "tc1"},
		expires:  time.Now().Add(-time.Second),
	}
	srv.mu.Unlock()
	srv.evictExpired()
	srv.mu.Lock()
	size := len(srv.idempotency)
	srv.mu.Unlock()
	if size != 0 {
		t.Fatalf("expected idempotency cache to be empty after sweep, size=%d", size)
	}
}

func TestSandboxServerProcessLifecycle(t *testing.T) {
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

	srv := NewSandboxServer(host)
	t.Cleanup(func() { _ = srv.Close() })
	_, err = srv.CreateSession(ctx, &api.CreateSessionRequest{SessionID: "s1"})
	if err != nil {
		t.Fatalf("CreateSession error = %v", err)
	}

	launch, err := srv.LaunchProcess(ctx, &api.LaunchProcessRequest{
		SessionID: "s1",
		Binary:    "/bin/sh",
		Args:      []string{"-c", "sleep 30"},
	})
	if err != nil {
		t.Fatalf("LaunchProcess error = %v", err)
	}
	if launch.ProcessID == "" {
		t.Fatalf("expected process ID")
	}
	if launch.Status != api.ProcessStatusRunning {
		t.Fatalf("status = %q, want %q", launch.Status, api.ProcessStatusRunning)
	}

	status, err := srv.GetProcessStatus(ctx, &api.GetProcessStatusRequest{SessionID: "s1", ProcessID: launch.ProcessID})
	if err != nil {
		t.Fatalf("GetProcessStatus error = %v", err)
	}
	if status.Status != api.ProcessStatusRunning {
		t.Fatalf("status = %q, want %q", status.Status, api.ProcessStatusRunning)
	}
	if status.ExitCode != nil {
		t.Fatalf("exit code should be nil while running")
	}

	if _, err := srv.KillProcess(ctx, &api.KillProcessRequest{SessionID: "s1", ProcessID: launch.ProcessID}); err != nil {
		t.Fatalf("KillProcess error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		status, err = srv.GetProcessStatus(ctx, &api.GetProcessStatusRequest{SessionID: "s1", ProcessID: launch.ProcessID})
		if err != nil {
			t.Fatalf("GetProcessStatus after kill error = %v", err)
		}
		if status.Status == api.ProcessStatusExited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("process did not exit in time; status=%q", status.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status.ExitCode == nil {
		t.Fatalf("expected exit code after process exit")
	}
}
