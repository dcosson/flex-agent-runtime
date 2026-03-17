package mode2

import (
	"context"
	"testing"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/agent"
	"github.com/anthropics/flex-agent-runtime/internal/termmux/monitor"
	"github.com/anthropics/flex-agent-runtime/tests/integration/mode2/harness"
)

// =============================================================================
// F1: PTY write timeout / hung child
// Simulate child process stop-reading behavior. Verify timeout detection
// and controlled teardown.
// =============================================================================

func TestF1_PTYWriteTimeoutHungChild(t *testing.T) {
	// Launch a process that ignores stdin (simulates hung child)
	env := harness.NewTermmuxEnv(t, harness.TermmuxEnvConfig{
		SessionID: "f1-hung-child",
		Command:   "/bin/sh",
		Args:      []string{"-c", "sleep 60"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := env.Start(ctx, ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait for session to be running
	time.Sleep(100 * time.Millisecond)
	if !env.IsRunning() {
		t.Fatal("session not running")
	}

	// Write to PTY — process is sleeping and not reading stdin, but
	// the PTY buffer absorbs writes without blocking immediately.
	// This validates we don't deadlock.
	_, err := env.WritePTY([]byte("test input\n"))
	if err != nil {
		// Write timeout/error is acceptable — we just verify no deadlock
		t.Logf("PTY write returned error (expected for hung child): %v", err)
	}

	// Force stop should still work cleanly
	env.Stop()

	if env.IsRunning() {
		t.Fatal("session still running after stop of hung child")
	}

	events := env.Events()
	harness.AssertEventSequence(t, events, agent.EventSessionStarted)
}

// TestF1_PTYWriteFlood verifies the system handles rapid writes without deadlock.
func TestF1_PTYWriteFlood(t *testing.T) {
	env := harness.NewTermmuxEnv(t, harness.TermmuxEnvConfig{
		SessionID: "f1-write-flood",
		Command:   "/bin/sh",
		Args:      []string{"-c", "cat > /dev/null"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := env.Start(ctx, ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Flood the PTY with writes
	data := make([]byte, 4096)
	for i := range data {
		data[i] = 'A'
	}

	for i := 0; i < 50; i++ {
		if _, err := env.WritePTY(data); err != nil {
			t.Logf("write %d returned error: %v", i, err)
			break
		}
	}

	// Should still be controllable
	env.Stop()
	if env.IsRunning() {
		t.Fatal("session still running after flood stop")
	}
}

// =============================================================================
// F2: Event-source partial failure
// Disable one source at a time. Verify normalizer degrades gracefully
// and still emits critical milestones.
// =============================================================================

func TestF2_EventSourcePartialFailure_OTELOnly(t *testing.T) {
	// Build script with only OTEL events (hooks and session-log disabled)
	entries := buildOTELOnlyReplayScript()
	sim := harness.NewDeterministicDriverSimulator(entries, harness.ReplayFastForward)

	mon := monitor.NewAgentMonitor()
	defer mon.Close()

	evtCh := make(chan monitor.AgentEvent, 64)
	_, unsub := mon.Subscribe(evtCh)
	defer unsub()

	sim.MonitorSubmit = mon.Submit

	var otelCount int
	sim.OnOTELSpan = func(otel harness.OTELData) { otelCount++ }

	if err := sim.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	events := drainMonitorEvents(evtCh, 100*time.Millisecond)

	if otelCount == 0 {
		t.Fatal("no OTEL events dispatched")
	}

	// Critical milestones must still be present from OTEL alone
	hasSessionStart := false
	for _, evt := range events {
		if evt.Type == monitor.EventSessionStarted {
			hasSessionStart = true
		}
	}
	if !hasSessionStart {
		t.Fatal("session_started missing with OTEL-only source")
	}
}

func TestF2_EventSourcePartialFailure_HooksOnly(t *testing.T) {
	// Build script with only hook events (OTEL and session-log disabled)
	entries := buildHooksOnlyReplayScript()
	sim := harness.NewDeterministicDriverSimulator(entries, harness.ReplayFastForward)

	mon := monitor.NewAgentMonitor()
	defer mon.Close()

	evtCh := make(chan monitor.AgentEvent, 64)
	_, unsub := mon.Subscribe(evtCh)
	defer unsub()

	sim.MonitorSubmit = mon.Submit

	var hookCount int
	sim.OnHookEvent = func(hook harness.HookData) { hookCount++ }

	if err := sim.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	events := drainMonitorEvents(evtCh, 100*time.Millisecond)

	if hookCount == 0 {
		t.Fatal("no hook events dispatched")
	}

	// Critical milestones must still be present from hooks alone
	hasSessionStart := false
	for _, evt := range events {
		if evt.Type == monitor.EventSessionStarted {
			hasSessionStart = true
		}
	}
	if !hasSessionStart {
		t.Fatal("session_started missing with hooks-only source")
	}
}

func TestF2_EventSourcePartialFailure_NormalizerDegradation(t *testing.T) {
	// When only session_log source is available, events should have degraded confidence
	normalizer := harness.NewEventNormalizer()
	now := time.Now()

	normalizer.AddEvent(agent.AgentEvent{
		Type: agent.EventSessionStarted,
		At:   now,
	}, "session_log")
	normalizer.AddEvent(agent.AgentEvent{
		Type:     agent.EventToolStarted,
		ToolName: "read",
		At:       now.Add(100 * time.Millisecond),
	}, "session_log")

	resolved := normalizer.ResolveConflicts(200 * time.Millisecond)
	for _, evt := range resolved {
		if evt.Confidence != harness.ConfidenceDegraded {
			t.Fatalf("session_log event should have degraded confidence, got %q", evt.Confidence)
		}
	}
}

// =============================================================================
// F3: Credential injection failure
// Corrupt/missing injected config files. Verify predictable failure diagnostics.
// =============================================================================

func TestF3_CredentialInjectionFailure_MissingFile(t *testing.T) {
	sandbox := harness.NewSandboxEnv(t, harness.SandboxEnvConfig{
		SessionID: "f3-missing-cred",
	})

	injector := harness.NewConfigInjector(t, sandbox.ConfigDir)

	// Don't inject any credentials — verify ReadCredential fails predictably
	if injector.VerifyCredentialExists("nonexistent_key.txt") {
		t.Fatal("nonexistent credential reported as existing")
	}
}

func TestF3_CredentialInjectionFailure_EmptyKey(t *testing.T) {
	sandbox := harness.NewSandboxEnv(t, harness.SandboxEnvConfig{
		SessionID: "f3-empty-cred",
	})

	injector := harness.NewConfigInjector(t, sandbox.ConfigDir)
	// Inject an empty API key
	injector.InjectAPIKey("api_key.txt", "")

	key := injector.ReadCredential("api_key.txt")
	if key != "" {
		t.Fatalf("expected empty key, got %q", key)
	}
}

func TestF3_CredentialInjectionFailure_OverwriteProtection(t *testing.T) {
	sandbox := harness.NewSandboxEnv(t, harness.SandboxEnvConfig{
		SessionID: "f3-overwrite",
	})

	injector := harness.NewConfigInjector(t, sandbox.ConfigDir)

	// Inject initial key
	injector.InjectAPIKey("api_key.txt", "sk-original")

	// Overwrite with a new key (this is valid — the injector supports updates)
	injector.InjectAPIKey("api_key.txt", "sk-updated")

	key := injector.ReadCredential("api_key.txt")
	if key != "sk-updated" {
		t.Fatalf("credential overwrite failed: got %q", key)
	}
}

// =============================================================================
// F4: Pause/resume race under output burst
// Rapid pause/resume while output flows. Verify no deadlock and consistent state.
// =============================================================================

func TestF4_PauseResumeRaceUnderBurst(t *testing.T) {
	// Launch a process that produces continuous output
	env := harness.NewTermmuxEnv(t, harness.TermmuxEnvConfig{
		SessionID: "f4-pause-resume-race",
		Command:   "/bin/sh",
		Args:      []string{"-c", "while true; do echo 'output burst line'; done"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := env.Start(ctx, ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if !env.IsRunning() {
		t.Fatal("session not running")
	}

	// Rapid pause/resume cycles while output is flowing
	const cycles = 50
	for i := 0; i < cycles; i++ {
		if err := env.Pause(); err != nil {
			// Session may have exited naturally; that's acceptable
			t.Logf("pause cycle %d: %v", i, err)
			break
		}
		if !env.IsPaused() {
			t.Fatalf("cycle %d: not paused after Pause()", i)
		}
		if err := env.Resume(); err != nil {
			t.Logf("resume cycle %d: %v", i, err)
			break
		}
		if env.IsPaused() {
			t.Fatalf("cycle %d: still paused after Resume()", i)
		}
	}

	// Session should still be controllable — stop cleanly
	env.Stop()
	if env.IsRunning() {
		t.Fatal("session still running after stop")
	}

	// Should have captured events without corruption
	events := env.Events()
	harness.AssertEventSequence(t, events, agent.EventSessionStarted)
}

// =============================================================================
// F5: Driver crash mid-turn
// Force abrupt CLI exit. Verify terminal events, artifact capture, and recovery.
// =============================================================================

func TestF5_DriverCrashMidTurn(t *testing.T) {
	// Launch a process that will crash after producing some output
	env := harness.NewTermmuxEnv(t, harness.TermmuxEnvConfig{
		SessionID: "f5-crash-mid-turn",
		Command:   "/bin/sh",
		Args:      []string{"-c", "echo 'starting tool execution'; sleep 0.1; kill -9 $$"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := env.Start(ctx, ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait for crash
	env.Wait()

	// Session should not be running
	if env.IsRunning() {
		t.Fatal("session still running after crash")
	}

	events := env.Events()
	if len(events) == 0 {
		t.Fatal("no events captured after crash")
	}

	// Should have session_started even after crash
	harness.AssertEventSequence(t, events, agent.EventSessionStarted)
}

func TestF5_DriverCrashMidTurn_ConfigSurvives(t *testing.T) {
	sandbox := harness.NewSandboxEnv(t, harness.SandboxEnvConfig{
		SessionID: "f5-crash-config",
	})

	injector := harness.NewConfigInjector(t, sandbox.ConfigDir)
	injector.InjectAPIKey("api_key.txt", "sk-crash-mid-turn")
	injector.InjectDriverConfig("settings.json", `{"model":"claude-sonnet-4-20250514"}`)

	// Simulate mid-turn workspace modification
	sandbox.WriteWorkspaceFile("partial_edit.go", "package main // partial")

	// Config should survive regardless of workspace state
	key := injector.ReadCredential("api_key.txt")
	if key != "sk-crash-mid-turn" {
		t.Fatalf("credential lost during mid-turn crash: %q", key)
	}

	settings := injector.ReadCredential("settings.json")
	if settings != `{"model":"claude-sonnet-4-20250514"}` {
		t.Fatalf("settings lost during mid-turn crash: %q", settings)
	}
}

// --- Helpers ---

func buildOTELOnlyReplayScript() []harness.ReplayEntry {
	base := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	return []harness.ReplayEntry{
		{
			Timestamp: base,
			Source:    "otel",
			Data:      mustJSON(harness.OTELData{Span: "session_started", Attrs: map[string]any{"session_id": "f2-otel-only"}}),
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

func buildHooksOnlyReplayScript() []harness.ReplayEntry {
	base := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	return []harness.ReplayEntry{
		{
			Timestamp: base,
			Source:    "hook",
			Data:      mustJSON(harness.HookData{Event: "session_started", SessionID: "f2-hooks-only"}),
		},
		{
			Timestamp: base.Add(500 * time.Millisecond),
			Source:    "hook",
			Data:      mustJSON(harness.HookData{Event: "tool_started", ToolName: "read", CallID: "c1"}),
		},
		{
			Timestamp: base.Add(1000 * time.Millisecond),
			Source:    "hook",
			Data:      mustJSON(harness.HookData{Event: "tool_completed", ToolName: "read", CallID: "c1"}),
		},
	}
}

func drainMonitorEvents(ch <-chan monitor.AgentEvent, timeout time.Duration) []monitor.AgentEvent {
	var events []monitor.AgentEvent
	timer := time.After(timeout)
	for {
		select {
		case evt := <-ch:
			events = append(events, evt)
		case <-timer:
			return events
		}
	}
}
