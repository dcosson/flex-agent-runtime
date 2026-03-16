// Package driver defines the TermmuxDriverAdapter interface and canonical
// conversation types for bidirectional session log conversion.
package driver

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"flex-agent-runtime/internal/termmux/monitor"
)

// TermmuxDriverAdapter abstracts a 3rd party agent CLI for termmux session control.
// Name intentionally differs from internal/agent.AgentDriver to avoid ambiguity.
type TermmuxDriverAdapter interface {
	// Identity
	Name() string
	Command() string

	// Launch configuration
	BuildCommandArgs(prependArgs, extraArgs []string) []string
	BuildCommandEnvVars(runtimeDir string) map[string]string
	PrepareForLaunch(dryRun bool) (LaunchConfig, error)

	// Capabilities
	SupportsHooks() bool
	SupportsResume() bool
	NativeSessionLogPath(configDir, cwd, sessionID string) string

	// Session log conversion
	ParseSessionLog(reader io.Reader) ([]ConversationEntry, error)
	WriteSessionLog(entries []ConversationEntry, writer io.Writer) error

	// Lifecycle
	Start(ctx context.Context, events chan<- monitor.AgentEvent) error
	HandleHookEvent(eventName string, payload json.RawMessage) bool
	HandleInterrupt() bool
	Stop()
}

// LaunchConfig is the result of PrepareForLaunch.
type LaunchConfig struct {
	OtelEndpoint string
	ExtraEnv     map[string]string
}

// ConversationEntry is the canonical conversation format for session log
// round-tripping between the runtime and driver-native formats.
type ConversationEntry struct {
	Timestamp time.Time        `json:"timestamp"`
	Role      ConversationRole `json:"role"`
	Content   string           `json:"content"`
	ToolCall  *ToolCallRecord  `json:"tool_call,omitempty"`
	Thinking  *ThinkingBlock   `json:"thinking,omitempty"`
	Usage     *TokenUsage      `json:"usage,omitempty"`
}

// ConversationRole identifies the role of a conversation entry.
type ConversationRole string

const (
	RoleUser       ConversationRole = "user"
	RoleAssistant  ConversationRole = "assistant"
	RoleToolUse    ConversationRole = "tool_use"
	RoleToolResult ConversationRole = "tool_result"
)

// ToolCallRecord holds tool invocation details.
type ToolCallRecord struct {
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
	Result string          `json:"result,omitempty"`
	CallID string          `json:"call_id"`
}

// ThinkingBlock holds extended thinking content.
type ThinkingBlock struct {
	Content string `json:"content"`
}

// TokenUsage holds token usage metrics.
type TokenUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	CachedTokens int64 `json:"cached_tokens,omitempty"`
}
