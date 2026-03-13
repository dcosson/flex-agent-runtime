package agent

import (
	"errors"
	"testing"
)

func TestControlQueuePreservesFIFO(t *testing.T) {
	q := NewControlQueue(8)
	if err := q.EnqueueSteer("a"); err != nil {
		t.Fatalf("enqueue steer: %v", err)
	}
	if err := q.EnqueueFollowUp("b"); err != nil {
		t.Fatalf("enqueue followup: %v", err)
	}
	if err := q.EnqueueAbort("c"); err != nil {
		t.Fatalf("enqueue abort: %v", err)
	}

	got := q.Drain()
	if len(got) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(got))
	}
	if got[0].Type != ControlSteer || got[1].Type != ControlFollowUp || got[2].Type != ControlAbort {
		t.Fatalf("unexpected order: %+v", got)
	}
}

func TestControlQueueCloseRejectsNewCommands(t *testing.T) {
	q := NewControlQueue(2)
	q.Close()

	err := q.EnqueueSteer("ignored")
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("expected ErrStopped, got %v", err)
	}
}

func TestControlQueueRejectsUnknownCommandType(t *testing.T) {
	q := NewControlQueue(1)
	err := q.Enqueue(ControlCommand{Type: "bad"})
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestControlQueueReturnsErrQueueFullWithoutBlocking(t *testing.T) {
	q := NewControlQueue(1)
	if err := q.EnqueueSteer("first"); err != nil {
		t.Fatalf("enqueue steer: %v", err)
	}

	if err := q.EnqueueFollowUp("second"); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}
}
