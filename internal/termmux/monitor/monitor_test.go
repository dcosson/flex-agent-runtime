package monitor

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMonitor_StateTransitions(t *testing.T) {
	m := NewAgentMonitor(WithIdleThreshold(100 * time.Millisecond))
	defer m.Close()

	// Initial state
	s, sub := m.State()
	if s != StateInitialized {
		t.Fatalf("expected Initialized, got %s", s)
	}
	if sub != SubStateNone {
		t.Fatalf("expected no sub-state, got %s", sub)
	}

	// Session started
	m.Submit(AgentEvent{
		Type:      EventSessionStarted,
		Timestamp: time.Now(),
		Data:      SessionStartedData{SessionID: "test-1"},
	})
	waitForState(t, m, StateActive, 500*time.Millisecond)
}

func TestMonitor_MetricsAccumulation(t *testing.T) {
	m := NewAgentMonitor()
	defer m.Close()

	m.Submit(AgentEvent{Type: EventSessionStarted, Timestamp: time.Now(), Data: SessionStartedData{SessionID: "s1"}})
	waitForState(t, m, StateActive, 500*time.Millisecond)

	// Submit turn completed
	m.Submit(AgentEvent{
		Type:      EventTurnCompleted,
		Timestamp: time.Now(),
		Data: TurnCompletedData{
			InputTokens:  100,
			OutputTokens: 50,
			CostUSD:      0.01,
		},
	})

	// Submit tool started
	m.Submit(AgentEvent{
		Type:      EventToolStarted,
		Timestamp: time.Now(),
		Data:      ToolStartedData{ToolName: "read_file"},
	})

	// Give the monitor time to process
	time.Sleep(50 * time.Millisecond)

	metrics := m.Metrics()
	if metrics.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", metrics.InputTokens)
	}
	if metrics.OutputTokens != 50 {
		t.Errorf("expected 50 output tokens, got %d", metrics.OutputTokens)
	}
	if metrics.TurnCount != 1 {
		t.Errorf("expected 1 turn, got %d", metrics.TurnCount)
	}
	if metrics.ToolCounts["read_file"] != 1 {
		t.Errorf("expected 1 read_file count, got %d", metrics.ToolCounts["read_file"])
	}
}

func TestMonitor_SubscriberFanOut(t *testing.T) {
	m := NewAgentMonitor()
	defer m.Close()

	ch1 := make(chan AgentEvent, 10)
	ch2 := make(chan AgentEvent, 10)

	_, unsub1 := m.Subscribe(ch1)
	_, unsub2 := m.Subscribe(ch2)
	defer unsub1()
	defer unsub2()

	m.Submit(AgentEvent{
		Type:      EventSessionStarted,
		Timestamp: time.Now(),
		Data:      SessionStartedData{SessionID: "test"},
	})

	// Both subscribers should receive the event (and possibly a state change event)
	received1 := drainChan(ch1, 500*time.Millisecond)
	received2 := drainChan(ch2, 500*time.Millisecond)

	if len(received1) == 0 {
		t.Error("subscriber 1 received no events")
	}
	if len(received2) == 0 {
		t.Error("subscriber 2 received no events")
	}
}

func TestMonitor_SubscriberUnsubscribe(t *testing.T) {
	m := NewAgentMonitor()
	defer m.Close()

	ch := make(chan AgentEvent, 10)
	_, unsub := m.Subscribe(ch)

	// Unsubscribe
	unsub()

	m.Submit(AgentEvent{
		Type:      EventSessionStarted,
		Timestamp: time.Now(),
		Data:      SessionStartedData{SessionID: "test"},
	})

	received := drainChan(ch, 200*time.Millisecond)
	if len(received) != 0 {
		t.Errorf("unsubscribed channel should receive 0 events, got %d", len(received))
	}
}

func TestMonitor_NonBlockingFanOut(t *testing.T) {
	m := NewAgentMonitor()
	defer m.Close()

	// Create a subscriber with a tiny buffer that will fill up
	slowCh := make(chan AgentEvent, 1)
	fastCh := make(chan AgentEvent, 100)

	_, unsub1 := m.Subscribe(slowCh)
	_, unsub2 := m.Subscribe(fastCh)
	defer unsub1()
	defer unsub2()

	// Submit many events — should not block even though slowCh is full
	for i := 0; i < 20; i++ {
		m.Submit(AgentEvent{
			Type:      EventAgentMessage,
			Timestamp: time.Now(),
			Data:      AgentMessageData{Content: "test"},
		})
	}

	// Fast channel should get events
	time.Sleep(100 * time.Millisecond)
	received := drainChan(fastCh, 100*time.Millisecond)
	if len(received) == 0 {
		t.Error("fast subscriber should receive events")
	}
}

func TestMonitor_IdleTransition(t *testing.T) {
	m := NewAgentMonitor(WithIdleThreshold(100 * time.Millisecond))
	defer m.Close()

	m.Submit(AgentEvent{
		Type:      EventSessionStarted,
		Timestamp: time.Now(),
		Data:      SessionStartedData{SessionID: "test"},
	})
	waitForState(t, m, StateActive, 500*time.Millisecond)

	// Wait for idle timeout
	waitForState(t, m, StateIdle, 500*time.Millisecond)
}

func TestMonitor_IdleToActiveOnActivity(t *testing.T) {
	m := NewAgentMonitor(WithIdleThreshold(50 * time.Millisecond))
	defer m.Close()

	m.Submit(AgentEvent{
		Type:      EventSessionStarted,
		Timestamp: time.Now(),
		Data:      SessionStartedData{SessionID: "test"},
	})
	waitForState(t, m, StateActive, 500*time.Millisecond)
	waitForState(t, m, StateIdle, 500*time.Millisecond)

	// Send activity event
	m.Submit(AgentEvent{
		Type:      EventAgentMessage,
		Timestamp: time.Now(),
		Data:      AgentMessageData{Content: "hello"},
	})
	waitForState(t, m, StateActive, 500*time.Millisecond)
}

func TestMonitor_WaitStateChange(t *testing.T) {
	m := NewAgentMonitor()
	defer m.Close()

	ch := m.WaitStateChange()

	m.Submit(AgentEvent{
		Type:      EventSessionStarted,
		Timestamp: time.Now(),
		Data:      SessionStartedData{SessionID: "test"},
	})

	select {
	case <-ch:
		// Expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WaitStateChange did not signal within timeout")
	}
}

func TestMonitor_ConcurrentSubmit(t *testing.T) {
	m := NewAgentMonitor()
	defer m.Close()

	m.Submit(AgentEvent{Type: EventSessionStarted, Timestamp: time.Now(), Data: SessionStartedData{SessionID: "test"}})
	waitForState(t, m, StateActive, 500*time.Millisecond)

	var wg sync.WaitGroup
	var count atomic.Int64

	ch := make(chan AgentEvent, 1000)
	_, unsub := m.Subscribe(ch)
	defer unsub()

	// Concurrent submissions
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Submit(AgentEvent{
				Type:      EventAgentMessage,
				Timestamp: time.Now(),
				Data:      AgentMessageData{Content: "concurrent"},
			})
			count.Add(1)
		}()
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	// Should not have panicked or deadlocked
	if count.Load() != 100 {
		t.Errorf("expected 100 submissions, got %d", count.Load())
	}
}

func TestMonitor_EventWriter(t *testing.T) {
	var written []AgentEvent
	var mu sync.Mutex

	m := NewAgentMonitor(WithEventWriter(func(evt AgentEvent) error {
		mu.Lock()
		written = append(written, evt)
		mu.Unlock()
		return nil
	}))
	defer m.Close()

	m.Submit(AgentEvent{
		Type:      EventSessionStarted,
		Timestamp: time.Now(),
		Data:      SessionStartedData{SessionID: "test"},
	})

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if len(written) == 0 {
		t.Error("event writer was not called")
	}
	mu.Unlock()
}

func TestMonitor_Close(t *testing.T) {
	m := NewAgentMonitor()
	m.Close()
	// Double close should be safe
	m.Close()

	// Submit after close should not panic
	m.Submit(AgentEvent{
		Type:      EventSessionStarted,
		Timestamp: time.Now(),
		Data:      SessionStartedData{SessionID: "test"},
	})
}

// waitForState polls until the monitor reaches the expected state or times out.
func waitForState(t *testing.T, m *AgentMonitor, expected State, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		s, _ := m.State()
		if s == expected {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for state %s (current: %s)", expected, s)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// drainChan reads from a channel until timeout.
func drainChan(ch <-chan AgentEvent, timeout time.Duration) []AgentEvent {
	var events []AgentEvent
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case evt := <-ch:
			events = append(events, evt)
		case <-timer.C:
			return events
		}
	}
}
