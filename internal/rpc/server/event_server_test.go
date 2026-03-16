package server

import (
	"context"
	"io"
	"testing"
	"time"

	"flex-agent-runtime/internal/agent"
	"flex-agent-runtime/internal/rpc/api"
)

func TestAgentEventServerStreamAndPublish(t *testing.T) {
	s := NewAgentEventServer()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	recv, err := s.StreamAgentEvents(ctx, &api.StreamAgentEventsRequest{SessionID: "s1"})
	if err != nil {
		t.Fatalf("StreamAgentEvents error = %v", err)
	}
	s.Publish("s1", agent.AgentEvent{Type: agent.EventTurnCompleted, SessionID: "s1", At: time.Now()})
	evt, err := recv.Recv()
	if err != nil {
		t.Fatalf("Recv error = %v", err)
	}
	if evt.Event.Type != agent.EventTurnCompleted {
		t.Fatalf("unexpected event type: %s", evt.Event.Type)
	}
	if err := recv.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestAgentEventServerSender(t *testing.T) {
	s := NewAgentEventServer()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recv, err := s.StreamAgentEvents(ctx, &api.StreamAgentEventsRequest{SessionID: "s1"})
	if err != nil {
		t.Fatalf("StreamAgentEvents error = %v", err)
	}
	sender := s.Sender("s1")
	if err := sender.Send(&api.AgentEventEnvelope{SessionID: "s1", Event: agent.AgentEvent{Type: agent.EventToolStarted}}); err != nil {
		t.Fatalf("Send error = %v", err)
	}
	evt, err := recv.Recv()
	if err != nil {
		t.Fatalf("Recv error = %v", err)
	}
	if evt.Event.Type != agent.EventToolStarted {
		t.Fatalf("unexpected event type: %s", evt.Event.Type)
	}
	cancel()
	_, err = recv.Recv()
	if err == nil {
		t.Fatalf("expected EOF after cancel")
	}
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}
