package agent

import (
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/termmux/monitor"
)

func TestAdaptMonitorEvent_SessionStarted(t *testing.T) {
	evt := monitor.AgentEvent{
		Type:      monitor.EventSessionStarted,
		Timestamp: time.Now(),
		Data:      monitor.SessionStartedData{SessionID: "test-1"},
	}

	canonical := adaptMonitorEvent(evt, "session-abc")
	if canonical.Type != EventSessionStarted {
		t.Errorf("expected EventSessionStarted, got %s", canonical.Type)
	}
	if canonical.SessionID != "session-abc" {
		t.Errorf("expected session-abc, got %s", canonical.SessionID)
	}
}

func TestAdaptMonitorEvent_SessionEnded(t *testing.T) {
	evt := monitor.AgentEvent{
		Type:      monitor.EventSessionEnded,
		Timestamp: time.Now(),
		Data:      monitor.SessionEndedData{Reason: "completed"},
	}

	canonical := adaptMonitorEvent(evt, "s1")
	if canonical.Type != EventSessionEnded {
		t.Errorf("expected EventSessionEnded, got %s", canonical.Type)
	}
	if canonical.ControlMessage != "completed" {
		t.Errorf("expected reason 'completed', got %q", canonical.ControlMessage)
	}
}

func TestAdaptMonitorEvent_TurnCompleted(t *testing.T) {
	evt := monitor.AgentEvent{
		Type:      monitor.EventTurnCompleted,
		Timestamp: time.Now(),
		Data: monitor.TurnCompletedData{
			InputTokens:  1000,
			OutputTokens: 500,
			CostUSD:      0.05,
		},
	}

	canonical := adaptMonitorEvent(evt, "s1")
	if canonical.Type != EventTurnCompleted {
		t.Errorf("expected EventTurnCompleted, got %s", canonical.Type)
	}
	if canonical.Metadata == nil {
		t.Fatal("expected metadata")
	}
	if canonical.Metadata["input_tokens"] != int64(1000) {
		t.Errorf("expected input_tokens=1000, got %v", canonical.Metadata["input_tokens"])
	}
}

func TestAdaptMonitorEvent_ToolStarted(t *testing.T) {
	evt := monitor.AgentEvent{
		Type:      monitor.EventToolStarted,
		Timestamp: time.Now(),
		Data: monitor.ToolStartedData{
			ToolName: "Bash",
			CallID:   "call-1",
		},
	}

	canonical := adaptMonitorEvent(evt, "s1")
	if canonical.Type != EventToolStarted {
		t.Errorf("expected EventToolStarted, got %s", canonical.Type)
	}
	if canonical.ToolName != "Bash" {
		t.Errorf("expected Bash, got %s", canonical.ToolName)
	}
	if canonical.ToolCallID != "call-1" {
		t.Errorf("expected call-1, got %s", canonical.ToolCallID)
	}
}

func TestAdaptMonitorEvent_AgentMessage(t *testing.T) {
	evt := monitor.AgentEvent{
		Type:      monitor.EventAgentMessage,
		Timestamp: time.Now(),
		Data:      monitor.AgentMessageData{Content: "Hello world"},
	}

	canonical := adaptMonitorEvent(evt, "s1")
	if canonical.Type != EventAgentMessageCompleted {
		t.Errorf("expected EventAgentMessageCompleted, got %s", canonical.Type)
	}
	if canonical.Delta != "Hello world" {
		t.Errorf("expected content, got %q", canonical.Delta)
	}
}

func TestAdaptMonitorEvent_StateChange(t *testing.T) {
	tests := []struct {
		monitorState  monitor.State
		expectedState AgentState
	}{
		{monitor.StateInitialized, StateIdle},
		{monitor.StateActive, StateStreaming},
		{monitor.StateIdle, StateIdle},
		{monitor.StateExited, StateExited},
	}

	for _, tt := range tests {
		evt := monitor.AgentEvent{
			Type:      monitor.EventStateChange,
			Timestamp: time.Now(),
			Data: monitor.StateChangeData{
				State: tt.monitorState,
			},
		}

		canonical := adaptMonitorEvent(evt, "s1")
		if canonical.State != tt.expectedState {
			t.Errorf("monitor state %s: expected agent state %s, got %s",
				tt.monitorState, tt.expectedState, canonical.State)
		}
	}
}

func TestTermmuxDriverAdapter_Subscribe(t *testing.T) {
	// Test subscribe/unsubscribe without starting (unit test only)
	adapter := &TermmuxDriverAdapter{}

	received := make(chan AgentEvent, 1)
	unsub := adapter.Subscribe(func(evt AgentEvent) {
		received <- evt
	})

	// Notify
	adapter.notifySubscribers(AgentEvent{Type: EventSessionStarted})

	select {
	case evt := <-received:
		if evt.Type != EventSessionStarted {
			t.Errorf("expected EventSessionStarted, got %s", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber not called")
	}

	// Unsubscribe
	unsub()
	adapter.notifySubscribers(AgentEvent{Type: EventSessionEnded})

	select {
	case <-received:
		t.Fatal("subscriber called after unsubscribe")
	case <-time.After(50 * time.Millisecond):
		// OK — not called
	}
}

func TestTermmuxDriverAdapter_PanicIsolation(t *testing.T) {
	adapter := &TermmuxDriverAdapter{}

	// Subscriber that panics
	adapter.Subscribe(func(evt AgentEvent) {
		panic("test panic")
	})

	// Should not panic
	adapter.notifySubscribers(AgentEvent{Type: EventSessionStarted})
}
