// Package claudecode implements the TermmuxDriverAdapter for Claude Code CLI.
package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/termmux/monitor"
)

// EventHandler processes three event sources (OTEL, hooks, session log)
// and normalizes them into AgentEvents for Claude Code sessions.
type EventHandler struct {
	mu        sync.Mutex
	sessionID string // Discovered from OTEL log attributes

	// Tracking for correlation
	pendingTools map[string]time.Time // callID → start time
	lastTurnID   string
}

// NewEventHandler creates a new Claude Code event handler.
func NewEventHandler() *EventHandler {
	return &EventHandler{
		pendingTools: make(map[string]time.Time),
	}
}

// SessionID returns the discovered session ID, or empty if not yet known.
func (eh *EventHandler) SessionID() string {
	eh.mu.Lock()
	defer eh.mu.Unlock()
	return eh.sessionID
}

// HandleOtelLogs processes OTEL log records from Claude Code.
// Claude Code emits logs for: api_request, api_error, tool_result.
func (eh *EventHandler) HandleOtelLogs(payload json.RawMessage) []monitor.AgentEvent {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic recovered in HandleOtelLogs: %v\n%s\n", r, debug.Stack())
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

	return events
}

// HandleOtelMetrics processes OTEL metrics — currently a no-op for Claude Code.
func (eh *EventHandler) HandleOtelMetrics(_ json.RawMessage) []monitor.AgentEvent {
	return nil
}

// HandleOtelTraces processes OTEL traces — currently a no-op for Claude Code.
func (eh *EventHandler) HandleOtelTraces(_ json.RawMessage) []monitor.AgentEvent {
	return nil
}

// HandleHookEvent processes hook events from Claude Code.
// Returns normalized events for the hook type.
func (eh *EventHandler) HandleHookEvent(eventName string, payload json.RawMessage) []monitor.AgentEvent {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic recovered in HandleHookEvent: %v\n%s\n", r, debug.Stack())
		}
	}()

	now := time.Now()

	switch eventName {
	case "PreToolUse":
		var hook hookToolUse
		if err := json.Unmarshal(payload, &hook); err != nil {
			return nil
		}
		eh.mu.Lock()
		eh.pendingTools[hook.CallID] = now
		eh.mu.Unlock()

		return []monitor.AgentEvent{{
			Type:      monitor.EventToolStarted,
			Timestamp: now,
			Data: monitor.ToolStartedData{
				ToolName: hook.ToolName,
				CallID:   hook.CallID,
			},
		}}

	case "PostToolUse":
		var hook hookToolUse
		if err := json.Unmarshal(payload, &hook); err != nil {
			return nil
		}
		eh.mu.Lock()
		startTime, ok := eh.pendingTools[hook.CallID]
		if ok {
			delete(eh.pendingTools, hook.CallID)
		}
		eh.mu.Unlock()

		var durationMs int64
		if ok {
			durationMs = time.Since(startTime).Milliseconds()
		}

		return []monitor.AgentEvent{{
			Type:      monitor.EventToolCompleted,
			Timestamp: now,
			Data: monitor.ToolCompletedData{
				ToolName:   hook.ToolName,
				CallID:     hook.CallID,
				DurationMs: durationMs,
				Success:    true,
			},
		}}

	case "PermissionRequest":
		var hook hookPermission
		if err := json.Unmarshal(payload, &hook); err != nil {
			return nil
		}
		return []monitor.AgentEvent{{
			Type:      monitor.EventApprovalRequested,
			Timestamp: now,
			Data: monitor.ApprovalRequestedData{
				ToolName: hook.ToolName,
				CallID:   hook.CallID,
				Payload:  payload,
			},
		}}

	case "permission_decision":
		var hook hookPermissionDecision
		if err := json.Unmarshal(payload, &hook); err != nil {
			return nil
		}
		if hook.Granted {
			return []monitor.AgentEvent{{
				Type:      monitor.EventPermissionGranted,
				Timestamp: now,
			}}
		}
		return []monitor.AgentEvent{{
			Type:      monitor.EventPermissionDenied,
			Timestamp: now,
		}}

	case "PreCompact":
		return []monitor.AgentEvent{{
			Type:      monitor.EventCompactionStarted,
			Timestamp: now,
		}}

	case "SessionStart":
		return []monitor.AgentEvent{{
			Type:      monitor.EventSessionStarted,
			Timestamp: now,
			Data:      monitor.SessionStartedData{},
		}}

	case "SessionEnd":
		return []monitor.AgentEvent{{
			Type:      monitor.EventSessionEnded,
			Timestamp: now,
			Data:      monitor.SessionEndedData{Reason: "hook_session_end"},
		}}
	}

	return nil
}

// HandleSessionLogLine processes a JSONL line from Claude Code's session.jsonl.
// Extracts assistant message content for the AgentMessage event.
func (eh *EventHandler) HandleSessionLogLine(line []byte) []monitor.AgentEvent {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic recovered in HandleSessionLogLine: %v\n%s\n", r, debug.Stack())
		}
	}()

	var entry sessionLogEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil
	}

	// Only extract assistant messages
	if entry.Role != "assistant" || entry.Content == "" {
		return nil
	}

	return []monitor.AgentEvent{{
		Type:      monitor.EventAgentMessage,
		Timestamp: time.Now(),
		Data:      monitor.AgentMessageData{Content: entry.Content},
	}}
}

// processLogRecord processes a single OTEL log record.
func (eh *EventHandler) processLogRecord(lr otelLogRecord) []monitor.AgentEvent {
	// Extract attributes
	attrs := make(map[string]string)
	for _, kv := range lr.Attributes {
		if kv.Value.StringValue != "" {
			attrs[kv.Key] = kv.Value.StringValue
		}
	}

	// Discover session ID from attributes
	if sid, ok := attrs["session_id"]; ok && sid != "" {
		eh.mu.Lock()
		if eh.sessionID == "" {
			eh.sessionID = sid
		}
		eh.mu.Unlock()

		// Filter events from other sessions
		if eh.sessionID != sid {
			return nil
		}
	}

	// Extract event type from body or attributes
	eventType := attrs["event_type"]
	if eventType == "" && lr.Body.StringValue != "" {
		eventType = lr.Body.StringValue
	}

	now := time.Now()

	switch {
	case eventType == "api_request" || strings.HasPrefix(eventType, "api_request"):
		return eh.handleAPIRequest(attrs, now)
	case eventType == "api_error":
		// API error — currently no specific event, could be extended
		return nil
	case eventType == "tool_result":
		return eh.handleToolResult(attrs, now)
	}

	return nil
}

// handleAPIRequest handles an api_request OTEL log, which signals turn completion
// with token counts.
func (eh *EventHandler) handleAPIRequest(attrs map[string]string, now time.Time) []monitor.AgentEvent {
	turnID := attrs["turn_id"]

	eh.mu.Lock()
	defer eh.mu.Unlock()

	// Deduplicate turns
	if turnID != "" && turnID == eh.lastTurnID {
		return nil
	}
	eh.lastTurnID = turnID

	data := monitor.TurnCompletedData{
		TurnID: turnID,
	}

	// Parse token counts
	if v, ok := attrs["input_tokens"]; ok {
		fmt.Sscanf(v, "%d", &data.InputTokens)
	}
	if v, ok := attrs["output_tokens"]; ok {
		fmt.Sscanf(v, "%d", &data.OutputTokens)
	}
	if v, ok := attrs["cached_tokens"]; ok {
		fmt.Sscanf(v, "%d", &data.CachedTokens)
	}
	if v, ok := attrs["cost_usd"]; ok {
		fmt.Sscanf(v, "%f", &data.CostUSD)
	}

	return []monitor.AgentEvent{{
		Type:      monitor.EventTurnCompleted,
		Timestamp: now,
		Data:      data,
	}}
}

// handleToolResult handles a tool_result OTEL log.
func (eh *EventHandler) handleToolResult(attrs map[string]string, now time.Time) []monitor.AgentEvent {
	callID := attrs["call_id"]
	toolName := attrs["tool_name"]

	eh.mu.Lock()
	startTime, pending := eh.pendingTools[callID]
	if pending {
		delete(eh.pendingTools, callID)
	}
	eh.mu.Unlock()

	var durationMs int64
	if pending {
		durationMs = now.Sub(startTime).Milliseconds()
	}

	return []monitor.AgentEvent{{
		Type:      monitor.EventToolCompleted,
		Timestamp: now,
		Data: monitor.ToolCompletedData{
			ToolName:   toolName,
			CallID:     callID,
			DurationMs: durationMs,
			Success:    attrs["success"] != "false",
		},
	}}
}

// OTEL log record types

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

type otelKeyValue struct {
	Key   string    `json:"key"`
	Value otelValue `json:"value"`
}

type otelValue struct {
	StringValue string `json:"stringValue"`
}

// Hook payload types

type hookToolUse struct {
	ToolName string `json:"tool_name"`
	CallID   string `json:"call_id"`
}

type hookPermission struct {
	ToolName string `json:"tool_name"`
	CallID   string `json:"call_id"`
}

type hookPermissionDecision struct {
	Granted bool `json:"granted"`
}

// Session log entry (from session.jsonl)
type sessionLogEntry struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
