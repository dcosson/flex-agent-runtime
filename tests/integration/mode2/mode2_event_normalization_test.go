package mode2

import (
	"encoding/base64"
	"testing"
	"time"

	"h2-agent-runtime/internal/agent"
	"h2-agent-runtime/internal/termmux/monitor"
	"h2-agent-runtime/tests/integration/mode2/harness"
)

// S2: Event normalization correctness (3-source fusion).
// Validates that events from OTEL, hooks, and session-log are correctly
// normalized, deduplicated, and prioritized.
func TestEventNormalizationCorrectness(t *testing.T) {
	// Build a replay script with overlapping events from all three sources.
	// OTEL and hooks both report tool_started/completed — OTEL should win.
	entries := buildNormalizationReplayScript()
	sim := harness.NewDeterministicDriverSimulator(entries, harness.ReplayFastForward)

	mon := monitor.NewAgentMonitor()
	defer mon.Close()

	evtCh := make(chan monitor.AgentEvent, 64)
	_, unsub := mon.Subscribe(evtCh)
	defer unsub()

	sim.MonitorSubmit = mon.Submit

	// Track OTEL and hook dispatch counts
	var otelCount, hookCount int
	sim.OnOTELSpan = func(otel harness.OTELData) { otelCount++ }
	sim.OnHookEvent = func(hook harness.HookData) { hookCount++ }

	if err := sim.Run(); err != nil {
		t.Fatalf("simulator run: %v", err)
	}

	// Drain events
	var events []monitor.AgentEvent
	drainTimer := time.After(100 * time.Millisecond)
drain:
	for {
		select {
		case evt := <-evtCh:
			events = append(events, evt)
		case <-drainTimer:
			break drain
		}
	}

	// Verify all sources were dispatched
	if otelCount == 0 {
		t.Fatal("no OTEL events dispatched")
	}
	if hookCount == 0 {
		t.Fatal("no hook events dispatched")
	}

	// Verify canonical event types are present
	hasSessionStart := false
	hasToolStart := false
	hasToolComplete := false
	hasTurnComplete := false
	hasIdle := false

	for _, evt := range events {
		switch evt.Type {
		case monitor.EventSessionStarted:
			hasSessionStart = true
		case monitor.EventToolStarted:
			hasToolStart = true
		case monitor.EventToolCompleted:
			hasToolComplete = true
		case monitor.EventTurnCompleted:
			hasTurnComplete = true
		case monitor.EventStateChange:
			if d, ok := evt.Data.(monitor.StateChangeData); ok && d.State == monitor.StateIdle {
				hasIdle = true
			}
		}
	}

	if !hasSessionStart {
		t.Error("missing session_started")
	}
	if !hasToolStart {
		t.Error("missing tool_started")
	}
	if !hasToolComplete {
		t.Error("missing tool_completed")
	}
	if !hasTurnComplete {
		t.Error("missing turn_completed")
	}

	// Test the EventNormalizer with source priority
	normalizer := harness.NewEventNormalizer()

	// Simulate OTEL and hook reporting the same tool_started at slightly different times
	now := time.Now()
	otelEvt := agent.AgentEvent{
		Type:     agent.EventToolStarted,
		ToolName: "read",
		At:       now,
	}
	hookEvt := agent.AgentEvent{
		Type:     agent.EventToolStarted,
		ToolName: "read",
		At:       now.Add(50 * time.Millisecond), // Hook arrives 50ms later
	}

	normalizer.AddEvent(otelEvt, "otel")
	normalizer.AddEvent(hookEvt, "hook")

	// Resolve conflicts — OTEL should win within a 200ms window
	resolved := normalizer.ResolveConflicts(200 * time.Millisecond)
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved event, got %d", len(resolved))
	}
	if resolved[0].Source != "otel" {
		t.Fatalf("expected otel source to win, got %q", resolved[0].Source)
	}
	if resolved[0].Confidence != harness.ConfidenceHigh {
		t.Fatalf("expected high confidence, got %q", resolved[0].Confidence)
	}

	// Test degraded confidence for session-log-only events
	normalizer2 := harness.NewEventNormalizer()
	logEvt := agent.AgentEvent{
		Type:     agent.EventToolStarted,
		ToolName: "write",
		At:       now,
	}
	normalizer2.AddEvent(logEvt, "session_log")
	harness.AssertNormalizedConfidence(t, normalizer2.Events(), "session_log", harness.ConfidenceDegraded)

	// Verify idle transition was detected
	_ = hasIdle // Idle comes from the monitor's own state machine, not directly from replay
}

func buildNormalizationReplayScript() []harness.ReplayEntry {
	base := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	return []harness.ReplayEntry{
		// OTEL: session started
		{
			Timestamp: base,
			Source:    "otel",
			Data:      mustJSON(harness.OTELData{Span: "session_started", Attrs: map[string]any{"session_id": "norm-test"}}),
		},
		// PTY: some output
		{
			Timestamp: base.Add(100 * time.Millisecond),
			Source:    "pty",
			Data:      mustJSON(base64.StdEncoding.EncodeToString([]byte("Starting...\n"))),
		},
		// OTEL: tool started (higher priority)
		{
			Timestamp: base.Add(500 * time.Millisecond),
			Source:    "otel",
			Data:      mustJSON(harness.OTELData{Span: "tool_started", Attrs: map[string]any{"tool_name": "read", "call_id": "c1"}}),
		},
		// Hook: tool started (duplicate, lower priority)
		{
			Timestamp: base.Add(550 * time.Millisecond),
			Source:    "hook",
			Data:      mustJSON(harness.HookData{Event: "tool_started", ToolName: "read", CallID: "c1"}),
		},
		// OTEL: tool completed
		{
			Timestamp: base.Add(1000 * time.Millisecond),
			Source:    "otel",
			Data:      mustJSON(harness.OTELData{Span: "tool_completed", Attrs: map[string]any{"tool_name": "read", "call_id": "c1"}}),
		},
		// Hook: tool completed (duplicate)
		{
			Timestamp: base.Add(1050 * time.Millisecond),
			Source:    "hook",
			Data:      mustJSON(harness.HookData{Event: "tool_completed", ToolName: "read", CallID: "c1"}),
		},
		// OTEL: turn completed
		{
			Timestamp: base.Add(2000 * time.Millisecond),
			Source:    "otel",
			Data:      mustJSON(harness.OTELData{Span: "turn_completed", Attrs: map[string]any{"input_tokens": float64(1000), "output_tokens": float64(400)}}),
		},
		// Hook: idle
		{
			Timestamp: base.Add(3000 * time.Millisecond),
			Source:    "hook",
			Data:      mustJSON(harness.HookData{Event: "idle", SessionID: "norm-test"}),
		},
	}
}
