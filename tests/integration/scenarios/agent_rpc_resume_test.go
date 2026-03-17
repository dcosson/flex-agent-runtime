package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/agent/agenttest"
	agentapi "github.com/anthropics/flex-agent-runtime/internal/agent/api"
	"github.com/anthropics/flex-agent-runtime/internal/ai"
	"github.com/anthropics/flex-agent-runtime/internal/rpc"
	"github.com/anthropics/flex-agent-runtime/internal/termmux/driver"
	"github.com/anthropics/flex-agent-runtime/internal/termmux/driver/claudecode"
)

func TestCR1_AgentRPCResumeRoundTrip(t *testing.T) {
	stack := agenttest.NewAgentTestStack(t)
	log := buildResumeLog(t, 3)

	resp, err := stack.Client.ResumeSession(context.Background(), &agentapi.ResumeSessionRequest{
		SessionConfig:   stack.BaseConfig("cr1"),
		SchemaVersion:   agentapi.CurrentConversationSchemaVersion,
		ConversationLog: log,
	})
	if err != nil {
		t.Fatalf("resume session: %v", err)
	}
	if resp.ConversationLen != len(log) {
		t.Fatalf("conversation len = %d, want %d", resp.ConversationLen, len(log))
	}

	get, err := stack.Client.GetSession(context.Background(), &agentapi.GetAgentSessionRequest{SessionID: "cr1"})
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if get.ConversationLen != len(log) {
		t.Fatalf("get conversation len = %d, want %d", get.ConversationLen, len(log))
	}

	recv, err := stack.Client.SendMessage(context.Background(), &agentapi.SendMessageRequest{SessionID: "cr1", Message: "continue"})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	events := agenttest.CollectAllEvents(t, recv)
	if len(events) == 0 {
		t.Fatalf("expected stream events")
	}
	if events[len(events)-1].Type != agentapi.EventTurnCompleted {
		t.Fatalf("last event = %s, want %s", events[len(events)-1].Type, agentapi.EventTurnCompleted)
	}
}

func TestCR2_RecordToConversationEntryConversion(t *testing.T) {
	base := time.Date(2026, time.March, 17, 11, 45, 0, 0, time.UTC)
	records := []agentapi.AgentMessageRecord{
		mustRecord(t, agentapi.AgentMessage{
			Turn:      1,
			CreatedAt: base,
			Message: &ai.UserMessage{
				Content: []ai.ContentBlock{&ai.TextContent{Text: "Fix the bug in main.go"}},
			},
		}),
		mustRecord(t, agentapi.AgentMessage{
			Turn:      1,
			CreatedAt: base.Add(time.Second),
			Message: &ai.AssistantMessage{
				Content: []ai.ContentBlock{
					&ai.ThinkingContent{Thinking: "I should inspect the file first."},
					&ai.TextContent{Text: "I'll read main.go and patch it."},
					&ai.ToolCall{
						ID:        "tc-1",
						Name:      "read_file",
						Arguments: map[string]any{"path": "main.go"},
					},
				},
				Usage: ai.Usage{Input: 120, Output: 45, CacheRead: 10},
			},
		}),
		mustRecord(t, agentapi.AgentMessage{
			Turn:      1,
			CreatedAt: base.Add(2 * time.Second),
			Message: &ai.ToolResultMessage{
				ToolCallID: "tc-1",
				ToolName:   "read_file",
				Content: []ai.ContentBlock{
					&ai.TextContent{Text: "package main\n\nfunc main() {}"},
				},
			},
		}),
	}

	entries := make([]driver.ConversationEntry, 0, 6)
	for _, rec := range records {
		msg, err := agentapi.RecordToAgentMessage(rec)
		if err != nil {
			t.Fatalf("RecordToAgentMessage: %v", err)
		}
		entries = append(entries, agentapi.AgentMessageToConversationEntries(msg)...)
	}

	if len(entries) < 4 {
		t.Fatalf("entries len = %d, want >= 4", len(entries))
	}
	if entries[0].Role != driver.RoleUser || entries[0].Content != "Fix the bug in main.go" {
		t.Fatalf("entry[0] = %#v, want user text entry", entries[0])
	}

	var hasThinking, hasToolUse, hasToolResult bool
	for _, entry := range entries {
		switch entry.Role {
		case driver.RoleAssistant:
			if entry.Thinking != nil && entry.Thinking.Content != "" {
				hasThinking = true
			}
		case driver.RoleToolUse:
			if entry.ToolCall == nil {
				t.Fatalf("tool_use entry missing tool call: %#v", entry)
			}
			if entry.ToolCall.CallID == "tc-1" && entry.ToolCall.Name == "read_file" {
				var args map[string]any
				if err := json.Unmarshal(entry.ToolCall.Args, &args); err != nil {
					t.Fatalf("unmarshal tool args: %v", err)
				}
				if args["path"] == "main.go" {
					hasToolUse = true
				}
			}
		case driver.RoleToolResult:
			if entry.ToolCall == nil {
				t.Fatalf("tool_result entry missing tool call: %#v", entry)
			}
			if entry.ToolCall.CallID == "tc-1" && entry.ToolCall.Result != "" {
				hasToolResult = true
			}
		}
	}
	if !hasThinking {
		t.Fatalf("missing thinking block conversion")
	}
	if !hasToolUse {
		t.Fatalf("missing tool_use conversion")
	}
	if !hasToolResult {
		t.Fatalf("missing tool_result conversion")
	}

	var buf bytes.Buffer
	if err := claudecode.WriteSessionLog(entries, &buf); err != nil {
		t.Fatalf("WriteSessionLog: %v", err)
	}
	parsed, err := claudecode.ParseSessionLog(&buf)
	if err != nil {
		t.Fatalf("ParseSessionLog: %v", err)
	}

	var parsedToolUse, parsedToolResult bool
	for _, entry := range parsed {
		if entry.Role == driver.RoleToolUse && entry.ToolCall != nil && entry.ToolCall.CallID == "tc-1" && entry.ToolCall.Name == "read_file" {
			parsedToolUse = true
		}
		if entry.Role == driver.RoleToolResult && entry.ToolCall != nil && entry.ToolCall.CallID == "tc-1" && entry.ToolCall.Result != "" {
			parsedToolResult = true
		}
	}
	if !parsedToolUse {
		t.Fatalf("parsed log missing tool_use call tc-1")
	}
	if !parsedToolResult {
		t.Fatalf("parsed log missing tool_result call tc-1")
	}
}

func TestCR3_AgentRPCResumeSchemaMismatchRejected(t *testing.T) {
	stack := agenttest.NewAgentTestStack(t)
	_, err := stack.Client.ResumeSession(context.Background(), &agentapi.ResumeSessionRequest{
		SessionConfig:   stack.BaseConfig("cr3"),
		SchemaVersion:   agentapi.CurrentConversationSchemaVersion + 1,
		ConversationLog: nil,
	})
	agenttest.AssertAgentRPCError(t, err, rpc.CodeInvalidArgument)
}

func mustRecord(t *testing.T, msg agentapi.AgentMessage) agentapi.AgentMessageRecord {
	t.Helper()
	rec, err := agentapi.AgentMessageToRecord(msg)
	if err != nil {
		t.Fatalf("AgentMessageToRecord: %v", err)
	}
	return rec
}
