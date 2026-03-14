package monitor

import (
	"encoding/json"
	"time"
)

// AgentEvent is a normalized event from the termmux event pipeline.
// This maps to internal/agent.AgentEvent types but is independently defined
// for the termmux context (3rd party CLI event normalization).
type AgentEvent struct {
	Type      AgentEventType `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	Data      any            `json:"data,omitempty"`
}

// AgentEventType identifies the kind of event.
type AgentEventType string

const (
	EventSessionStarted      AgentEventType = "session_started"
	EventSessionEnded        AgentEventType = "session_ended"
	EventTurnCompleted       AgentEventType = "turn_completed"
	EventToolStarted         AgentEventType = "tool_started"
	EventToolCompleted       AgentEventType = "tool_completed"
	EventApprovalRequested   AgentEventType = "approval_requested"
	EventPermissionGranted   AgentEventType = "permission_granted"
	EventPermissionDenied    AgentEventType = "permission_denied"
	EventCompactionStarted   AgentEventType = "compaction_started"
	EventCompactionCompleted AgentEventType = "compaction_completed"
	EventAgentMessage        AgentEventType = "agent_message"
	EventStateChange         AgentEventType = "state_change"
)

// Data payloads for events.

// SessionStartedData carries session start metadata.
type SessionStartedData struct {
	SessionID string `json:"session_id"`
	Model     string `json:"model,omitempty"`
}

// SessionEndedData carries session end information.
type SessionEndedData struct {
	Reason string `json:"reason,omitempty"`
}

// TurnCompletedData carries turn completion metrics.
type TurnCompletedData struct {
	TurnID       string  `json:"turn_id,omitempty"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CachedTokens int64   `json:"cached_tokens,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
}

// ToolStartedData carries tool invocation start info.
type ToolStartedData struct {
	ToolName string `json:"tool_name"`
	CallID   string `json:"call_id,omitempty"`
}

// ToolCompletedData carries tool invocation completion info.
type ToolCompletedData struct {
	ToolName   string `json:"tool_name"`
	CallID     string `json:"call_id,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Success    bool   `json:"success"`
}

// ApprovalRequestedData carries tool permission request info.
type ApprovalRequestedData struct {
	ToolName string          `json:"tool_name"`
	CallID   string          `json:"call_id,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

// AgentMessageData carries assistant message content.
type AgentMessageData struct {
	Content string `json:"content"`
}

// StateChangeData carries state transition information.
type StateChangeData struct {
	State    State    `json:"state"`
	SubState SubState `json:"sub_state,omitempty"`
}
