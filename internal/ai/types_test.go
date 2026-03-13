package ai

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMessageRolesAndTimestamps(t *testing.T) {
	now := TimeToMillis(time.Now())

	um := &UserMessage{Timestamp: now}
	if got := um.messageRole(); got != RoleUser {
		t.Fatalf("role mismatch: got %q", got)
	}
	if um.GetTimestamp() != now {
		t.Fatalf("timestamp mismatch")
	}

	am := &AssistantMessage{Timestamp: now}
	if got := am.messageRole(); got != RoleAssistant {
		t.Fatalf("role mismatch: got %q", got)
	}

	tm := &ToolResultMessage{Timestamp: now}
	if got := tm.messageRole(); got != RoleToolResult {
		t.Fatalf("role mismatch: got %q", got)
	}
}

func TestContentTypes(t *testing.T) {
	cases := []struct {
		name string
		got  ContentType
		want ContentType
	}{
		{"text", (&TextContent{}).contentBlockType(), ContentTypeText},
		{"thinking", (&ThinkingContent{}).contentBlockType(), ContentTypeThinking},
		{"image", (&ImageContent{}).contentBlockType(), ContentTypeImage},
		{"tool", (&ToolCall{}).contentBlockType(), ContentTypeToolCall},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %q want %q", tc.got, tc.want)
			}
		})
	}
}

func TestTimeMillisRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	ms := TimeToMillis(now)
	got := MillisToTime(ms)
	if !got.Equal(now) {
		t.Fatalf("round trip mismatch: got %v want %v", got, now)
	}
}

func TestToolParametersJSON(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)
	tool := Tool{Name: "x", Parameters: raw}
	if string(tool.Parameters) != string(raw) {
		t.Fatalf("parameters changed")
	}
}
