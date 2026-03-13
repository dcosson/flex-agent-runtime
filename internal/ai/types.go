package ai

import "encoding/json"

// Message is the sealed interface for conversation messages.
type Message interface {
	messageRole() Role
	GetTimestamp() int64
}

type Role string

const (
	RoleUser       Role = "user"
	RoleAssistant  Role = "assistant"
	RoleToolResult Role = "toolResult"
)

// UserMessage represents a user turn in the conversation.
type UserMessage struct {
	Content   []ContentBlock // TextContent | ImageContent
	Timestamp int64          // Unix milliseconds
}

func (m *UserMessage) messageRole() Role   { return RoleUser }
func (m *UserMessage) GetTimestamp() int64 { return m.Timestamp }

// AssistantMessage represents an LLM response.
type AssistantMessage struct {
	Content      []ContentBlock // TextContent | ThinkingContent | ToolCall
	API          string
	Provider     string
	Model        string
	Usage        Usage
	StopReason   StopReason
	ErrorMessage string
	Timestamp    int64
}

func (m *AssistantMessage) messageRole() Role   { return RoleAssistant }
func (m *AssistantMessage) GetTimestamp() int64 { return m.Timestamp }

// ToolResultMessage represents the result of a tool execution.
type ToolResultMessage struct {
	ToolCallID string
	ToolName   string
	Content    []ContentBlock // TextContent | ImageContent
	IsError    bool
	Timestamp  int64
}

func (m *ToolResultMessage) messageRole() Role   { return RoleToolResult }
func (m *ToolResultMessage) GetTimestamp() int64 { return m.Timestamp }

// ContentBlock is the sealed interface for message content.
type ContentBlock interface {
	contentBlockType() ContentType
}

type ContentType string

const (
	ContentTypeText     ContentType = "text"
	ContentTypeThinking ContentType = "thinking"
	ContentTypeImage    ContentType = "image"
	ContentTypeToolCall ContentType = "toolCall"
)

type TextContent struct {
	Text          string
	TextSignature string // optional, provider-specific
}

func (c *TextContent) contentBlockType() ContentType { return ContentTypeText }

type ThinkingContent struct {
	Thinking          string
	ThinkingSignature string // optional, provider-specific
	Redacted          bool   // true if redacted by safety filters
}

func (c *ThinkingContent) contentBlockType() ContentType { return ContentTypeThinking }

type ImageContent struct {
	Data     string // base64 encoded
	MimeType string // e.g. image/jpeg
}

func (c *ImageContent) contentBlockType() ContentType { return ContentTypeImage }

type ToolCall struct {
	ID               string
	Name             string
	Arguments        map[string]any
	ThoughtSignature string // optional, Google-specific
}

func (c *ToolCall) contentBlockType() ContentType { return ContentTypeToolCall }

// Usage tracks token accounting and computed cost.
type Usage struct {
	Input       int
	Output      int
	CacheRead   int
	CacheWrite  int
	TotalTokens int
	Cost        UsageCost
}

// UsageCost stores monetary cost components.
type UsageCost struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	Total      float64
}

type StopReason string

const (
	StopReasonStop    StopReason = "stop"
	StopReasonLength  StopReason = "length"
	StopReasonToolUse StopReason = "toolUse"
	StopReasonError   StopReason = "error"
	StopReasonAborted StopReason = "aborted"
)

// Tool defines a callable tool schema.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema object
}

// Context is conversation state for provider calls.
type Context struct {
	SystemPrompt string
	Messages     []Message
	Tools        []Tool
}
