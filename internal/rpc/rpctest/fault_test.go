package rpctest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"h2-agent-runtime/internal/rpc"
	"h2-agent-runtime/internal/rpc/api"
	"h2-agent-runtime/internal/tools"
)

// F1. Network flapping — simulated via intermittent errors on SandboxService.
func TestF1_NetworkFlapping(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()
	sess := createRPCSession(t, stack.Server, "f1")

	// Alternate between working and failing by injecting gVisor errors
	successes := 0
	failures := 0
	for i := 0; i < 20; i++ {
		if i%3 == 0 {
			stack.GVisor.SetError(fmt.Errorf("transient failure"))
		} else {
			stack.GVisor.SetError(nil)
		}
		_, err := stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
			SessionID:  sess.ID,
			ToolCallID: fmt.Sprintf("tc-flap-%d", i),
			ToolName:   "bash",
			Params:     map[string]any{"cmd": "echo ok"},
		})
		if err != nil {
			failures++
		} else {
			successes++
		}
	}

	if successes == 0 {
		t.Fatal("no successful calls during flapping")
	}
	if failures == 0 {
		t.Fatal("expected some failures during flapping")
	}

	// Session should still be active after flapping
	get, err := stack.Server.GetSession(ctx, &api.GetSessionRequest{SessionID: sess.ID})
	if err != nil {
		t.Fatal(err)
	}
	if get.Session.State != "active" {
		t.Fatalf("state = %s, want active", get.Session.State)
	}
}

// F2. Half-open stream stalls — deadline/cancellation closes resources cleanly.
func TestF2_HalfOpenStreamStall(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()
	createRPCSession(t, stack.Server, "f2")

	// Create a stream with a tight deadline
	streamCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	// Subscribe to events, then let the context expire without consuming
	recv, err := stack.Events.StreamAgentEvents(streamCtx, &api.StreamAgentEventsRequest{SessionID: "f2"})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for context to expire
	<-streamCtx.Done()

	// Recv should return EOF or stream closed error
	_, err = recv.Recv()
	if err == nil {
		t.Fatal("expected error after context expiry")
	}
	// Should be EOF (channel closed by context cancellation goroutine)
	if !errors.Is(err, io.EOF) && !errors.Is(err, api.ErrStreamClosed) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// F3. Server-side transient overload — return unavailable bursts.
func TestF3_TransientOverload(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()
	sess := createRPCSession(t, stack.Server, "f3")

	// Inject overload errors via ZFS
	stack.ZFS.SetError("CreateSnapshot", fmt.Errorf("zfs: pool busy"))

	_, err := stack.Server.TurnComplete(ctx, &api.TurnCompleteRequest{SessionID: sess.ID})
	if err == nil {
		t.Fatal("expected error from overloaded server")
	}
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected RPCError, got %T: %v", err, err)
	}

	// Clear and verify recovery
	stack.ZFS.ClearError("CreateSnapshot")
	resp, err := stack.Server.TurnComplete(ctx, &api.TurnCompleteRequest{SessionID: sess.ID})
	if err != nil {
		t.Fatalf("recovery failed: %v", err)
	}
	if resp.TurnNumber != 1 {
		t.Fatalf("turn = %d, want 1", resp.TurnNumber)
	}
}

// F4. Malformed payload injection — nil and invalid requests.
func TestF4_MalformedPayloads(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()

	// Nil request to ExecuteTool
	_, err := stack.Server.ExecuteTool(ctx, nil)
	assertRPCError(t, err, rpc.CodeInvalidArgument)

	// Empty ToolCallID
	createRPCSession(t, stack.Server, "f4")
	_, err = stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
		SessionID: "f4", ToolCallID: "", ToolName: "bash",
	})
	assertRPCError(t, err, rpc.CodeInvalidArgument)

	// Nil request to ExecuteToolStream
	_, err = stack.Server.ExecuteToolStream(ctx, nil)
	assertRPCError(t, err, rpc.CodeInvalidArgument)

	// Nil/empty SessionID to StreamAgentEvents
	_, err = stack.Events.StreamAgentEvents(ctx, nil)
	assertRPCError(t, err, rpc.CodeInvalidArgument)

	_, err = stack.Events.StreamAgentEvents(ctx, &api.StreamAgentEventsRequest{SessionID: ""})
	assertRPCError(t, err, rpc.CodeInvalidArgument)
}

// F4b. Client rejects missing ToolCallID
func TestF4b_ClientRejectsMissingToolCallID(t *testing.T) {
	stack := newTestStack(t)
	createRPCSession(t, stack.Server, "f4b")

	_, err := stack.Client.ExecuteTool(context.Background(), "f4b", tools.ToolRequest{
		ToolCallID: "", ToolName: "bash",
	}, nil)
	if err == nil {
		t.Fatal("expected error for empty ToolCallID")
	}
}

// F5. Duplicate delivery — idempotency cache returns same response.
func TestF5_DuplicateDelivery(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()
	sess := createRPCSession(t, stack.Server, "f5")

	// First call
	resp1, err := stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
		SessionID: sess.ID, ToolCallID: "tc-dup", ToolName: "bash",
		Params: map[string]any{"cmd": "echo dup"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Second call with same ToolCallID — should return cached response
	resp2, err := stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
		SessionID: sess.ID, ToolCallID: "tc-dup", ToolName: "bash",
		Params: map[string]any{"cmd": "echo dup"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if resp1.Content != resp2.Content || resp1.ToolCallID != resp2.ToolCallID {
		t.Fatalf("idempotent response mismatch: %+v vs %+v", resp1, resp2)
	}

	// Different ToolCallID should execute independently
	resp3, err := stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
		SessionID: sess.ID, ToolCallID: "tc-dup-2", ToolName: "bash",
		Params: map[string]any{"cmd": "echo dup2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp3.ToolCallID == resp1.ToolCallID {
		t.Fatal("different ToolCallIDs should return different responses")
	}
}

// F5b. Duplicate delivery via streaming — idempotency returns buffered stream.
func TestF5b_DuplicateDeliveryStreaming(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()
	sess := createRPCSession(t, stack.Server, "f5b")

	// First stream call
	stream1, err := stack.Server.ExecuteToolStream(ctx, &api.ExecuteToolRequest{
		SessionID: sess.ID, ToolCallID: "tc-dup-stream", ToolName: "bash",
		Params: map[string]any{"cmd": "echo stream"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var resp1 *api.ExecuteToolResponse
	for {
		msg, err := stream1.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if msg.Response != nil {
			resp1 = msg.Response
		}
	}
	if resp1 == nil {
		t.Fatal("no response from first stream")
	}

	// Second stream call with same ToolCallID — should return cached
	stream2, err := stack.Server.ExecuteToolStream(ctx, &api.ExecuteToolRequest{
		SessionID: sess.ID, ToolCallID: "tc-dup-stream", ToolName: "bash",
		Params: map[string]any{"cmd": "echo stream"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var resp2 *api.ExecuteToolResponse
	for {
		msg, err := stream2.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if msg.Response != nil {
			resp2 = msg.Response
		}
	}
	if resp2 == nil {
		t.Fatal("no response from cached stream")
	}
	if resp1.Content != resp2.Content {
		t.Fatalf("cached stream response mismatch: %q vs %q", resp1.Content, resp2.Content)
	}
}

// F5c. Concurrent duplicate delivery — same ToolCallID sent concurrently.
func TestF5c_ConcurrentDuplicateDelivery(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()
	sess := createRPCSession(t, stack.Server, "f5c")

	const concurrent = 10
	var wg sync.WaitGroup
	responses := make([]*api.ExecuteToolResponse, concurrent)
	errs := make([]error, concurrent)

	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, err := stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
				SessionID: sess.ID, ToolCallID: "tc-concurrent-dup", ToolName: "bash",
				Params: map[string]any{"cmd": "echo concurrent"},
			})
			responses[idx] = resp
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	// All should succeed (or all get same cached response)
	var firstContent string
	for i, resp := range responses {
		if errs[i] != nil {
			t.Fatalf("concurrent call %d error: %v", i, errs[i])
		}
		if resp == nil {
			t.Fatalf("concurrent call %d: nil response", i)
		}
		if firstContent == "" {
			firstContent = resp.Content
		} else if resp.Content != firstContent {
			t.Fatalf("concurrent response mismatch at %d: %q vs %q", i, resp.Content, firstContent)
		}
	}
}
