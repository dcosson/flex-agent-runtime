package termmux

import (
	"sync"
	"testing"
	"time"
)

// --- scrollbackBuffer tests ---

func TestScrollbackBuffer_BasicWrite(t *testing.T) {
	sb := newScrollbackBuffer(100)

	sb.Write([]byte("hello"))
	snap := sb.Snapshot(100)
	if string(snap) != "hello" {
		t.Errorf("expected 'hello', got %q", string(snap))
	}
}

func TestScrollbackBuffer_MultipleWrites(t *testing.T) {
	sb := newScrollbackBuffer(100)

	sb.Write([]byte("hello "))
	sb.Write([]byte("world"))
	snap := sb.Snapshot(100)
	if string(snap) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(snap))
	}
}

func TestScrollbackBuffer_Wraparound(t *testing.T) {
	sb := newScrollbackBuffer(10)

	sb.Write([]byte("12345"))
	sb.Write([]byte("67890"))
	sb.Write([]byte("ABC"))

	// Buffer is 10 bytes, should contain "890ABC" (wait, let me think...)
	// After "12345": buf = "12345_____", head=5, size=5
	// After "67890": buf = "1234567890", head=0, size=10
	// After "ABC":   buf = "ABC4567890", head=3, size=10
	// Most recent 10 bytes = "4567890ABC"
	snap := sb.Snapshot(100)
	if string(snap) != "4567890ABC" {
		t.Errorf("expected '4567890ABC', got %q", string(snap))
	}
}

func TestScrollbackBuffer_SnapshotMaxBytes(t *testing.T) {
	sb := newScrollbackBuffer(100)

	sb.Write([]byte("hello world this is a long string"))
	snap := sb.Snapshot(5)
	if string(snap) != "tring" {
		t.Errorf("expected last 5 bytes 'tring', got %q", string(snap))
	}
}

func TestScrollbackBuffer_WriteExceedsCapacity(t *testing.T) {
	sb := newScrollbackBuffer(5)

	sb.Write([]byte("1234567890"))
	snap := sb.Snapshot(10)
	if string(snap) != "67890" {
		t.Errorf("expected '67890', got %q", string(snap))
	}
}

func TestScrollbackBuffer_EmptySnapshot(t *testing.T) {
	sb := newScrollbackBuffer(100)

	snap := sb.Snapshot(100)
	if snap != nil {
		t.Errorf("expected nil, got %v", snap)
	}
}

func TestScrollbackBuffer_EmptyWrite(t *testing.T) {
	sb := newScrollbackBuffer(100)

	sb.Write([]byte{})
	if sb.Len() != 0 {
		t.Errorf("expected 0 len after empty write, got %d", sb.Len())
	}
}

// --- TerminalSubscription tests ---

func TestTerminalSubscribers_SubscribeUnsubscribe(t *testing.T) {
	ts := newTerminalSubscribers()

	sub := ts.Subscribe("client-1", []byte("scrollback"), 24, 80)
	if sub.ID != "client-1" {
		t.Errorf("expected id client-1, got %s", sub.ID)
	}
	if string(sub.Scrollback) != "scrollback" {
		t.Errorf("expected scrollback, got %q", string(sub.Scrollback))
	}
	if sub.Rows != 24 || sub.Cols != 80 {
		t.Errorf("expected 24x80, got %dx%d", sub.Rows, sub.Cols)
	}
	if ts.Count() != 1 {
		t.Errorf("expected 1 subscriber, got %d", ts.Count())
	}

	ts.Unsubscribe("client-1")
	if ts.Count() != 0 {
		t.Errorf("expected 0 subscribers after unsubscribe, got %d", ts.Count())
	}

	// done channel should be closed
	select {
	case <-sub.Done:
		// OK
	default:
		t.Error("expected Done channel to be closed after unsubscribe")
	}
}

func TestTerminalSubscribers_FanOut(t *testing.T) {
	ts := newTerminalSubscribers()

	sub1 := ts.Subscribe("c1", nil, 24, 80)
	sub2 := ts.Subscribe("c2", nil, 24, 80)

	ts.FanOut([]byte("hello"))

	select {
	case data := <-sub1.Chunks:
		if string(data) != "hello" {
			t.Errorf("sub1: expected 'hello', got %q", string(data))
		}
	case <-time.After(time.Second):
		t.Fatal("sub1: timeout waiting for chunk")
	}

	select {
	case data := <-sub2.Chunks:
		if string(data) != "hello" {
			t.Errorf("sub2: expected 'hello', got %q", string(data))
		}
	case <-time.After(time.Second):
		t.Fatal("sub2: timeout waiting for chunk")
	}
}

func TestTerminalSubscribers_FanOutNonBlocking(t *testing.T) {
	ts := newTerminalSubscribers()

	// Create a subscriber but don't read from it (simulates slow consumer)
	slow := ts.Subscribe("slow", nil, 24, 80)
	fast := ts.Subscribe("fast", nil, 24, 80)

	// Fill slow subscriber's buffer
	for i := 0; i < SubscriptionBufferSize+10; i++ {
		ts.FanOut([]byte("x"))
	}

	// Fast subscriber should still receive the latest chunks
	received := 0
	for {
		select {
		case <-fast.Chunks:
			received++
		default:
			goto done
		}
	}
done:

	if received == 0 {
		t.Error("fast subscriber received no chunks")
	}

	// Slow subscriber should have some chunks (buffer size)
	slowReceived := 0
	for {
		select {
		case <-slow.Chunks:
			slowReceived++
		default:
			goto slowDone
		}
	}
slowDone:
	if slowReceived > SubscriptionBufferSize {
		t.Errorf("slow subscriber received more than buffer size: %d", slowReceived)
	}
}

func TestTerminalSubscribers_CloseAll(t *testing.T) {
	ts := newTerminalSubscribers()

	sub1 := ts.Subscribe("c1", nil, 24, 80)
	sub2 := ts.Subscribe("c2", nil, 24, 80)

	ts.CloseAll()

	if ts.Count() != 0 {
		t.Errorf("expected 0 subscribers after CloseAll, got %d", ts.Count())
	}

	// Both done channels should be closed
	select {
	case <-sub1.Done:
	default:
		t.Error("sub1.Done not closed")
	}
	select {
	case <-sub2.Done:
	default:
		t.Error("sub2.Done not closed")
	}
}

func TestTerminalSubscribers_FanOutEmptyChunk(t *testing.T) {
	ts := newTerminalSubscribers()
	sub := ts.Subscribe("c1", nil, 24, 80)

	// Empty chunk should be no-op
	ts.FanOut(nil)
	ts.FanOut([]byte{})

	// Subscriber should have no data
	time.Sleep(10 * time.Millisecond)
	select {
	case chunk := <-sub.Chunks:
		t.Errorf("expected no data, got %q", string(chunk))
	default:
		// OK - no data
	}
}

func TestTerminalSubscribers_ConcurrentFanOut(t *testing.T) {
	ts := newTerminalSubscribers()

	const numSubscribers = 10
	subs := make([]*TerminalSubscription, numSubscribers)
	for i := 0; i < numSubscribers; i++ {
		subs[i] = ts.Subscribe(string(rune('A'+i)), nil, 24, 80)
	}

	var wg sync.WaitGroup

	// Fan out from multiple goroutines concurrently
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ts.FanOut([]byte{byte(n)})
		}(i)
	}

	// Simultaneously unsubscribe some
	for i := 0; i < numSubscribers/2; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			ts.Unsubscribe(id)
		}(subs[i].ID)
	}

	wg.Wait()
	// No panic = success
}

func TestTerminalSubscribers_UnsubscribeNonexistent(t *testing.T) {
	ts := newTerminalSubscribers()
	ts.Unsubscribe("nonexistent") // should not panic
}

func TestTerminalSubscribers_FanOutDataIsolation(t *testing.T) {
	ts := newTerminalSubscribers()
	sub := ts.Subscribe("c1", nil, 24, 80)

	original := []byte("hello")
	ts.FanOut(original)

	// Mutate the original — subscriber's copy should be unaffected
	original[0] = 'X'

	select {
	case data := <-sub.Chunks:
		if string(data) != "hello" {
			t.Errorf("expected 'hello', got %q — data was not copied", string(data))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}
