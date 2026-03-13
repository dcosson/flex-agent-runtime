package agent

import (
	"errors"
	"fmt"
)

var (
	ErrBusy         = errors.New("agent is busy")
	ErrStopped      = errors.New("agent is stopped")
	ErrInvalidState = errors.New("invalid agent state transition")
)

// BusyError is returned when a state-changing operation is requested while active work is in flight.
type BusyError struct {
	State AgentState
}

func (e BusyError) Error() string {
	if e.State == "" {
		return ErrBusy.Error()
	}
	return fmt.Sprintf("%s: current state=%s", ErrBusy, e.State)
}

func (BusyError) Unwrap() error { return ErrBusy }

// StoppedError is returned when an operation is attempted after the agent has exited.
type StoppedError struct{}

func (StoppedError) Error() string { return ErrStopped.Error() }
func (StoppedError) Unwrap() error { return ErrStopped }

// InvalidStateError reports an illegal FSM transition.
type InvalidStateError struct {
	From AgentState
	To   AgentState
}

func (e InvalidStateError) Error() string {
	return fmt.Sprintf("%s: %s -> %s", ErrInvalidState, e.From, e.To)
}

func (InvalidStateError) Unwrap() error { return ErrInvalidState }
