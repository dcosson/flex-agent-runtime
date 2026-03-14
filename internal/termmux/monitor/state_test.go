package monitor

import "testing"

func TestTransition_SessionStarted(t *testing.T) {
	s, sub, ok := transition(StateInitialized, SubStateNone, TransitionSessionStarted)
	if !ok {
		t.Fatal("expected valid transition")
	}
	if s != StateActive {
		t.Errorf("expected Active, got %s", s)
	}
	if sub != SubStateThinking {
		t.Errorf("expected Thinking, got %s", sub)
	}
}

func TestTransition_SessionStartedFromNonInit(t *testing.T) {
	s, sub, ok := transition(StateActive, SubStateThinking, TransitionSessionStarted)
	if ok {
		t.Errorf("expected invalid transition from Active, got %s/%s", s, sub)
	}
}

func TestTransition_ToolLifecycle(t *testing.T) {
	// Active/Thinking → Active/ToolUse
	s, sub, ok := transition(StateActive, SubStateThinking, TransitionToolStarted)
	if !ok || s != StateActive || sub != SubStateToolUse {
		t.Errorf("tool started: got %s/%s ok=%v", s, sub, ok)
	}

	// Active/ToolUse → Active/Thinking
	s, sub, ok = transition(StateActive, SubStateToolUse, TransitionToolCompleted)
	if !ok || s != StateActive || sub != SubStateThinking {
		t.Errorf("tool completed: got %s/%s ok=%v", s, sub, ok)
	}
}

func TestTransition_ApprovalFlow(t *testing.T) {
	// Thinking → WaitingForPermission
	s, sub, ok := transition(StateActive, SubStateThinking, TransitionApprovalRequested)
	if !ok || sub != SubStateWaitingForPermission {
		t.Errorf("approval requested: got %s/%s ok=%v", s, sub, ok)
	}

	// WaitingForPermission → ToolUse (granted)
	s, sub, ok = transition(StateActive, SubStateWaitingForPermission, TransitionPermissionGranted)
	if !ok || sub != SubStateToolUse {
		t.Errorf("permission granted: got %s/%s ok=%v", s, sub, ok)
	}
}

func TestTransition_PermissionDenied(t *testing.T) {
	s, sub, ok := transition(StateActive, SubStateWaitingForPermission, TransitionPermissionDenied)
	if !ok || sub != SubStateThinking {
		t.Errorf("permission denied: got %s/%s ok=%v", s, sub, ok)
	}
	_ = s
}

func TestTransition_CompactionLifecycle(t *testing.T) {
	s, sub, ok := transition(StateActive, SubStateThinking, TransitionCompactionStarted)
	if !ok || sub != SubStateCompacting {
		t.Errorf("compaction started: got %s/%s ok=%v", s, sub, ok)
	}

	s, sub, ok = transition(StateActive, SubStateCompacting, TransitionCompactionCompleted)
	if !ok || sub != SubStateThinking {
		t.Errorf("compaction completed: got %s/%s ok=%v", s, sub, ok)
	}
	_ = s
}

func TestTransition_IdleTimeout(t *testing.T) {
	s, sub, ok := transition(StateActive, SubStateThinking, TransitionIdleTimeout)
	if !ok || s != StateIdle {
		t.Errorf("idle timeout: got %s/%s ok=%v", s, sub, ok)
	}
	if sub != SubStateNone {
		t.Errorf("expected no sub-state in idle, got %s", sub)
	}
}

func TestTransition_ActivityFromIdle(t *testing.T) {
	s, sub, ok := transition(StateIdle, SubStateNone, TransitionActivity)
	if !ok || s != StateActive || sub != SubStateThinking {
		t.Errorf("activity from idle: got %s/%s ok=%v", s, sub, ok)
	}
}

func TestTransition_SessionEndedFromActive(t *testing.T) {
	s, sub, ok := transition(StateActive, SubStateThinking, TransitionSessionEnded)
	if !ok || s != StateExited {
		t.Errorf("session ended from active: got %s/%s ok=%v", s, sub, ok)
	}
	_ = sub
}

func TestTransition_SessionEndedFromIdle(t *testing.T) {
	s, sub, ok := transition(StateIdle, SubStateNone, TransitionSessionEnded)
	if !ok || s != StateExited {
		t.Errorf("session ended from idle: got %s/%s ok=%v", s, sub, ok)
	}
	_ = sub
}

func TestTransition_ProcessExitFromActive(t *testing.T) {
	s, _, ok := transition(StateActive, SubStateToolUse, TransitionProcessExit)
	if !ok || s != StateExited {
		t.Errorf("process exit from active: got %s ok=%v", s, ok)
	}
}

func TestTransition_ExitedIsTerminal(t *testing.T) {
	// No transition should be valid from Exited
	events := []TransitionEvent{
		TransitionSessionStarted, TransitionToolStarted, TransitionToolCompleted,
		TransitionApprovalRequested, TransitionPermissionGranted, TransitionPermissionDenied,
		TransitionIdleTimeout, TransitionActivity, TransitionSessionEnded, TransitionProcessExit,
	}
	for _, evt := range events {
		_, _, ok := transition(StateExited, SubStateNone, evt)
		if ok {
			t.Errorf("transition from Exited with %s should be invalid", evt)
		}
	}
}

func TestTransition_InvalidTransitions(t *testing.T) {
	cases := []struct {
		name  string
		state State
		sub   SubState
		event TransitionEvent
	}{
		{"tool started from idle", StateIdle, SubStateNone, TransitionToolStarted},
		{"tool completed from thinking", StateActive, SubStateThinking, TransitionToolCompleted},
		{"approval from tool use", StateActive, SubStateToolUse, TransitionApprovalRequested},
		{"permission granted from thinking", StateActive, SubStateThinking, TransitionPermissionGranted},
		{"idle timeout from idle", StateIdle, SubStateNone, TransitionIdleTimeout},
		{"activity from active", StateActive, SubStateThinking, TransitionActivity},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, ok := transition(tc.state, tc.sub, tc.event)
			if ok {
				t.Errorf("expected invalid transition for %s", tc.name)
			}
		})
	}
}
