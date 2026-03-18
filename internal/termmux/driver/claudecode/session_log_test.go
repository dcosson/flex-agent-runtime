package claudecode

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/termmux/driver"
)

func TestParseSessionLog_UserMessage(t *testing.T) {
	input := `{"role":"user","content":"What is Go?"}` + "\n"
	entries, err := ParseSessionLog(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Role != driver.RoleUser {
		t.Errorf("expected role user, got %s", entries[0].Role)
	}
	if entries[0].Content != "What is Go?" {
		t.Errorf("expected content 'What is Go?', got %q", entries[0].Content)
	}
}

func TestParseSessionLog_AssistantWithToolUse(t *testing.T) {
	input := `{"role":"assistant","content":[{"type":"text","text":"Let me read that file."},{"type":"tool_use","id":"call-1","name":"Read","input":{"path":"/foo"}}]}` + "\n"
	entries, err := ParseSessionLog(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (text + tool_use), got %d", len(entries))
	}

	// First entry: text
	if entries[0].Content != "Let me read that file." {
		t.Errorf("expected text content, got %q", entries[0].Content)
	}

	// Second entry: tool_use
	if entries[1].Role != driver.RoleToolUse {
		t.Errorf("expected role tool_use, got %s", entries[1].Role)
	}
	if entries[1].ToolCall == nil {
		t.Fatal("expected tool call data")
	}
	if entries[1].ToolCall.Name != "Read" {
		t.Errorf("expected tool name Read, got %s", entries[1].ToolCall.Name)
	}
	if entries[1].ToolCall.CallID != "call-1" {
		t.Errorf("expected call ID call-1, got %s", entries[1].ToolCall.CallID)
	}
}

func TestParseSessionLog_WithUsage(t *testing.T) {
	input := `{"role":"assistant","content":"Answer.","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":30}}` + "\n"
	entries, err := ParseSessionLog(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Usage == nil {
		t.Fatal("expected usage data")
	}
	if entries[0].Usage.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", entries[0].Usage.InputTokens)
	}
	if entries[0].Usage.OutputTokens != 50 {
		t.Errorf("expected 50 output tokens, got %d", entries[0].Usage.OutputTokens)
	}
	if entries[0].Usage.CachedTokens != 30 {
		t.Errorf("expected 30 cached tokens, got %d", entries[0].Usage.CachedTokens)
	}
}

func TestParseSessionLog_WithThinking(t *testing.T) {
	input := `{"role":"assistant","content":"Yes.","thinking":[{"type":"thinking","content":"Let me think about this..."}]}` + "\n"
	entries, err := ParseSessionLog(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Thinking == nil {
		t.Fatal("expected thinking data")
	}
	if entries[0].Thinking.Content != "Let me think about this..." {
		t.Errorf("expected thinking content, got %q", entries[0].Thinking.Content)
	}
}

func TestParseSessionLog_SkipsMalformed(t *testing.T) {
	input := `{"role":"user","content":"OK"}` + "\n" +
		`this is not json` + "\n" +
		`{"role":"assistant","content":"Done."}` + "\n"

	entries, err := ParseSessionLog(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (skipping malformed), got %d", len(entries))
	}
}

func TestSessionLog_RoundTrip(t *testing.T) {
	now := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)

	original := []driver.ConversationEntry{
		{
			Timestamp: now,
			Role:      driver.RoleUser,
			Content:   "Hello!",
		},
		{
			Timestamp: now.Add(time.Second),
			Role:      driver.RoleAssistant,
			Content:   "Hi there!",
			Usage: &driver.TokenUsage{
				InputTokens:  100,
				OutputTokens: 50,
			},
		},
		{
			Timestamp: now.Add(2 * time.Second),
			Role:      driver.RoleToolUse,
			ToolCall: &driver.ToolCallRecord{
				Name:   "Read",
				Args:   json.RawMessage(`{"path":"/foo"}`),
				CallID: "call-1",
			},
		},
	}

	// Write
	var buf bytes.Buffer
	if err := WriteSessionLog(original, &buf); err != nil {
		t.Fatalf("write error: %v", err)
	}

	// Read back
	parsed, err := ParseSessionLog(&buf)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(parsed) != len(original) {
		t.Fatalf("expected %d entries, got %d", len(original), len(parsed))
	}

	// Verify user message
	if parsed[0].Role != driver.RoleUser {
		t.Errorf("entry 0: expected user, got %s", parsed[0].Role)
	}
	if parsed[0].Content != "Hello!" {
		t.Errorf("entry 0: expected 'Hello!', got %q", parsed[0].Content)
	}

	// Verify assistant message with usage
	if parsed[1].Role != driver.RoleAssistant {
		t.Errorf("entry 1: expected assistant, got %s", parsed[1].Role)
	}
	if parsed[1].Content != "Hi there!" {
		t.Errorf("entry 1: expected 'Hi there!', got %q", parsed[1].Content)
	}
	if parsed[1].Usage == nil {
		t.Error("entry 1: expected usage data")
	} else {
		if parsed[1].Usage.InputTokens != 100 {
			t.Errorf("entry 1: expected 100 input tokens, got %d", parsed[1].Usage.InputTokens)
		}
	}

	// Verify tool use
	if parsed[2].Role != driver.RoleToolUse {
		t.Errorf("entry 2: expected tool_use, got %s", parsed[2].Role)
	}
	if parsed[2].ToolCall == nil {
		t.Fatal("entry 2: expected tool call data")
	}
	if parsed[2].ToolCall.Name != "Read" {
		t.Errorf("entry 2: expected Read, got %s", parsed[2].ToolCall.Name)
	}
}

func TestParseSessionLog_Empty(t *testing.T) {
	entries, err := ParseSessionLog(strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries for empty input, got %d", len(entries))
	}
}

func TestParseSessionLog_MultipleLines(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, `{"role":"user","content":"msg"}`)
	}
	input := strings.Join(lines, "\n") + "\n"

	entries, err := ParseSessionLog(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 100 {
		t.Fatalf("expected 100 entries, got %d", len(entries))
	}
}
