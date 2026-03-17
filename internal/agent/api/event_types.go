package api

import (
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/ai"
)

type AgentState string

type AgentEventType string

const (
	EventSessionStarted        AgentEventType = "session_started"
	EventSessionEnded          AgentEventType = "session_ended"
	EventTurnStarted           AgentEventType = "turn_started"
	EventTurnCompleted         AgentEventType = "turn_completed"
	EventAgentMessageDelta     AgentEventType = "agent_message_delta"
	EventAgentMessageCompleted AgentEventType = "agent_message_completed"
	EventThinkingDelta         AgentEventType = "thinking_delta"
	EventToolStarted           AgentEventType = "tool_started"
	EventToolUpdate            AgentEventType = "tool_update"
	EventToolCompleted         AgentEventType = "tool_completed"
	EventStateChange           AgentEventType = "state_change"
	EventSteeringApplied       AgentEventType = "steering_applied"
	EventFollowUpEnqueued      AgentEventType = "followup_enqueued"
	EventAborted               AgentEventType = "aborted"
	EventDriverError           AgentEventType = "driver_error"
	EventProviderError         AgentEventType = "provider_error"
	EventToolError             AgentEventType = "tool_error"
	EventTerminalToolCompleted AgentEventType = "terminal_tool_completed"
)

type AgentToolResult struct {
	Content    []ai.ContentBlock
	SnapshotID string
	ExitCode   *int
	IsError    bool
	Metadata   map[string]any
}

type AgentEvent struct {
	Type            AgentEventType
	SessionID       string
	DriverSessionID string
	State           AgentState
	Turn            int
	Message         *AgentMessage
	Assistant       *ai.AssistantMessage
	ToolName        string
	ToolCallID      string
	ToolResult      *AgentToolResult
	Delta           string
	ControlMessage  string
	ErrorMessage    string
	At              time.Time
	Metadata        map[string]any
}
