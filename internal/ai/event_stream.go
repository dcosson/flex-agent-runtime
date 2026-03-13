package ai

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

const defaultEventBufferSize = 32

var ErrStreamClosedWithoutTerminalEvent = errors.New("event stream closed without terminal done/error event")

type resultOrError struct {
	Message AssistantMessage
	Err     error
}

// EventStream wraps a buffered channel for streaming AssistantMessageEvents.
type EventStream struct {
	C <-chan AssistantMessageEvent

	ch         chan AssistantMessageEvent
	result     chan resultOrError
	closeOnce  sync.Once
	terminated atomic.Bool
}

// NewEventStream creates a new EventStream with a buffered channel.
func NewEventStream() *EventStream {
	ch := make(chan AssistantMessageEvent, defaultEventBufferSize)
	return &EventStream{
		C:      ch,
		ch:     ch,
		result: make(chan resultOrError, 1),
	}
}

// Send pushes an event into the stream and publishes terminal result on done/error.
func (s *EventStream) Send(event AssistantMessageEvent) {
	s.ch <- event

	switch event.Type {
	case EventDone:
		if event.Message != nil && s.terminated.CompareAndSwap(false, true) {
			s.result <- resultOrError{Message: *event.Message}
		}
	case EventError:
		if event.Error != nil && s.terminated.CompareAndSwap(false, true) {
			err := providerErrorFromMessage(event.Error)
			if err == nil {
				err = errors.New("provider error")
			}
			s.result <- resultOrError{Message: *event.Error, Err: err}
		}
	}
}

// Close closes the stream and injects a terminal error if none was sent.
func (s *EventStream) Close() {
	s.closeOnce.Do(func() {
		close(s.ch)
		if s.terminated.CompareAndSwap(false, true) {
			s.result <- resultOrError{
				Message: AssistantMessage{
					StopReason:   StopReasonError,
					ErrorMessage: ErrStreamClosedWithoutTerminalEvent.Error(),
					Timestamp:    TimeToMillis(time.Now()),
				},
				Err: ErrStreamClosedWithoutTerminalEvent,
			}
		}
	})
}

// Result blocks until the stream terminal result is available.
func (s *EventStream) Result() (AssistantMessage, error) {
	r := <-s.result
	return r.Message, r.Err
}

// Drain consumes all events and returns the final result.
func (s *EventStream) Drain() (AssistantMessage, error) {
	for range s.C {
	}
	return s.Result()
}
