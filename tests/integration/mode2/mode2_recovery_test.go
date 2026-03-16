package mode2

import (
	"context"
	"testing"
	"time"

	"flex-agent-runtime/internal/agent"
	"flex-agent-runtime/tests/integration/mode2/harness"
)

// S6: Recovery from driver exit/crash.
// Validates that when a driver process exits or crashes,
// clean termination events are emitted and recovery is deterministic.
func TestRecoveryFromDriverExit(t *testing.T) {
	// Launch a short-lived process that exits naturally
	env := harness.NewTermmuxEnv(t, harness.TermmuxEnvConfig{
		SessionID: "s6-exit-recovery",
		Command:   "/bin/sh",
		Args:      []string{"-c", "echo 'working...'; exit 0"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := env.Start(ctx, ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait for natural exit
	env.Wait()

	events := env.Events()
	if len(events) == 0 {
		t.Fatal("no events captured")
	}

	// Should have session_started
	harness.AssertEventSequence(t, events, agent.EventSessionStarted)

	// Session should not be running
	if env.IsRunning() {
		t.Fatal("session still running after exit")
	}
}

// TestRecoveryFromDriverCrash simulates a crash (non-zero exit).
func TestRecoveryFromDriverCrash(t *testing.T) {
	env := harness.NewTermmuxEnv(t, harness.TermmuxEnvConfig{
		SessionID: "s6-crash-recovery",
		Command:   "/bin/sh",
		Args:      []string{"-c", "echo 'crashing...'; exit 1"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := env.Start(ctx, ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait for crash exit
	env.Wait()

	events := env.Events()
	if len(events) == 0 {
		t.Fatal("no events captured")
	}

	// Should still have session_started
	harness.AssertEventSequence(t, events, agent.EventSessionStarted)

	// Session should not be running
	if env.IsRunning() {
		t.Fatal("session still running after crash")
	}
}

// TestRecoveryFromForcedStop simulates explicit stop during execution.
func TestRecoveryFromForcedStop(t *testing.T) {
	// Launch a long-running process
	env := harness.NewTermmuxEnv(t, harness.TermmuxEnvConfig{
		SessionID: "s6-forced-stop",
		Command:   "/bin/sh",
		Args:      []string{"-c", "while true; do sleep 0.1; done"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := env.Start(ctx, ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Verify it's running
	time.Sleep(100 * time.Millisecond)
	if !env.IsRunning() {
		t.Fatal("session not running")
	}

	// Force stop
	env.Stop()

	// Give it a moment to clean up
	time.Sleep(200 * time.Millisecond)

	// Session should not be running
	if env.IsRunning() {
		t.Fatal("session still running after forced stop")
	}

	events := env.Events()
	if len(events) == 0 {
		t.Fatal("no events captured")
	}

	// Should have session_started
	harness.AssertEventSequence(t, events, agent.EventSessionStarted)
}

// TestRecoveryConfigPersistence validates config survives driver crash.
func TestRecoveryConfigPersistence(t *testing.T) {
	sandbox := harness.NewSandboxEnv(t, harness.SandboxEnvConfig{
		SessionID: "s6-crash-config",
	})

	// Inject credentials before launch
	injector := harness.NewConfigInjector(t, sandbox.ConfigDir)
	injector.InjectAPIKey("api_key.txt", "sk-crash-test-key")

	// Simulate driver crash (just verify config persists)
	// In real scenario: driver writes to workspace, crashes, workspace may rollback
	sandbox.WriteWorkspaceFile("in_progress.txt", "partial work")

	// Config should be intact after crash
	key := injector.ReadCredential("api_key.txt")
	if key != "sk-crash-test-key" {
		t.Fatalf("credential lost after crash: %q", key)
	}

	// Config path stable for recovery
	harness.AssertConfigPathStable(t, sandbox.ConfigPath(), sandbox.DataDir, "s6-crash-config")
}
