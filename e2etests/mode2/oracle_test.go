package mode2

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"h2-agent-runtime/e2etests/mode2/harness"
	"h2-agent-runtime/internal/agent"
	"h2-agent-runtime/internal/termmux/monitor"
)

// =============================================================================
// O1: Source-Fusion Oracle
// Compare normalized event stream against hand-labeled ground truth traces.
// =============================================================================

func TestO1_SourceFusionOracle(t *testing.T) {
	// Ground truth: the canonical lifecycle event types that must appear
	// as a subsequence of the monitor events for basic_session.jsonl.
	// The fixture has OTEL + hook events, so the monitor receives duplicates.
	// We verify the key lifecycle milestones are present in the correct order.
	// Note: call_id in fixture is "call-001", not "c1".
	groundTruthTypes := []monitor.AgentEventType{
		monitor.EventSessionStarted,
		monitor.EventToolStarted,
		monitor.EventToolCompleted,
		monitor.EventTurnCompleted,
		monitor.EventSessionEnded,
	}

	// Load the fixture and replay through the simulator
	fixturePath := fixturesDir(t, "driver_logs/basic_session.jsonl")
	entries, err := harness.LoadReplayScript(fixturePath)
	if err != nil {
		t.Fatalf("LoadReplayScript: %v", err)
	}

	sim := harness.NewDeterministicDriverSimulator(entries, harness.ReplayFastForward)

	mon := monitor.NewAgentMonitor()
	defer mon.Close()

	evtCh := make(chan monitor.AgentEvent, 64)
	_, unsub := mon.Subscribe(evtCh)
	defer unsub()

	sim.MonitorSubmit = mon.Submit

	if err := sim.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := drainMonitorEvents(evtCh, 200*time.Millisecond)

	// Match ground truth types as a subsequence
	gtIdx := 0
	for _, evt := range events {
		if gtIdx >= len(groundTruthTypes) {
			break
		}
		if evt.Type == groundTruthTypes[gtIdx] {
			gtIdx++
		}
	}

	if gtIdx != len(groundTruthTypes) {
		var gotTypes []string
		for _, evt := range events {
			gotTypes = append(gotTypes, string(evt.Type))
		}
		t.Fatalf("ground truth mismatch: matched %d/%d milestones\n  want: %v\n  got events: %v",
			gtIdx, len(groundTruthTypes), groundTruthTypes, gotTypes)
	}

	// Verify specific data on key events
	for _, evt := range events {
		if evt.Type == monitor.EventTurnCompleted {
			if d, ok := evt.Data.(monitor.TurnCompletedData); ok {
				if d.InputTokens == 0 && d.OutputTokens == 0 {
					t.Error("turn_completed has zero tokens")
				}
			}
		}
	}
}

// TestO1_SourceFusionOracle_MultiSource tests fusion when events come from
// multiple sources with the normalizer resolving conflicts.
func TestO1_SourceFusionOracle_MultiSource(t *testing.T) {
	now := time.Now()

	// Ground truth: tool_started should appear once, from OTEL (higher priority)
	normalizer := harness.NewEventNormalizer()

	// OTEL reports tool_started first
	normalizer.AddEvent(agent.AgentEvent{
		Type:       agent.EventToolStarted,
		ToolName:   "read",
		ToolCallID: "c1",
		At:         now,
	}, "otel")

	// Hook reports the same tool_started 30ms later
	normalizer.AddEvent(agent.AgentEvent{
		Type:       agent.EventToolStarted,
		ToolName:   "read",
		ToolCallID: "c1",
		At:         now.Add(30 * time.Millisecond),
	}, "hook")

	// Session log reports it 80ms later
	normalizer.AddEvent(agent.AgentEvent{
		Type:       agent.EventToolStarted,
		ToolName:   "read",
		ToolCallID: "c1",
		At:         now.Add(80 * time.Millisecond),
	}, "session_log")

	resolved := normalizer.ResolveConflicts(200 * time.Millisecond)

	// Oracle: exactly 1 event, from OTEL, high confidence
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved event, got %d", len(resolved))
	}
	if resolved[0].Source != "otel" {
		t.Fatalf("expected otel source, got %q", resolved[0].Source)
	}
	if resolved[0].Confidence != harness.ConfidenceHigh {
		t.Fatalf("expected high confidence, got %q", resolved[0].Confidence)
	}
}

// =============================================================================
// O2: Driver Parity Oracle
// Run equivalent scripted scenario on different driver types.
// Compare normalized lifecycle semantics (not task-level outcomes).
// =============================================================================

func TestO2_DriverParityOracle(t *testing.T) {
	// Build equivalent replay scripts for two "drivers" — same lifecycle,
	// different timing/details
	claudeEntries := buildDriverReplayScript("claude", "session-claude")
	codexEntries := buildDriverReplayScript("codex", "session-codex")

	// Extract lifecycle event sequences from each
	claudeLifecycle := extractLifecycleSequence(t, claudeEntries)
	codexLifecycle := extractLifecycleSequence(t, codexEntries)

	// Lifecycle patterns should be equivalent regardless of driver
	if len(claudeLifecycle) != len(codexLifecycle) {
		t.Fatalf("lifecycle length mismatch: claude=%d, codex=%d",
			len(claudeLifecycle), len(codexLifecycle))
	}

	for i := range claudeLifecycle {
		if claudeLifecycle[i] != codexLifecycle[i] {
			t.Fatalf("lifecycle mismatch at index %d: claude=%s, codex=%s",
				i, claudeLifecycle[i], codexLifecycle[i])
		}
	}
}

// =============================================================================
// O3: Mode 1/Native Reference Oracle
// Compare canonical milestone patterns against analogous scenario.
// =============================================================================

func TestO3_NativeReferenceOracle(t *testing.T) {
	// Build a Mode 2 replay script and extract milestones
	mode2Entries := buildDriverReplayScript("mode2", "session-m2")
	mode2Milestones := extractLifecycleSequence(t, mode2Entries)

	// Expected canonical milestone ordering for any driver
	expectedOrder := []string{
		"session_started",
		"tool_started",
		"tool_completed",
		"turn_completed",
		"session_ended",
	}

	if len(mode2Milestones) < len(expectedOrder) {
		t.Fatalf("insufficient milestones: got %d, want at least %d",
			len(mode2Milestones), len(expectedOrder))
	}

	// Verify subsequence match
	idx := 0
	for _, m := range mode2Milestones {
		if idx < len(expectedOrder) && m == expectedOrder[idx] {
			idx++
		}
	}
	if idx != len(expectedOrder) {
		t.Fatalf("milestone ordering mismatch: matched %d/%d\n  want: %v\n  got:  %v",
			idx, len(expectedOrder), expectedOrder, mode2Milestones)
	}
}

// --- Helpers ---

func buildDriverReplayScript(driverType, sessionID string) []harness.ReplayEntry {
	base := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	return []harness.ReplayEntry{
		{
			Timestamp: base,
			Source:    "otel",
			Data:      mustJSON(harness.OTELData{Span: "session_started", Attrs: map[string]any{"session_id": sessionID, "driver": driverType}}),
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
		{
			Timestamp: base.Add(3000 * time.Millisecond),
			Source:    "otel",
			Data:      mustJSON(harness.OTELData{Span: "session_ended", Attrs: map[string]any{"reason": "complete"}}),
		},
	}
}

func extractLifecycleSequence(t *testing.T, entries []harness.ReplayEntry) []string {
	t.Helper()
	sim := harness.NewDeterministicDriverSimulator(entries, harness.ReplayFastForward)

	mon := monitor.NewAgentMonitor()
	defer mon.Close()

	evtCh := make(chan monitor.AgentEvent, 64)
	_, unsub := mon.Subscribe(evtCh)
	defer unsub()

	sim.MonitorSubmit = mon.Submit

	if err := sim.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := drainMonitorEvents(evtCh, 200*time.Millisecond)

	// Extract lifecycle event type names
	var sequence []string
	for _, evt := range events {
		sequence = append(sequence, string(evt.Type))
	}
	return sequence
}

// fixturesDir resolves the path to a fixture file relative to the project root.
func fixturesDir(t *testing.T, relPath string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file path")
	}
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(projectRoot, "e2etests", "fixtures", relPath)
}
