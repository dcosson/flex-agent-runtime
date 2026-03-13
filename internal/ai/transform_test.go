package ai

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"pgregory.net/rapid"
)

// P2: TransformMessages idempotency.
func TestTransformIdempotency(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		msgs := generateRandomConversation(t)
		model := drawRandomModel(t)
		once := TransformMessages(msgs, model, nil)
		twice := TransformMessages(once, model, nil)
		if !reflect.DeepEqual(once, twice) {
			t.Fatalf("transform should be idempotent")
		}
	})
}

// P6: transformed message count bound.
func TestTransformMessageCountBound(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		msgs := generateRandomConversation(t)
		model := drawRandomModel(t)
		out := TransformMessages(msgs, model, nil)
		if len(out) > len(msgs)+toolCallCount(msgs) {
			t.Fatalf("output too large: got=%d in=%d toolcalls=%d", len(out), len(msgs), toolCallCount(msgs))
		}
	})
}

// P8: deterministic output with orphaned tool calls.
func TestTransformDeterministic(t *testing.T) {
	msgs := []Message{
		&AssistantMessage{
			Provider: "anthropic",
			API:      "anthropic-messages",
			Model:    "claude-sonnet-4-20250514",
			Content: []ContentBlock{
				&ToolCall{ID: "z-call", Name: "toolZ", Arguments: map[string]any{}},
				&ToolCall{ID: "a-call", Name: "toolA", Arguments: map[string]any{}},
				&ToolCall{ID: "m-call", Name: "toolM", Arguments: map[string]any{}},
			},
			StopReason: StopReasonToolUse,
			Timestamp:  1000,
		},
		&UserMessage{Content: []ContentBlock{&TextContent{Text: "continue"}}, Timestamp: 2000},
	}
	model := Model{ID: "other", Provider: "openai", API: "openai-completions"}
	first := TransformMessages(msgs, model, nil)
	for i := 0; i < 100; i++ {
		result := TransformMessages(msgs, model, nil)
		if !reflect.DeepEqual(first, result) {
			fb, _ := json.Marshal(first)
			rb, _ := json.Marshal(result)
			t.Fatalf("iteration %d produced different output\nfirst=%s\nresult=%s", i, fb, rb)
		}
	}
}

func TestTransformBehaviorCrossModel(t *testing.T) {
	target := Model{ID: "gpt-4o", Provider: "openai", API: "openai-completions"}
	msgs := []Message{
		&AssistantMessage{
			Provider: "anthropic", API: "anthropic-messages", Model: "claude-sonnet",
			StopReason: StopReasonStop,
			Content: []ContentBlock{
				&TextContent{Text: "hello", TextSignature: "sig"},
				&ThinkingContent{Thinking: "chain", ThinkingSignature: "ts", Redacted: false},
				&ThinkingContent{Thinking: "hidden", Redacted: true},
				&ToolCall{ID: "tc1", Name: "search", Arguments: map[string]any{"q": "x"}, ThoughtSignature: "keep?"},
			},
			Timestamp: 42,
		},
		&ToolResultMessage{ToolCallID: "tc1", ToolName: "search", Content: []ContentBlock{&TextContent{Text: "ok"}}, Timestamp: 43},
	}
	norm := func(id string, _ Model, _ *AssistantMessage) string { return "norm-" + id }
	out := TransformMessages(msgs, target, norm)
	if len(out) != 2 {
		t.Fatalf("unexpected len=%d", len(out))
	}
	am, ok := out[0].(*AssistantMessage)
	if !ok {
		t.Fatalf("expected assistant message")
	}
	if len(am.Content) != 3 { // text + converted thinking + toolcall (redacted dropped)
		t.Fatalf("unexpected content len=%d", len(am.Content))
	}
	if tc, ok := am.Content[2].(*ToolCall); !ok || tc.ID != "norm-tc1" || tc.ThoughtSignature != "" {
		t.Fatalf("tool call normalization/signature stripping failed: %#v", am.Content[2])
	}
	tr, ok := out[1].(*ToolResultMessage)
	if !ok || tr.ToolCallID != "norm-tc1" {
		t.Fatalf("tool result id remap failed: %#v", out[1])
	}
}

func TestTransformSkipsErrorAndAbortedAssistant(t *testing.T) {
	target := Model{ID: "x", Provider: "p", API: "a"}
	msgs := []Message{
		&AssistantMessage{StopReason: StopReasonError, Content: []ContentBlock{&TextContent{Text: "err"}}},
		&AssistantMessage{StopReason: StopReasonAborted, Content: []ContentBlock{&TextContent{Text: "aborted"}}},
		&UserMessage{Content: []ContentBlock{&TextContent{Text: "u"}}},
	}
	out := TransformMessages(msgs, target, nil)
	if len(out) != 1 {
		t.Fatalf("expected only user message, got %d", len(out))
	}
	if _, ok := out[0].(*UserMessage); !ok {
		t.Fatalf("expected user message")
	}
}

func TestTransformKeepsEmptyAssistantCrossModel(t *testing.T) {
	target := Model{ID: "other", Provider: "other", API: "other-api"}
	msgs := []Message{
		&AssistantMessage{
			Content: []ContentBlock{
				&ThinkingContent{Redacted: true},
				&ThinkingContent{Thinking: ""},
			},
			Provider:   "src",
			API:        "src-api",
			Model:      "src-model",
			StopReason: StopReasonStop,
			Timestamp:  111,
		},
	}

	out := TransformMessages(msgs, target, nil)
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	am, ok := out[0].(*AssistantMessage)
	if !ok {
		t.Fatalf("expected assistant message, got %T", out[0])
	}
	if len(am.Content) != 0 {
		t.Fatalf("expected empty content to be preserved, got %d blocks", len(am.Content))
	}
}

// B3 benchmark.
func BenchmarkTransformMessages(b *testing.B) {
	msgs := make([]Message, 0, 50)
	for i := 0; i < 25; i++ {
		msgs = append(msgs, &UserMessage{Content: []ContentBlock{&TextContent{Text: fmt.Sprintf("u-%d", i)}}, Timestamp: int64(i)})
		msgs = append(msgs, &AssistantMessage{
			Provider: "anthropic", API: "anthropic-messages", Model: "claude-sonnet",
			StopReason: StopReasonStop,
			Content: []ContentBlock{
				&TextContent{Text: fmt.Sprintf("a-%d", i)},
				&ToolCall{ID: fmt.Sprintf("tc-%d", i), Name: "tool", Arguments: map[string]any{"i": i}},
			},
			Timestamp: int64(i),
		})
	}
	model := Model{ID: "gpt-4o", Provider: "openai", API: "openai-completions"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = TransformMessages(msgs, model, nil)
	}
}

func generateRandomConversation(t *rapid.T) []Message {
	n := rapid.IntRange(1, 40).Draw(t, "n")
	msgs := make([]Message, 0, n)
	for i := 0; i < n; i++ {
		switch rapid.IntRange(0, 2).Draw(t, "kind") {
		case 0:
			msgs = append(msgs, &UserMessage{Content: []ContentBlock{&TextContent{Text: rapid.String().Draw(t, "txt")}}, Timestamp: int64(i + 1)})
		case 1:
			stop := []StopReason{StopReasonStop, StopReasonToolUse, StopReasonError, StopReasonAborted}[rapid.IntRange(0, 3).Draw(t, "stop")]
			content := []ContentBlock{&TextContent{Text: rapid.String().Draw(t, "a")}}
			if rapid.Bool().Draw(t, "tc") {
				content = append(content, &ToolCall{ID: fmt.Sprintf("tc-%d-%d", i, rapid.Int().Draw(t, "x")), Name: "tool", Arguments: map[string]any{"k": i}})
			}
			msgs = append(msgs, &AssistantMessage{Provider: "p", API: "a", Model: "m", StopReason: stop, Content: content, Timestamp: int64(i + 1), ErrorMessage: "too many tokens"})
		case 2:
			msgs = append(msgs, &ToolResultMessage{ToolCallID: fmt.Sprintf("tc-%d", rapid.IntRange(0, i+1).Draw(t, "id")), ToolName: "tool", Content: []ContentBlock{&TextContent{Text: "r"}}, Timestamp: int64(i + 1)})
		}
	}
	return msgs
}

func drawRandomModel(t *rapid.T) Model {
	apis := []string{"anthropic-messages", "openai-completions", "google-genai"}
	providers := []string{"anthropic", "openai", "google"}
	i := rapid.IntRange(0, 2).Draw(t, "api")
	return Model{ID: fmt.Sprintf("m-%d", i), API: apis[i], Provider: providers[i]}
}

func toolCallCount(msgs []Message) int {
	c := 0
	for _, m := range msgs {
		if am, ok := m.(*AssistantMessage); ok {
			for _, b := range am.Content {
				if _, ok := b.(*ToolCall); ok {
					c++
				}
			}
		}
	}
	return c
}
