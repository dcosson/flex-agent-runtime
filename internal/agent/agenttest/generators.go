package agenttest

import (
	"fmt"
	"time"

	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"pgregory.net/rapid"
)

func GenerateConversationLog(t *rapid.T, maxLen int) []agentapi.AgentMessageRecord {
	if maxLen <= 0 {
		maxLen = 1
	}
	n := rapid.IntRange(0, maxLen).Draw(t, "log_len")
	out := make([]agentapi.AgentMessageRecord, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, GenerateAgentMessageRecord(t, i+1))
	}
	return out
}

func GenerateAgentMessageRecord(t *rapid.T, turn int) agentapi.AgentMessageRecord {
	role := rapid.SampledFrom([]string{"user", "assistant", "tool_result"}).Draw(t, "role")
	createdAt := time.Unix(int64(rapid.Int64Range(0, 1_000_000).Draw(t, "ts")), 0)

	var msg agentapi.AgentMessage
	switch role {
	case "assistant":
		msg = agentapi.AgentMessage{
			Turn: turn,
			Message: &ai.AssistantMessage{
				Content: []ai.ContentBlock{&ai.TextContent{Text: rapid.StringMatching(`[a-z]{1,20}`).Draw(t, "assistant_text")}},
				Model:   "mock-model",
			},
			CreatedAt: createdAt,
		}
	case "tool_result":
		msg = agentapi.AgentMessage{
			Turn: turn,
			Message: &ai.ToolResultMessage{
				ToolCallID: fmt.Sprintf("tool-%d", turn),
				ToolName:   "bash",
				Content:    []ai.ContentBlock{&ai.TextContent{Text: "ok"}},
				IsError:    false,
			},
			CreatedAt: createdAt,
		}
	default:
		msg = agentapi.AgentMessage{
			Turn:      turn,
			Message:   &ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: rapid.StringMatching(`[a-z]{1,20}`).Draw(t, "user_text")}}},
			CreatedAt: createdAt,
		}
	}

	rec, err := agentapi.AgentMessageToRecord(msg)
	if err != nil {
		t.Fatalf("AgentMessageToRecord: %v", err)
	}
	return rec
}
