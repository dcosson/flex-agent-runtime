package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/termmux/driver"
)

func TestAgentMessageToConversationEntries_AssistantToolUseThinking(t *testing.T) {
	ts := time.Date(2026, time.March, 17, 12, 0, 0, 0, time.UTC)
	msg := AgentMessage{
		Turn:      3,
		CreatedAt: ts,
		Message: &ai.AssistantMessage{
			Content: []ai.ContentBlock{
				&ai.ThinkingContent{Thinking: "analyze first"},
				&ai.TextContent{Text: "I'll inspect main.go"},
				&ai.ToolCall{
					ID:        "tc-1",
					Name:      "read_file",
					Arguments: map[string]any{"path": "main.go"},
				},
			},
			Usage: ai.Usage{Input: 11, Output: 7, CacheRead: 3},
		},
	}

	entries := AgentMessageToConversationEntries(msg)
	if len(entries) != 2 {
		t.Fatalf("entries len = %d, want 2", len(entries))
	}

	if entries[0].Timestamp != ts {
		t.Fatalf("entry[0].timestamp = %v, want %v", entries[0].Timestamp, ts)
	}
	if entries[0].Role != driver.RoleAssistant {
		t.Fatalf("entry[0].role = %s, want %s", entries[0].Role, driver.RoleAssistant)
	}
	if entries[0].Content != "I'll inspect main.go" {
		t.Fatalf("entry[0].content = %q", entries[0].Content)
	}
	if entries[0].Thinking == nil || entries[0].Thinking.Content != "analyze first" {
		t.Fatalf("entry[0].thinking = %#v, want analyze first", entries[0].Thinking)
	}
	if entries[0].Usage == nil || entries[0].Usage.InputTokens != 11 || entries[0].Usage.OutputTokens != 7 || entries[0].Usage.CachedTokens != 3 {
		t.Fatalf("entry[0].usage = %#v", entries[0].Usage)
	}

	if entries[1].Role != driver.RoleToolUse {
		t.Fatalf("entry[1].role = %s, want %s", entries[1].Role, driver.RoleToolUse)
	}
	if entries[1].ToolCall == nil {
		t.Fatalf("entry[1].tool_call is nil")
	}
	if entries[1].ToolCall.CallID != "tc-1" || entries[1].ToolCall.Name != "read_file" {
		t.Fatalf("entry[1].tool_call = %#v", entries[1].ToolCall)
	}
	var args map[string]any
	if err := json.Unmarshal(entries[1].ToolCall.Args, &args); err != nil {
		t.Fatalf("unmarshal tool args: %v", err)
	}
	if args["path"] != "main.go" {
		t.Fatalf("tool arg path = %v, want main.go", args["path"])
	}
}

func TestAgentMessageToConversationEntries_ToolResultFlattensText(t *testing.T) {
	msg := AgentMessage{
		CreatedAt: time.Date(2026, time.March, 17, 12, 5, 0, 0, time.UTC),
		Message: &ai.ToolResultMessage{
			ToolCallID: "tc-9",
			ToolName:   "read_file",
			Content: []ai.ContentBlock{
				&ai.TextContent{Text: "line one"},
				&ai.ImageContent{Data: "iVBORw0KGgo=", MimeType: "image/png"},
				&ai.TextContent{Text: "line two"},
			},
		},
	}

	entries := AgentMessageToConversationEntries(msg)
	if len(entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Role != driver.RoleToolResult {
		t.Fatalf("role = %s, want %s", entry.Role, driver.RoleToolResult)
	}
	if entry.Content != "line one\nline two" {
		t.Fatalf("content = %q", entry.Content)
	}
	if entry.ToolCall == nil {
		t.Fatalf("tool_call is nil")
	}
	if entry.ToolCall.CallID != "tc-9" || entry.ToolCall.Name != "read_file" || entry.ToolCall.Result != "line one\nline two" {
		t.Fatalf("tool_call = %#v", entry.ToolCall)
	}
}

func TestAgentMessageToConversationEntries_UsesMessageTimestampWhenCreatedAtMissing(t *testing.T) {
	msg := AgentMessage{
		Message: &ai.UserMessage{
			Content:   []ai.ContentBlock{&ai.TextContent{Text: "hello"}},
			Timestamp: 1_700_000_000_123,
		},
	}

	entries := AgentMessageToConversationEntries(msg)
	if len(entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(entries))
	}
	want := time.UnixMilli(1_700_000_000_123).UTC()
	if !entries[0].Timestamp.Equal(want) {
		t.Fatalf("timestamp = %v, want %v", entries[0].Timestamp, want)
	}
}
