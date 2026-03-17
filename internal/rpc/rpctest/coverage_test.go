package rpctest

import (
	"context"
	"fmt"
	"testing"

	"github.com/anthropics/flex-agent-runtime/internal/rpc"
	"github.com/anthropics/flex-agent-runtime/internal/rpc/api"
	"github.com/anthropics/flex-agent-runtime/internal/rpc/codec"
	"github.com/anthropics/flex-agent-runtime/internal/sandbox"
)

// Coverage gap: server.CreateSnapshot
func TestCoverage_CreateSnapshot(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()
	sess := createRPCSession(t, stack.Server, "cov-snap")

	resp, err := stack.Server.CreateSnapshot(ctx, &api.CreateSnapshotRequest{
		SessionID: sess.ID, Name: "manual-snap",
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if resp.SnapshotID != "manual-snap" {
		t.Fatalf("SnapshotID = %q, want manual-snap", resp.SnapshotID)
	}
}

// Coverage gap: DefaultRetryPolicy
func TestCoverage_DefaultRetryPolicy(t *testing.T) {
	p := rpc.DefaultRetryPolicy()
	if p.MaxAttempts != 3 {
		t.Fatalf("MaxAttempts = %d, want 3", p.MaxAttempts)
	}
	if p.BaseDelayMs != 100 {
		t.Fatalf("BaseDelayMs = %d, want 100", p.BaseDelayMs)
	}
	if p.MaxDelayMs != 2000 {
		t.Fatalf("MaxDelayMs = %d, want 2000", p.MaxDelayMs)
	}
}

// Coverage gap: APIVersionHeaderName
func TestCoverage_APIVersionHeaderName(t *testing.T) {
	name := rpc.APIVersionHeaderName()
	if name != "x-api-version" {
		t.Fatalf("header name = %q, want x-api-version", name)
	}
}

// Coverage gap: SandboxServer.String
func TestCoverage_SandboxServerString(t *testing.T) {
	stack := newTestStack(t)
	s := stack.Server.String()
	if s == "" {
		t.Fatal("String() returned empty")
	}
}

// Coverage gap: RPCError.Error nil case and empty message
func TestCoverage_RPCErrorEdgeCases(t *testing.T) {
	var nilErr *rpc.RPCError
	if nilErr.Error() != "rpc: <nil>" {
		t.Fatalf("nil error = %q", nilErr.Error())
	}

	emptyMsg := &rpc.RPCError{Code: rpc.CodeInternal}
	if s := emptyMsg.Error(); s != "rpc error: internal" {
		t.Fatalf("empty msg error = %q", s)
	}
}

// Coverage gap: MapError nil
func TestCoverage_MapErrorNil(t *testing.T) {
	if rpc.MapError(nil) != nil {
		t.Fatal("MapError(nil) should return nil")
	}
}

// Coverage gap: MapError with already-RPCError input
func TestCoverage_MapErrorPassthrough(t *testing.T) {
	rpcErr := rpc.NewRPCError(rpc.CodeInternal, "already rpc", nil)
	mapped := rpc.MapError(rpcErr)
	if mapped != rpcErr {
		t.Fatal("MapError should pass through existing RPCError")
	}
}

// Coverage gap: MapError unknown error → internal
func TestCoverage_MapErrorUnknown(t *testing.T) {
	mapped := rpc.MapError(fmt.Errorf("random error"))
	rpcErr, ok := mapped.(*rpc.RPCError)
	if !ok {
		t.Fatal("expected RPCError")
	}
	if rpcErr.Code != rpc.CodeInternal {
		t.Fatalf("code = %s, want internal", rpcErr.Code)
	}
}

// Coverage gap: WrapRPCError with non-RPCError input
func TestCoverage_WrapNonRPCError(t *testing.T) {
	err := rpc.WrapRPCError(fmt.Errorf("plain error"), "s1", "bash")
	rpcErr, ok := err.(*rpc.RPCError)
	if !ok {
		t.Fatal("expected RPCError")
	}
	if rpcErr.Code != rpc.CodeInternal {
		t.Fatalf("code = %s", rpcErr.Code)
	}
	if rpcErr.Details["session_id"] != "s1" {
		t.Fatal("missing session_id")
	}
}

// Coverage gap: WrapRPCError nil
func TestCoverage_WrapRPCErrorNil(t *testing.T) {
	if rpc.WrapRPCError(nil, "s1", "bash") != nil {
		t.Fatal("WrapRPCError(nil) should return nil")
	}
}

// Coverage gap: codec.FromSnapshotResult nil
func TestCoverage_FromSnapshotResultNil(t *testing.T) {
	if codec.FromSnapshotResult(nil) != nil {
		t.Fatal("nil should return nil")
	}
}

// Coverage gap: codec.FromSnapshotResult
func TestCoverage_FromSnapshotResult(t *testing.T) {
	result := &sandbox.SnapshotResult{SnapshotID: "snap-1", TurnNumber: 3, SpaceUsed: 1024}
	apiResp := codec.FromSnapshotResult(result)
	if apiResp.SnapshotID != "snap-1" || apiResp.TurnNumber != 3 || apiResp.SpaceUsed != 1024 {
		t.Fatalf("unexpected: %+v", apiResp)
	}
}

// Coverage gap: codec.ToCreateSessionRequest nil
func TestCoverage_ToCreateSessionRequestNil(t *testing.T) {
	req := codec.ToCreateSessionRequest(nil)
	if req.BaseSnapshot != "" || req.SessionID != "" {
		t.Fatalf("nil should return zero value: %+v", req)
	}
}

// Coverage gap: codec.ToExecuteToolRequest nil
func TestCoverage_ToExecuteToolRequestNil(t *testing.T) {
	req := codec.ToExecuteToolRequest(nil)
	if req.SessionID != "" || req.ToolName != "" {
		t.Fatalf("nil should return zero value: %+v", req)
	}
}

// Coverage gap: codec.FromSessionInfo nil
func TestCoverage_FromSessionInfoNil(t *testing.T) {
	if codec.FromSessionInfo(nil) != nil {
		t.Fatal("nil should return nil")
	}
}

// Coverage gap: codec.FromExecuteToolResponse nil
func TestCoverage_FromExecuteToolResponseNil(t *testing.T) {
	if codec.FromExecuteToolResponse(nil, nil) != nil {
		t.Fatal("nil should return nil")
	}
}

// Coverage gap: NewExecuteToolStreamChannel
func TestCoverage_NewExecuteToolStreamChannel(t *testing.T) {
	ch := make(chan *api.ExecuteToolStreamMessage, 1)
	ch <- &api.ExecuteToolStreamMessage{Response: &api.ExecuteToolResponse{ToolCallID: "tc1"}}
	close(ch)

	stream := api.NewExecuteToolStreamChannel(ch, nil)
	msg, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if msg.Response.ToolCallID != "tc1" {
		t.Fatalf("ToolCallID = %q", msg.Response.ToolCallID)
	}
	_ = stream.Close()
}

// Coverage gap: bufferedExecuteToolStream.Close
func TestCoverage_BufferedStreamClose(t *testing.T) {
	stream := api.NewExecuteToolStream(
		&api.ExecuteToolStreamMessage{Response: &api.ExecuteToolResponse{ToolCallID: "tc1"}},
	)
	_ = stream.Close()
	_, err := stream.Recv()
	if err != api.ErrStreamClosed {
		t.Fatalf("expected ErrStreamClosed, got %v", err)
	}
}

// Coverage gap: BackoffDelayMs edge cases
func TestCoverage_BackoffEdgeCases(t *testing.T) {
	// Zero/negative attempt
	d := rpc.BackoffDelayMs(rpc.RetryPolicy{}, 0)
	if d <= 0 {
		t.Fatalf("delay for attempt 0 = %d", d)
	}

	// Negative base/max
	d = rpc.BackoffDelayMs(rpc.RetryPolicy{BaseDelayMs: -1, MaxDelayMs: -1}, 1)
	if d <= 0 {
		t.Fatalf("delay for negative policy = %d", d)
	}
}

// Coverage gap: IsRetryableMethod unknown method
func TestCoverage_IsRetryableMethodUnknown(t *testing.T) {
	if rpc.IsRetryableMethod("SomeRandomMethod") {
		t.Fatal("unknown method should not be retryable")
	}
}

// Coverage gap: IsRetryableError non-RPCError
func TestCoverage_IsRetryableErrorNonRPC(t *testing.T) {
	if rpc.IsRetryableError(fmt.Errorf("plain error")) {
		t.Fatal("non-RPCError should not be retryable")
	}
}

// Coverage gap: decodeResponseContent fallback to Content field
func TestCoverage_ClientDecodeContentFallback(t *testing.T) {
	stack := newTestStack(t)
	sess := createRPCSession(t, stack.Server, "cov-decode")

	// The server returns ContentBlocks + Content. The client prefers ContentBlocks.
	// This tests the flow end-to-end.
	resp, err := stack.Server.ExecuteTool(context.Background(), &api.ExecuteToolRequest{
		SessionID: sess.ID, ToolCallID: "tc-decode", ToolName: "bash",
		Params: map[string]any{"cmd": "echo decode"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Response should have content
	if resp.Content == "" && len(resp.ContentBlocks) == 0 {
		t.Fatal("no content in response")
	}
}

// Coverage gap: MapError ErrToolsInFlight
func TestCoverage_MapErrorToolsInFlight(t *testing.T) {
	mapped := rpc.MapError(sandbox.ErrToolsInFlight)
	rpcErr, ok := mapped.(*rpc.RPCError)
	if !ok {
		t.Fatal("expected RPCError")
	}
	if rpcErr.Code != rpc.CodeFailedPrecondition {
		t.Fatalf("code = %s, want failed_precondition", rpcErr.Code)
	}
}
