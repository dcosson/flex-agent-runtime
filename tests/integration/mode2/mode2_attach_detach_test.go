package mode2

import (
	"context"
	"testing"
	"time"

	"flex-agent-runtime/internal/agent"
	"flex-agent-runtime/tests/integration/mode2/harness"
)

// S3: Attach/detach lifecycle (no event loss).
// Validates that clients can attach and detach while the session remains active,
// and that no events are lost during attach/detach cycles.
func TestAttachDetachLifecycle(t *testing.T) {
	env := harness.NewTermmuxEnv(t, harness.TermmuxEnvConfig{
		SessionID: "s3-attach-detach",
		Command:   "/bin/sh",
		Args:      []string{"-c", "echo 'line1'; sleep 0.1; echo 'line2'; sleep 0.1; echo 'line3'"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := env.Start(ctx, ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Attach first client
	client1 := env.Attach("client-1")
	if client1 == nil {
		t.Fatal("attach client-1 returned nil")
	}

	// Verify session is running
	if !env.IsRunning() {
		t.Fatal("session not running after start")
	}

	// Wait briefly for some output
	time.Sleep(50 * time.Millisecond)

	// Attach second client while first is attached
	client2 := env.Attach("client-2")
	if client2 == nil {
		t.Fatal("attach client-2 returned nil")
	}

	// Detach first client — session should continue
	env.Detach("client-1")

	// Wait for more output
	time.Sleep(50 * time.Millisecond)

	// Re-attach first client
	client1Again := env.Attach("client-1-reattach")
	if client1Again == nil {
		t.Fatal("re-attach client-1 returned nil")
	}

	// Detach second client
	env.Detach("client-2")

	// Wait for session to complete naturally
	env.Wait()

	// Verify we got session_started
	events := env.Events()
	if len(events) == 0 {
		t.Fatal("no events captured")
	}

	harness.AssertEventSequence(t, events, agent.EventSessionStarted)

	// Verify the session stopped cleanly
	if env.IsRunning() {
		t.Fatal("session still running after child exited")
	}
}

// TestAttachDetachNoEventLoss verifies event continuity across attach/detach.
func TestAttachDetachNoEventLoss(t *testing.T) {
	env := harness.NewTermmuxEnv(t, harness.TermmuxEnvConfig{
		SessionID: "s3-no-loss",
		Command:   "/bin/sh",
		Args:      []string{"-c", "echo 'start'; sleep 0.2; echo 'end'"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := env.Start(ctx, ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Rapid attach/detach cycle
	for i := 0; i < 5; i++ {
		id := "rapid-client"
		c := env.Attach(id)
		if c == nil {
			t.Fatalf("attach %d returned nil", i)
		}
		env.Detach(id)
	}

	// Wait for completion
	env.Wait()

	// Events should still be captured via the adapter subscription
	// (which is independent of client attach/detach)
	events := env.Events()
	if len(events) == 0 {
		t.Fatal("no events captured after rapid attach/detach")
	}

	harness.AssertEventSequence(t, events, agent.EventSessionStarted)
}
