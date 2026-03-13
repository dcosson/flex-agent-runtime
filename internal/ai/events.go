package ai

// EventType enumerates streaming event kinds.
type EventType string

const (
	EventStart         EventType = "start"
	EventTextStart     EventType = "text_start"
	EventTextDelta     EventType = "text_delta"
	EventTextEnd       EventType = "text_end"
	EventThinkingStart EventType = "thinking_start"
	EventThinkingDelta EventType = "thinking_delta"
	EventThinkingEnd   EventType = "thinking_end"
	EventToolCallStart EventType = "toolcall_start"
	EventToolCallDelta EventType = "toolcall_delta"
	EventToolCallEnd   EventType = "toolcall_end"
	EventDone          EventType = "done"
	EventError         EventType = "error"
)

// AssistantMessageEvent is the tagged union of streaming events.
type AssistantMessageEvent struct {
	Type         EventType
	ContentIndex int
	Delta        string
	Content      string
	ToolCall     *ToolCall
	Partial      *AssistantMessage
	Message      *AssistantMessage
	Error        *AssistantMessage
	Reason       StopReason
}
