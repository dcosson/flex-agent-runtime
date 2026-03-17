package agent_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/anthropics/flex-agent-runtime/internal/agent/agenttest"
	agentapi "github.com/anthropics/flex-agent-runtime/internal/agent/api"
	"pgregory.net/rapid"
)

func TestP3_ResumeSessionValidationProperty(t *testing.T) {
	stack := agenttest.NewAgentTestStack(t)
	ctx := context.Background()
	var seq atomic.Uint64

	rapid.Check(t, func(rt *rapid.T) {
		sessionID := fmt.Sprintf("prop-resume-%d", seq.Add(1))
		log := agenttest.GenerateConversationLog(rt, 12)
		malformed := rapid.Bool().Draw(rt, "malformed")
		if malformed && len(log) > 0 {
			log[0].Role = "unknown"
		}

		resp, err := stack.Client.ResumeSession(ctx, &agentapi.ResumeSessionRequest{
			SessionConfig:   stack.BaseConfig(sessionID),
			SchemaVersion:   agentapi.CurrentConversationSchemaVersion,
			ConversationLog: log,
		})
		if malformed && len(log) > 0 {
			if err == nil {
				t.Fatalf("expected resume error for malformed record")
			}
			return
		}
		if err != nil {
			t.Fatalf("resume valid log: %v", err)
		}
		if resp.ConversationLen != len(log) {
			t.Fatalf("conversation len = %d, want %d", resp.ConversationLen, len(log))
		}

		// Clean up session so it doesn't accumulate across iterations.
		_, _ = stack.Client.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{SessionID: sessionID})
	})
}

func TestP6_CrossAgentResumeRoundTripProperty(t *testing.T) {
	stack := agenttest.NewAgentTestStack(t)
	ctx := context.Background()
	var seq atomic.Uint64

	rapid.Check(t, func(rt *rapid.T) {
		sessionID := fmt.Sprintf("prop-roundtrip-%d", seq.Add(1))
		log := agenttest.GenerateConversationLog(rt, 8)

		_, err := stack.Client.ResumeSession(ctx, &agentapi.ResumeSessionRequest{
			SessionConfig:   stack.BaseConfig(sessionID),
			SchemaVersion:   agentapi.CurrentConversationSchemaVersion,
			ConversationLog: log,
		})
		if err != nil {
			t.Fatalf("resume: %v", err)
		}

		recv, err := stack.Client.SendMessage(ctx, &agentapi.SendMessageRequest{SessionID: sessionID, Message: "continue"})
		if err != nil {
			t.Fatalf("send message: %v", err)
		}
		events := agenttest.CollectAllEvents(t, recv)
		if len(events) == 0 {
			t.Fatalf("expected streamed events")
		}
		if events[len(events)-1].Type != agentapi.EventTurnCompleted {
			t.Fatalf("last event = %s, want %s", events[len(events)-1].Type, agentapi.EventTurnCompleted)
		}

		getResp, err := stack.Client.GetSession(ctx, &agentapi.GetAgentSessionRequest{SessionID: sessionID})
		if err != nil {
			t.Fatalf("get session: %v", err)
		}
		if getResp.ConversationLen < len(log) {
			t.Fatalf("conversation shrank: got %d want >= %d", getResp.ConversationLen, len(log))
		}

		// Clean up session so it doesn't accumulate across iterations.
		_, _ = stack.Client.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{SessionID: sessionID})
	})
}
