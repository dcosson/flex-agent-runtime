package agenttest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/agent"
)

// MockAgentDriver implements agent.AgentDriver for AgentLoopService tests.
type MockAgentDriver struct {
	mu          sync.Mutex
	startCount  int
	stopCount   int
	startErr    error
	resumeErr   error
	subscribers []func(agent.AgentEvent)

	TurnScript     []agent.AgentEvent
	TurnDelay      time.Duration
	ToolCallScript []ToolCallEntry
	BlockOnStart   chan struct{}
	PanicOnStart   bool
}

type ToolCallEntry struct {
	ToolName  string
	Arguments map[string]any
}

var _ agent.AgentDriver = (*MockAgentDriver)(nil)

func (d *MockAgentDriver) Start(ctx context.Context, sess *agent.Session, prompt string) error {
	_ = prompt
	d.mu.Lock()
	d.startCount++
	err := d.startErr
	d.mu.Unlock()
	if err != nil {
		return err
	}
	if d.PanicOnStart {
		panic("mock driver panic")
	}
	sessionID := ""
	if sess != nil {
		sessionID = sess.ID
	}
	go d.runTurn(ctx, sessionID)
	return nil
}

func (d *MockAgentDriver) Resume(ctx context.Context, sess *agent.Session) error {
	d.mu.Lock()
	err := d.resumeErr
	d.mu.Unlock()
	if err != nil {
		return err
	}
	sessionID := ""
	if sess != nil {
		sessionID = sess.ID
	}
	go d.runTurn(ctx, sessionID)
	return nil
}

func (d *MockAgentDriver) Stop(ctx context.Context) error {
	_ = ctx
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopCount++
	return nil
}

func (d *MockAgentDriver) Subscribe(fn func(agent.AgentEvent)) func() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.subscribers = append(d.subscribers, fn)
	idx := len(d.subscribers) - 1
	return func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if idx >= 0 && idx < len(d.subscribers) {
			d.subscribers[idx] = nil
		}
	}
}

func (d *MockAgentDriver) SetStartError(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.startErr = err
}

func (d *MockAgentDriver) SetResumeError(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.resumeErr = err
}

func (d *MockAgentDriver) StartCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.startCount
}

func (d *MockAgentDriver) StopCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stopCount
}

func (d *MockAgentDriver) runTurn(ctx context.Context, sessionID string) {
	if d.BlockOnStart != nil {
		select {
		case <-d.BlockOnStart:
		case <-ctx.Done():
			return
		}
	}

	script := d.turnScript()
	for _, evt := range script {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if d.TurnDelay > 0 {
			select {
			case <-time.After(d.TurnDelay):
			case <-ctx.Done():
				return
			}
		}
		normalized := evt
		if normalized.SessionID == "" {
			normalized.SessionID = sessionID
		}
		d.emit(normalized)
	}
}

func (d *MockAgentDriver) turnScript() []agent.AgentEvent {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.TurnScript) == 0 {
		return defaultTurnScript()
	}
	out := make([]agent.AgentEvent, len(d.TurnScript))
	copy(out, d.TurnScript)
	return out
}

func (d *MockAgentDriver) emit(evt agent.AgentEvent) {
	d.mu.Lock()
	subs := make([]func(agent.AgentEvent), 0, len(d.subscribers))
	for _, sub := range d.subscribers {
		if sub != nil {
			subs = append(subs, sub)
		}
	}
	d.mu.Unlock()

	for _, fn := range subs {
		fn(evt)
	}
}

func defaultTurnScript() []agent.AgentEvent {
	return []agent.AgentEvent{
		{Type: agent.EventTurnStarted, Turn: 1},
		{Type: agent.EventAgentMessageDelta, Turn: 1, Delta: "Hello from mock agent"},
		{
			Type:    agent.EventAgentMessageCompleted,
			Turn:    1,
			Message: &agent.AgentMessage{Turn: 1, CreatedAt: time.Now()},
		},
		{Type: agent.EventTurnCompleted, Turn: 1},
	}
}

// NewDriverFactory returns a name-aware factory for service.SetDriverFactory.
func NewDriverFactory(resolve func(driverName string, cfg agent.DriverConfig) (*MockAgentDriver, error)) func(string, agent.DriverConfig) (agent.AgentDriver, error) {
	return func(driverName string, cfg agent.DriverConfig) (agent.AgentDriver, error) {
		driver, err := resolve(driverName, cfg)
		if err != nil {
			return nil, err
		}
		if driver == nil {
			return nil, fmt.Errorf("mock driver factory returned nil driver")
		}
		return driver, nil
	}
}
