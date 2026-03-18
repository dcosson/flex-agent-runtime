package rpctest

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/agent"
	"github.com/dcosson/flex-agent-runtime/internal/agent/agenttest"
	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
)

func TestEF1_AgentRPCNoDroppedEvents(t *testing.T) {
	stack := agenttest.NewAgentTestStack(t)
	const messageCount = 500
	script := make([]agent.AgentEvent, 0, messageCount+2)
	script = append(script, agent.AgentEvent{Type: agent.EventTurnStarted, Turn: 1})
	for i := 0; i < messageCount; i++ {
		script = append(script, agent.AgentEvent{
			Type:     agent.EventAgentMessageDelta,
			Turn:     1,
			Delta:    "chunk",
			Metadata: map[string]any{"seq": i},
		})
	}
	script = append(script, agent.AgentEvent{Type: agent.EventTurnCompleted, Turn: 1})
	stack.DriverFactory.SetDriver("default", &agenttest.MockAgentDriver{TurnScript: script})

	if _, err := stack.CreateAgentSession(context.Background(), "ef1"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	recv, err := stack.Client.SendMessage(context.Background(), &agentapi.SendMessageRequest{SessionID: "ef1", Message: "go"})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	events := agenttest.CollectAllEvents(t, recv)

	deltas := 0
	for _, evt := range events {
		if evt.Type == agentapi.EventAgentMessageDelta {
			deltas++
		}
	}
	if deltas != messageCount {
		t.Fatalf("delta count = %d, want %d", deltas, messageCount)
	}
	if events[len(events)-1].Type != agentapi.EventTurnCompleted {
		t.Fatalf("last event = %s, want %s", events[len(events)-1].Type, agentapi.EventTurnCompleted)
	}
}

func TestEF2_TerminalEventGuarantees(t *testing.T) {
	stack := agenttest.NewAgentTestStack(t)
	stack.DriverFactory.SetDriver("default", &agenttest.MockAgentDriver{TurnScript: []agent.AgentEvent{
		{Type: agent.EventTurnStarted, Turn: 1},
		{Type: agent.EventAgentMessageDelta, Turn: 1, Delta: "done"},
		{Type: agent.EventTurnCompleted, Turn: 1},
	}})

	if _, err := stack.CreateAgentSession(context.Background(), "ef2"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	recv, err := stack.Client.SendMessage(context.Background(), &agentapi.SendMessageRequest{SessionID: "ef2", Message: "finish"})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}

	for {
		_, err := recvWithTimeout(recv, 2*time.Second)
		if err == nil {
			continue
		}
		if !errors.Is(err, io.EOF) {
			t.Fatalf("terminal recv error = %v, want io.EOF", err)
		}
		break
	}

	for i := 0; i < 2; i++ {
		_, err := recvWithTimeout(recv, 2*time.Second)
		if !errors.Is(err, io.EOF) {
			t.Fatalf("recv after EOF #%d = %v, want io.EOF", i+1, err)
		}
	}
}

func TestEF3_EventOrderingAcrossRPC(t *testing.T) {
	stack := agenttest.NewAgentTestStack(t)
	const eventCount = 200
	script := []agent.AgentEvent{{Type: agent.EventTurnStarted, Turn: 1}}
	for i := 0; i < eventCount; i++ {
		script = append(script, agent.AgentEvent{Type: agent.EventAgentMessageDelta, Turn: 1, Delta: "d", Metadata: map[string]any{"seq": i}})
	}
	script = append(script, agent.AgentEvent{Type: agent.EventTurnCompleted, Turn: 1})
	stack.DriverFactory.SetDriver("default", &agenttest.MockAgentDriver{TurnScript: script})

	if _, err := stack.CreateAgentSession(context.Background(), "ef3"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	recv, err := stack.Client.SendMessage(context.Background(), &agentapi.SendMessageRequest{SessionID: "ef3", Message: "ordered"})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	events := agenttest.CollectAllEvents(t, recv)

	seq := 0
	for _, evt := range events {
		if evt.Type != agentapi.EventAgentMessageDelta {
			continue
		}
		raw, ok := evt.Metadata["seq"]
		if !ok {
			t.Fatalf("delta event missing seq metadata")
		}
		v, ok := raw.(float64) // JSON codec decodes numbers as float64
		if !ok {
			t.Fatalf("seq metadata type = %T", raw)
		}
		if int(v) != seq {
			t.Fatalf("out of order seq: got %d want %d", int(v), seq)
		}
		seq++
	}
	if seq != eventCount {
		t.Fatalf("ordered delta count = %d, want %d", seq, eventCount)
	}
}

func TestEF4_DestroySessionClosesSubscriberStream(t *testing.T) {
	stack := agenttest.NewAgentTestStack(t)
	if _, err := stack.CreateAgentSession(context.Background(), "ef4"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	sub, err := stack.Client.SubscribeEvents(context.Background(), &agentapi.SubscribeEventsRequest{SessionID: "ef4"})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()

	// SubscribeEvents pushes an initial state_change event to flush RPC
	// response headers.  Drain it before testing the destroy-EOF path.
	initEvt, initErr := sub.Recv()
	if initErr != nil {
		t.Fatalf("expected initial event, got error: %v", initErr)
	}
	if initEvt == nil {
		t.Fatalf("expected initial event, got nil")
	}

	if _, err := stack.Client.DestroySession(context.Background(), &agentapi.DestroyAgentSessionRequest{SessionID: "ef4"}); err != nil {
		t.Fatalf("destroy: %v", err)
	}

	err = agenttest.WaitForReceiverEOF(sub, 5*time.Second)
	if err != nil {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func BenchmarkB3_AgentRPCOverheadVsInProcess(b *testing.B) {
	stack := agenttest.NewAgentTestStack(b)
	if _, err := stack.CreateAgentSession(context.Background(), "bench-b3"); err != nil {
		b.Fatalf("create session: %v", err)
	}
	ctx := context.Background()
	req := &agentapi.GetAgentSessionRequest{SessionID: "bench-b3"}

	b.Run("rpc", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := stack.Client.GetSession(ctx, req); err != nil {
				b.Fatalf("rpc get session: %v", err)
			}
		}
	})

	b.Run("inprocess", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := stack.Service.GetSession(ctx, req); err != nil {
				b.Fatalf("in-process get session: %v", err)
			}
		}
	})
}

func recvWithTimeout(recv agentapi.EventReceiver, timeout time.Duration) (*agentapi.AgentEvent, error) {
	type result struct {
		evt *agentapi.AgentEvent
		err error
	}
	done := make(chan result, 1)
	go func() {
		evt, err := recv.Recv()
		done <- result{evt: evt, err: err}
	}()
	select {
	case out := <-done:
		return out.evt, out.err
	case <-time.After(timeout):
		return nil, context.DeadlineExceeded
	}
}
