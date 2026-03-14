package monitor

import (
	"encoding/json"
	"fmt"
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

// agentEventJSON is a helper for custom JSON unmarshalling.
type agentEventJSON struct {
	Type      AgentEventType  `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// UnmarshalJSON deserializes an AgentEvent, using the Type field to
// reconstruct the correct typed Data payload instead of a generic map.
func (e *AgentEvent) UnmarshalJSON(b []byte) error {
	var raw agentEventJSON
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("unmarshal agent event: %w", err)
	}

	e.Type = raw.Type
	e.Timestamp = raw.Timestamp

	if len(raw.Data) == 0 || string(raw.Data) == "null" {
		return nil
	}

	var data any
	switch raw.Type {
	case EventSessionStarted:
		var d SessionStartedData
		if err := json.Unmarshal(raw.Data, &d); err != nil {
			return err
		}
		data = d
	case EventSessionEnded:
		var d SessionEndedData
		if err := json.Unmarshal(raw.Data, &d); err != nil {
			return err
		}
		data = d
	case EventTurnCompleted:
		var d TurnCompletedData
		if err := json.Unmarshal(raw.Data, &d); err != nil {
			return err
		}
		data = d
	case EventToolStarted:
		var d ToolStartedData
		if err := json.Unmarshal(raw.Data, &d); err != nil {
			return err
		}
		data = d
	case EventToolCompleted:
		var d ToolCompletedData
		if err := json.Unmarshal(raw.Data, &d); err != nil {
			return err
		}
		data = d
	case EventApprovalRequested:
		var d ApprovalRequestedData
		if err := json.Unmarshal(raw.Data, &d); err != nil {
			return err
		}
		data = d
	case EventAgentMessage:
		var d AgentMessageData
		if err := json.Unmarshal(raw.Data, &d); err != nil {
			return err
		}
		data = d
	case EventStateChange:
		var d StateChangeData
		if err := json.Unmarshal(raw.Data, &d); err != nil {
			return err
		}
		data = d
	default:
		// Unknown event type — keep as raw JSON
		var m map[string]any
		if err := json.Unmarshal(raw.Data, &m); err != nil {
			return err
		}
		data = m
	}

	e.Data = data
	return nil
}
