package codex

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/termmux/monitor"
)

func TestHandleOtelLogs_TurnComplete(t *testing.T) {
	eh := NewEventHandler()

	payload := makeOtelLogPayload("turn_complete", map[string]string{
		"input_tokens":  "1000",
		"output_tokens": "500",
	})

	events := eh.HandleOtelLogs(payload)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Type != monitor.EventTurnCompleted {
		t.Errorf("expected EventTurnCompleted, got %s", events[0].Type)
	}

	data := events[0].Data.(monitor.TurnCompletedData)
	// First call: delta = absolute values since baseline is 0
	if data.InputTokens != 1000 {
		t.Errorf("expected 1000 input tokens, got %d", data.InputTokens)
	}
	if data.OutputTokens != 500 {
		t.Errorf("expected 500 output tokens, got %d", data.OutputTokens)
	}
}

func TestHandleOtelLogs_DeltaTokenCalculation(t *testing.T) {
	eh := NewEventHandler()

	// First turn
	payload1 := makeOtelLogPayload("turn_complete", map[string]string{
		"input_tokens":  "1000",
		"output_tokens": "500",
	})
	eh.HandleOtelLogs(payload1)

	// Second turn — delta calculation
	payload2 := makeOtelLogPayload("turn_complete", map[string]string{
		"input_tokens":  "1500",
		"output_tokens": "700",
	})
	events := eh.HandleOtelLogs(payload2)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	data := events[0].Data.(monitor.TurnCompletedData)
	if data.InputTokens != 500 { // 1500 - 1000
		t.Errorf("expected delta 500 input tokens, got %d", data.InputTokens)
	}
	if data.OutputTokens != 200 { // 700 - 500
		t.Errorf("expected delta 200 output tokens, got %d", data.OutputTokens)
	}
}

func TestHandleOtelLogs_ToolEvents(t *testing.T) {
	eh := NewEventHandler()

	// Tool start
	payload := makeOtelLogPayload("tool_start", map[string]string{
		"tool_name": "bash",
		"call_id":   "call-1",
	})
	events := eh.HandleOtelLogs(payload)
	if len(events) != 1 || events[0].Type != monitor.EventToolStarted {
		t.Fatalf("expected EventToolStarted, got %v", events)
	}

	// Tool complete
	payload = makeOtelLogPayload("tool_complete", map[string]string{
		"tool_name": "bash",
		"call_id":   "call-1",
		"success":   "true",
	})
	events = eh.HandleOtelLogs(payload)
	if len(events) != 1 || events[0].Type != monitor.EventToolCompleted {
		t.Fatalf("expected EventToolCompleted, got %v", events)
	}
}

func TestHandleOtelTraces_ToolExecution(t *testing.T) {
	eh := NewEventHandler()

	batch := otelTraceBatch{
		ResourceSpans: []otelResourceSpan{{
			ScopeSpans: []otelScopeSpan{{
				Spans: []otelSpan{{
					Name:   "tool_execution",
					Status: otelStatus{Code: "OK"},
					Attributes: []otelKeyValue{
						{Key: "tool_name", Value: otelValue{StringValue: "write"}},
						{Key: "call_id", Value: otelValue{StringValue: "call-2"}},
					},
				}},
			}},
		}},
	}
	payload, _ := json.Marshal(batch)

	events := eh.HandleOtelTraces(payload)
	if len(events) != 1 || events[0].Type != monitor.EventToolCompleted {
		t.Fatalf("expected EventToolCompleted from trace, got %v", events)
	}
}

func TestInterruptSuppression(t *testing.T) {
	eh := NewEventHandler()

	// Trigger interrupt
	eh.HandleInterrupt()

	// Events during suppression window should be dropped
	payload := makeOtelLogPayload("turn_complete", map[string]string{
		"input_tokens":  "100",
		"output_tokens": "50",
	})
	events := eh.HandleOtelLogs(payload)
	if len(events) != 0 {
		t.Fatalf("expected 0 events during interrupt suppression, got %d", len(events))
	}

	// After suppression window, events should work again
	// Can't easily test time-based suppression in unit test without mocking time,
	// but we can verify the mechanism is in place by checking the flag is set
	if eh.isInInterruptSuppression() != true {
		t.Error("expected to be in interrupt suppression window")
	}
}

func TestIdleCallback(t *testing.T) {
	eh := NewEventHandler()

	called := make(chan struct{}, 1)
	eh.SetIdleCallback(func() {
		called <- struct{}{}
	})

	// Trigger activity to start idle timer
	payload := makeOtelLogPayload("turn_complete", map[string]string{
		"input_tokens":  "100",
		"output_tokens": "50",
	})
	eh.HandleOtelLogs(payload)

	// Wait for idle callback (200ms + some margin)
	select {
	case <-called:
		// OK
	case <-time.After(500 * time.Millisecond):
		t.Error("idle callback not called within timeout")
	}

	eh.Stop()
}

func TestStop(t *testing.T) {
	eh := NewEventHandler()
	eh.SetIdleCallback(func() {})

	// Trigger activity to start idle timer
	payload := makeOtelLogPayload("turn_complete", map[string]string{})
	eh.HandleOtelLogs(payload)

	// Stop should clean up timer
	eh.Stop()

	// Verify no panic
}

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
