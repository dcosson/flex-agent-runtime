package agent

import (
	"context"
	"sync"
	"time"
)

var validTransitions = map[AgentState]map[AgentState]struct{}{
	StateIdle: {
		StateStreaming: {},
		StateExited:    {},
	},
	StateStreaming: {
		StateToolExecution:   {},
		StateWaitingFollowUp: {},
		StateIdle:            {},
		StateExited:          {},
	},
	StateToolExecution: {
		StateStreaming: {},
		StateIdle:      {},
		StateExited:    {},
	},
	StateWaitingFollowUp: {
		StateStreaming: {},
		StateIdle:      {},
		StateExited:    {},
	},
	StateExited: {},
}

// Agent is the thread-safe state owner and event hub for one runtime session.
type Agent struct {
	mu      sync.Mutex
	session *Session
	driver  AgentDriver
	running bool

	state   AgentState
	bus     *eventBus
	control *ControlQueue
}

type agentBinder interface {
	bindAgent(a *Agent)
}

func New(driver AgentDriver) *Agent {
	a := &Agent{
		driver:  driver,
		state:   StateIdle,
		bus:     newEventBus(),
		control: NewControlQueue(64),
	}

	if driver != nil {
		driver.Subscribe(func(event AgentEvent) {
			a.emit(event)
		})
		if b, ok := driver.(agentBinder); ok {
			b.bindAgent(a)
		}
	}

	return a
}

func (a *Agent) State() AgentState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *Agent) Session() *Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil {
		return nil
	}
	return a.session.Clone()
}

func (a *Agent) SetSession(session *Session) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if session == nil {
		a.session = nil
		return
	}
	cp := session.Clone()
	if len(cp.StateHistory) == 0 {
		cp.StateHistory = append(cp.StateHistory, a.state)
	}
	a.session = cp
}

func (a *Agent) Subscribe(fn func(AgentEvent)) (unsubscribe func()) {
	if fn == nil {
		return func() {}
	}
	_, unsubBus := a.bus.subscribe(fn)
	return unsubBus
}

// Transition applies an FSM transition and emits state_change on success.
func (a *Agent) Transition(to AgentState) error {
	a.mu.Lock()
	from := a.state
	if from == StateExited {
		a.mu.Unlock()
		return StoppedError{}
	}
	if from == to {
		a.mu.Unlock()
		return nil
	}
	if _, ok := validTransitions[from][to]; !ok {
		a.mu.Unlock()
		return InvalidStateError{From: from, To: to}
	}
	a.state = to
	if a.session != nil {
		a.session.StateHistory = append(a.session.StateHistory, to)
	}
	a.mu.Unlock()

	a.emit(AgentEvent{Type: EventStateChange, State: to, At: time.Now()})
	return nil
}

func (a *Agent) Start(ctx context.Context, session *Session, prompt string) error {
	a.mu.Lock()
	if a.state == StateExited {
		a.mu.Unlock()
		return StoppedError{}
	}
	if a.running {
		st := a.state
		a.mu.Unlock()
		return BusyError{State: st}
	}
	a.running = true
	if session != nil {
		a.session = session.Clone()
	}
	a.mu.Unlock()

	a.emit(AgentEvent{Type: EventSessionStarted, At: time.Now()})
	if a.driver != nil {
		if err := a.driver.Start(ctx, a.Session(), prompt); err != nil {
			a.RecordError()
			a.mu.Lock()
			a.running = false
			a.mu.Unlock()
			_ = a.Transition(StateIdle)
			return err
		}
	}
	return nil
}

// Prompt starts an agent turn with a user prompt.
func (a *Agent) Prompt(ctx context.Context, prompt string) error {
	return a.Start(ctx, a.Session(), prompt)
}

// Continue resumes an existing session without appending a new user prompt.
func (a *Agent) Continue(ctx context.Context) error {
	a.mu.Lock()
	if a.state == StateExited {
		a.mu.Unlock()
		return StoppedError{}
	}
	if a.running {
		st := a.state
		a.mu.Unlock()
		return BusyError{State: st}
	}
	if a.session == nil {
		a.mu.Unlock()
		return InvalidStateError{From: a.state, To: a.state}
	}
	a.running = true
	sess := a.session
	a.mu.Unlock()

	if a.driver == nil {
		a.onDriverIdle()
		return nil
	}
	if err := a.driver.Resume(ctx, sess.Clone()); err != nil {
		a.RecordError()
		a.onDriverIdle()
		return err
	}
	return nil
}

func (a *Agent) Stop(ctx context.Context) error {
	_ = ctx
	a.mu.Lock()
	if a.state == StateExited {
		a.mu.Unlock()
		return nil
	}
	a.running = false
	a.mu.Unlock()

	if a.driver != nil {
		if err := a.driver.Stop(ctx); err != nil {
			return err
		}
	}
	if err := a.Transition(StateExited); err != nil {
		return err
	}
	a.emit(AgentEvent{Type: EventSessionEnded, At: time.Now()})
	a.control.Close()
	a.bus.close()
	return nil
}

func (a *Agent) ControlQueue() *ControlQueue {
	return a.control
}

func (a *Agent) Steer(message string) error {
	if err := a.control.EnqueueSteer(message); err != nil {
		return err
	}
	a.emit(AgentEvent{Type: EventSteeringApplied, ControlMessage: message, At: time.Now()})
	return nil
}

func (a *Agent) FollowUp(message string) error {
	if err := a.control.EnqueueFollowUp(message); err != nil {
		return err
	}
	a.emit(AgentEvent{Type: EventFollowUpEnqueued, ControlMessage: message, At: time.Now()})
	return nil
}

func (a *Agent) Abort(reason string) error {
	if err := a.control.EnqueueAbort(reason); err != nil {
		return err
	}
	a.emit(AgentEvent{Type: EventAborted, ControlMessage: reason, At: time.Now()})
	return nil
}

func (a *Agent) onDriverIdle() {
	a.mu.Lock()
	a.running = false
	a.mu.Unlock()
}

func (a *Agent) AppendConversation(msg AgentMessage) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil {
		return InvalidStateError{From: a.state, To: a.state}
	}
	msgCopy := msg
	a.session.ConversationLog = append(a.session.ConversationLog, msgCopy)
	a.session.Metrics.MessagesAppended++
	return nil
}

func (a *Agent) MarkTurnStarted() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session != nil {
		a.session.Metrics.TurnsStarted++
	}
}

func (a *Agent) MarkTurnCompleted() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session != nil {
		a.session.Metrics.TurnsCompleted++
	}
}

func (a *Agent) RecordToolStarted() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session != nil {
		a.session.Metrics.ToolCallsStarted++
	}
}

func (a *Agent) RecordToolFinished() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session != nil {
		a.session.Metrics.ToolCallsFinished++
	}
}

func (a *Agent) RecordError() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session != nil {
		a.session.Metrics.Errors++
	}
}

func (a *Agent) emit(event AgentEvent) {
	a.mu.Lock()
	sess := a.session
	state := a.state
	a.mu.Unlock()

	if event.At.IsZero() {
		event.At = time.Now()
	}
	if sess != nil {
		if event.SessionID == "" {
			event.SessionID = sess.ID
		}
		if event.DriverSessionID == "" {
			event.DriverSessionID = sess.DriverSessionID
		}
	}
	if event.State == "" {
		event.State = state
	}
	a.bus.publish(event)
}
