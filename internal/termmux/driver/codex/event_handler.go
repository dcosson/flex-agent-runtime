// Package codex implements the TermmuxDriverAdapter for the Codex CLI.
package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"flex-agent-runtime/internal/termmux/monitor"
)

const (
	// Debouncing parameters from plan §4.5
	idleDelay            = 200 * time.Millisecond
	interruptSuppression = 500 * time.Millisecond
)

// EventHandler processes OTEL events from Codex and normalizes them
// into AgentEvents, with debouncing for out-of-order events.
type EventHandler struct {
	mu sync.Mutex

	// Debouncing state
	lastEventTime time.Time
	interruptedAt time.Time
	idleTimer     *time.Timer

	// Token baseline tracking for delta calculation
	baselineInputTokens  int64
	baselineOutputTokens int64

	// Callback for idle transition
	onIdle func()
}

// NewEventHandler creates a new Codex event handler.
func NewEventHandler() *EventHandler {
	return &EventHandler{}
}

// SetIdleCallback sets the function called when the idle debounce timer fires.
func (eh *EventHandler) SetIdleCallback(fn func()) {
	eh.mu.Lock()
	defer eh.mu.Unlock()
	eh.onIdle = fn
}

// HandleOtelLogs processes OTEL log records from Codex.
func (eh *EventHandler) HandleOtelLogs(payload json.RawMessage) []monitor.AgentEvent {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic recovered in codex HandleOtelLogs: %v\n%s\n", r, debug.Stack())
		}
	}()

	var batch otelLogBatch
	if err := json.Unmarshal(payload, &batch); err != nil {
		return nil
	}

	var events []monitor.AgentEvent

	for _, rl := range batch.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				evts := eh.processLogRecord(lr)
				events = append(events, evts...)
			}
		}
	}

	// Reset idle timer on any activity
	if len(events) > 0 {
		eh.resetIdleTimer()
	}

	return events
}

// HandleOtelTraces processes OTEL traces from Codex.
func (eh *EventHandler) HandleOtelTraces(payload json.RawMessage) []monitor.AgentEvent {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic recovered in codex HandleOtelTraces: %v\n%s\n", r, debug.Stack())
		}
	}()

	var batch otelTraceBatch
	if err := json.Unmarshal(payload, &batch); err != nil {
		return nil
	}

	var events []monitor.AgentEvent

	for _, rs := range batch.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, span := range ss.Spans {
				evts := eh.processSpan(span)
				events = append(events, evts...)
			}
		}
	}

	if len(events) > 0 {
		eh.resetIdleTimer()
	}

	return events
}

// HandleOtelMetrics processes OTEL metrics from Codex.
func (eh *EventHandler) HandleOtelMetrics(_ json.RawMessage) []monitor.AgentEvent {
	return nil
}

// HandleInterrupt records an interrupt event and starts the suppression window.
func (eh *EventHandler) HandleInterrupt() {
	eh.mu.Lock()
	defer eh.mu.Unlock()
	eh.interruptedAt = time.Now()
}

// isInInterruptSuppression checks if we're within the interrupt suppression window.
func (eh *EventHandler) isInInterruptSuppression() bool {
	eh.mu.Lock()
	defer eh.mu.Unlock()
	if eh.interruptedAt.IsZero() {
		return false
	}
	return time.Since(eh.interruptedAt) < interruptSuppression
}

func (eh *EventHandler) processLogRecord(lr otelLogRecord) []monitor.AgentEvent {
	// Suppress events during interrupt window
	if eh.isInInterruptSuppression() {
		return nil
	}

	attrs := make(map[string]string)
	for _, kv := range lr.Attributes {
		if kv.Value.StringValue != "" {
			attrs[kv.Key] = kv.Value.StringValue
		}
	}

	eventType := attrs["event_type"]
	if eventType == "" && lr.Body.StringValue != "" {
		eventType = lr.Body.StringValue
	}

	now := time.Now()

	switch eventType {
	case "turn_complete", "api_request":
		return eh.handleTurnComplete(attrs, now)
	case "tool_start":
		return []monitor.AgentEvent{{
			Type:      monitor.EventToolStarted,
			Timestamp: now,
			Data: monitor.ToolStartedData{
				ToolName: attrs["tool_name"],
				CallID:   attrs["call_id"],
			},
		}}
	case "tool_complete":
		return []monitor.AgentEvent{{
			Type:      monitor.EventToolCompleted,
			Timestamp: now,
			Data: monitor.ToolCompletedData{
				ToolName: attrs["tool_name"],
				CallID:   attrs["call_id"],
				Success:  attrs["success"] != "false",
			},
		}}
	}

	return nil
}

func (eh *EventHandler) processSpan(span otelSpan) []monitor.AgentEvent {
	if eh.isInInterruptSuppression() {
		return nil
	}

	attrs := make(map[string]string)
	for _, kv := range span.Attributes {
		if kv.Value.StringValue != "" {
			attrs[kv.Key] = kv.Value.StringValue
		}
	}

	now := time.Now()

	switch span.Name {
	case "tool_execution":
		if span.Status.Code == "OK" || span.Status.Code == "" {
			return []monitor.AgentEvent{{
				Type:      monitor.EventToolCompleted,
				Timestamp: now,
				Data: monitor.ToolCompletedData{
					ToolName: attrs["tool_name"],
					CallID:   attrs["call_id"],
					Success:  true,
				},
			}}
		}
	}

	return nil
}

// handleTurnComplete processes turn completion with delta token calculation.
func (eh *EventHandler) handleTurnComplete(attrs map[string]string, now time.Time) []monitor.AgentEvent {
	var inputTokens, outputTokens int64
	fmt.Sscanf(attrs["input_tokens"], "%d", &inputTokens)
	fmt.Sscanf(attrs["output_tokens"], "%d", &outputTokens)

	// Calculate deltas from baseline
	eh.mu.Lock()
	deltaInput := inputTokens - eh.baselineInputTokens
	deltaOutput := outputTokens - eh.baselineOutputTokens
	eh.baselineInputTokens = inputTokens
	eh.baselineOutputTokens = outputTokens
	eh.mu.Unlock()

	if deltaInput < 0 {
		deltaInput = inputTokens
	}
	if deltaOutput < 0 {
		deltaOutput = outputTokens
	}

	return []monitor.AgentEvent{{
		Type:      monitor.EventTurnCompleted,
		Timestamp: now,
		Data: monitor.TurnCompletedData{
			InputTokens:  deltaInput,
			OutputTokens: deltaOutput,
		},
	}}
}

func (eh *EventHandler) resetIdleTimer() {
	eh.mu.Lock()
	defer eh.mu.Unlock()

	eh.lastEventTime = time.Now()

	if eh.idleTimer != nil {
		eh.idleTimer.Stop()
	}

	eh.idleTimer = time.AfterFunc(idleDelay, func() {
		eh.mu.Lock()
		fn := eh.onIdle
		eh.mu.Unlock()
		if fn != nil {
			fn()
		}
	})
}

// Stop cleans up the event handler.
func (eh *EventHandler) Stop() {
	eh.mu.Lock()
	defer eh.mu.Unlock()
	if eh.idleTimer != nil {
		eh.idleTimer.Stop()
		eh.idleTimer = nil
	}
}

// OTEL types shared with Claude Code (could be extracted to a shared package)

type otelLogBatch struct {
	ResourceLogs []otelResourceLog `json:"resourceLogs"`
}

type otelResourceLog struct {
	ScopeLogs []otelScopeLog `json:"scopeLogs"`
}

type otelScopeLog struct {
	LogRecords []otelLogRecord `json:"logRecords"`
}

type otelLogRecord struct {
	Body       otelValue      `json:"body"`
	Attributes []otelKeyValue `json:"attributes"`
}

type otelTraceBatch struct {
	ResourceSpans []otelResourceSpan `json:"resourceSpans"`
}

type otelResourceSpan struct {
	ScopeSpans []otelScopeSpan `json:"scopeSpans"`
}

type otelScopeSpan struct {
	Spans []otelSpan `json:"spans"`
}

type otelSpan struct {
	Name       string         `json:"name"`
	Status     otelStatus     `json:"status"`
	Attributes []otelKeyValue `json:"attributes"`
}

type otelStatus struct {
	Code string `json:"code"`
}

type otelKeyValue struct {
	Key   string    `json:"key"`
	Value otelValue `json:"value"`
}

type otelValue struct {
	StringValue string `json:"stringValue"`
}
