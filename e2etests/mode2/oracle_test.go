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
	// Build structurally different replay scripts for Claude and Codex drivers.
	// Claude: multi-turn with compaction and approval events.
	// Codex: single-turn, no compaction, no approval.
	// Despite structural differences, both should normalize to the same
	// canonical lifecycle milestone sequence.
	claudeEntries := buildClaudeReplayScript()
	codexEntries := buildCodexReplayScript()

	// Extract lifecycle event sequences from each
	claudeLifecycle := extractLifecycleMilestones(t, claudeEntries)
	codexLifecycle := extractLifecycleMilestones(t, codexEntries)

	// Both should have the canonical milestones in order
	canonicalMilestones := []monitor.AgentEventType{
		monitor.EventSessionStarted,
		monitor.EventToolStarted,
		monitor.EventToolCompleted,
		monitor.EventTurnCompleted,
		monitor.EventSessionEnded,
	}

	verifyMilestoneSubsequence(t, "claude", claudeLifecycle, canonicalMilestones)
	verifyMilestoneSubsequence(t, "codex", codexLifecycle, canonicalMilestones)
}

// =============================================================================
// O3: Mode 1/Native Reference Oracle
// Compare canonical milestone patterns against analogous scenario.
// =============================================================================

func TestO3_NativeReferenceOracle(t *testing.T) {
	// Build a Mode 2 replay script with OTEL/hook sources (termmux driver)
	// and a Mode 1/Native reference script with only hook-style events
	// (no OTEL, simulating a native driver without instrumentation).
	// Both should normalize to the same canonical milestone sequence.
	mode2Entries := buildClaudeReplayScript() // OTEL+hook sources
	nativeEntries := buildNativeDriverReplayScript() // hook-only sources

	mode2Milestones := extractLifecycleMilestones(t, mode2Entries)
	nativeMilestones := extractLifecycleMilestones(t, nativeEntries)

	// Expected canonical milestone ordering for any driver
	canonicalMilestones := []monitor.AgentEventType{
		monitor.EventSessionStarted,
		monitor.EventToolStarted,
		monitor.EventToolCompleted,
		monitor.EventSessionEnded,
	}

	verifyMilestoneSubsequence(t, "mode2", mode2Milestones, canonicalMilestones)
	verifyMilestoneSubsequence(t, "native", nativeMilestones, canonicalMilestones)
}

// --- Helpers ---

// buildClaudeReplayScript creates a Claude-like driver replay with OTEL+hook
// sources, multi-tool execution, and turn completion with token metrics.
func buildClaudeReplayScript() []harness.ReplayEntry {
	base := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	return []harness.ReplayEntry{
		// OTEL: session started
		{Timestamp: base, Source: "otel",
			Data: mustJSON(harness.OTELData{Span: "session_started", Attrs: map[string]any{"session_id": "claude-1", "driver": "claude"}})},
		// Hook: duplicate session started (lower priority)
		{Timestamp: base.Add(20 * time.Millisecond), Source: "hook",
			Data: mustJSON(harness.HookData{Event: "session_started", SessionID: "claude-1"})},
		// OTEL: tool started (read)
		{Timestamp: base.Add(300 * time.Millisecond), Source: "otel",
			Data: mustJSON(harness.OTELData{Span: "tool_started", Attrs: map[string]any{"tool_name": "read", "call_id": "c1"}})},
		// Hook: duplicate tool started
		{Timestamp: base.Add(330 * time.Millisecond), Source: "hook",
			Data: mustJSON(harness.HookData{Event: "tool_started", ToolName: "read", CallID: "c1"})},
		// OTEL: tool completed
		{Timestamp: base.Add(800 * time.Millisecond), Source: "otel",
			Data: mustJSON(harness.OTELData{Span: "tool_completed", Attrs: map[string]any{"tool_name": "read", "call_id": "c1"}})},
		// OTEL: second tool (write) — Claude does multi-tool
		{Timestamp: base.Add(1200 * time.Millisecond), Source: "otel",
			Data: mustJSON(harness.OTELData{Span: "tool_started", Attrs: map[string]any{"tool_name": "write", "call_id": "c2"}})},
		{Timestamp: base.Add(1700 * time.Millisecond), Source: "otel",
			Data: mustJSON(harness.OTELData{Span: "tool_completed", Attrs: map[string]any{"tool_name": "write", "call_id": "c2"}})},
		// OTEL: turn completed with token metrics
		{Timestamp: base.Add(2000 * time.Millisecond), Source: "otel",
			Data: mustJSON(harness.OTELData{Span: "turn_completed", Attrs: map[string]any{"input_tokens": float64(1500), "output_tokens": float64(600)}})},
		// OTEL: session ended
		{Timestamp: base.Add(3000 * time.Millisecond), Source: "otel",
			Data: mustJSON(harness.OTELData{Span: "session_ended", Attrs: map[string]any{"reason": "complete"}})},
	}
}

// buildCodexReplayScript creates a Codex-like driver replay with single-turn,
// single-tool, hook-only sources (no OTEL), and no compaction.
func buildCodexReplayScript() []harness.ReplayEntry {
	base := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	return []harness.ReplayEntry{
		// Hook: session started (no OTEL for Codex)
		{Timestamp: base, Source: "hook",
			Data: mustJSON(harness.HookData{Event: "session_started", SessionID: "codex-1"})},
		// Hook: single tool call
		{Timestamp: base.Add(400 * time.Millisecond), Source: "hook",
			Data: mustJSON(harness.HookData{Event: "tool_started", ToolName: "bash", CallID: "cx1"})},
		{Timestamp: base.Add(1500 * time.Millisecond), Source: "hook",
			Data: mustJSON(harness.HookData{Event: "tool_completed", ToolName: "bash", CallID: "cx1"})},
		// OTEL: turn completed (even Codex might emit OTEL for turns)
		{Timestamp: base.Add(2000 * time.Millisecond), Source: "otel",
			Data: mustJSON(harness.OTELData{Span: "turn_completed", Attrs: map[string]any{"input_tokens": float64(200), "output_tokens": float64(100)}})},
		// Hook: session ended
		{Timestamp: base.Add(2500 * time.Millisecond), Source: "hook",
			Data: mustJSON(harness.HookData{Event: "session_ended", SessionID: "codex-1"})},
	}
}

// buildNativeDriverReplayScript creates a Mode 1/Native replay with hook-only
// events (no OTEL instrumentation), different timing and tool names.
func buildNativeDriverReplayScript() []harness.ReplayEntry {
	base := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	return []harness.ReplayEntry{
		// Hook: session started
		{Timestamp: base, Source: "hook",
			Data: mustJSON(harness.HookData{Event: "session_started", SessionID: "native-1"})},
		// Hook: tool started (native uses different tool names)
		{Timestamp: base.Add(200 * time.Millisecond), Source: "hook",
			Data: mustJSON(harness.HookData{Event: "tool_started", ToolName: "edit", CallID: "n1"})},
		// Hook: tool completed
		{Timestamp: base.Add(600 * time.Millisecond), Source: "hook",
			Data: mustJSON(harness.HookData{Event: "tool_completed", ToolName: "edit", CallID: "n1"})},
		// Hook: session ended
		{Timestamp: base.Add(1500 * time.Millisecond), Source: "hook",
			Data: mustJSON(harness.HookData{Event: "session_ended", SessionID: "native-1"})},
	}
}

// extractLifecycleMilestones runs entries through the monitor and returns
// the event types received.
func extractLifecycleMilestones(t *testing.T, entries []harness.ReplayEntry) []monitor.AgentEventType {
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

	var milestones []monitor.AgentEventType
	for _, evt := range events {
		milestones = append(milestones, evt.Type)
	}
	return milestones
}

// verifyMilestoneSubsequence checks that expected milestones appear as a
// subsequence in the actual event types.
func verifyMilestoneSubsequence(t *testing.T, driverName string, actual []monitor.AgentEventType, expected []monitor.AgentEventType) {
	t.Helper()
	idx := 0
	for _, m := range actual {
		if idx < len(expected) && m == expected[idx] {
			idx++
		}
	}
	if idx != len(expected) {
		t.Fatalf("%s: milestone subsequence mismatch: matched %d/%d\n  want: %v\n  got:  %v",
			driverName, idx, len(expected), expected, actual)
	}
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
