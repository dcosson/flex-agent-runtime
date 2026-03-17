package api

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/anthropics/flex-agent-runtime/internal/agent"
	"github.com/anthropics/flex-agent-runtime/internal/ai"
)

func TestAgentMessageCodecRoundTrip_Table(t *testing.T) {
	baseTime := time.Date(2026, time.March, 17, 9, 30, 0, 0, time.UTC)
	tests := []struct {
		name string
		msg  agent.AgentMessage
	}{
		{
			name: "user text unicode",
			msg: agent.AgentMessage{
				Turn:      1,
				CreatedAt: baseTime,
				Message: &ai.UserMessage{
					Content:   []ai.ContentBlock{&ai.TextContent{Text: "hello\nこんにちは"}},
					Timestamp: 1001,
				},
			},
		},
		{
			name: "assistant multi block",
			msg: agent.AgentMessage{
				Turn:      2,
				CreatedAt: baseTime.Add(time.Second),
				Message: &ai.AssistantMessage{
					Content: []ai.ContentBlock{
						&ai.TextContent{Text: "first", TextSignature: "sig-text"},
						&ai.ThinkingContent{Thinking: "plan", ThinkingSignature: "sig-think"},
						&ai.ToolCall{
							ID:               "tool-1",
							Name:             "read_file",
							Arguments:        map[string]any{"path": "/tmp/a.txt", "recursive": true},
							ThoughtSignature: "sig-tool",
						},
						&ai.ImageContent{Data: "iVBORw0KGgo=", MimeType: "image/png"},
					},
					Model:        "claude-sonnet-4-6",
					StopReason:   ai.StopReasonToolUse,
					ErrorMessage: "",
					Usage: ai.Usage{
						Input:       100,
						Output:      50,
						CacheRead:   10,
						CacheWrite:  5,
						TotalTokens: 165,
						Cost: ai.UsageCost{
							Input:      1.1,
							Output:     2.2,
							CacheRead:  0.3,
							CacheWrite: 0.4,
							Total:      4.0,
						},
					},
					Timestamp: 1002,
				},
			},
		},
		{
			name: "assistant empty content",
			msg: agent.AgentMessage{
				Turn:      3,
				CreatedAt: baseTime.Add(2 * time.Second),
				Message: &ai.AssistantMessage{
					Content:      nil,
					Model:        "claude-sonnet-4-6",
					StopReason:   ai.StopReasonStop,
					ErrorMessage: "none",
					Timestamp:    1003,
				},
			},
		},
		{
			name: "tool result with image block",
			msg: agent.AgentMessage{
				Turn:      4,
				CreatedAt: baseTime.Add(3 * time.Second),
				Message: &ai.ToolResultMessage{
					ToolCallID: "tool-1",
					ToolName:   "read_file",
					Content: []ai.ContentBlock{
						&ai.TextContent{Text: "ok"},
						&ai.ImageContent{Data: "R0lGODlhAQABAAAAACw=", MimeType: "image/gif"},
					},
					IsError:   true,
					Timestamp: 1004,
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			record, err := AgentMessageToRecord(tc.msg)
			if err != nil {
				t.Fatalf("AgentMessageToRecord() error = %v", err)
			}

			decoded, err := RecordToAgentMessage(record)
			if err != nil {
				t.Fatalf("RecordToAgentMessage() error = %v", err)
			}

			assertAgentMessagesEqual(t, tc.msg, decoded)
		})
	}
}

func TestAgentMessageCodecErrors(t *testing.T) {
	t.Run("reject unknown role", func(t *testing.T) {
		_, err := RecordToAgentMessage(AgentMessageRecord{
			Role:    "unknown",
			Content: json.RawMessage(`{}`),
		})
		if err == nil {
			t.Fatalf("expected error for unknown role")
		}
	})

	t.Run("reject malformed json", func(t *testing.T) {
		_, err := RecordToAgentMessage(AgentMessageRecord{
			Role:    AgentMessageRoleAssistant,
			Content: json.RawMessage(`{"content_blocks":[`),
		})
		if err == nil {
			t.Fatalf("expected error for malformed json")
		}
	})

	t.Run("reject user image payload encode", func(t *testing.T) {
		_, err := AgentMessageToRecord(agent.AgentMessage{
			Message: &ai.UserMessage{
				Content: []ai.ContentBlock{
					&ai.ImageContent{Data: "abc", MimeType: "image/png"},
				},
			},
		})
		if err == nil {
			t.Fatalf("expected error for unsupported user image content")
		}
	})
}

func TestPropertyCodecRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		role := rapid.SampledFrom([]string{
			AgentMessageRoleUser,
			AgentMessageRoleAssistant,
			AgentMessageRoleToolResult,
		}).Draw(rt, "role")
		msg := generateRandomAgentMessage(rt, role)

		record, err := AgentMessageToRecord(msg)
		if err != nil {
			rt.Fatalf("AgentMessageToRecord() error = %v", err)
		}
		if record.Role != role {
			rt.Fatalf("record role = %q, want %q", record.Role, role)
		}

		decoded, err := RecordToAgentMessage(record)
		if err != nil {
			rt.Fatalf("RecordToAgentMessage() error = %v", err)
		}

		assertAgentMessagesEqual(t, msg, decoded)
	})
}

func generateRandomAgentMessage(rt *rapid.T, role string) agent.AgentMessage {
	turn := rapid.IntRange(0, 100).Draw(rt, "turn")
	createdUnix := rapid.Int64Range(0, 2_000_000_000).Draw(rt, "created_unix")
	createdAt := time.Unix(createdUnix, 0).UTC()
	timestamp := rapid.Int64Range(0, 2_000_000_000_000).Draw(rt, "msg_ts")

	out := agent.AgentMessage{
		Turn:      turn,
		CreatedAt: createdAt,
	}

	switch role {
	case AgentMessageRoleUser:
		out.Message = &ai.UserMessage{
			Content: []ai.ContentBlock{
				&ai.TextContent{Text: rapid.StringMatching(`[ -~\n\t]{0,128}`).Draw(rt, "user_text")},
			},
			Timestamp: timestamp,
		}
	case AgentMessageRoleAssistant:
		blocks := make([]ai.ContentBlock, 0)
		blockCount := rapid.IntRange(0, 6).Draw(rt, "assistant_block_count")
		for i := 0; i < blockCount; i++ {
			switch rapid.IntRange(0, 3).Draw(rt, fmt.Sprintf("assistant_block_type_%d", i)) {
			case 0:
				blocks = append(blocks, &ai.TextContent{
					Text:          rapid.StringMatching(`[ -~\n\t]{0,64}`).Draw(rt, fmt.Sprintf("assistant_text_%d", i)),
					TextSignature: rapid.StringMatching(`[a-z0-9_-]{0,16}`).Draw(rt, fmt.Sprintf("assistant_text_sig_%d", i)),
				})
			case 1:
				blocks = append(blocks, &ai.ThinkingContent{
					Thinking:          rapid.StringMatching(`[ -~\n\t]{0,64}`).Draw(rt, fmt.Sprintf("assistant_thinking_%d", i)),
					ThinkingSignature: rapid.StringMatching(`[a-z0-9_-]{0,16}`).Draw(rt, fmt.Sprintf("assistant_think_sig_%d", i)),
					Redacted:          rapid.Bool().Draw(rt, fmt.Sprintf("assistant_redacted_%d", i)),
				})
			case 2:
				blocks = append(blocks, &ai.ToolCall{
					ID:   rapid.StringMatching(`[a-z0-9_-]{1,16}`).Draw(rt, fmt.Sprintf("assistant_tool_id_%d", i)),
					Name: rapid.StringMatching(`[a-z_]{1,16}`).Draw(rt, fmt.Sprintf("assistant_tool_name_%d", i)),
					Arguments: map[string]any{
						"arg":   rapid.StringMatching(`[a-z0-9_-]{0,16}`).Draw(rt, fmt.Sprintf("assistant_arg_%d", i)),
						"valid": rapid.Bool().Draw(rt, fmt.Sprintf("assistant_valid_%d", i)),
						"n":     float64(rapid.IntRange(0, 999).Draw(rt, fmt.Sprintf("assistant_n_%d", i))),
					},
					ThoughtSignature: rapid.StringMatching(`[a-z0-9_-]{0,16}`).Draw(rt, fmt.Sprintf("assistant_tool_sig_%d", i)),
				})
			default:
				blocks = append(blocks, &ai.ImageContent{
					Data:     rapid.StringMatching(`[A-Za-z0-9+/=]{0,48}`).Draw(rt, fmt.Sprintf("assistant_image_data_%d", i)),
					MimeType: rapid.SampledFrom([]string{"image/png", "image/jpeg", "image/webp"}).Draw(rt, fmt.Sprintf("assistant_mime_%d", i)),
				})
			}
		}
		out.Message = &ai.AssistantMessage{
			Content:      blocks,
			Model:        rapid.StringMatching(`[a-z0-9._-]{1,24}`).Draw(rt, "assistant_model"),
			StopReason:   rapid.SampledFrom([]ai.StopReason{ai.StopReasonStop, ai.StopReasonLength, ai.StopReasonToolUse, ai.StopReasonError, ai.StopReasonAborted}).Draw(rt, "assistant_stop"),
			ErrorMessage: rapid.StringMatching(`[ -~\n\t]{0,48}`).Draw(rt, "assistant_error"),
			Usage: ai.Usage{
				Input:       rapid.IntRange(0, 10000).Draw(rt, "assistant_usage_input"),
				Output:      rapid.IntRange(0, 10000).Draw(rt, "assistant_usage_output"),
				CacheRead:   rapid.IntRange(0, 10000).Draw(rt, "assistant_usage_cache_read"),
				CacheWrite:  rapid.IntRange(0, 10000).Draw(rt, "assistant_usage_cache_write"),
				TotalTokens: rapid.IntRange(0, 20000).Draw(rt, "assistant_usage_total"),
				Cost: ai.UsageCost{
					Input:      rapid.Float64Range(0, 100).Draw(rt, "assistant_cost_input"),
					Output:     rapid.Float64Range(0, 100).Draw(rt, "assistant_cost_output"),
					CacheRead:  rapid.Float64Range(0, 100).Draw(rt, "assistant_cost_cache_read"),
					CacheWrite: rapid.Float64Range(0, 100).Draw(rt, "assistant_cost_cache_write"),
					Total:      rapid.Float64Range(0, 400).Draw(rt, "assistant_cost_total"),
				},
			},
			Timestamp: timestamp,
		}
	case AgentMessageRoleToolResult:
		blocks := make([]ai.ContentBlock, 0)
		blockCount := rapid.IntRange(0, 5).Draw(rt, "tool_result_block_count")
		for i := 0; i < blockCount; i++ {
			switch rapid.IntRange(0, 1).Draw(rt, fmt.Sprintf("tool_result_block_type_%d", i)) {
			case 0:
				blocks = append(blocks, &ai.TextContent{
					Text:          rapid.StringMatching(`[ -~\n\t]{0,64}`).Draw(rt, fmt.Sprintf("tool_result_text_%d", i)),
					TextSignature: rapid.StringMatching(`[a-z0-9_-]{0,16}`).Draw(rt, fmt.Sprintf("tool_result_text_sig_%d", i)),
				})
			default:
				blocks = append(blocks, &ai.ImageContent{
					Data:     rapid.StringMatching(`[A-Za-z0-9+/=]{0,48}`).Draw(rt, fmt.Sprintf("tool_result_image_data_%d", i)),
					MimeType: rapid.SampledFrom([]string{"image/png", "image/jpeg", "image/webp"}).Draw(rt, fmt.Sprintf("tool_result_mime_%d", i)),
				})
			}
		}
		out.Message = &ai.ToolResultMessage{
			ToolCallID: rapid.StringMatching(`[a-z0-9_-]{1,16}`).Draw(rt, "tool_result_id"),
			ToolName:   rapid.StringMatching(`[a-z_]{1,16}`).Draw(rt, "tool_result_name"),
			Content:    blocks,
			IsError:    rapid.Bool().Draw(rt, "tool_result_is_error"),
			Timestamp:  timestamp,
		}
	}
	return out
}

func assertAgentMessagesEqual(t *testing.T, expected, got agent.AgentMessage) {
	t.Helper()
	wantJSON := canonicalJSON(t, snapshotAgentMessage(expected))
	gotJSON := canonicalJSON(t, snapshotAgentMessage(got))
	if wantJSON != gotJSON {
		t.Fatalf("agent message mismatch\nwant: %s\ngot:  %s", wantJSON, gotJSON)
	}
}

func canonicalJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	b, err = json.Marshal(out)
	if err != nil {
		t.Fatalf("json marshal normalized: %v", err)
	}
	return string(b)
}

func snapshotAgentMessage(msg agent.AgentMessage) map[string]any {
	return map[string]any{
		"turn":       msg.Turn,
		"created_at": msg.CreatedAt.UnixNano(),
		"message":    snapshotAIMessage(msg.Message),
	}
}

func snapshotAIMessage(msg ai.Message) map[string]any {
	switch m := msg.(type) {
	case *ai.UserMessage:
		return map[string]any{
			"role":      "user",
			"timestamp": m.Timestamp,
			"content":   snapshotContentBlocks(m.Content),
		}
	case *ai.AssistantMessage:
		return map[string]any{
			"role":          "assistant",
			"timestamp":     m.Timestamp,
			"content":       snapshotContentBlocks(m.Content),
			"model":         m.Model,
			"stop_reason":   string(m.StopReason),
			"error_message": m.ErrorMessage,
			"usage":         m.Usage,
		}
	case *ai.ToolResultMessage:
		return map[string]any{
			"role":         "tool_result",
			"timestamp":    m.Timestamp,
			"tool_call_id": m.ToolCallID,
			"tool_name":    m.ToolName,
			"is_error":     m.IsError,
			"content":      snapshotContentBlocks(m.Content),
		}
	default:
		return map[string]any{"role": fmt.Sprintf("unsupported:%T", msg)}
	}
}

func snapshotContentBlocks(blocks []ai.ContentBlock) []any {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]any, 0, len(blocks))
	for _, block := range blocks {
		switch b := block.(type) {
		case *ai.TextContent:
			out = append(out, map[string]any{
				"type":           "text",
				"text":           b.Text,
				"text_signature": b.TextSignature,
			})
		case *ai.ThinkingContent:
			out = append(out, map[string]any{
				"type":               "thinking",
				"thinking":           b.Thinking,
				"thinking_signature": b.ThinkingSignature,
				"redacted":           b.Redacted,
			})
		case *ai.ImageContent:
			out = append(out, map[string]any{
				"type":      "image",
				"data":      b.Data,
				"mime_type": b.MimeType,
			})
		case *ai.ToolCall:
			out = append(out, map[string]any{
				"type":              "tool_call",
				"id":                b.ID,
				"name":              b.Name,
				"arguments":         b.Arguments,
				"thought_signature": b.ThoughtSignature,
			})
		default:
			out = append(out, map[string]any{
				"type": fmt.Sprintf("unsupported:%T", block),
			})
		}
	}
	return out
}
