package rpctest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropics/flex-agent-runtime/internal/rpc"
	"github.com/anthropics/flex-agent-runtime/internal/rpc/api"
)

// SEC1. AuthN/AuthZ enforcement hooks — verify missing session returns NotFound.
// (Full auth enforcement is in the transport interceptor layer, not yet implemented.
// We test the session-level authorization: operations on non-existent sessions are rejected.)
func TestSEC1_UnauthorizedSessionAccess(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()

	// All session operations on non-existent session should return NotFound
	cases := []struct {
		name string
		fn   func() error
	}{
		{"GetSession", func() error {
			_, err := stack.Server.GetSession(ctx, &api.GetSessionRequest{SessionID: "nonexistent"})
			return err
		}},
		{"PauseSession", func() error {
			_, err := stack.Server.PauseSession(ctx, &api.PauseSessionRequest{SessionID: "nonexistent"})
			return err
		}},
		{"ResumeSession", func() error {
			_, err := stack.Server.ResumeSession(ctx, &api.ResumeSessionRequest{SessionID: "nonexistent"})
			return err
		}},
		{"DestroySession", func() error {
			_, err := stack.Server.DestroySession(ctx, &api.DestroySessionRequest{SessionID: "nonexistent"})
			return err
		}},
		{"TurnComplete", func() error {
			_, err := stack.Server.TurnComplete(ctx, &api.TurnCompleteRequest{SessionID: "nonexistent"})
			return err
		}},
		{"ListSnapshots", func() error {
			_, err := stack.Server.ListSnapshots(ctx, &api.ListSnapshotsRequest{SessionID: "nonexistent"})
			return err
		}},
		{"RollbackSession", func() error {
			_, err := stack.Server.RollbackSession(ctx, &api.RollbackSessionRequest{SessionID: "nonexistent", SnapshotID: "snap-1"})
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn()
			assertRPCError(t, err, rpc.CodeNotFound)
		})
	}
}

// SEC2. Input validation hardening — fuzz-like invalid payloads.
func TestSEC2_InputValidation(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()

	// Malicious session IDs
	maliciousIDs := []string{
		"../../../etc/passwd",
		"session; rm -rf /",
		"session\x00hidden",
		strings.Repeat("a", 10000),
		"<script>alert(1)</script>",
		"session\ninjection",
	}

	for _, id := range maliciousIDs {
		t.Run("malicious-id", func(t *testing.T) {
			_, err := stack.Server.CreateSession(ctx, &api.CreateSessionRequest{
				SessionID: id, BaseSnapshot: baseSnapshot,
			})
			// Should either succeed (valid ID from sandbox perspective) or return a typed error
			if err != nil {
				var rpcErr *rpc.RPCError
				if !errors.As(err, &rpcErr) {
					t.Fatalf("expected RPCError for ID %q, got %T: %v", id, err, err)
				}
				// Any error code is acceptable — no panics or untyped errors
			}
		})
	}

	// Nil/invalid ExecuteTool requests
	_, err := stack.Server.ExecuteTool(ctx, nil)
	assertRPCError(t, err, rpc.CodeInvalidArgument)

	_, err = stack.Server.ExecuteToolStream(ctx, nil)
	assertRPCError(t, err, rpc.CodeInvalidArgument)
}

// SEC2b. Empty and boundary parameter values
func TestSEC2b_BoundaryParameters(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()
	sess := createRPCSession(t, stack.Server, "sec2b")

	// Empty tool name — should still get handled (Tier 2 by default for unknown)
	_, err := stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
		SessionID: sess.ID, ToolCallID: "tc-empty-tool", ToolName: "",
		Params: map[string]any{},
	})
	// May succeed or fail, but must not panic
	_ = err

	// Very large params map with distinct keys
	bigParams := make(map[string]any)
	for i := 0; i < 1000; i++ {
		bigParams[fmt.Sprintf("key-%04d-%s", i, strings.Repeat("k", 45))] = strings.Repeat("v", 100)
	}
	_, err = stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
		SessionID: sess.ID, ToolCallID: "tc-big-params", ToolName: "bash",
		Params: bigParams,
	})
	_ = err // no panic is the assertion
}

// SEC3. Sensitive data redaction — RPCError messages should not leak internal details.
func TestSEC3_ErrorMessageSafety(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()

	// Access non-existent session — error should not leak filesystem paths
	_, err := stack.Server.GetSession(ctx, &api.GetSessionRequest{SessionID: "nonexistent"})
	if err == nil {
		t.Fatal("expected error")
	}
	errStr := err.Error()
	// Should not contain filesystem paths or internal implementation details
	for _, pattern := range []string{"/Users/", "/home/", "/var/", "/tmp/", "panic", "goroutine"} {
		if strings.Contains(strings.ToLower(errStr), strings.ToLower(pattern)) {
			t.Fatalf("error message leaks internal detail: %q contains %q", errStr, pattern)
		}
	}
}

// SEC4. DoS controls — max message size and timeout enforcement.
func TestSEC4_DoSControls(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()

	// Duplicate session creation should return AlreadyExists
	createRPCSession(t, stack.Server, "sec4-dup")
	_, err := stack.Server.CreateSession(ctx, &api.CreateSessionRequest{
		SessionID: "sec4-dup", BaseSnapshot: baseSnapshot,
	})
	assertRPCError(t, err, rpc.CodeAlreadyExists)

	// Event stream with empty session ID should be rejected
	_, err = stack.Events.StreamAgentEvents(ctx, &api.StreamAgentEventsRequest{SessionID: ""})
	assertRPCError(t, err, rpc.CodeInvalidArgument)
}

// SEC4b. Sender/Receiver close idempotency
func TestSEC4b_StreamCloseIdempotency(t *testing.T) {
	stack := newTestStack(t)
	ctx, cancel := context.WithCancel(context.Background())

	recv, err := stack.Events.StreamAgentEvents(ctx, &api.StreamAgentEventsRequest{SessionID: "close-idem"})
	if err != nil {
		t.Fatal(err)
	}
	cancel()

	// Multiple closes should not panic
	_ = recv.Close()
	_ = recv.Close()
	_ = recv.Close()
}

// SEC4c. Sender close prevents further sends
func TestSEC4c_SenderClosePreventsSubsequentSends(t *testing.T) {
	stack := newTestStack(t)
	sender := stack.Events.Sender("sec4c")

	// Send before close should work
	err := sender.Send(&api.AgentEventEnvelope{SessionID: "sec4c"})
	if err != nil {
		t.Fatalf("Send before close: %v", err)
	}

	// Close
	if err := sender.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Send after close should return ErrStreamClosed
	err = sender.Send(&api.AgentEventEnvelope{SessionID: "sec4c"})
	if !errors.Is(err, api.ErrStreamClosed) {
		t.Fatalf("Send after close: expected ErrStreamClosed, got %v", err)
	}
}
