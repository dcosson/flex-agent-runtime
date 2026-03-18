package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
)

type userRecordContent struct {
	Text         string `json:"text"`
	HasTextBlock bool   `json:"has_text_block,omitempty"`
	Timestamp    int64  `json:"timestamp,omitempty"`
}

type assistantRecordContent struct {
	ContentBlocks []recordContentBlock `json:"content_blocks"`
	Model         string               `json:"model,omitempty"`
	StopReason    string               `json:"stop_reason,omitempty"`
	ErrorMessage  string               `json:"error_message,omitempty"`
	Usage         assistantUsageRecord `json:"usage"`
	Timestamp     int64                `json:"timestamp,omitempty"`
}

type toolResultRecordContent struct {
	ToolCallID string               `json:"tool_call_id"`
	ToolName   string               `json:"tool_name"`
	Content    []recordContentBlock `json:"content"`
	IsError    bool                 `json:"is_error"`
	Timestamp  int64                `json:"timestamp,omitempty"`
}

type assistantUsageRecord struct {
	InputTokens      int                      `json:"input_tokens"`
	OutputTokens     int                      `json:"output_tokens"`
	CacheReadTokens  int                      `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int                      `json:"cache_write_tokens,omitempty"`
	TotalTokens      int                      `json:"total_tokens,omitempty"`
	Cost             assistantUsageCostRecord `json:"cost"`
}

type assistantUsageCostRecord struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
	Total      float64 `json:"total"`
}

type recordContentBlock struct {
	Type string `json:"type"`

	Text      string `json:"text,omitempty"`
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Redacted  bool   `json:"redacted,omitempty"`

	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	ThoughtSignature string `json:"thought_signature,omitempty"`

	Data     string `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

// AgentMessageToRecord converts an in-memory AgentMessage to the serializable
// record format used for persistence and resume.
func AgentMessageToRecord(msg AgentMessage) (AgentMessageRecord, error) {
	record := AgentMessageRecord{
		Turn:      msg.Turn,
		CreatedAt: msg.CreatedAt,
	}

	switch m := msg.Message.(type) {
	case *ai.UserMessage:
		text, err := flattenUserText(m.Content)
		if err != nil {
			return AgentMessageRecord{}, err
		}
		payload := userRecordContent{
			Text:         text,
			HasTextBlock: len(m.Content) > 0,
			Timestamp:    m.Timestamp,
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return AgentMessageRecord{}, fmt.Errorf("marshal user record: %w", err)
		}
		record.Role = AgentMessageRoleUser
		record.Content = raw
		return record, nil
	case *ai.AssistantMessage:
		blocks, err := toAssistantRecordBlocks(m.Content)
		if err != nil {
			return AgentMessageRecord{}, err
		}
		payload := assistantRecordContent{
			ContentBlocks: blocks,
			Model:         m.Model,
			StopReason:    string(m.StopReason),
			ErrorMessage:  m.ErrorMessage,
			Usage:         toAssistantUsageRecord(m.Usage),
			Timestamp:     m.Timestamp,
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return AgentMessageRecord{}, fmt.Errorf("marshal assistant record: %w", err)
		}
		record.Role = AgentMessageRoleAssistant
		record.Content = raw
		return record, nil
	case *ai.ToolResultMessage:
		blocks, err := toToolResultRecordBlocks(m.Content)
		if err != nil {
			return AgentMessageRecord{}, err
		}
		payload := toolResultRecordContent{
			ToolCallID: m.ToolCallID,
			ToolName:   m.ToolName,
			Content:    blocks,
			IsError:    m.IsError,
			Timestamp:  m.Timestamp,
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return AgentMessageRecord{}, fmt.Errorf("marshal tool_result record: %w", err)
		}
		record.Role = AgentMessageRoleToolResult
		record.Content = raw
		return record, nil
	default:
		return AgentMessageRecord{}, fmt.Errorf("unsupported agent message type %T", msg.Message)
	}
}

// RecordToAgentMessage converts a serialized record back to an in-memory
// AgentMessage.
func RecordToAgentMessage(rec AgentMessageRecord) (AgentMessage, error) {
	out := AgentMessage{
		Turn:      rec.Turn,
		CreatedAt: rec.CreatedAt,
	}
	switch rec.Role {
	case AgentMessageRoleUser:
		var payload userRecordContent
		if err := json.Unmarshal(rec.Content, &payload); err != nil {
			return AgentMessage{}, fmt.Errorf("parse user content: %w", err)
		}
		user := &ai.UserMessage{Timestamp: payload.Timestamp}
		if payload.HasTextBlock {
			user.Content = []ai.ContentBlock{&ai.TextContent{Text: payload.Text}}
		}
		out.Message = user
		return out, nil
	case AgentMessageRoleAssistant:
		var payload assistantRecordContent
		if err := json.Unmarshal(rec.Content, &payload); err != nil {
			return AgentMessage{}, fmt.Errorf("parse assistant content: %w", err)
		}
		content, err := fromAssistantRecordBlocks(payload.ContentBlocks)
		if err != nil {
			return AgentMessage{}, err
		}
		out.Message = &ai.AssistantMessage{
			Content:      content,
			Model:        payload.Model,
			StopReason:   ai.StopReason(payload.StopReason),
			ErrorMessage: payload.ErrorMessage,
			Usage:        fromAssistantUsageRecord(payload.Usage),
			Timestamp:    payload.Timestamp,
		}
		return out, nil
	case AgentMessageRoleToolResult:
		var payload toolResultRecordContent
		if err := json.Unmarshal(rec.Content, &payload); err != nil {
			return AgentMessage{}, fmt.Errorf("parse tool_result content: %w", err)
		}
		content, err := fromToolResultRecordBlocks(payload.Content)
		if err != nil {
			return AgentMessage{}, err
		}
		out.Message = &ai.ToolResultMessage{
			ToolCallID: payload.ToolCallID,
			ToolName:   payload.ToolName,
			Content:    content,
			IsError:    payload.IsError,
			Timestamp:  payload.Timestamp,
		}
		return out, nil
	default:
		return AgentMessage{}, fmt.Errorf("unknown record role %q", rec.Role)
	}
}

func flattenUserText(blocks []ai.ContentBlock) (string, error) {
	if len(blocks) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		text, ok := block.(*ai.TextContent)
		if !ok {
			return "", fmt.Errorf("unsupported user content block type %T", block)
		}
		parts = append(parts, text.Text)
	}
	return strings.Join(parts, "\n"), nil
}

func toAssistantUsageRecord(usage ai.Usage) assistantUsageRecord {
	return assistantUsageRecord{
		InputTokens:      usage.Input,
		OutputTokens:     usage.Output,
		CacheReadTokens:  usage.CacheRead,
		CacheWriteTokens: usage.CacheWrite,
		TotalTokens:      usage.TotalTokens,
		Cost: assistantUsageCostRecord{
			Input:      usage.Cost.Input,
			Output:     usage.Cost.Output,
			CacheRead:  usage.Cost.CacheRead,
			CacheWrite: usage.Cost.CacheWrite,
			Total:      usage.Cost.Total,
		},
	}
}

func fromAssistantUsageRecord(usage assistantUsageRecord) ai.Usage {
	return ai.Usage{
		Input:       usage.InputTokens,
		Output:      usage.OutputTokens,
		CacheRead:   usage.CacheReadTokens,
		CacheWrite:  usage.CacheWriteTokens,
		TotalTokens: usage.TotalTokens,
		Cost: ai.UsageCost{
			Input:      usage.Cost.Input,
			Output:     usage.Cost.Output,
			CacheRead:  usage.Cost.CacheRead,
			CacheWrite: usage.Cost.CacheWrite,
			Total:      usage.Cost.Total,
		},
	}
}

func toAssistantRecordBlocks(blocks []ai.ContentBlock) ([]recordContentBlock, error) {
	if len(blocks) == 0 {
		return nil, nil
	}
	out := make([]recordContentBlock, 0, len(blocks))
	for _, block := range blocks {
		recordBlock, err := toRecordBlock(block, true)
		if err != nil {
			return nil, err
		}
		out = append(out, recordBlock)
	}
	return out, nil
}

func toToolResultRecordBlocks(blocks []ai.ContentBlock) ([]recordContentBlock, error) {
	if len(blocks) == 0 {
		return nil, nil
	}
	out := make([]recordContentBlock, 0, len(blocks))
	for _, block := range blocks {
		recordBlock, err := toRecordBlock(block, false)
		if err != nil {
			return nil, err
		}
		out = append(out, recordBlock)
	}
	return out, nil
}

func toRecordBlock(block ai.ContentBlock, allowThinkingAndToolUse bool) (recordContentBlock, error) {
	switch b := block.(type) {
	case *ai.TextContent:
		return recordContentBlock{
			Type:      "text",
			Text:      b.Text,
			Signature: b.TextSignature,
		}, nil
	case *ai.ImageContent:
		return recordContentBlock{
			Type:     "image",
			Data:     b.Data,
			MimeType: b.MimeType,
		}, nil
	case *ai.ThinkingContent:
		if !allowThinkingAndToolUse {
			return recordContentBlock{}, fmt.Errorf("unsupported tool_result content block type %T", block)
		}
		return recordContentBlock{
			Type:      "thinking",
			Thinking:  b.Thinking,
			Signature: b.ThinkingSignature,
			Redacted:  b.Redacted,
		}, nil
	case *ai.ToolCall:
		if !allowThinkingAndToolUse {
			return recordContentBlock{}, fmt.Errorf("unsupported tool_result content block type %T", block)
		}
		rawArgs, err := json.Marshal(b.Arguments)
		if err != nil {
			return recordContentBlock{}, fmt.Errorf("marshal tool_use arguments: %w", err)
		}
		return recordContentBlock{
			Type:             "tool_use",
			ID:               b.ID,
			Name:             b.Name,
			Input:            rawArgs,
			ThoughtSignature: b.ThoughtSignature,
		}, nil
	default:
		return recordContentBlock{}, fmt.Errorf("unsupported content block type %T", block)
	}
}

func fromAssistantRecordBlocks(blocks []recordContentBlock) ([]ai.ContentBlock, error) {
	if len(blocks) == 0 {
		return nil, nil
	}
	out := make([]ai.ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		decoded, err := fromRecordBlock(block, true)
		if err != nil {
			return nil, err
		}
		out = append(out, decoded)
	}
	return out, nil
}

func fromToolResultRecordBlocks(blocks []recordContentBlock) ([]ai.ContentBlock, error) {
	if len(blocks) == 0 {
		return nil, nil
	}
	out := make([]ai.ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		decoded, err := fromRecordBlock(block, false)
		if err != nil {
			return nil, err
		}
		out = append(out, decoded)
	}
	return out, nil
}

func fromRecordBlock(block recordContentBlock, allowThinkingAndToolUse bool) (ai.ContentBlock, error) {
	switch block.Type {
	case "text":
		return &ai.TextContent{
			Text:          block.Text,
			TextSignature: block.Signature,
		}, nil
	case "image":
		return &ai.ImageContent{
			Data:     block.Data,
			MimeType: block.MimeType,
		}, nil
	case "thinking":
		if !allowThinkingAndToolUse {
			return nil, fmt.Errorf("unsupported tool_result content block type %q", block.Type)
		}
		return &ai.ThinkingContent{
			Thinking:          block.Thinking,
			ThinkingSignature: block.Signature,
			Redacted:          block.Redacted,
		}, nil
	case "tool_use":
		if !allowThinkingAndToolUse {
			return nil, fmt.Errorf("unsupported tool_result content block type %q", block.Type)
		}
		var args map[string]any
		if len(block.Input) > 0 {
			if err := json.Unmarshal(block.Input, &args); err != nil {
				return nil, fmt.Errorf("parse tool_use input: %w", err)
			}
		}
		return &ai.ToolCall{
			ID:               block.ID,
			Name:             block.Name,
			Arguments:        args,
			ThoughtSignature: block.ThoughtSignature,
		}, nil
	default:
		return nil, fmt.Errorf("unknown content block type %q", block.Type)
	}
}
