package harness

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"h2-agent-runtime/internal/termmux/monitor"
)

func TestLoadReplayScript_FixtureRoundTrip(t *testing.T) {
	// Load the basic_session.jsonl fixture
	fixturePath := fixturesDir(t, "driver_logs/basic_session.jsonl")
	entries, err := LoadReplayScript(fixturePath)
	if err != nil {
		t.Fatalf("LoadReplayScript: %v", err)
	}

	if len(entries) != 9 {
		t.Fatalf("expected 9 entries, got %d", len(entries))
	}

	// Verify entry sources
	sources := make(map[string]int)
	for _, e := range entries {
		sources[e.Source]++
	}
	if sources["otel"] != 5 {
		t.Fatalf("expected 5 otel entries, got %d", sources["otel"])
	}
	if sources["pty"] != 1 {
		t.Fatalf("expected 1 pty entry, got %d", sources["pty"])
	}
	if sources["hook"] != 3 {
		t.Fatalf("expected 3 hook entries, got %d", sources["hook"])
	}

	// Verify timestamps are ordered
	for i := 1; i < len(entries); i++ {
		if entries[i].Timestamp.Before(entries[i-1].Timestamp) {
			t.Fatalf("entry %d timestamp before entry %d", i, i-1)
		}
	}
}

func TestDeterministicDriverSimulator_FastForward(t *testing.T) {
	fixturePath := fixturesDir(t, "driver_logs/basic_session.jsonl")
	entries, err := LoadReplayScript(fixturePath)
	if err != nil {
		t.Fatalf("LoadReplayScript: %v", err)
	}

	sim := NewDeterministicDriverSimulator(entries, ReplayFastForward)

	var ptyCount, otelCount, hookCount int
	sim.OnPTYOutput = func(data []byte) { ptyCount++ }
	sim.OnOTELSpan = func(otel OTELData) { otelCount++ }
	sim.OnHookEvent = func(hook HookData) { hookCount++ }

	mon := monitor.NewAgentMonitor()
	defer mon.Close()
	evtCh := make(chan monitor.AgentEvent, 64)
	_, unsub := mon.Subscribe(evtCh)
	defer unsub()
	sim.MonitorSubmit = mon.Submit

	start := time.Now()
	if err := sim.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	elapsed := time.Since(start)

	// Fast-forward should complete nearly instantly (< 1s for 5s of events)
	if elapsed > 1*time.Second {
		t.Fatalf("fast-forward took too long: %v", elapsed)
	}

	if ptyCount != 1 {
		t.Fatalf("pty callbacks: %d want 1", ptyCount)
	}
	if otelCount != 5 {
		t.Fatalf("otel callbacks: %d want 5", otelCount)
	}
	if hookCount != 3 {
		t.Fatalf("hook callbacks: %d want 3", hookCount)
	}

	// Drain monitor events
	var monEvents []monitor.AgentEvent
	drainTimer := time.After(100 * time.Millisecond)
drain:
	for {
		select {
		case evt := <-evtCh:
			monEvents = append(monEvents, evt)
		case <-drainTimer:
			break drain
		}
	}

	// Should have monitor events from OTEL and hook dispatches
	if len(monEvents) == 0 {
		t.Fatal("no monitor events received")
	}

	// Verify session_started is present
	found := false
	for _, evt := range monEvents {
		if evt.Type == monitor.EventSessionStarted {
			found = true
		}
	}
	if !found {
		t.Fatal("session_started not found in monitor events")
	}
}

func TestParseReplayScript_Empty(t *testing.T) {
	entries, err := ParseReplayScript([]byte{})
	if err != nil {
		t.Fatalf("ParseReplayScript: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

// fixturesDir resolves the path to a fixture file relative to the project root.
func fixturesDir(t *testing.T, relPath string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file path")
	}
	// Go up from tests/integration/mode2/harness/ to project root.
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	return filepath.Join(projectRoot, "tests", "integration", "fixtures", relPath)
}
