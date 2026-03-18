package api

import (
	"encoding/json"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
)

// CurrentConversationSchemaVersion is the supported schema version for
// ResumeSessionRequest.ConversationLog.
const CurrentConversationSchemaVersion = 1

type ToolEnvironmentType string

const (
	ToolEnvLocal   ToolEnvironmentType = "local"
	ToolEnvSandbox ToolEnvironmentType = "sandbox"
)

type SessionConfig struct {
	SessionID    string
	Driver       string
	Model        string
	Provider     string
	SystemPrompt string
	Tools        []string
	Metadata     map[string]any

	ToolEnvironment ToolEnvironmentConfig
}

type ToolEnvironmentConfig struct {
	Type ToolEnvironmentType

	SandboxHostAddr  string
	SandboxSessionID string

	LocalRootDir string
}

type CreateAgentSessionRequest struct {
	SessionConfig
}

type CreateAgentSessionResponse struct {
	SessionID string
	State     string
}

type GetAgentSessionRequest struct {
	SessionID string
}

type GetAgentSessionResponse struct {
	SessionID       string
	State           string
	Metrics         SessionMetrics
	ConversationLen int
}

type SessionMetrics struct {
	TurnsStarted      uint64
	TurnsCompleted    uint64
	MessagesAppended  uint64
	ToolCallsStarted  uint64
	ToolCallsFinished uint64
	Errors            uint64
}

type ListAgentSessionsRequest struct {
	Labels map[string]string
}

type ListAgentSessionsResponse struct {
	Sessions []AgentSessionSummary
}

type AgentSessionSummary struct {
	SessionID string
	State     string
	CreatedAt time.Time
}

type SendMessageRequest struct {
	SessionID string
	Message   string
}

type ContinueRequest struct {
	SessionID string
}

type SteerRequest struct {
	SessionID string
	Message   string
}

type SteerResponse struct{}

type FollowUpRequest struct {
	SessionID string
	Message   string
}

type FollowUpResponse struct{}

type AbortRequest struct {
	SessionID string
	Reason    string
}

type AbortResponse struct{}

const (
	AgentMessageRoleUser       = "user"
	AgentMessageRoleAssistant  = "assistant"
	AgentMessageRoleToolResult = "tool_result"
)

type ResumeSessionRequest struct {
	SessionConfig

	SchemaVersion   int
	ConversationLog []AgentMessageRecord
}

type AgentMessageRecord struct {
	Turn      int
	Role      string
	Content   json.RawMessage
	CreatedAt time.Time
}

type AgentMessage struct {
	Turn      int
	Message   ai.Message
	CreatedAt time.Time
}

type ResumeSessionResponse struct {
	SessionID       string
	State           string
	ConversationLen int
	Warnings        []string
}

type SubscribeEventsRequest struct {
	SessionID string
}

type DestroyAgentSessionRequest struct {
	SessionID string
}

type DestroyAgentSessionResponse struct{}
