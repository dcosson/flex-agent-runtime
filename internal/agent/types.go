package agent

import (
	"context"
	"maps"
	"time"

	"flex-agent-runtime/internal/ai"
)

// AgentState is the finite-state machine state for one agent instance.
type AgentState string

const (
	StateIdle            AgentState = "idle"
	StateStreaming       AgentState = "streaming"
	StateToolExecution   AgentState = "tool_execution"
	StateWaitingFollowUp AgentState = "waiting_follow_up"
	StateExited          AgentState = "exited"
)

// AgentMessage stores a conversation entry and the turn it belongs to.
type AgentMessage struct {
	Turn      int
	Message   ai.Message
	CreatedAt time.Time
}

// SessionMetrics stores low-cardinality counters for session instrumentation.
type SessionMetrics struct {
	TurnsStarted      uint64
	TurnsCompleted    uint64
	MessagesAppended  uint64
	ToolCallsStarted  uint64
	ToolCallsFinished uint64
	Errors            uint64
}

// Session is the authoritative runtime session record.
type Session struct {
	ID              string
	DriverSessionID string
	ConversationLog []AgentMessage
	StateHistory    []AgentState
	Metrics         SessionMetrics
}

// Clone returns a deep-copy of session slices and maps for safe external reads.
func (s *Session) Clone() *Session {
	if s == nil {
		return nil
	}
	cp := *s
	if len(s.ConversationLog) > 0 {
		cp.ConversationLog = make([]AgentMessage, len(s.ConversationLog))
		copy(cp.ConversationLog, s.ConversationLog)
	}
	if len(s.StateHistory) > 0 {
		cp.StateHistory = make([]AgentState, len(s.StateHistory))
		copy(cp.StateHistory, s.StateHistory)
	}
	return &cp
}

// AgentToolResult is the normalized result contract for tool execution callbacks.
type AgentToolResult struct {
	Content    []ai.ContentBlock
	SnapshotID string
	ExitCode   *int
	IsError    bool
	Metadata   map[string]any
}

func (r AgentToolResult) clone() AgentToolResult {
	cp := r
	if len(r.Content) > 0 {
		cp.Content = append([]ai.ContentBlock(nil), r.Content...)
	}
	if len(r.Metadata) > 0 {
		cp.Metadata = maps.Clone(r.Metadata)
	}
	if r.ExitCode != nil {
		exit := *r.ExitCode
		cp.ExitCode = &exit
	}
	return cp
}

// AgentTool is the backend-agnostic tool contract used by the agent loop.
type AgentTool struct {
	ai.Tool
	Label    string
	Terminal bool
	Execute  func(ctx context.Context, toolCallID string, params map[string]any, onUpdate func(AgentToolResult)) (AgentToolResult, error)
}

// AgentEventType is the canonical runtime event taxonomy.
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

// AgentEvent is the normalized event payload used by all driver implementations.
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
	Error           error
	ErrorMessage    string
	At              time.Time
	Metadata        map[string]any
}

func (e AgentEvent) clone() AgentEvent {
	cp := e
	if e.Message != nil {
		m := *e.Message
		cp.Message = &m
	}
	if e.ToolResult != nil {
		tr := e.ToolResult.clone()
		cp.ToolResult = &tr
	}
	if len(e.Metadata) > 0 {
		cp.Metadata = maps.Clone(e.Metadata)
	}
	return cp
}
