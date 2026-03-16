package mode2

import (
	"context"
	"fmt"
	"testing"
	"time"

	"h2-agent-runtime/internal/agent"
	"h2-agent-runtime/internal/termmux/monitor"
	"h2-agent-runtime/tests/integration/mode2/harness"
)

// =============================================================================
// S1: Session-Log Replay Simulation
// Replay captured driver logs through the normalizer and verify
// deterministic outputs.
// =============================================================================

func TestS1_SessionLogReplay_Deterministic(t *testing.T) {
	fixturePath := fixturesDir(t, "driver_logs/basic_session.jsonl")
	entries, err := harness.LoadReplayScript(fixturePath)
	if err != nil {
		t.Fatalf("LoadReplayScript: %v", err)
	}

	// Run the same replay script 3 times — outputs should be identical
	var allRuns [][]string
	for run := 0; run < 3; run++ {
		sim := harness.NewDeterministicDriverSimulator(entries, harness.ReplayFastForward)

		mon := monitor.NewAgentMonitor()
		evtCh := make(chan monitor.AgentEvent, 64)
		_, unsub := mon.Subscribe(evtCh)

		sim.MonitorSubmit = mon.Submit

		if err := sim.Run(); err != nil {
			unsub()
			mon.Close()
			t.Fatalf("run %d: %v", run, err)
		}

		events := drainMonitorEvents(evtCh, 200*time.Millisecond)
		unsub()
		mon.Close()

		var types []string
		for _, evt := range events {
			types = append(types, string(evt.Type))
		}
		allRuns = append(allRuns, types)
	}

	// All runs should produce the same event type sequence
	for i := 1; i < len(allRuns); i++ {
		if len(allRuns[i]) != len(allRuns[0]) {
			t.Fatalf("run %d produced %d events, run 0 produced %d",
				i, len(allRuns[i]), len(allRuns[0]))
		}
		for j := range allRuns[0] {
			if allRuns[i][j] != allRuns[0][j] {
				t.Fatalf("run %d event %d: %s != %s", i, j, allRuns[i][j], allRuns[0][j])
			}
		}
	}
}

func TestS1_SessionLogReplay_CallbackCounts(t *testing.T) {
	fixturePath := fixturesDir(t, "driver_logs/basic_session.jsonl")
	entries, err := harness.LoadReplayScript(fixturePath)
	if err != nil {
		t.Fatalf("LoadReplayScript: %v", err)
	}

	sim := harness.NewDeterministicDriverSimulator(entries, harness.ReplayFastForward)

	var ptyCount, otelCount, hookCount int
	sim.OnPTYOutput = func(data []byte) { ptyCount++ }
	sim.OnOTELSpan = func(otel harness.OTELData) { otelCount++ }
	sim.OnHookEvent = func(hook harness.HookData) { hookCount++ }

	if err := sim.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	// basic_session.jsonl has: 5 otel, 1 pty, 3 hook
	if ptyCount != 1 {
		t.Fatalf("pty callbacks: %d want 1", ptyCount)
	}
	if otelCount != 5 {
		t.Fatalf("otel callbacks: %d want 5", otelCount)
	}
	if hookCount != 3 {
		t.Fatalf("hook callbacks: %d want 3", hookCount)
	}
}

// =============================================================================
// S2: Lifecycle Command Simulation
// Simulate attach/detach/stop command sequences and verify expected
// state transitions.
// =============================================================================

func TestS2_LifecycleCommandSimulation_AttachDetachStop(t *testing.T) {
	env := harness.NewTermmuxEnv(t, harness.TermmuxEnvConfig{
		SessionID: "s2-lifecycle-sim",
		Command:   "/bin/sh",
		Args:      []string{"-c", "while true; do sleep 0.1; done"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := env.Start(ctx, ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait for session to be active
	time.Sleep(200 * time.Millisecond)
	if !env.IsRunning() {
		t.Fatal("session not running")
	}

	// Simulate lifecycle sequence: attach → detach → attach → stop
	client1 := env.Attach("sim-client-1")
	if client1 == nil {
		t.Fatal("attach returned nil")
	}

	env.Detach("sim-client-1")

	client2 := env.Attach("sim-client-2")
	if client2 == nil {
		t.Fatal("second attach returned nil")
	}

	env.Detach("sim-client-2")

	// Verify session still running after attach/detach cycles
	if !env.IsRunning() {
		t.Fatal("session died during attach/detach")
	}

	// Stop should terminate cleanly
	env.Stop()
	time.Sleep(200 * time.Millisecond)

	if env.IsRunning() {
		t.Fatal("session still running after stop")
	}

	events := env.Events()
	harness.AssertEventSequence(t, events, agent.EventSessionStarted)
}

func TestS2_LifecycleCommandSimulation_MultiClientConcurrent(t *testing.T) {
	env := harness.NewTermmuxEnv(t, harness.TermmuxEnvConfig{
		SessionID: "s2-multi-client",
		Command:   "/bin/sh",
		Args:      []string{"-c", "while true; do sleep 0.1; done"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := env.Start(ctx, ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Attach multiple clients simultaneously
	clients := make([]string, 5)
	for i := range clients {
		clients[i] = fmt.Sprintf("client-%d", i)
		c := env.Attach(clients[i])
		if c == nil {
			t.Fatalf("attach client %d returned nil", i)
		}
	}

	// Detach all clients
	for _, cid := range clients {
		env.Detach(cid)
	}

	// Session should still be running
	if !env.IsRunning() {
		t.Fatal("session died during multi-client lifecycle")
	}

	env.Stop()
}

// =============================================================================
// S3: Config-Dir Migration Simulation
// Simulate path changes and verify auth invalidation behavior.
// =============================================================================

func TestS3_ConfigDirMigration_PathChange(t *testing.T) {
	sessionID := "s3-migration"

	// Create initial sandbox with credentials
	sandbox1 := harness.NewSandboxEnv(t, harness.SandboxEnvConfig{
		SessionID: sessionID,
	})

	injector1 := harness.NewConfigInjector(t, sandbox1.ConfigDir)
	injector1.InjectAPIKey("api_key.txt", "sk-original-path")
	injector1.InjectDriverConfig("settings.json", `{"model":"claude-sonnet-4-20250514"}`)

	originalPath := sandbox1.ConfigPath()

	// Create a new sandbox with a DIFFERENT data dir (simulates migration)
	sandbox2 := harness.NewSandboxEnv(t, harness.SandboxEnvConfig{
		SessionID: sessionID,
	})

	newPath := sandbox2.ConfigPath()

	// Paths should be different (different base dirs)
	if originalPath == newPath {
		t.Fatal("migration paths should differ when base dirs differ")
	}

	// New sandbox should NOT have the old credentials (clean start)
	if sandbox2.ConfigFileExists("api_key.txt") {
		t.Fatal("credentials leaked to new config dir during migration")
	}

	// Verify the path scheme is still valid for both
	harness.AssertConfigPathStable(t, originalPath, sandbox1.DataDir, sessionID)
	harness.AssertConfigPathStable(t, newPath, sandbox2.DataDir, sessionID)
}

func TestS3_ConfigDirMigration_SameBasePreservesAuth(t *testing.T) {
	sessionID := "s3-same-base"

	// Create sandbox and inject credentials
	sandbox1 := harness.NewSandboxEnv(t, harness.SandboxEnvConfig{
		SessionID: sessionID,
	})

	injector1 := harness.NewConfigInjector(t, sandbox1.ConfigDir)
	injector1.InjectAPIKey("api_key.txt", "sk-preserved-key")

	// Create second sandbox sharing the same base dir
	sandbox2 := harness.NewSandboxEnv(t, harness.SandboxEnvConfig{
		SessionID: sessionID,
		DataDir:   sandbox1.DataDir + "/..",
	})

	// Credentials should be preserved (same base + same session ID)
	injector2 := harness.NewConfigInjector(t, sandbox2.ConfigDir)
	key := injector2.ReadCredential("api_key.txt")
	if key != "sk-preserved-key" {
		t.Fatalf("credential not preserved during same-base migration: %q", key)
	}
}
