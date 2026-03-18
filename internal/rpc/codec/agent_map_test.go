package codec

import (
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/agent"
	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	"github.com/dcosson/flex-agent-runtime/internal/ai"
)

func TestCopySessionMetrics(t *testing.T) {
	in := agentapi.SessionMetrics{
		TurnsStarted:      1,
		TurnsCompleted:    2,
		MessagesAppended:  3,
		ToolCallsStarted:  4,
		ToolCallsFinished: 5,
		Errors:            6,
	}
	out := CopySessionMetrics(in)
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("metrics mismatch: got %+v want %+v", out, in)
	}
}

func TestAgentEventRuntimeRoundTrip(t *testing.T) {
	exit := 0
	now := time.Unix(123, 0)
	apiEvt := agentapi.AgentEvent{
		Type:            agentapi.EventToolCompleted,
		SessionID:       "s1",
		DriverSessionID: "driver-1",
		State:           agentapi.AgentState(agent.StateIdle),
		Turn:            7,
		Message: &agentapi.AgentMessage{
			Turn:      7,
			Message:   &ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hello"}}},
			CreatedAt: now,
		},
		Assistant:  &ai.AssistantMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "done"}}},
		ToolName:   "bash",
		ToolCallID: "tc-1",
		ToolResult: &agentapi.AgentToolResult{
			Content:    []ai.ContentBlock{&ai.TextContent{Text: "ok"}},
			SnapshotID: "snap-1",
			ExitCode:   &exit,
			IsError:    false,
			Metadata:   map[string]any{"k": "v"},
		},
		Delta:          "chunk",
		ControlMessage: "ctrl",
		ErrorMessage:   "",
		At:             now,
		Metadata:       map[string]any{"meta": "value"},
	}

	runtimeEvt := ToRuntimeEvent(apiEvt)
	if runtimeEvt.Type != agent.EventToolCompleted || runtimeEvt.SessionID != "s1" {
		t.Fatalf("unexpected runtime event: %+v", runtimeEvt)
	}
	if runtimeEvt.ToolResult == nil || runtimeEvt.ToolResult.ExitCode == nil || *runtimeEvt.ToolResult.ExitCode != 0 {
		t.Fatalf("tool result not mapped: %+v", runtimeEvt.ToolResult)
	}

	out := FromRuntimeEvent(runtimeEvt)
	if out.Type != apiEvt.Type || out.SessionID != apiEvt.SessionID || out.ToolCallID != apiEvt.ToolCallID {
		t.Fatalf("round-trip mismatch: got %+v want %+v", out, apiEvt)
	}
	if out.Message == nil || out.Message.Turn != apiEvt.Message.Turn {
		t.Fatalf("message not preserved: %+v", out.Message)
	}
	if out.ToolResult == nil || out.ToolResult.SnapshotID != apiEvt.ToolResult.SnapshotID {
		t.Fatalf("tool result not preserved: %+v", out.ToolResult)
	}
}

func TestEventReceiverAdapters(t *testing.T) {
	base := &stubAgentEventReceiver{events: []*agentapi.AgentEvent{{
		Type:      agentapi.EventTurnStarted,
		SessionID: "inner-session",
		Turn:      1,
		At:        time.Unix(1, 0),
	}}}

	wrapped := WrapEventReceiver("outer-session", base)
	if wrapped == nil {
		t.Fatalf("WrapEventReceiver returned nil")
	}
	unwrapped := UnwrapEventReceiver(wrapped)
	if unwrapped == nil {
		t.Fatalf("UnwrapEventReceiver returned nil")
	}

	evt, err := unwrapped.Recv()
	if err != nil {
		t.Fatalf("Recv error: %v", err)
	}
	if evt == nil {
		t.Fatalf("expected event")
	}
	if evt.Type != agentapi.EventTurnStarted {
		t.Fatalf("type mismatch: %s", evt.Type)
	}
	if evt.SessionID != "inner-session" {
		t.Fatalf("session mismatch: got %q want %q", evt.SessionID, "inner-session")
	}

	_, err = unwrapped.Recv()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
	if err := unwrapped.Close(); err != nil {
		t.Fatalf("close error: %v", err)
	}
	if !base.closed {
		t.Fatalf("expected underlying receiver to be closed")
	}
}

func TestEventReceiverAdaptersNil(t *testing.T) {
	if WrapEventReceiver("s", nil) != nil {
		t.Fatalf("expected nil wrapped receiver")
	}
	if UnwrapEventReceiver(nil) != nil {
		t.Fatalf("expected nil unwrapped receiver")
	}
}

type stubAgentEventReceiver struct {
	events   []*agentapi.AgentEvent
	closed   bool
	closeErr error
}

func (s *stubAgentEventReceiver) Recv() (*agentapi.AgentEvent, error) {
	if len(s.events) == 0 {
		return nil, io.EOF
	}
	evt := s.events[0]
	s.events = s.events[1:]
	return evt, nil
}

func (s *stubAgentEventReceiver) Close() error {
	s.closed = true
	return s.closeErr
}
