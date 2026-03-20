package anthropic

import "encoding/json"

type wireRequest struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	TopP      *float64       `json:"top_p,omitempty"`
	TopK      *int           `json:"top_k,omitempty"`
	Messages  []wireMessage  `json:"messages"`
	System    string         `json:"system,omitempty"`
	Tools     []wireTool     `json:"tools,omitempty"`
	Thinking  *wireThinking  `json:"thinking,omitempty"`
	Stream    bool           `json:"stream"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type wireThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type wireTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type wireMessage struct {
	Role    string             `json:"role"`
	Content []wireContentBlock `json:"content"`
}

type wireImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type wireContentBlock struct {
	Type      string           `json:"type"`
	Text      string           `json:"text,omitempty"`
	Thinking  string           `json:"thinking,omitempty"`
	Signature string           `json:"signature,omitempty"`
	ID        string           `json:"id,omitempty"`
	Name      string           `json:"name,omitempty"`
	Input     map[string]any   `json:"input,omitempty"`
	Source    *wireImageSource `json:"source,omitempty"`

	ToolUseID string `json:"tool_use_id,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
	Content   any    `json:"content,omitempty"`
}

type wireEventEnvelope struct {
	Type string `json:"type"`
}

type wireMessageStartEvent struct {
	Type    string          `json:"type"`
	Message wireMessageData `json:"message"`
}

type wireMessageData struct {
	ID         string    `json:"id"`
	Role       string    `json:"role"`
	Model      string    `json:"model"`
	StopReason string    `json:"stop_reason"`
	Usage      wireUsage `json:"usage"`
}

type wireContentBlockStartEvent struct {
	Type         string           `json:"type"`
	Index        int              `json:"index"`
	ContentBlock wireContentBlock `json:"content_block"`
}

type wireContentBlockDeltaEvent struct {
	Type  string           `json:"type"`
	Index int              `json:"index"`
	Delta wireDeltaPayload `json:"delta"`
}

type wireDeltaPayload struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	Signature   string `json:"signature,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

type wireContentBlockStopEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

type wireMessageDeltaEvent struct {
	Type  string           `json:"type"`
	Delta wireMessageDelta `json:"delta"`
	Usage wireUsage        `json:"usage"`
}

type wireMessageDelta struct {
	StopReason string `json:"stop_reason"`
}

type wireErrorEvent struct {
	Type  string        `json:"type"`
	Error wireErrorBody `json:"error"`
}

type wireErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type wireUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

type wireErrorResponse struct {
	Type  string        `json:"type"`
	Error wireErrorBody `json:"error"`
}

func unmarshalEvent(data string, dst any) error {
	return json.Unmarshal([]byte(data), dst)
}
