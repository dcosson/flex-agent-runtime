package mode2

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/termmux/monitor"
	"github.com/anthropics/flex-agent-runtime/tests/integration/mode2/harness"
)

// S1: Driver launch with credential injection.
// Validates that a driver launches in the sandbox with managed config,
// credentials are accessible, and initial events are emitted.
func TestDriverLaunchWithCredentialInjection(t *testing.T) {
	// Set up sandbox environment
	sandbox := harness.NewSandboxEnv(t, harness.SandboxEnvConfig{
		SessionID: "s1-launch-test",
	})

	// Set up config injection
	injector := harness.NewConfigInjector(t, sandbox.ConfigDir)
	injector.InjectAPIKey("api_key.txt", "sk-test-key-12345")
	injector.InjectDriverConfig("settings.json", `{"model":"claude-sonnet-4-20250514","theme":"dark"}`)

	// Verify credentials were written
	if !injector.VerifyCredentialExists("api_key.txt") {
		t.Fatal("api_key.txt not found after injection")
	}
	if !injector.VerifyCredentialExists("settings.json") {
		t.Fatal("settings.json not found after injection")
	}

	// Verify credential content
	key := injector.ReadCredential("api_key.txt")
	if key != "sk-test-key-12345" {
		t.Fatalf("api key mismatch: got %q", key)
	}

	// Verify config path follows scheme
	harness.AssertConfigPathStable(t, sandbox.ConfigPath(), sandbox.DataDir, "s1-launch-test")

	// Build env vars with config directory
	envVars := injector.BuildEnvVars()
	if envVars["CONFIG_DIR"] != sandbox.ConfigDir {
		t.Fatalf("CONFIG_DIR mismatch: %q", envVars["CONFIG_DIR"])
	}

	// Simulate driver launch with deterministic replay
	entries := buildLaunchReplayScript("s1-launch-test")
	sim := harness.NewDeterministicDriverSimulator(entries, harness.ReplayFastForward)

	// Create a monitor to collect events
	mon := monitor.NewAgentMonitor()
	defer mon.Close()

	evtCh := make(chan monitor.AgentEvent, 64)
	_, unsub := mon.Subscribe(evtCh)
	defer unsub()

	sim.MonitorSubmit = mon.Submit

	var ptyOutput []byte
	sim.OnPTYOutput = func(data []byte) {
		ptyOutput = append(ptyOutput, data...)
	}

	if err := sim.Run(); err != nil {
		t.Fatalf("simulator run: %v", err)
	}

	// Verify events were emitted
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

	if len(events) == 0 {
		t.Fatal("no events received from simulator")
	}

	// Verify session_started event
	foundStart := false
	for _, evt := range events {
		if evt.Type == monitor.EventSessionStarted {
			foundStart = true
			if d, ok := evt.Data.(monitor.SessionStartedData); ok {
				if d.SessionID != "s1-launch-test" {
					t.Fatalf("session_id=%q want s1-launch-test", d.SessionID)
				}
			}
		}
	}
	if !foundStart {
		t.Fatal("session_started event not found")
	}

	// Verify PTY output was captured
	if len(ptyOutput) == 0 {
		t.Fatal("no PTY output captured")
	}
}

func buildLaunchReplayScript(sessionID string) []harness.ReplayEntry {
	return []harness.ReplayEntry{
		{
			Timestamp: time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC),
			Source:    "otel",
			Data: mustJSON(harness.OTELData{
				Span:  "session_started",
				Attrs: map[string]any{"session_id": sessionID, "model": "claude-sonnet-4-20250514"},
			}),
		},
		{
			Timestamp: time.Date(2026, 3, 12, 10, 0, 0, 100_000_000, time.UTC),
			Source:    "pty",
			Data:      mustJSON(base64.StdEncoding.EncodeToString([]byte("Welcome to Claude Code\n"))),
		},
		{
			Timestamp: time.Date(2026, 3, 12, 10, 0, 1, 0, time.UTC),
			Source:    "otel",
			Data: mustJSON(harness.OTELData{
				Span:  "turn_completed",
				Attrs: map[string]any{"input_tokens": float64(500), "output_tokens": float64(200)},
			}),
		},
	}
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
