package monitor

import (
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"
)

const (
	defaultEventBufferSize = 256
	defaultIdleThreshold   = 2 * time.Second
)

// AgentMonitor processes normalized events, maintains the state machine,
// tracks metrics, and fans out events to subscribers.
type AgentMonitor struct {
	events     chan AgentEvent
	writeEvent func(AgentEvent) error

	mu             sync.RWMutex
	state          State
	subState       SubState
	stateChangedAt time.Time
	stateCh        chan struct{} // Closed+replaced on state change

	// Accumulated metrics
	inputTokens  int64
	outputTokens int64
	totalCostUSD float64
	turnCount    int64
	toolCounts   map[string]int64

	// Subscriber fan-out
	subscribers   []subscriber
	subscribersMu sync.RWMutex

	// Idle detection
	idleThreshold time.Duration
	idleTimer     *time.Timer
	idleTimerMu   sync.Mutex

	// Lifecycle
	done chan struct{}
	once sync.Once
}

type subscriber struct {
	id int
	ch chan<- AgentEvent
}

// Metrics is a snapshot of accumulated metrics.
type Metrics struct {
	InputTokens  int64
	OutputTokens int64
	TotalCostUSD float64
	TurnCount    int64
	ToolCounts   map[string]int64
}

// MonitorOption configures the AgentMonitor.
type MonitorOption func(*AgentMonitor)

// WithEventWriter sets a function called for each processed event (e.g., JSONL persistence).
func WithEventWriter(fn func(AgentEvent) error) MonitorOption {
	return func(m *AgentMonitor) {
		m.writeEvent = fn
	}
}

// WithIdleThreshold sets the idle timeout duration.
func WithIdleThreshold(d time.Duration) MonitorOption {
	return func(m *AgentMonitor) {
		m.idleThreshold = d
	}
}

// NewAgentMonitor creates and starts a new monitor.
func NewAgentMonitor(opts ...MonitorOption) *AgentMonitor {
	m := &AgentMonitor{
		events:         make(chan AgentEvent, defaultEventBufferSize),
		state:          StateInitialized,
		stateChangedAt: time.Now(),
		stateCh:        make(chan struct{}),
		toolCounts:     make(map[string]int64),
		idleThreshold:  defaultIdleThreshold,
		done:           make(chan struct{}),
	}
	for _, opt := range opts {
		opt(m)
	}
	go m.processLoop()
	return m
}

// Submit sends an event to the monitor for processing.
// Non-blocking: drops the event if the buffer is full.
func (m *AgentMonitor) Submit(evt AgentEvent) {
	select {
	case m.events <- evt:
	default:
		// Buffer full — drop rather than block.
	}
}

// State returns the current state and sub-state.
func (m *AgentMonitor) State() (State, SubState) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state, m.subState
}

// StateChangedAt returns the timestamp of the last state change.
func (m *AgentMonitor) StateChangedAt() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stateChangedAt
}

// WaitStateChange returns a channel that is closed when the state changes.
// Callers should call State() first, then wait on the returned channel for changes.
func (m *AgentMonitor) WaitStateChange() <-chan struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stateCh
}

// Metrics returns a snapshot of accumulated metrics.
func (m *AgentMonitor) Metrics() Metrics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tc := make(map[string]int64, len(m.toolCounts))
	for k, v := range m.toolCounts {
		tc[k] = v
	}
	return Metrics{
		InputTokens:  m.inputTokens,
		OutputTokens: m.outputTokens,
		TotalCostUSD: m.totalCostUSD,
		TurnCount:    m.turnCount,
		ToolCounts:   tc,
	}
}

// Subscribe registers a channel to receive events. Returns an ID and an
// unsubscribe function. The channel must be buffered — events are dropped
// for slow subscribers.
func (m *AgentMonitor) Subscribe(ch chan<- AgentEvent) (int, func()) {
	m.subscribersMu.Lock()
	defer m.subscribersMu.Unlock()

	id := len(m.subscribers)
	m.subscribers = append(m.subscribers, subscriber{id: id, ch: ch})

	return id, func() {
		m.subscribersMu.Lock()
		defer m.subscribersMu.Unlock()
		for i, s := range m.subscribers {
			if s.id == id {
				m.subscribers = append(m.subscribers[:i], m.subscribers[i+1:]...)
				break
			}
		}
	}
}

// Close shuts down the monitor. Safe to call multiple times.
func (m *AgentMonitor) Close() {
	m.once.Do(func() {
		close(m.done)
	})
}

func (m *AgentMonitor) processLoop() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic recovered in monitor processLoop: %v\n%s\n", r, debug.Stack())
		}
	}()

	for {
		select {
		case evt := <-m.events:
			m.processEvent(evt)
		case <-m.done:
			// Drain remaining events
			for {
				select {
				case evt := <-m.events:
					m.processEvent(evt)
				default:
					m.stopIdleTimer()
					return
				}
			}
		}
	}
}

func (m *AgentMonitor) processEvent(evt AgentEvent) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic recovered in processEvent: %v\n%s\n", r, debug.Stack())
		}
	}()

	// Update state machine
	m.updateState(evt)

	// Update metrics
	m.updateMetrics(evt)

	// Persist event
	if m.writeEvent != nil {
		if err := m.writeEvent(evt); err != nil {
			fmt.Fprintf(os.Stderr, "monitor: event write error: %v\n", err)
		}
	}

	// Fan-out to subscribers (non-blocking)
	m.fanOut(evt)
}

func (m *AgentMonitor) updateState(evt AgentEvent) {
	var te TransitionEvent
	switch evt.Type {
	case EventSessionStarted:
		te = TransitionSessionStarted
	case EventSessionEnded:
		te = TransitionSessionEnded
	case EventToolStarted:
		te = TransitionToolStarted
	case EventToolCompleted:
		te = TransitionToolCompleted
	case EventApprovalRequested:
		te = TransitionApprovalRequested
	case EventPermissionGranted:
		te = TransitionPermissionGranted
	case EventPermissionDenied:
		te = TransitionPermissionDenied
	case EventCompactionStarted:
		te = TransitionCompactionStarted
	case EventCompactionCompleted:
		te = TransitionCompactionCompleted
	default:
		// Non-state-changing event: reset idle timer if active
		if s, _ := m.State(); s == StateActive {
			m.resetIdleTimer()
		} else if s == StateIdle {
			// Activity while idle → transition back to active
			te = TransitionActivity
		}
		if te == "" {
			return
		}
	}

	m.mu.Lock()
	newState, newSub, valid := transition(m.state, m.subState, te)
	if valid && (newState != m.state || newSub != m.subState) {
		m.state = newState
		m.subState = newSub
		m.stateChangedAt = time.Now()
		// Close old channel and create new one to signal waiters
		close(m.stateCh)
		m.stateCh = make(chan struct{})

		// Manage idle timer
		if newState == StateActive {
			m.mu.Unlock()
			m.resetIdleTimer()
			// Emit state change event
			m.fanOut(AgentEvent{
				Type:      EventStateChange,
				Timestamp: time.Now(),
				Data:      StateChangeData{State: newState, SubState: newSub},
			})
			return
		}
		if newState == StateIdle || newState == StateExited {
			m.mu.Unlock()
			m.stopIdleTimer()
			m.fanOut(AgentEvent{
				Type:      EventStateChange,
				Timestamp: time.Now(),
				Data:      StateChangeData{State: newState, SubState: newSub},
			})
			return
		}
		m.mu.Unlock()
		m.fanOut(AgentEvent{
			Type:      EventStateChange,
			Timestamp: time.Now(),
			Data:      StateChangeData{State: newState, SubState: newSub},
		})
		return
	}
	m.mu.Unlock()
}

func (m *AgentMonitor) updateMetrics(evt AgentEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch evt.Type {
	case EventTurnCompleted:
		if d, ok := evt.Data.(TurnCompletedData); ok {
			m.inputTokens += d.InputTokens
			m.outputTokens += d.OutputTokens
			m.totalCostUSD += d.CostUSD
			m.turnCount++
		}
	case EventToolStarted:
		if d, ok := evt.Data.(ToolStartedData); ok {
			m.toolCounts[d.ToolName]++
		}
	}
}

func (m *AgentMonitor) fanOut(evt AgentEvent) {
	m.subscribersMu.RLock()
	subs := make([]chan<- AgentEvent, len(m.subscribers))
	for i, s := range m.subscribers {
		subs[i] = s.ch
	}
	m.subscribersMu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- evt:
		default:
			// Subscriber is slow — drop rather than block
		}
	}
}

func (m *AgentMonitor) resetIdleTimer() {
	m.idleTimerMu.Lock()
	defer m.idleTimerMu.Unlock()

	if m.idleTimer != nil {
		m.idleTimer.Stop()
	}
	m.idleTimer = time.AfterFunc(m.idleThreshold, func() {
		m.mu.Lock()
		newState, newSub, valid := transition(m.state, m.subState, TransitionIdleTimeout)
		if valid {
			m.state = newState
			m.subState = newSub
			m.stateChangedAt = time.Now()
			close(m.stateCh)
			m.stateCh = make(chan struct{})
		}
		m.mu.Unlock()

		if valid {
			m.fanOut(AgentEvent{
				Type:      EventStateChange,
				Timestamp: time.Now(),
				Data:      StateChangeData{State: newState, SubState: newSub},
			})
		}
	})
}

func (m *AgentMonitor) stopIdleTimer() {
	m.idleTimerMu.Lock()
	defer m.idleTimerMu.Unlock()

	if m.idleTimer != nil {
		m.idleTimer.Stop()
		m.idleTimer = nil
	}
}
