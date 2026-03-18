package claudecode

import (
	"encoding/json"
	"testing"

	"github.com/dcosson/flex-agent-runtime/internal/termmux/monitor"
)

func TestHandleOtelLogs_APIRequest(t *testing.T) {
	eh := NewEventHandler()

	payload := makeOtelLogPayload("api_request", map[string]string{
		"session_id":    "test-session-1",
		"turn_id":       "turn-001",
		"input_tokens":  "1000",
		"output_tokens": "500",
		"cached_tokens": "200",
		"cost_usd":      "0.05",
	})

	events := eh.HandleOtelLogs(payload)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	evt := events[0]
	if evt.Type != monitor.EventTurnCompleted {
		t.Errorf("expected EventTurnCompleted, got %s", evt.Type)
	}

	data, ok := evt.Data.(monitor.TurnCompletedData)
	if !ok {
		t.Fatal("expected TurnCompletedData")
	}
	if data.TurnID != "turn-001" {
		t.Errorf("expected turn-001, got %s", data.TurnID)
	}
	if data.InputTokens != 1000 {
		t.Errorf("expected 1000 input tokens, got %d", data.InputTokens)
	}
	if data.OutputTokens != 500 {
		t.Errorf("expected 500 output tokens, got %d", data.OutputTokens)
	}

	// Session ID should be discovered
	if eh.SessionID() != "test-session-1" {
		t.Errorf("expected session ID test-session-1, got %s", eh.SessionID())
	}
}

func TestHandleOtelLogs_DuplicateTurn(t *testing.T) {
	eh := NewEventHandler()

	payload := makeOtelLogPayload("api_request", map[string]string{
		"turn_id": "turn-001",
	})

	events1 := eh.HandleOtelLogs(payload)
	events2 := eh.HandleOtelLogs(payload)

	if len(events1) != 1 {
		t.Fatalf("first call: expected 1 event, got %d", len(events1))
	}
	if len(events2) != 0 {
		t.Fatalf("second call: expected 0 events (deduplicated), got %d", len(events2))
	}
}

func TestHandleOtelLogs_SessionIDFilter(t *testing.T) {
	eh := NewEventHandler()

	// First event sets session ID
	payload1 := makeOtelLogPayload("api_request", map[string]string{
		"session_id": "session-A",
		"turn_id":    "turn-001",
	})
	events1 := eh.HandleOtelLogs(payload1)
	if len(events1) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events1))
	}

	// Event from different session should be filtered
	payload2 := makeOtelLogPayload("api_request", map[string]string{
		"session_id": "session-B",
		"turn_id":    "turn-002",
	})
	events2 := eh.HandleOtelLogs(payload2)
	if len(events2) != 0 {
		t.Fatalf("expected 0 events (filtered by session), got %d", len(events2))
	}
}

func TestHandleOtelLogs_ToolResult(t *testing.T) {
	eh := NewEventHandler()

	payload := makeOtelLogPayload("tool_result", map[string]string{
		"tool_name": "Read",
		"call_id":   "call-001",
		"success":   "true",
	})

	events := eh.HandleOtelLogs(payload)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Type != monitor.EventToolCompleted {
		t.Errorf("expected EventToolCompleted, got %s", events[0].Type)
	}
}

func TestHandleHookEvent_PrePostToolUse(t *testing.T) {
	eh := NewEventHandler()

	// PreToolUse
	prePayload, _ := json.Marshal(hookToolUse{ToolName: "Bash", CallID: "call-1"})
	events := eh.HandleHookEvent("PreToolUse", prePayload)
	if len(events) != 1 || events[0].Type != monitor.EventToolStarted {
		t.Fatalf("PreToolUse: expected EventToolStarted, got %v", events)
	}
	data := events[0].Data.(monitor.ToolStartedData)
	if data.ToolName != "Bash" {
		t.Errorf("expected Bash, got %s", data.ToolName)
	}

	// PostToolUse should calculate duration
	postPayload, _ := json.Marshal(hookToolUse{ToolName: "Bash", CallID: "call-1"})
	events = eh.HandleHookEvent("PostToolUse", postPayload)
	if len(events) != 1 || events[0].Type != monitor.EventToolCompleted {
		t.Fatalf("PostToolUse: expected EventToolCompleted, got %v", events)
	}
	completed := events[0].Data.(monitor.ToolCompletedData)
	if completed.ToolName != "Bash" {
		t.Errorf("expected Bash, got %s", completed.ToolName)
	}
	if !completed.Success {
		t.Error("expected success=true")
	}
}

func TestHandleHookEvent_PermissionFlow(t *testing.T) {
	eh := NewEventHandler()

	// PermissionRequest
	reqPayload, _ := json.Marshal(hookPermission{ToolName: "Write", CallID: "call-2"})
	events := eh.HandleHookEvent("PermissionRequest", reqPayload)
	if len(events) != 1 || events[0].Type != monitor.EventApprovalRequested {
		t.Fatalf("expected EventApprovalRequested, got %v", events)
	}

	// permission_decision granted
	decPayload, _ := json.Marshal(hookPermissionDecision{Granted: true})
	events = eh.HandleHookEvent("permission_decision", decPayload)
	if len(events) != 1 || events[0].Type != monitor.EventPermissionGranted {
		t.Fatalf("expected EventPermissionGranted, got %v", events)
	}

	// permission_decision denied
	decPayload, _ = json.Marshal(hookPermissionDecision{Granted: false})
	events = eh.HandleHookEvent("permission_decision", decPayload)
	if len(events) != 1 || events[0].Type != monitor.EventPermissionDenied {
		t.Fatalf("expected EventPermissionDenied, got %v", events)
	}
}

func TestHandleHookEvent_Compaction(t *testing.T) {
	eh := NewEventHandler()

	events := eh.HandleHookEvent("PreCompact", json.RawMessage("{}"))
	if len(events) != 1 || events[0].Type != monitor.EventCompactionStarted {
		t.Fatalf("expected EventCompactionStarted, got %v", events)
	}
}

func TestHandleSessionLogLine_AssistantMessage(t *testing.T) {
	eh := NewEventHandler()

	line := `{"role":"assistant","content":"Here is the answer."}`
	events := eh.HandleSessionLogLine([]byte(line))
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != monitor.EventAgentMessage {
		t.Errorf("expected EventAgentMessage, got %s", events[0].Type)
	}
	data := events[0].Data.(monitor.AgentMessageData)
	if data.Content != "Here is the answer." {
		t.Errorf("expected content 'Here is the answer.', got %q", data.Content)
	}
}

func TestHandleSessionLogLine_UserMessage_Ignored(t *testing.T) {
	eh := NewEventHandler()

	line := `{"role":"user","content":"hello"}`
	events := eh.HandleSessionLogLine([]byte(line))
	if len(events) != 0 {
		t.Fatalf("expected 0 events for user message, got %d", len(events))
	}
}

func TestHandleSessionLogLine_EmptyContent_Ignored(t *testing.T) {
	eh := NewEventHandler()

	line := `{"role":"assistant","content":""}`
	events := eh.HandleSessionLogLine([]byte(line))
	if len(events) != 0 {
		t.Fatalf("expected 0 events for empty content, got %d", len(events))
	}
}

func TestHandleHookEvent_UnknownEvent(t *testing.T) {
	eh := NewEventHandler()
	events := eh.HandleHookEvent("UnknownEvent", json.RawMessage("{}"))
	if len(events) != 0 {
		t.Fatalf("expected 0 events for unknown hook, got %d", len(events))
	}
}

// Helper to create OTEL log payload
func makeOtelLogPayload(eventType string, attrs map[string]string) json.RawMessage {
	kvs := make([]otelKeyValue, 0, len(attrs)+1)
	kvs = append(kvs, otelKeyValue{
		Key:   "event_type",
		Value: otelValue{StringValue: eventType},
	})
	for k, v := range attrs {
		kvs = append(kvs, otelKeyValue{
			Key:   k,
			Value: otelValue{StringValue: v},
		})
	}

	batch := otelLogBatch{
		ResourceLogs: []otelResourceLog{{
			ScopeLogs: []otelScopeLog{{
				LogRecords: []otelLogRecord{{
					Attributes: kvs,
				}},
			}},
		}},
	}

	data, _ := json.Marshal(batch)
	return data
}
