package server

import (
	"context"
	"errors"
	"io"
	"testing"

	"h2-agent-runtime/internal/rpc/api"
	"h2-agent-runtime/internal/sandbox"
	"h2-agent-runtime/internal/sandbox/gvisor"
	"h2-agent-runtime/internal/sandbox/zfs"
)

type testGVisor struct{}

func (g *testGVisor) Run(context.Context, gvisor.ContainerOptions) (*gvisor.ContainerResult, error) {
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
	host := sandbox.NewSandboxHostService(cfg, zm, &testGVisor{}, nil)
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
	}
	if !seenResp {
		t.Fatalf("expected final response message")
	}

	if _, err := srv.DestroySession(ctx, &api.DestroySessionRequest{SessionID: "s1"}); err != nil {
		t.Fatalf("DestroySession error = %v", err)
	}
}

func TestSandboxServerExecuteToolIdempotency(t *testing.T) {
	host, _ := newHostForRPC(t)
	srv := NewSandboxServer(host)
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
