package agent

import (
	"fmt"
	"sync"
	"time"
)

// ControlCommandType is the serialized control-plane command taxonomy.
type ControlCommandType string

const (
	ControlSteer    ControlCommandType = "steer"
	ControlFollowUp ControlCommandType = "followup"
	ControlAbort    ControlCommandType = "abort"
)

// ControlCommand represents one serialized control-plane request.
type ControlCommand struct {
	Type      ControlCommandType
	Message   string
	CreatedAt time.Time
}

func (c ControlCommand) validate() error {
	switch c.Type {
	case ControlSteer, ControlFollowUp, ControlAbort:
		return nil
	default:
		return fmt.Errorf("unsupported control command type %q", c.Type)
	}
}

// ControlQueue serializes steer/follow-up/abort commands from concurrent callers.
type ControlQueue struct {
	mu     sync.RWMutex
	ch     chan ControlCommand
	closed bool
}

func NewControlQueue(buffer int) *ControlQueue {
	if buffer <= 0 {
		buffer = 64
	}
	return &ControlQueue{ch: make(chan ControlCommand, buffer)}
}

func (q *ControlQueue) C() <-chan ControlCommand {
	return q.ch
}

func (q *ControlQueue) EnqueueSteer(message string) error {
	return q.Enqueue(ControlCommand{Type: ControlSteer, Message: message, CreatedAt: time.Now()})
}

func (q *ControlQueue) EnqueueFollowUp(message string) error {
	return q.Enqueue(ControlCommand{Type: ControlFollowUp, Message: message, CreatedAt: time.Now()})
}

func (q *ControlQueue) EnqueueAbort(message string) error {
	return q.Enqueue(ControlCommand{Type: ControlAbort, Message: message, CreatedAt: time.Now()})
}

func (q *ControlQueue) Enqueue(cmd ControlCommand) error {
	if err := cmd.validate(); err != nil {
		return err
	}

	q.mu.RLock()
	if q.closed {
		q.mu.RUnlock()
		return StoppedError{}
	}
	ch := q.ch
	q.mu.RUnlock()

	select {
	case ch <- cmd:
		return nil
	default:
		return ErrQueueFull
	}
}

func (q *ControlQueue) Drain() []ControlCommand {
	out := make([]ControlCommand, 0)
	for {
		select {
		case cmd, ok := <-q.ch:
			if !ok {
				return out
			}
			out = append(out, cmd)
		default:
			return out
		}
	}
}

func (q *ControlQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	close(q.ch)
}
