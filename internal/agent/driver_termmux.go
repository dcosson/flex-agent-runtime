package agent

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"h2-agent-runtime/internal/termmux"
	"h2-agent-runtime/internal/termmux/monitor"
)

// TermmuxDriverAdapter bridges a termmux session to the AgentDriver interface.
// This allows 3rd party CLI agents (Claude Code, Codex) managed via termmux
// to be consumed through the same driver contract as the native agent loop.
type TermmuxDriverAdapter struct {
	session *termmux.Session

	mu          sync.RWMutex
	started     bool
	stopped     bool
	subscribers []func(AgentEvent)
	cancelFn    context.CancelFunc
}

// Compile-time assertion.
var _ AgentDriver = (*TermmuxDriverAdapter)(nil)

// NewTermmuxDriverAdapter creates a new adapter wrapping a termmux session.
func NewTermmuxDriverAdapter(session *termmux.Session) *TermmuxDriverAdapter {
	return &TermmuxDriverAdapter{
		session: session,
	}
}

// Start launches the termmux session and begins forwarding events.
func (a *TermmuxDriverAdapter) Start(ctx context.Context, session *Session, prompt string) error {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return fmt.Errorf("termmux adapter already started")
	}
	a.started = true
	ctx, a.cancelFn = context.WithCancel(ctx)
	a.mu.Unlock()

	// Start the termmux session
	if err := a.session.Start(ctx); err != nil {
		return fmt.Errorf("start termmux session: %w", err)
	}

	// Subscribe to monitor events and forward as canonical AgentEvents
	ch := make(chan monitor.AgentEvent, 256)
	_, unsub := a.session.Monitor().Subscribe(ch)

	go func() {
		defer unsub()
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "panic recovered in termmux adapter event forwarder: %v\n%s\n", r, debug.Stack())
			}
		}()

		for {
			select {
			case evt, ok := <-ch:
				if !ok {
					return
				}
				canonical := adaptMonitorEvent(evt, a.session.ID)
				a.notifySubscribers(canonical)
			case <-ctx.Done():
				return
			}
		}
	}()

	// Write prompt to PTY if provided
	if prompt != "" {
		go func() {
			time.Sleep(500 * time.Millisecond) // Brief delay for CLI to be ready
			_, _ = a.session.WritePTY([]byte(prompt + "\n"))
		}()
	}

	// Emit session started
	a.notifySubscribers(AgentEvent{
		Type:      EventSessionStarted,
		SessionID: session.ID,
		At:        time.Now(),
	})

	return nil
}

// Resume resumes an existing termmux session.
func (a *TermmuxDriverAdapter) Resume(ctx context.Context, session *Session) error {
	// For termmux sessions, resume is the same as start with no prompt
	return a.Start(ctx, session, "")
}

// Stop stops the termmux session.
func (a *TermmuxDriverAdapter) Stop(ctx context.Context) error {
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		return nil
	}
	a.stopped = true
	cancelFn := a.cancelFn
	a.mu.Unlock()

	a.session.Stop()
	if cancelFn != nil {
		cancelFn()
	}

	return nil
}

// Subscribe registers a callback for events. Returns an unsubscribe function.
// Callbacks are isolated — a panic in one doesn't affect others.
func (a *TermmuxDriverAdapter) Subscribe(fn func(AgentEvent)) (unsubscribe func()) {
	a.mu.Lock()
	defer a.mu.Unlock()

	idx := len(a.subscribers)
	a.subscribers = append(a.subscribers, fn)

	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if idx < len(a.subscribers) {
			a.subscribers[idx] = nil // nil out rather than remove to preserve indices
		}
	}
}

func (a *TermmuxDriverAdapter) notifySubscribers(evt AgentEvent) {
	a.mu.RLock()
	subs := make([]func(AgentEvent), len(a.subscribers))
	copy(subs, a.subscribers)
	a.mu.RUnlock()

	for _, fn := range subs {
		if fn == nil {
			continue
		}
		safeCallTermmuxSubscriber(fn, evt)
	}
}

func safeCallTermmuxSubscriber(fn func(AgentEvent), evt AgentEvent) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic recovered in termmux subscriber: %v\n%s\n", r, debug.Stack())
		}
	}()
	fn(evt)
}

// adaptMonitorEvent converts a termmux monitor event to a canonical AgentEvent.
func adaptMonitorEvent(evt monitor.AgentEvent, sessionID string) AgentEvent {
	canonical := AgentEvent{
		SessionID: sessionID,
		At:        evt.Timestamp,
	}

	switch evt.Type {
	case monitor.EventSessionStarted:
		canonical.Type = EventSessionStarted

	case monitor.EventSessionEnded:
		canonical.Type = EventSessionEnded
		if d, ok := evt.Data.(monitor.SessionEndedData); ok {
			canonical.ControlMessage = d.Reason
		}

	case monitor.EventTurnCompleted:
		canonical.Type = EventTurnCompleted
		if d, ok := evt.Data.(monitor.TurnCompletedData); ok {
			canonical.Metadata = map[string]any{
				"input_tokens":  d.InputTokens,
				"output_tokens": d.OutputTokens,
				"cached_tokens": d.CachedTokens,
				"cost_usd":      d.CostUSD,
			}
		}

	case monitor.EventToolStarted:
		canonical.Type = EventToolStarted
		if d, ok := evt.Data.(monitor.ToolStartedData); ok {
			canonical.ToolName = d.ToolName
			canonical.ToolCallID = d.CallID
		}

	case monitor.EventToolCompleted:
		canonical.Type = EventToolCompleted
		if d, ok := evt.Data.(monitor.ToolCompletedData); ok {
			canonical.ToolName = d.ToolName
			canonical.ToolCallID = d.CallID
		}

	case monitor.EventApprovalRequested:
		canonical.Type = EventStateChange
		canonical.State = StateWaitingFollowUp
		if d, ok := evt.Data.(monitor.ApprovalRequestedData); ok {
			canonical.ToolName = d.ToolName
			canonical.ToolCallID = d.CallID
		}

	case monitor.EventAgentMessage:
		canonical.Type = EventAgentMessageCompleted
		if d, ok := evt.Data.(monitor.AgentMessageData); ok {
			canonical.Delta = d.Content
		}

	case monitor.EventStateChange:
		canonical.Type = EventStateChange
		if d, ok := evt.Data.(monitor.StateChangeData); ok {
			canonical.State = adaptMonitorState(d.State)
		}

	default:
		canonical.Type = AgentEventType(string(evt.Type))
	}

	return canonical
}

// adaptMonitorState converts a monitor state to an agent state.
func adaptMonitorState(s monitor.State) AgentState {
	switch s {
	case monitor.StateInitialized:
		return StateIdle
	case monitor.StateActive:
		return StateStreaming
	case monitor.StateIdle:
		return StateIdle
	case monitor.StateExited:
		return StateExited
	default:
		return StateIdle
	}
}
