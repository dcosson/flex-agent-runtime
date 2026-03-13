package agent

import (
	"sync/atomic"
	"testing"
)

func TestEventBusSubscriberPanicIsolation(t *testing.T) {
	bus := newEventBus()
	var received atomic.Int64

	bus.subscribe(func(AgentEvent) {
		panic("boom")
	})
	bus.subscribe(func(AgentEvent) {
		received.Add(1)
	})

	bus.publish(AgentEvent{Type: EventTurnStarted, SessionID: "s1"})
	if received.Load() != 1 {
		t.Fatalf("expected healthy subscriber to receive event, got %d", received.Load())
	}
}

func TestEventBusUnsubscribe(t *testing.T) {
	bus := newEventBus()
	var count atomic.Int64
	_, unsub := bus.subscribe(func(AgentEvent) { count.Add(1) })

	bus.publish(AgentEvent{Type: EventTurnStarted, SessionID: "s1"})
	unsub()
	bus.publish(AgentEvent{Type: EventTurnStarted, SessionID: "s1"})

	if count.Load() != 1 {
		t.Fatalf("expected exactly one event before unsubscribe, got %d", count.Load())
	}
}
