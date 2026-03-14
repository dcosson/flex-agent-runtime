package rpctest

import (
	"context"
	"errors"
	"io"
	"testing"

	"h2-agent-runtime/internal/rpc"
	"h2-agent-runtime/internal/rpc/api"
	"h2-agent-runtime/internal/tools"
)

// O1. Contract golden tests — verify each RPC method returns expected structures.
func TestO1_GoldenCreateSession(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()

	resp, err := stack.Server.CreateSession(ctx, &api.CreateSessionRequest{
		SessionID: "golden-1", BaseSnapshot: baseSnapshot,
		Labels: map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := resp.Session
	if s.ID != "golden-1" {
		t.Fatalf("ID = %q, want golden-1", s.ID)
	}
	if s.State != "active" {
		t.Fatalf("State = %q, want active", s.State)
	}
	if s.TurnCount != 0 {
		t.Fatalf("TurnCount = %d, want 0", s.TurnCount)
	}
	if s.Labels["env"] != "test" {
		t.Fatalf("Labels[env] = %q, want test", s.Labels["env"])
	}
}

func TestO1_GoldenGetSession(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()
	createRPCSession(t, stack.Server, "golden-get")

	resp, err := stack.Server.GetSession(ctx, &api.GetSessionRequest{SessionID: "golden-get"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Session.ID != "golden-get" || resp.Session.State != "active" {
		t.Fatalf("unexpected GetSession: %+v", resp.Session)
	}
}

func TestO1_GoldenPauseResume(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()
	createRPCSession(t, stack.Server, "golden-pr")

	_, err := stack.Server.PauseSession(ctx, &api.PauseSessionRequest{SessionID: "golden-pr"})
	if err != nil {
		t.Fatal(err)
	}
	get, _ := stack.Server.GetSession(ctx, &api.GetSessionRequest{SessionID: "golden-pr"})
	if get.Session.State != "paused" {
		t.Fatalf("State after pause = %q, want paused", get.Session.State)
	}

	_, err = stack.Server.ResumeSession(ctx, &api.ResumeSessionRequest{SessionID: "golden-pr"})
	if err != nil {
		t.Fatal(err)
	}
	get, _ = stack.Server.GetSession(ctx, &api.GetSessionRequest{SessionID: "golden-pr"})
	if get.Session.State != "active" {
		t.Fatalf("State after resume = %q, want active", get.Session.State)
	}
}

func TestO1_GoldenTurnComplete(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()
	createRPCSession(t, stack.Server, "golden-tc")

	resp, err := stack.Server.TurnComplete(ctx, &api.TurnCompleteRequest{SessionID: "golden-tc"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TurnNumber != 1 {
		t.Fatalf("TurnNumber = %d, want 1", resp.TurnNumber)
	}
	if resp.SnapshotID == "" {
		t.Fatal("SnapshotID should not be empty")
	}
}

func TestO1_GoldenListSnapshots(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()
	createRPCSession(t, stack.Server, "golden-ls")

	// No snapshots initially
	listResp, err := stack.Server.ListSnapshots(ctx, &api.ListSnapshotsRequest{SessionID: "golden-ls"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listResp.Snapshots) != 0 {
		t.Fatalf("expected 0 snapshots, got %d", len(listResp.Snapshots))
	}

	// After TurnComplete
	stack.Server.TurnComplete(ctx, &api.TurnCompleteRequest{SessionID: "golden-ls"})
	listResp, _ = stack.Server.ListSnapshots(ctx, &api.ListSnapshotsRequest{SessionID: "golden-ls"})
	if len(listResp.Snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(listResp.Snapshots))
	}
}

func TestO1_GoldenHealthCheck(t *testing.T) {
	stack := newTestStack(t)
	resp, err := stack.Server.HealthCheck(context.Background(), &api.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != "healthy" {
		t.Fatalf("Status = %q, want healthy", resp.Status)
	}
}

func TestO1_GoldenDestroySession(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()
	createRPCSession(t, stack.Server, "golden-destroy")

	_, err := stack.Server.DestroySession(ctx, &api.DestroySessionRequest{SessionID: "golden-destroy"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = stack.Server.GetSession(ctx, &api.GetSessionRequest{SessionID: "golden-destroy"})
	assertRPCError(t, err, rpc.CodeNotFound)
}

func TestO1_GoldenRollback(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()
	createRPCSession(t, stack.Server, "golden-rb")

	// Create 3 turns
	for i := 0; i < 3; i++ {
		stack.Server.TurnComplete(ctx, &api.TurnCompleteRequest{SessionID: "golden-rb"})
	}

	snaps, _ := stack.Server.ListSnapshots(ctx, &api.ListSnapshotsRequest{SessionID: "golden-rb"})
	if len(snaps.Snapshots) != 3 {
		t.Fatalf("expected 3 snapshots, got %d", len(snaps.Snapshots))
	}

	// Rollback to first snapshot
	_, err := stack.Server.RollbackSession(ctx, &api.RollbackSessionRequest{
		SessionID: "golden-rb", SnapshotID: snaps.Snapshots[0].Name,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Should have 1 snapshot now
	snapsAfter, _ := stack.Server.ListSnapshots(ctx, &api.ListSnapshotsRequest{SessionID: "golden-rb"})
	if len(snapsAfter.Snapshots) != 1 {
		t.Fatalf("after rollback: expected 1 snapshot, got %d", len(snapsAfter.Snapshots))
	}
}

func TestO1_GoldenExecuteToolStream(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()
	createRPCSession(t, stack.Server, "golden-stream")

	stream, err := stack.Server.ExecuteToolStream(ctx, &api.ExecuteToolRequest{
		SessionID: "golden-stream", ToolCallID: "tc-gs", ToolName: "bash",
		Params: map[string]any{"cmd": "echo hello"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var gotResponse bool
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if msg.Response != nil {
			gotResponse = true
			if msg.Response.ToolCallID != "tc-gs" {
				t.Fatalf("ToolCallID = %q, want tc-gs", msg.Response.ToolCallID)
			}
			if msg.Response.SessionID != "golden-stream" {
				t.Fatalf("SessionID = %q, want golden-stream", msg.Response.SessionID)
			}
		}
	}
	if !gotResponse {
		t.Fatal("missing final response in stream")
	}
}

// O2. Cross-version compatibility — test API version context propagation.
func TestO2_APIVersionPropagation(t *testing.T) {
	ctx := rpc.WithAPIVersion(context.Background(), "v2")
	version := rpc.APIVersionFromContext(ctx)
	if version != "v2" {
		t.Fatalf("API version = %q, want v2", version)
	}

	// Nil context
	if v := rpc.APIVersionFromContext(nil); v != "" {
		t.Fatalf("nil context version = %q, want empty", v)
	}

	// No version set
	if v := rpc.APIVersionFromContext(context.Background()); v != "" {
		t.Fatalf("no version = %q, want empty", v)
	}

	// Min supported version
	if v := rpc.MinSupportedAPIVersion(); v != "v1" {
		t.Fatalf("min version = %q, want v1", v)
	}
}

// O3. SandboxBackend parity oracle — compare direct host results vs RPC-mediated.
func TestO3_SandboxBackendParity(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()

	// Create session via direct host API
	createRPCSession(t, stack.Server, "parity")

	// Execute via RPC (SandboxClient implements SandboxToolClient)
	rpcResp, err := stack.Client.ExecuteTool(ctx, "parity", tools.ToolRequest{
		ToolCallID: "tc-parity",
		ToolName:   "bash",
		Params:     map[string]any{"cmd": "echo parity"},
	}, nil)
	if err != nil {
		t.Fatalf("RPC ExecuteTool: %v", err)
	}

	// Execute the same tool via direct server (unary)
	directResp, err := stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
		SessionID: "parity", ToolCallID: "tc-parity-direct",
		ToolName: "bash", Params: map[string]any{"cmd": "echo parity"},
	})
	if err != nil {
		t.Fatalf("direct ExecuteTool: %v", err)
	}

	// Both should succeed and produce similar output
	if rpcResp == nil || directResp == nil {
		t.Fatal("nil response")
	}
	if len(rpcResp.Content) == 0 {
		t.Fatal("RPC response has no content blocks")
	}
	if directResp.Content == "" && len(directResp.ContentBlocks) == 0 {
		t.Fatal("direct response has no content")
	}

	// Both should be same tool
	if directResp.ToolName != "bash" {
		t.Fatalf("direct ToolName = %q", directResp.ToolName)
	}
}
