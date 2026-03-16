package rpctest

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"flex-agent-runtime/internal/agent"
	"flex-agent-runtime/internal/ai"
	"flex-agent-runtime/internal/rpc"
	"flex-agent-runtime/internal/rpc/api"
	"flex-agent-runtime/internal/rpc/codec"
	"flex-agent-runtime/internal/sandbox"
	"flex-agent-runtime/internal/tools"

	"pgregory.net/rapid"
)

// P1. DTO Round-trip Fidelity
func TestPropertyDTORoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate random CreateSessionRequest
		baseSnap := rapid.StringMatching(`^[a-z]+/[a-z]+@v[0-9]+$`).Draw(t, "baseSnap")
		sessID := rapid.StringMatching(`^sess-[a-z0-9]{4,8}$`).Draw(t, "sessID")
		quota := int64(rapid.IntRange(0, 10000).Draw(t, "quota"))

		apiReq := &api.CreateSessionRequest{
			BaseSnapshot: baseSnap,
			SessionID:    sessID,
			Quota:        quota,
			Labels:       map[string]string{"env": "test"},
		}
		domReq := codec.ToCreateSessionRequest(apiReq)

		if domReq.BaseSnapshot != baseSnap {
			t.Fatalf("BaseSnapshot: %q != %q", domReq.BaseSnapshot, baseSnap)
		}
		if domReq.SessionID != sessID {
			t.Fatalf("SessionID: %q != %q", domReq.SessionID, sessID)
		}
		if domReq.Quota != quota {
			t.Fatalf("Quota: %d != %d", domReq.Quota, quota)
		}
	})
}

// P1b. ContentBlock round-trip
func TestPropertyContentBlockRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		blockType := rapid.IntRange(0, 3).Draw(t, "blockType")
		var block ai.ContentBlock
		switch blockType {
		case 0:
			text := rapid.String().Draw(t, "text")
			sig := rapid.String().Draw(t, "sig")
			block = &ai.TextContent{Text: text, TextSignature: sig}
		case 1:
			thinking := rapid.String().Draw(t, "thinking")
			block = &ai.ThinkingContent{Thinking: thinking, Redacted: rapid.Bool().Draw(t, "redacted")}
		case 2:
			block = &ai.ImageContent{Data: rapid.String().Draw(t, "data"), MimeType: "image/png"}
		case 3:
			block = &ai.ToolCall{
				ID:        rapid.StringMatching(`^tc-[0-9]+$`).Draw(t, "id"),
				Name:      rapid.StringMatching(`^[a-z_]+$`).Draw(t, "name"),
				Arguments: map[string]any{"key": "val"},
			}
		}

		// To API and back
		apiBlocks := codec.ToAPIContentBlocks([]ai.ContentBlock{block})
		roundTripped := codec.FromAPIContentBlocks(apiBlocks)

		if len(roundTripped) != 1 {
			t.Fatalf("expected 1 block, got %d", len(roundTripped))
		}

		// Type-specific assertions
		switch orig := block.(type) {
		case *ai.TextContent:
			rt, ok := roundTripped[0].(*ai.TextContent)
			if !ok {
				t.Fatalf("expected TextContent, got %T", roundTripped[0])
			}
			if rt.Text != orig.Text || rt.TextSignature != orig.TextSignature {
				t.Fatalf("text mismatch: %q/%q != %q/%q", rt.Text, rt.TextSignature, orig.Text, orig.TextSignature)
			}
		case *ai.ThinkingContent:
			rt, ok := roundTripped[0].(*ai.ThinkingContent)
			if !ok {
				t.Fatalf("expected ThinkingContent, got %T", roundTripped[0])
			}
			if rt.Thinking != orig.Thinking || rt.Redacted != orig.Redacted {
				t.Fatalf("thinking mismatch")
			}
		case *ai.ImageContent:
			rt, ok := roundTripped[0].(*ai.ImageContent)
			if !ok {
				t.Fatalf("expected ImageContent, got %T", roundTripped[0])
			}
			if rt.Data != orig.Data || rt.MimeType != orig.MimeType {
				t.Fatalf("image mismatch")
			}
		case *ai.ToolCall:
			rt, ok := roundTripped[0].(*ai.ToolCall)
			if !ok {
				t.Fatalf("expected ToolCall, got %T", roundTripped[0])
			}
			if rt.ID != orig.ID || rt.Name != orig.Name {
				t.Fatalf("tool call mismatch: %q/%q != %q/%q", rt.ID, rt.Name, orig.ID, orig.Name)
			}
		}
	})
}

// P1c. ExecuteToolRequest round-trip
func TestPropertyExecuteToolRequestRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		sessID := rapid.StringMatching(`^sess-[a-z0-9]+$`).Draw(t, "sessID")
		toolCallID := rapid.StringMatching(`^tc-[0-9]+$`).Draw(t, "tcID")
		toolName := rapid.SampledFrom([]string{"bash", "read_file", "grep", "glob"}).Draw(t, "tool")
		hasResources := rapid.Bool().Draw(t, "hasResources")

		domReq := tools.ToolRequest{
			ToolCallID: toolCallID,
			ToolName:   toolName,
			Params:     map[string]any{"key": "val"},
		}
		if hasResources {
			domReq.Resources = &tools.ResourceSpec{CPUs: 2, MemMB: 512}
		}

		apiReq := codec.ToToolRequest(sessID, domReq)
		if apiReq.SessionID != sessID {
			t.Fatalf("SessionID: %q != %q", apiReq.SessionID, sessID)
		}
		if apiReq.ToolCallID != toolCallID {
			t.Fatalf("ToolCallID: %q != %q", apiReq.ToolCallID, toolCallID)
		}
		if apiReq.ToolName != toolName {
			t.Fatalf("ToolName: %q != %q", apiReq.ToolName, toolName)
		}

		// Round-trip back to domain
		domBack := codec.ToExecuteToolRequest(apiReq)
		if domBack.SessionID != sessID || domBack.ToolName != toolName || domBack.ToolCallID != toolCallID {
			t.Fatalf("round-trip mismatch")
		}
		if hasResources && domBack.Resources == nil {
			t.Fatal("resources lost in round-trip")
		}
	})
}

// P2. Error Mapping Stability
func TestPropertyErrorMappingStability(t *testing.T) {
	domainErrors := []struct {
		err  error
		code rpc.Code
	}{
		{sandbox.ErrSessionNotFound, rpc.CodeNotFound},
		{sandbox.ErrSessionExists, rpc.CodeAlreadyExists},
		{sandbox.ErrSessionPaused, rpc.CodeFailedPrecondition},
		{sandbox.ErrSessionDestroying, rpc.CodeFailedPrecondition},
		{sandbox.ErrInvalidState, rpc.CodeFailedPrecondition},
		{sandbox.ErrRollbackInProgress, rpc.CodeFailedPrecondition},
		{sandbox.ErrMaxSessionsReached, rpc.CodeFailedPrecondition},
		{context.Canceled, rpc.CodeCanceled},
		{context.DeadlineExceeded, rpc.CodeDeadlineExceeded},
	}

	rapid.Check(t, func(t *rapid.T) {
		idx := rapid.IntRange(0, len(domainErrors)-1).Draw(t, "errIdx")
		tc := domainErrors[idx]

		// Map the same error multiple times — should be deterministic
		mapped1 := rpc.MapError(tc.err)
		mapped2 := rpc.MapError(tc.err)

		rpcErr1, ok1 := mapped1.(*rpc.RPCError)
		rpcErr2, ok2 := mapped2.(*rpc.RPCError)
		if !ok1 || !ok2 {
			t.Fatalf("MapError didn't return RPCError")
		}
		if rpcErr1.Code != rpcErr2.Code {
			t.Fatalf("non-deterministic mapping: %s vs %s", rpcErr1.Code, rpcErr2.Code)
		}
		if rpcErr1.Code != tc.code {
			t.Fatalf("MapError(%v) = %s, want %s", tc.err, rpcErr1.Code, tc.code)
		}
	})
}

// P2b. MapError preserves cause chain
func TestPropertyErrorMappingPreservesCause(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		msg := rapid.String().Draw(t, "msg")
		wrapped := fmt.Errorf("%s: %w", msg, sandbox.ErrSessionNotFound)
		mapped := rpc.MapError(wrapped)
		rpcErr, ok := mapped.(*rpc.RPCError)
		if !ok {
			t.Fatal("expected RPCError")
		}
		if rpcErr.Code != rpc.CodeNotFound {
			t.Fatalf("code = %s, want not_found", rpcErr.Code)
		}
		if rpcErr.Cause == nil {
			t.Fatal("cause lost")
		}
	})
}

// P3. Retry Policy Soundness
func TestPropertyRetryPolicySoundness(t *testing.T) {
	retryableMethods := []string{"GetSession", "ListSnapshots", "RollbackSession", "DestroySession", "ExecuteTool"}
	nonRetryableMethods := []string{"CreateSession", "PauseSession", "ResumeSession", "CreateSnapshot", "TurnComplete"}

	rapid.Check(t, func(t *rapid.T) {
		// Retryable methods must be classified as retryable
		retryIdx := rapid.IntRange(0, len(retryableMethods)-1).Draw(t, "retryIdx")
		if !rpc.IsRetryableMethod(retryableMethods[retryIdx]) {
			t.Fatalf("method %q should be retryable", retryableMethods[retryIdx])
		}

		// Non-retryable methods must NOT be retryable
		nonRetryIdx := rapid.IntRange(0, len(nonRetryableMethods)-1).Draw(t, "nonRetryIdx")
		if rpc.IsRetryableMethod(nonRetryableMethods[nonRetryIdx]) {
			t.Fatalf("method %q should NOT be retryable", nonRetryableMethods[nonRetryIdx])
		}
	})
}

// P3b. Backoff is monotonically non-decreasing and bounded
func TestPropertyBackoffBounded(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		base := rapid.IntRange(10, 500).Draw(t, "baseMs")
		max := rapid.IntRange(base, 10000).Draw(t, "maxMs")
		policy := rpc.RetryPolicy{MaxAttempts: 5, BaseDelayMs: base, MaxDelayMs: max}

		prev := 0
		for attempt := 1; attempt <= 10; attempt++ {
			delay := rpc.BackoffDelayMs(policy, attempt)
			if delay < 0 {
				t.Fatalf("negative delay at attempt %d: %d", attempt, delay)
			}
			if delay > max {
				t.Fatalf("delay %d exceeds max %d at attempt %d", delay, max, attempt)
			}
			if delay < prev {
				t.Fatalf("non-monotonic: attempt %d delay %d < prev %d", attempt, delay, prev)
			}
			prev = delay
		}
	})
}

// P3c. Retryable/non-retryable error classification
func TestPropertyRetryableErrorClassification(t *testing.T) {
	retryableCodes := []rpc.Code{rpc.CodeUnavailable, rpc.CodeDeadlineExceeded}
	nonRetryableCodes := []rpc.Code{
		rpc.CodeNotFound, rpc.CodeAlreadyExists, rpc.CodeFailedPrecondition,
		rpc.CodePermissionDenied, rpc.CodeResourceExhausted, rpc.CodeInvalidArgument,
		rpc.CodeInternal, rpc.CodeCanceled,
	}

	rapid.Check(t, func(t *rapid.T) {
		retryIdx := rapid.IntRange(0, len(retryableCodes)-1).Draw(t, "retryCodeIdx")
		retryErr := rpc.NewRPCError(retryableCodes[retryIdx], "test", nil)
		if !rpc.IsRetryableError(retryErr) {
			t.Fatalf("code %s should be retryable", retryableCodes[retryIdx])
		}

		nonRetryIdx := rapid.IntRange(0, len(nonRetryableCodes)-1).Draw(t, "nonRetryCodeIdx")
		nonRetryErr := rpc.NewRPCError(nonRetryableCodes[nonRetryIdx], "test", nil)
		if rpc.IsRetryableError(nonRetryErr) {
			t.Fatalf("code %s should NOT be retryable", nonRetryableCodes[nonRetryIdx])
		}
	})
}

// P4. Event Stream Ordering
func TestPropertyEventStreamOrdering(t *testing.T) {
	// Create stack outside rapid.Check since newTestStack needs testing.T
	stack := newTestStack(t)
	rapid.Check(t, func(t *rapid.T) {
		eventSrv := stack.Events
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sessID := fmt.Sprintf("ordering-%d", rapid.IntRange(0, 10000).Draw(t, "sessID"))
		n := rapid.IntRange(1, 50).Draw(t, "eventCount")

		recv, err := eventSrv.StreamAgentEvents(ctx, &api.StreamAgentEventsRequest{SessionID: sessID})
		if err != nil {
			t.Fatal(err)
		}

		// Publish events with sequence numbers stored in Metadata
		eventTypes := []agent.AgentEventType{
			agent.EventToolStarted, agent.EventToolCompleted, agent.EventTurnCompleted,
		}
		for i := 0; i < n; i++ {
			typeIdx := i % len(eventTypes)
			eventSrv.Publish(sessID, agent.AgentEvent{
				Type:      eventTypes[typeIdx],
				SessionID: sessID,
				At:        time.Now(),
				Metadata:  map[string]any{"seq": i},
			})
		}

		// Receive and verify order is preserved
		for i := 0; i < n; i++ {
			evt, err := recv.Recv()
			if err != nil {
				t.Fatalf("Recv %d: %v", i, err)
			}
			seq, ok := evt.Event.Metadata["seq"].(int)
			if !ok {
				t.Fatalf("event %d: missing seq in metadata", i)
			}
			if seq != i {
				t.Fatalf("event %d: seq = %d, want %d", i, seq, i)
			}
			if evt.SessionID != sessID {
				t.Fatalf("event %d: sessionID = %q, want %q", i, evt.SessionID, sessID)
			}
		}
	})
}

// P5. Session ID Authority
var p5SessionCounter atomic.Uint64

func TestPropertySessionIDAuthority(t *testing.T) {
	stack := newTestStack(t)
	rapid.Check(t, func(t *rapid.T) {
		ctx := context.Background()

		// Generate a runtime session ID
		runtimeID := fmt.Sprintf("runtime-%d", p5SessionCounter.Add(1))

		// Generate a distinct "driver-native" ID that should never appear
		driverID := fmt.Sprintf("driver-native-%d", p5SessionCounter.Add(1))
		_ = driverID // used in assertions below

		// Create session with runtime ID
		resp, err := stack.Server.CreateSession(ctx, &api.CreateSessionRequest{
			SessionID:    runtimeID,
			BaseSnapshot: baseSnapshot,
		})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Session.ID != runtimeID {
			t.Fatalf("session ID = %q, want %q", resp.Session.ID, runtimeID)
		}

		// GetSession returns the runtime ID
		getResp, err := stack.Server.GetSession(ctx, &api.GetSessionRequest{SessionID: runtimeID})
		if err != nil {
			t.Fatal(err)
		}
		if getResp.Session.ID != runtimeID {
			t.Fatalf("GetSession ID = %q, want %q", getResp.Session.ID, runtimeID)
		}

		// ExecuteTool response carries the runtime ID
		toolResp, err := stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
			SessionID:  runtimeID,
			ToolCallID: fmt.Sprintf("tc-%d", p5SessionCounter.Add(1)),
			ToolName:   "bash",
			Params:     map[string]any{"cmd": "echo hi"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if toolResp.SessionID != runtimeID {
			t.Fatalf("ExecuteTool response sessionID = %q, want %q", toolResp.SessionID, runtimeID)
		}

		// Event stream carries the runtime ID
		recv, err := stack.Events.StreamAgentEvents(ctx, &api.StreamAgentEventsRequest{SessionID: runtimeID})
		if err != nil {
			t.Fatal(err)
		}
		stack.Events.Publish(runtimeID, agent.AgentEvent{Type: agent.EventToolStarted, SessionID: runtimeID})
		evt, err := recv.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if evt.SessionID != runtimeID {
			t.Fatalf("event sessionID = %q, want %q", evt.SessionID, runtimeID)
		}

		// Driver ID should not appear anywhere
		_, getErr := stack.Server.GetSession(ctx, &api.GetSessionRequest{SessionID: driverID})
		if getErr == nil {
			t.Fatal("expected error for driver ID lookup")
		}
		var rpcErr *rpc.RPCError
		if !errors.As(getErr, &rpcErr) || rpcErr.Code != rpc.CodeNotFound {
			t.Fatalf("expected CodeNotFound for driver ID, got: %v", getErr)
		}

		// Clean up session to avoid accumulation across rapid iterations
		stack.Server.DestroySession(ctx, &api.DestroySessionRequest{SessionID: runtimeID})
	})
}
