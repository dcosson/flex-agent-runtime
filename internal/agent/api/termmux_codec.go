package api

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/ai"
	"github.com/anthropics/flex-agent-runtime/internal/termmux/driver"
)

// AgentMessageToConversationEntries converts an AgentMessage into canonical
// termmux conversation entries used for cross-agent resume/session-log bridges.
func AgentMessageToConversationEntries(msg AgentMessage) []driver.ConversationEntry {
	ts := conversationTimestamp(msg)
	switch m := msg.Message.(type) {
	case *ai.UserMessage:
		return []driver.ConversationEntry{{
			Timestamp: ts,
			Role:      driver.RoleUser,
			Content:   flattenTextContent(m.Content),
		}}
	case *ai.AssistantMessage:
		return assistantToConversationEntries(ts, m)
	case *ai.ToolResultMessage:
		content := flattenTextContent(m.Content)
		return []driver.ConversationEntry{{
			Timestamp: ts,
			Role:      driver.RoleToolResult,
			Content:   content,
			ToolCall: &driver.ToolCallRecord{
				Name:   m.ToolName,
				CallID: m.ToolCallID,
				Result: content,
			},
		}}
	default:
		return nil
	}
}

func assistantToConversationEntries(ts time.Time, msg *ai.AssistantMessage) []driver.ConversationEntry {
	if msg == nil {
		return nil
	}

	var entries []driver.ConversationEntry
	var thinking []string
	usage := tokenUsage(msg.Usage)

	for _, block := range msg.Content {
		switch b := block.(type) {
		case *ai.TextContent:
			entries = append(entries, driver.ConversationEntry{
				Timestamp: ts,
				Role:      driver.RoleAssistant,
				Content:   b.Text,
				Usage:     usage,
			})
		case *ai.ThinkingContent:
			if b.Thinking != "" {
				thinking = append(thinking, b.Thinking)
			}
		case *ai.ToolCall:
			args, err := json.Marshal(b.Arguments)
			if err != nil {
				args = json.RawMessage(`{}`)
			}
			entries = append(entries, driver.ConversationEntry{
				Timestamp: ts,
				Role:      driver.RoleToolUse,
				ToolCall: &driver.ToolCallRecord{
					Name:   b.Name,
					Args:   args,
					CallID: b.ID,
				},
			})
		}
	}

	if len(thinking) > 0 {
		block := &driver.ThinkingBlock{Content: strings.Join(thinking, "\n")}
		attached := false
		for i := range entries {
			if entries[i].Role == driver.RoleAssistant {
				entries[i].Thinking = block
				attached = true
				break
			}
		}
		if !attached {
			entries = append(entries, driver.ConversationEntry{
				Timestamp: ts,
				Role:      driver.RoleAssistant,
				Thinking:  block,
				Usage:     usage,
			})
		}
	}

	return entries
}

func flattenTextContent(blocks []ai.ContentBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if text, ok := block.(*ai.TextContent); ok && text.Text != "" {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func tokenUsage(usage ai.Usage) *driver.TokenUsage {
	if usage.Input == 0 && usage.Output == 0 && usage.CacheRead == 0 {
		return nil
	}
	return &driver.TokenUsage{
		InputTokens:  int64(usage.Input),
		OutputTokens: int64(usage.Output),
		CachedTokens: int64(usage.CacheRead),
	}
}

func conversationTimestamp(msg AgentMessage) time.Time {
	if !msg.CreatedAt.IsZero() {
		return msg.CreatedAt
	}
	if msg.Message != nil && msg.Message.GetTimestamp() > 0 {
		return time.UnixMilli(msg.Message.GetTimestamp()).UTC()
	}
	return time.Unix(0, 0).UTC()
}
