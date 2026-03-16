package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"flex-agent-runtime/internal/ai"
)

type mockDriver struct {
	mu          sync.Mutex
	startCount  int
	stopCount   int
	startErr    error
	subscribers []func(AgentEvent)
}

func (d *mockDriver) Start(context.Context, *Session, string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.startCount++
	return d.startErr
}

func (d *mockDriver) Resume(context.Context, *Session) error { return nil }

func (d *mockDriver) Stop(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopCount++
	return nil
}

func (d *mockDriver) Subscribe(fn func(AgentEvent)) func() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.subscribers = append(d.subscribers, fn)
	idx := len(d.subscribers) - 1
	return func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.subscribers[idx] = nil
	}
}

func TestAgentTransitionValidation(t *testing.T) {
	a := New(nil)
	if err := a.Transition(StateStreaming); err != nil {
		t.Fatalf("idle->streaming should be valid: %v", err)
	}
	if err := a.Transition(StateWaitingFollowUp); err != nil {
		t.Fatalf("streaming->waiting_follow_up should be valid: %v", err)
	}
	if err := a.Transition(StateToolExecution); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("waiting_follow_up->tool_execution should be invalid, got: %v", err)
	}
}

func TestAgentExitedIsTerminal(t *testing.T) {
	a := New(nil)
	if err := a.Transition(StateExited); err != nil {
		t.Fatalf("idle->exited should be valid: %v", err)
	}
	if err := a.Transition(StateIdle); !errors.Is(err, ErrStopped) {
		t.Fatalf("expected ErrStopped after exited, got: %v", err)
	}
}

func TestAgentBusyError(t *testing.T) {
	d := &mockDriver{}
	a := New(d)
	a.running = true
	if err := a.Start(context.Background(), &Session{ID: "s1"}, "prompt"); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
}

func TestAgentSubscribeReceivesSessionScopedEvents(t *testing.T) {
	a := New(nil)
	a.SetSession(&Session{ID: "runtime-1", DriverSessionID: "driver-1"})

	received := make(chan AgentEvent, 1)
	unsub := a.Subscribe(func(evt AgentEvent) {
		received <- evt
	})
	defer unsub()

	a.emit(AgentEvent{Type: EventTurnStarted})

	select {
	case evt := <-received:
		if evt.SessionID != "runtime-1" {
			t.Fatalf("expected runtime session id, got %q", evt.SessionID)
		}
		if evt.DriverSessionID != "driver-1" {
			t.Fatalf("expected driver session id, got %q", evt.DriverSessionID)
		}
	case <-time.After(time.Second):
		t.Fatalf("timeout waiting for event")
	}
}

func TestAppendConversationAndMetrics(t *testing.T) {
	a := New(nil)
	a.SetSession(&Session{ID: "s1"})
	msg := AgentMessage{Turn: 1, Message: &ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}, CreatedAt: time.Now()}
	if err := a.AppendConversation(msg); err != nil {
		t.Fatalf("append: %v", err)
	}
	sess := a.Session()
	if len(sess.ConversationLog) != 1 {
		t.Fatalf("expected 1 conversation message, got %d", len(sess.ConversationLog))
	}
	if sess.Metrics.MessagesAppended != 1 {
		t.Fatalf("expected MessagesAppended=1, got %d", sess.Metrics.MessagesAppended)
	}
}

func TestAbortIdempotentQueueing(t *testing.T) {
	a := New(nil)
	if err := a.Abort("first"); err != nil {
		t.Fatalf("abort #1: %v", err)
	}
	if err := a.Abort("second"); err != nil {
		t.Fatalf("abort #2: %v", err)
	}

	cmds := a.ControlQueue().Drain()
	if len(cmds) != 2 {
		t.Fatalf("expected two queued abort commands, got %d", len(cmds))
	}
	if cmds[0].Type != ControlAbort || cmds[1].Type != ControlAbort {
		t.Fatalf("unexpected command sequence: %+v", cmds)
	}
}

func TestSteerAndFollowUpEmitCorrectEvents(t *testing.T) {
	a := New(nil)
	a.SetSession(&Session{ID: "s1"})
	events := make(chan AgentEvent, 2)
	a.Subscribe(func(evt AgentEvent) {
		if evt.Type == EventSteeringApplied || evt.Type == EventFollowUpEnqueued {
			events <- evt
		}
	})

	if err := a.Steer("change course"); err != nil {
		t.Fatalf("steer: %v", err)
	}
	if err := a.FollowUp("next step"); err != nil {
		t.Fatalf("followup: %v", err)
	}

	e1 := <-events
	e2 := <-events
	if e1.Type != EventSteeringApplied || e1.ControlMessage != "change course" {
		t.Fatalf("unexpected steer event: %+v", e1)
	}
	if e2.Type != EventFollowUpEnqueued || e2.ControlMessage != "next step" {
		t.Fatalf("unexpected follow-up event: %+v", e2)
	}
	if e1.ErrorMessage != "" || e2.ErrorMessage != "" {
		t.Fatalf("control events should not write ErrorMessage")
	}
}
