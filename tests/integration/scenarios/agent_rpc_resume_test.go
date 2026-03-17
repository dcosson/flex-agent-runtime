package scenarios

import (
	"context"
	"testing"

	"github.com/anthropics/flex-agent-runtime/internal/agent/agenttest"
	agentapi "github.com/anthropics/flex-agent-runtime/internal/agent/api"
	"github.com/anthropics/flex-agent-runtime/internal/rpc"
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

func TestCR3_AgentRPCResumeSchemaMismatchRejected(t *testing.T) {
	stack := agenttest.NewAgentTestStack(t)
	_, err := stack.Client.ResumeSession(context.Background(), &agentapi.ResumeSessionRequest{
		SessionConfig:   stack.BaseConfig("cr3"),
		SchemaVersion:   agentapi.CurrentConversationSchemaVersion + 1,
		ConversationLog: nil,
	})
	agenttest.AssertAgentRPCError(t, err, rpc.CodeInvalidArgument)
}
