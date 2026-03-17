package harness

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/agent"
	"github.com/anthropics/flex-agent-runtime/internal/termmux"
	"github.com/anthropics/flex-agent-runtime/internal/termmux/monitor"
)

// TermmuxEnv wraps a termmux session with E2E test helpers for
// PTY launch, attach/detach, event capture, and lifecycle control.
type TermmuxEnv struct {
	Session *termmux.Session
	Adapter *agent.TermmuxDriverAdapter
	Monitor *monitor.AgentMonitor

	mu       sync.Mutex
	events   []agent.AgentEvent
	eventsCh chan agent.AgentEvent
	unsub    func()
}

// TermmuxEnvConfig controls TermmuxEnv setup.
type TermmuxEnvConfig struct {
	SessionID  string
	DriverType string
	Command    string
	Args       []string
	CWD        string
	Env        map[string]string
}

// NewTermmuxEnv creates a TermmuxEnv with a termmux session and adapter.
func NewTermmuxEnv(t *testing.T, cfg TermmuxEnvConfig) *TermmuxEnv {
	t.Helper()

	if cfg.SessionID == "" {
		cfg.SessionID = fmt.Sprintf("mode2-%s", t.Name())
	}
	if cfg.Command == "" {
		cfg.Command = "/bin/sh"
	}

	sess := termmux.NewSession(cfg.SessionID, termmux.SessionConfig{
		DriverType:  cfg.DriverType,
		Command:     cfg.Command,
		Args:        cfg.Args,
		CWD:         cfg.CWD,
		Env:         cfg.Env,
		InitialRows: 24,
		InitialCols: 80,
	})

	adapter := agent.NewTermmuxDriverAdapter(sess)
	env := &TermmuxEnv{
		Session:  sess,
		Adapter:  adapter,
		Monitor:  sess.Monitor(),
		eventsCh: make(chan agent.AgentEvent, 256),
	}

	// Subscribe to capture all events
	env.unsub = adapter.Subscribe(func(evt agent.AgentEvent) {
		env.mu.Lock()
		env.events = append(env.events, evt)
		env.mu.Unlock()
		select {
		case env.eventsCh <- evt:
		default:
		}
	})

	t.Cleanup(func() {
		env.Stop()
	})

	return env
}

// Start launches the termmux session via the adapter.
func (e *TermmuxEnv) Start(ctx context.Context, prompt string) error {
	agentSession := &agent.Session{ID: e.Session.ID}
	return e.Adapter.Start(ctx, agentSession, prompt)
}

// Stop stops the session and cleans up.
func (e *TermmuxEnv) Stop() {
	if e.unsub != nil {
		e.unsub()
		e.unsub = nil
	}
	e.Session.Stop()
}

// Attach adds a client to the session.
func (e *TermmuxEnv) Attach(clientID string) *termmux.Client {
	return e.Session.Attach(clientID)
}

// Detach removes a client from the session.
func (e *TermmuxEnv) Detach(clientID string) {
	e.Session.Detach(clientID)
}

// WritePTY writes data to the session's PTY.
func (e *TermmuxEnv) WritePTY(data []byte) (int, error) {
	return e.Session.WritePTY(data)
}

// Events returns a snapshot of all captured events.
func (e *TermmuxEnv) Events() []agent.AgentEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	cp := make([]agent.AgentEvent, len(e.events))
	copy(cp, e.events)
	return cp
}

// WaitForEvent waits for an event of the given type with a timeout.
func (e *TermmuxEnv) WaitForEvent(t *testing.T, typ agent.AgentEventType, timeout time.Duration) agent.AgentEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case evt := <-e.eventsCh:
			if evt.Type == typ {
				return evt
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event %s after %v", typ, timeout)
			return agent.AgentEvent{}
		}
	}
}

// WaitForState waits for the monitor to reach the given state.
func (e *TermmuxEnv) WaitForState(t *testing.T, state monitor.State, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		s, _ := e.Monitor.State()
		if s == state {
			return
		}
		ch := e.Monitor.WaitStateChange()
		select {
		case <-ch:
			continue
		case <-deadline:
			s, _ := e.Monitor.State()
			t.Fatalf("timed out waiting for state %s, current state: %s", state, s)
		}
	}
}

// EventsOfType returns all captured events matching the given type.
func (e *TermmuxEnv) EventsOfType(typ agent.AgentEventType) []agent.AgentEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []agent.AgentEvent
	for _, evt := range e.events {
		if evt.Type == typ {
			out = append(out, evt)
		}
	}
	return out
}

// HasEventSequence checks that the given event types appear in order.
func (e *TermmuxEnv) HasEventSequence(types ...agent.AgentEventType) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	idx := 0
	for _, evt := range e.events {
		if idx < len(types) && evt.Type == types[idx] {
			idx++
		}
	}
	return idx == len(types)
}

// Pause pauses the session, gating new interactions.
func (e *TermmuxEnv) Pause() error {
	return e.Session.Pause()
}

// Resume unpauses the session.
func (e *TermmuxEnv) Resume() error {
	return e.Session.Resume()
}

// IsPaused returns whether the session is paused.
func (e *TermmuxEnv) IsPaused() bool {
	return e.Session.IsPaused()
}

// IsRunning returns whether the session's child process is running.
func (e *TermmuxEnv) IsRunning() bool {
	return e.Session.IsRunning()
}

// Wait blocks until the session exits.
func (e *TermmuxEnv) Wait() {
	e.Session.Wait()
}
