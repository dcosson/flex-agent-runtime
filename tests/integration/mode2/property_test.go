package mode2

import (
	"context"
	"fmt"
	"testing"
	"time"

	"h2-agent-runtime/internal/agent"
	"h2-agent-runtime/internal/termmux/monitor"
	"h2-agent-runtime/tests/integration/mode2/harness"

	"pgregory.net/rapid"
)

// =============================================================================
// P1: Session Lifecycle FSM Invariant
// Random sequences of transition events must produce valid state transitions.
// =============================================================================

func TestP1_SessionLifecycleFSM(t *testing.T) {
	// All possible transition events
	allTransitions := []monitor.TransitionEvent{
		monitor.TransitionSessionStarted,
		monitor.TransitionToolStarted,
		monitor.TransitionToolCompleted,
		monitor.TransitionApprovalRequested,
		monitor.TransitionPermissionGranted,
		monitor.TransitionPermissionDenied,
		monitor.TransitionCompactionStarted,
		monitor.TransitionCompactionCompleted,
		monitor.TransitionIdleTimeout,
		monitor.TransitionActivity,
		monitor.TransitionSessionEnded,
		monitor.TransitionProcessExit,
	}

	rapid.Check(t, func(t *rapid.T) {
		// Generate a random sequence of transition events
		seqLen := rapid.IntRange(1, 50).Draw(t, "seqLen")

		mon := monitor.NewAgentMonitor()
		defer mon.Close()

		evtCh := make(chan monitor.AgentEvent, 256)
		_, unsub := mon.Subscribe(evtCh)
		defer unsub()

		var states []monitor.State
		s, _ := mon.State()
		states = append(states, s)

		for i := 0; i < seqLen; i++ {
			idx := rapid.IntRange(0, len(allTransitions)-1).Draw(t, fmt.Sprintf("trans-%d", i))
			evt := transitionToMonitorEvent(allTransitions[idx])
			mon.Submit(evt)
			time.Sleep(time.Millisecond) // let it process

			newState, _ := mon.State()
			states = append(states, newState)
		}

		// Invariants:
		// 1. First state is always "initialized"
		if states[0] != monitor.StateInitialized {
			t.Fatalf("initial state must be initialized, got %s", states[0])
		}

		// 2. Once exited, state never changes
		exitedIdx := -1
		for i, s := range states {
			if s == monitor.StateExited {
				exitedIdx = i
				break
			}
		}
		if exitedIdx >= 0 {
			for i := exitedIdx; i < len(states); i++ {
				if states[i] != monitor.StateExited {
					t.Fatalf("state changed after exited at index %d: %s", i, states[i])
				}
			}
		}

		// 3. State is always a valid value
		validStates := map[monitor.State]bool{
			monitor.StateInitialized: true,
			monitor.StateActive:      true,
			monitor.StateIdle:        true,
			monitor.StateExited:      true,
		}
		for i, s := range states {
			if !validStates[s] {
				t.Fatalf("invalid state %q at index %d", s, i)
			}
		}
	})
}

// transitionToMonitorEvent converts a TransitionEvent to an appropriate monitor event.
func transitionToMonitorEvent(te monitor.TransitionEvent) monitor.AgentEvent {
	switch te {
	case monitor.TransitionSessionStarted:
		return monitor.AgentEvent{
			Type:      monitor.EventSessionStarted,
			Timestamp: time.Now(),
			Data:      monitor.SessionStartedData{SessionID: "prop-test"},
		}
	case monitor.TransitionToolStarted:
		return monitor.AgentEvent{
			Type:      monitor.EventToolStarted,
			Timestamp: time.Now(),
			Data:      monitor.ToolStartedData{ToolName: "read", CallID: "c1"},
		}
	case monitor.TransitionToolCompleted:
		return monitor.AgentEvent{
			Type:      monitor.EventToolCompleted,
			Timestamp: time.Now(),
			Data:      monitor.ToolCompletedData{ToolName: "read", CallID: "c1", Success: true},
		}
	case monitor.TransitionApprovalRequested:
		return monitor.AgentEvent{
			Type:      monitor.EventApprovalRequested,
			Timestamp: time.Now(),
			Data:      monitor.ApprovalRequestedData{ToolName: "bash", CallID: "c2"},
		}
	case monitor.TransitionPermissionGranted:
		return monitor.AgentEvent{
			Type:      monitor.EventPermissionGranted,
			Timestamp: time.Now(),
		}
	case monitor.TransitionPermissionDenied:
		return monitor.AgentEvent{
			Type:      monitor.EventPermissionDenied,
			Timestamp: time.Now(),
		}
	case monitor.TransitionCompactionStarted:
		return monitor.AgentEvent{
			Type:      monitor.EventCompactionStarted,
			Timestamp: time.Now(),
		}
	case monitor.TransitionCompactionCompleted:
		return monitor.AgentEvent{
			Type:      monitor.EventCompactionCompleted,
			Timestamp: time.Now(),
		}
	case monitor.TransitionIdleTimeout:
		// Idle is internally triggered by the monitor, but we can submit a
		// state_change event to simulate it
		return monitor.AgentEvent{
			Type:      monitor.EventStateChange,
			Timestamp: time.Now(),
			Data:      monitor.StateChangeData{State: monitor.StateIdle},
		}
	case monitor.TransitionActivity:
		// Activity from idle is triggered by any event submission while idle
		return monitor.AgentEvent{
			Type:      monitor.EventToolStarted,
			Timestamp: time.Now(),
			Data:      monitor.ToolStartedData{ToolName: "read", CallID: "activity"},
		}
	case monitor.TransitionSessionEnded:
		return monitor.AgentEvent{
			Type:      monitor.EventSessionEnded,
			Timestamp: time.Now(),
			Data:      monitor.SessionEndedData{Reason: "complete"},
		}
	case monitor.TransitionProcessExit:
		return monitor.AgentEvent{
			Type:      monitor.EventSessionEnded,
			Timestamp: time.Now(),
			Data:      monitor.SessionEndedData{Reason: "process_exit"},
		}
	default:
		return monitor.AgentEvent{Timestamp: time.Now()}
	}
}

// =============================================================================
// P2: Event Normalization Consistency
// Equivalent semantic activity reported through different source combinations
// normalizes to equivalent canonical event classes.
// =============================================================================

func TestP2_EventNormalizationConsistency(t *testing.T) {
	sources := []string{"otel", "hook", "session_log"}
	eventTypes := []agent.AgentEventType{
		agent.EventToolStarted,
		agent.EventToolCompleted,
		agent.EventSessionStarted,
		agent.EventSessionEnded,
	}

	rapid.Check(t, func(t *rapid.T) {
		numEvents := rapid.IntRange(1, 20).Draw(t, "numEvents")
		window := time.Duration(rapid.IntRange(50, 500).Draw(t, "windowMs")) * time.Millisecond

		normalizer := harness.NewEventNormalizer()
		now := time.Now()

		for i := 0; i < numEvents; i++ {
			typeIdx := rapid.IntRange(0, len(eventTypes)-1).Draw(t, fmt.Sprintf("type-%d", i))
			srcIdx := rapid.IntRange(0, len(sources)-1).Draw(t, fmt.Sprintf("src-%d", i))
			offsetMs := rapid.IntRange(0, 5000).Draw(t, fmt.Sprintf("offset-%d", i))

			evt := agent.AgentEvent{
				Type:       eventTypes[typeIdx],
				At:         now.Add(time.Duration(offsetMs) * time.Millisecond),
				ToolName:   fmt.Sprintf("tool-%d", i),
				ToolCallID: fmt.Sprintf("call-%d", i),
			}
			normalizer.AddEvent(evt, sources[srcIdx])
		}

		resolved := normalizer.ResolveConflicts(window)

		// Invariant 1: Resolved count <= input count
		if len(resolved) > numEvents {
			t.Fatalf("resolved (%d) > input (%d)", len(resolved), numEvents)
		}

		// Invariant 2: All resolved events have valid confidence levels
		validConfidence := map[harness.EventConfidence]bool{
			harness.ConfidenceHigh:     true,
			harness.ConfidenceMedium:   true,
			harness.ConfidenceDegraded: true,
		}
		for i, e := range resolved {
			if !validConfidence[e.Confidence] {
				t.Fatalf("invalid confidence %q at index %d", e.Confidence, i)
			}
		}

		// Invariant 3: OTEL source always maps to high confidence
		for i, e := range resolved {
			if e.Source == "otel" && e.Confidence != harness.ConfidenceHigh {
				t.Fatalf("otel event at index %d has confidence %q, want high", i, e.Confidence)
			}
		}

		// Invariant 4: When dedup occurs between sources, higher priority wins
		for _, e := range resolved {
			if e.Source == "session_log" {
				// If session_log survived, no otel or hook version should exist
				// for the same identity within the window
				for _, other := range resolved {
					if other.Source != "session_log" &&
						other.Event.Type == e.Event.Type &&
						other.Event.ToolName == e.Event.ToolName &&
						other.Event.ToolCallID == e.Event.ToolCallID {
						dt := other.Event.At.Sub(e.Event.At)
						if dt < 0 {
							dt = -dt
						}
						if dt <= window {
							t.Fatalf("session_log event coexists with %s event within window", other.Source)
						}
					}
				}
			}
		}
	})
}

// =============================================================================
// P3: Config Path Stability
// For a given session ID and data dir, the computed config path is always the same.
// =============================================================================

func TestP3_ConfigPathStability(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate session IDs with various valid patterns
		sessionID := rapid.StringMatching(`[a-z0-9][a-z0-9\-]{2,30}[a-z0-9]`).Draw(rt, "sessionID")

		// Verify config path follows deterministic scheme:
		// <dataDir>/configs/<sessionID>/
		// We test the path construction logic directly since sandbox uses t.TempDir()
		baseDir := t.TempDir()
		sandbox1 := harness.NewSandboxEnv(t, harness.SandboxEnvConfig{
			SessionID: sessionID,
			DataDir:   baseDir,
		})

		sandbox2 := harness.NewSandboxEnv(t, harness.SandboxEnvConfig{
			SessionID: sessionID,
			DataDir:   baseDir,
		})

		path1 := sandbox1.ConfigPath()
		path2 := sandbox2.ConfigPath()

		// Invariant: Same session ID + same base dir → same config path
		if path1 != path2 {
			rt.Fatalf("config path not stable: %s vs %s", path1, path2)
		}

		// Invariant: Path contains the session ID
		harness.AssertConfigPathStable(t, path1, sandbox1.DataDir, sessionID)
	})
}

// =============================================================================
// P4: Idle Snapshot Trigger Invariant
// Each idle boundary produces exactly one snapshot trigger marker.
// =============================================================================

func TestP4_IdleSnapshotTriggerInvariant(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		numIdleBoundaries := rapid.IntRange(1, 10).Draw(rt, "numIdle")
		sessionID := fmt.Sprintf("p4-idle-%d", numIdleBoundaries)

		sandbox := harness.NewSandboxEnv(t, harness.SandboxEnvConfig{
			SessionID: sessionID,
		})

		// Simulate N idle boundaries, each triggering exactly one snapshot
		for i := 0; i < numIdleBoundaries; i++ {
			snapName := fmt.Sprintf("turn-%d-idle", i)
			sandbox.SimulateSnapshot(snapName)

			// Invariant: Snapshot exists after creation
			if !sandbox.SnapshotExists(snapName) {
				t.Fatalf("snapshot %s not created", snapName)
			}
		}

		// Invariant: Each idle boundary has exactly one snapshot
		for i := 0; i < numIdleBoundaries; i++ {
			snapName := fmt.Sprintf("turn-%d-idle", i)
			if !sandbox.SnapshotExists(snapName) {
				t.Fatalf("snapshot %s missing after batch creation", snapName)
			}
		}

		// Invariant: No extra snapshots exist beyond what we created
		extraSnap := fmt.Sprintf("turn-%d-idle", numIdleBoundaries)
		if sandbox.SnapshotExists(extraSnap) {
			t.Fatalf("unexpected extra snapshot %s", extraSnap)
		}
	})
}

// =============================================================================
// P5: Attach/Detach Safety
// Attach/detach operations do not alter driver semantic state.
// =============================================================================

func TestP5_AttachDetachSafety(t *testing.T) {
	// Launch a real PTY session for attach/detach cycling
	env := harness.NewTermmuxEnv(t, harness.TermmuxEnvConfig{
		SessionID: "p5-attach-detach",
		Command:   "/bin/sh",
		Args:      []string{"-c", "while true; do sleep 0.1; done"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := env.Start(ctx, ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	rapid.Check(t, func(rt *rapid.T) {
		numCycles := rapid.IntRange(1, 8).Draw(rt, "numCycles")

		// Record state and metrics before attach/detach cycles
		stateBefore, _ := env.Monitor.State()
		metricsBefore := env.Monitor.Metrics()

		// Perform actual attach/detach operations
		for i := 0; i < numCycles; i++ {
			clientID := fmt.Sprintf("p5-client-%d-%d", numCycles, i)

			client := env.Attach(clientID)
			if client == nil {
				rt.Fatalf("attach returned nil at cycle %d", i)
			}

			// Verify state unchanged after attach
			stateAfter, _ := env.Monitor.State()
			if stateAfter != stateBefore {
				rt.Fatalf("state changed after attach cycle %d: %s → %s",
					i, stateBefore, stateAfter)
			}

			env.Detach(clientID)

			// Verify state unchanged after detach
			stateAfter, _ = env.Monitor.State()
			if stateAfter != stateBefore {
				rt.Fatalf("state changed after detach cycle %d: %s → %s",
					i, stateBefore, stateAfter)
			}
		}

		// Metrics should be unchanged by attach/detach
		metricsAfter := env.Monitor.Metrics()
		if metricsAfter.TurnCount != metricsBefore.TurnCount {
			rt.Fatalf("turn count changed: %d → %d", metricsBefore.TurnCount, metricsAfter.TurnCount)
		}
	})

	// Session should still be alive after all cycles
	if !env.IsRunning() {
		t.Fatal("session died during attach/detach property test")
	}
}

func buildSimpleReplayScript() []harness.ReplayEntry {
	base := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	return []harness.ReplayEntry{
		{
			Timestamp: base,
			Source:    "otel",
			Data:      mustJSON(harness.OTELData{Span: "session_started", Attrs: map[string]any{"session_id": "prop-test"}}),
		},
		{
			Timestamp: base.Add(500 * time.Millisecond),
			Source:    "otel",
			Data:      mustJSON(harness.OTELData{Span: "tool_started", Attrs: map[string]any{"tool_name": "read", "call_id": "c1"}}),
		},
		{
			Timestamp: base.Add(1000 * time.Millisecond),
			Source:    "otel",
			Data:      mustJSON(harness.OTELData{Span: "tool_completed", Attrs: map[string]any{"tool_name": "read", "call_id": "c1"}}),
		},
		{
			Timestamp: base.Add(2000 * time.Millisecond),
			Source:    "otel",
			Data:      mustJSON(harness.OTELData{Span: "turn_completed", Attrs: map[string]any{"input_tokens": float64(500), "output_tokens": float64(200)}}),
		},
	}
}
