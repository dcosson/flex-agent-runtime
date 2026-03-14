// Package monitor provides the agent state machine, metrics, and event fan-out.
package monitor

// State represents the top-level agent state.
type State string

const (
	StateInitialized State = "initialized"
	StateActive      State = "active"
	StateIdle        State = "idle"
	StateExited      State = "exited"
)

// SubState represents the sub-state within Active.
type SubState string

const (
	SubStateNone                 SubState = ""
	SubStateThinking             SubState = "thinking"
	SubStateToolUse              SubState = "tool_use"
	SubStateWaitingForPermission SubState = "waiting_for_permission"
	SubStateCompacting           SubState = "compacting"
)

// transition validates and applies a state transition.
// Returns true if the transition is valid.
func transition(current State, currentSub SubState, event TransitionEvent) (State, SubState, bool) {
	switch event {
	case TransitionSessionStarted:
		if current == StateInitialized {
			return StateActive, SubStateThinking, true
		}
	case TransitionToolStarted:
		if current == StateActive && (currentSub == SubStateThinking || currentSub == SubStateNone) {
			return StateActive, SubStateToolUse, true
		}
	case TransitionToolCompleted:
		if current == StateActive && currentSub == SubStateToolUse {
			return StateActive, SubStateThinking, true
		}
	case TransitionApprovalRequested:
		if current == StateActive && currentSub == SubStateThinking {
			return StateActive, SubStateWaitingForPermission, true
		}
	case TransitionPermissionGranted:
		if current == StateActive && currentSub == SubStateWaitingForPermission {
			return StateActive, SubStateToolUse, true
		}
	case TransitionPermissionDenied:
		if current == StateActive && currentSub == SubStateWaitingForPermission {
			return StateActive, SubStateThinking, true
		}
	case TransitionCompactionStarted:
		if current == StateActive && currentSub == SubStateThinking {
			return StateActive, SubStateCompacting, true
		}
	case TransitionCompactionCompleted:
		if current == StateActive && currentSub == SubStateCompacting {
			return StateActive, SubStateThinking, true
		}
	case TransitionIdleTimeout:
		if current == StateActive {
			return StateIdle, SubStateNone, true
		}
	case TransitionActivity:
		if current == StateIdle {
			return StateActive, SubStateThinking, true
		}
	case TransitionSessionEnded, TransitionProcessExit:
		if current == StateActive || current == StateIdle {
			return StateExited, SubStateNone, true
		}
	}
	return current, currentSub, false
}

// TransitionEvent represents events that trigger state transitions.
type TransitionEvent string

const (
	TransitionSessionStarted      TransitionEvent = "session_started"
	TransitionToolStarted         TransitionEvent = "tool_started"
	TransitionToolCompleted       TransitionEvent = "tool_completed"
	TransitionApprovalRequested   TransitionEvent = "approval_requested"
	TransitionPermissionGranted   TransitionEvent = "permission_granted"
	TransitionPermissionDenied    TransitionEvent = "permission_denied"
	TransitionCompactionStarted   TransitionEvent = "compaction_started"
	TransitionCompactionCompleted TransitionEvent = "compaction_completed"
	TransitionIdleTimeout         TransitionEvent = "idle_timeout"
	TransitionActivity            TransitionEvent = "activity"
	TransitionSessionEnded        TransitionEvent = "session_ended"
	TransitionProcessExit         TransitionEvent = "process_exit"
)
