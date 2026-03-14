package termmux

import (
	"sync"
)

const (
	// SubscriptionBufferSize is the number of output chunks buffered per subscriber.
	// At ~4KB per chunk, this allows ~256KB of buffered output before drops.
	SubscriptionBufferSize = 64

	// MaxScrollbackSnapshotBytes caps the scrollback snapshot sent to subscribers.
	// At 4MB, this fits within ConnectRPC's default 16MB message limit while
	// providing substantial history (~40K lines of typical terminal output).
	MaxScrollbackSnapshotBytes = 4 * 1024 * 1024

	// maxScrollbackBytes is the internal scrollback buffer capacity.
	maxScrollbackBytes = 8 * 1024 * 1024 // 8MB ring buffer
)

// TerminalSubscription is a per-subscriber output stream with bounded buffering.
// Each subscriber gets its own ring buffer of raw PTY output chunks. Slow
// consumers drop oldest chunks rather than blocking the output pipeline.
type TerminalSubscription struct {
	ID     string
	Chunks chan []byte   // Buffered channel acts as bounded queue
	Done   chan struct{} // Closed when subscription is cancelled

	// Scrollback snapshot delivered on subscribe
	Scrollback []byte
	Rows, Cols int
}

// scrollbackBuffer is a bounded byte ring buffer for terminal scrollback.
// It stores raw PTY output bytes for replay to late-attaching subscribers.
// Thread safety is provided by the VirtualTerminal mutex.
type scrollbackBuffer struct {
	buf  []byte
	head int // write position (wraps)
	size int // bytes currently stored (up to cap)
	cap  int // maximum capacity
}

func newScrollbackBuffer(capacity int) *scrollbackBuffer {
	return &scrollbackBuffer{
		buf: make([]byte, capacity),
		cap: capacity,
	}
}

// Write appends data to the scrollback buffer, overwriting oldest bytes.
func (sb *scrollbackBuffer) Write(data []byte) {
	if len(data) == 0 {
		return
	}

	// If data is larger than capacity, only keep the tail
	if len(data) >= sb.cap {
		copy(sb.buf, data[len(data)-sb.cap:])
		sb.head = 0
		sb.size = sb.cap
		return
	}

	// Write data, wrapping around
	n := copy(sb.buf[sb.head:], data)
	if n < len(data) {
		copy(sb.buf, data[n:])
	}
	sb.head = (sb.head + len(data)) % sb.cap
	sb.size += len(data)
	if sb.size > sb.cap {
		sb.size = sb.cap
	}
}

// Snapshot returns a copy of the most recent bytes, up to maxBytes.
func (sb *scrollbackBuffer) Snapshot(maxBytes int) []byte {
	if sb.size == 0 {
		return nil
	}

	take := sb.size
	if take > maxBytes {
		take = maxBytes
	}

	result := make([]byte, take)

	// Calculate start position for the most recent 'take' bytes
	start := (sb.head - take + sb.cap) % sb.cap
	if start+take <= sb.cap {
		copy(result, sb.buf[start:start+take])
	} else {
		// Wraps around
		firstPart := sb.cap - start
		copy(result, sb.buf[start:])
		copy(result[firstPart:], sb.buf[:take-firstPart])
	}

	return result
}

// Len returns the number of bytes in the buffer.
func (sb *scrollbackBuffer) Len() int {
	return sb.size
}

// terminalSubscribers manages the set of active terminal subscribers on a session.
type terminalSubscribers struct {
	mu   sync.RWMutex
	subs map[string]*TerminalSubscription
}

func newTerminalSubscribers() *terminalSubscribers {
	return &terminalSubscribers{
		subs: make(map[string]*TerminalSubscription),
	}
}

// Subscribe creates a new terminal subscription.
func (ts *terminalSubscribers) Subscribe(id string, scrollback []byte, rows, cols int) *TerminalSubscription {
	sub := &TerminalSubscription{
		ID:         id,
		Chunks:     make(chan []byte, SubscriptionBufferSize),
		Done:       make(chan struct{}),
		Scrollback: scrollback,
		Rows:       rows,
		Cols:       cols,
	}

	ts.mu.Lock()
	ts.subs[id] = sub
	ts.mu.Unlock()

	return sub
}

// Unsubscribe removes a terminal subscription and signals done.
func (ts *terminalSubscribers) Unsubscribe(id string) {
	ts.mu.Lock()
	sub, ok := ts.subs[id]
	if ok {
		delete(ts.subs, id)
		close(sub.Done)
	}
	ts.mu.Unlock()
}

// FanOut sends a chunk to all subscribers. Non-blocking: slow subscribers
// have their oldest chunk dropped to make room.
func (ts *terminalSubscribers) FanOut(chunk []byte) {
	if len(chunk) == 0 {
		return
	}

	// Make a copy for each subscriber since the underlying buffer may be reused
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	for _, sub := range ts.subs {
		cp := make([]byte, len(chunk))
		copy(cp, chunk)

		select {
		case sub.Chunks <- cp:
		default:
			// Subscriber is slow — drop oldest chunk to make room
			select {
			case <-sub.Chunks:
			default:
			}
			// Retry once
			select {
			case sub.Chunks <- cp:
			default:
			}
		}
	}
}

// CloseAll signals done to all remaining subscribers.
func (ts *terminalSubscribers) CloseAll() {
	ts.mu.Lock()
	for id, sub := range ts.subs {
		close(sub.Done)
		delete(ts.subs, id)
	}
	ts.mu.Unlock()
}

// Count returns the number of active subscribers.
func (ts *terminalSubscribers) Count() int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return len(ts.subs)
}
