package ai

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// P1: EventStream ordering guarantee.
func TestEventStreamOrdering(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 500).Draw(t, "eventCount")
		es := NewEventStream()

		events := make([]AssistantMessageEvent, 0, n+1)
		for i := 0; i < n; i++ {
			events = append(events, AssistantMessageEvent{
				Type:         EventTextDelta,
				ContentIndex: i,
				Delta:        fmt.Sprintf("chunk-%d", i),
			})
		}
		events = append(events, AssistantMessageEvent{
			Type:    EventDone,
			Reason:  StopReasonStop,
			Message: &AssistantMessage{Model: "ok"},
		})

		go func() {
			defer es.Close()
			for _, e := range events {
				es.Send(e)
			}
		}()

		i := 0
		for e := range es.C {
			if i >= len(events) {
				t.Fatalf("received too many events")
			}
			if e.Type != events[i].Type {
				t.Fatalf("type mismatch at %d: got %q want %q", i, e.Type, events[i].Type)
			}
			if e.ContentIndex != events[i].ContentIndex {
				t.Fatalf("index mismatch at %d: got %d want %d", i, e.ContentIndex, events[i].ContentIndex)
			}
			i++
		}
		if i != len(events) {
			t.Fatalf("event count mismatch: got %d want %d", i, len(events))
		}
		if _, err := es.Result(); err != nil {
			t.Fatalf("result err: %v", err)
		}
	})
}

// P5: terminal-state guarantee under multiple end conditions.
func TestEventStreamAlwaysTerminates(t *testing.T) {
	cases := []struct {
		name string
		run  func(es *EventStream)
	}{
		{"done_event", func(es *EventStream) {
			es.Send(AssistantMessageEvent{Type: EventDone, Message: &AssistantMessage{Model: "done"}})
		}},
		{"error_event", func(es *EventStream) {
			es.Send(AssistantMessageEvent{Type: EventError, Error: &AssistantMessage{StopReason: StopReasonError, ErrorMessage: "boom"}})
		}},
		{"close_without_terminal", func(es *EventStream) {
			// no terminal event
		}},
		{"panic_in_provider", func(es *EventStream) {
			panic("simulated provider panic")
		}},
		{"double_done", func(es *EventStream) {
			es.Send(AssistantMessageEvent{Type: EventDone, Message: &AssistantMessage{Model: "first"}})
			es.Send(AssistantMessageEvent{Type: EventDone, Message: &AssistantMessage{Model: "second"}})
		}},
		{"done_then_close", func(es *EventStream) {
			es.Send(AssistantMessageEvent{Type: EventDone, Message: &AssistantMessage{Model: "done"}})
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			es := NewEventStream()
			go func() {
				defer func() { _ = recover() }()
				defer es.Close()
				tc.run(es)
			}()

			done := make(chan struct{})
			go func() {
				for range es.C {
				}
				_, _ = es.Result()
				close(done)
			}()

			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("Result() did not unblock")
			}
		})
	}
}

func TestEventStreamCloseIdempotent(t *testing.T) {
	es := NewEventStream()
	es.Close()
	es.Close()
	_, err := es.Result()
	if !errors.Is(err, ErrStreamClosedWithoutTerminalEvent) {
		t.Fatalf("expected close-without-terminal error, got %v", err)
	}
}

func TestEventStreamDrain(t *testing.T) {
	es := NewEventStream()
	go func() {
		defer es.Close()
		es.Send(AssistantMessageEvent{Type: EventTextDelta, Delta: "x"})
		es.Send(AssistantMessageEvent{Type: EventDone, Message: &AssistantMessage{Model: "ok"}})
	}()

	msg, err := es.Drain()
	if err != nil {
		t.Fatalf("drain err: %v", err)
	}
	if msg.Model != "ok" {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

// D1: simulated slow consumer should not deadlock the producer.
func TestEventStreamSlowConsumer(t *testing.T) {
	es := NewEventStream()
	const n = 200

	go func() {
		defer es.Close()
		for i := 0; i < n; i++ {
			es.Send(AssistantMessageEvent{Type: EventTextDelta, ContentIndex: i, Delta: "x"})
		}
		es.Send(AssistantMessageEvent{Type: EventDone, Message: &AssistantMessage{Model: "ok"}})
	}()

	count := 0
	for range es.C {
		count++
		time.Sleep(100 * time.Microsecond)
	}
	if count != n+1 {
		t.Fatalf("got %d events, want %d", count, n+1)
	}
	if _, err := es.Result(); err != nil {
		t.Fatalf("result err: %v", err)
	}
}

// D2: context cancellation timing should still produce terminal result.
func TestEventStreamCancelMidStream(t *testing.T) {
	es := NewEventStream()
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		defer es.Close()
		for i := 0; i < 10_000; i++ {
			select {
			case <-ctx.Done():
				return
			default:
				es.Send(AssistantMessageEvent{Type: EventTextDelta, ContentIndex: i, Delta: "x"})
			}
		}
		es.Send(AssistantMessageEvent{Type: EventDone, Message: &AssistantMessage{Model: "ok"}})
	}()

	time.Sleep(2 * time.Millisecond)
	cancel()
	for range es.C {
	}
	_, err := es.Result()
	if err != nil && !errors.Is(err, ErrStreamClosedWithoutTerminalEvent) {
		t.Fatalf("unexpected err: %v", err)
	}
}

// S1: high-throughput stream path.
func TestEventStreamHighThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping high-throughput in short mode")
	}

	es := NewEventStream()
	const n = 1_000_000

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range es.C {
		}
	}()

	start := time.Now()
	go func() {
		defer es.Close()
		for i := 0; i < n; i++ {
			es.Send(AssistantMessageEvent{Type: EventTextDelta, ContentIndex: i})
		}
		es.Send(AssistantMessageEvent{Type: EventDone, Message: &AssistantMessage{Model: "ok"}})
	}()

	msg, err := es.Result()
	if err != nil {
		t.Fatalf("result err: %v", err)
	}
	if msg.Model != "ok" {
		t.Fatalf("unexpected result")
	}
	wg.Wait()
	if d := time.Since(start); d > 8*time.Second {
		t.Fatalf("throughput too slow: %v", d)
	}
}

// B1 benchmark.
func BenchmarkEventStreamSendReceive(b *testing.B) {
	for i := 0; i < b.N; i++ {
		es := NewEventStream()
		go func() {
			for range es.C {
			}
		}()
		es.Send(AssistantMessageEvent{Type: EventTextDelta, ContentIndex: 1, Delta: "x"})
		es.Send(AssistantMessageEvent{Type: EventDone, Message: &AssistantMessage{Model: "ok"}})
		es.Close()
		_, _ = es.Result()
	}
}
