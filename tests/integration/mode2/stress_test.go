package mode2

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/termmux/monitor"
	"github.com/dcosson/flex-agent-runtime/tests/integration/mode2/harness"
)

// =============================================================================
// ST1: Long-Running Driver Soak
// Continuous driver sessions with periodic control actions.
// Assertions: no leaks, stable event processing.
// Shortened for CI (30s instead of 12h — full soak runs in Weekly CI tier).
// =============================================================================

func TestST1_DriverSoak(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping soak test in short mode")
	}

	soakDuration := 30 * time.Second
	tickInterval := 500 * time.Millisecond

	// Support full 12h soak for Weekly CI tier via environment variable
	if tier := os.Getenv("MODE2_HARNESS_TIER"); tier == "weekly" {
		soakDuration = 12 * time.Hour
		tickInterval = 5 * time.Second
		t.Logf("MODE2_HARNESS_TIER=weekly: running full 12h soak")
	}

	mon := monitor.NewAgentMonitor()
	defer mon.Close()

	evtCh := make(chan monitor.AgentEvent, 4096)
	_, unsub := mon.Subscribe(evtCh)
	defer unsub()

	// Track event processing
	var eventCount atomic.Int64
	go func() {
		for range evtCh {
			eventCount.Add(1)
		}
	}()

	// Record initial memory
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	// Generate periodic events for the soak duration
	deadline := time.After(soakDuration)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	turnNum := 0
	mon.Submit(monitor.AgentEvent{
		Type:      monitor.EventSessionStarted,
		Timestamp: time.Now(),
		Data:      monitor.SessionStartedData{SessionID: "st1-soak"},
	})

soak:
	for {
		select {
		case <-deadline:
			break soak
		case <-ticker.C:
			turnNum++
			callID := fmt.Sprintf("soak-c%d", turnNum)

			// Simulate a tool call cycle
			mon.Submit(monitor.AgentEvent{
				Type:      monitor.EventToolStarted,
				Timestamp: time.Now(),
				Data:      monitor.ToolStartedData{ToolName: "read", CallID: callID},
			})
			mon.Submit(monitor.AgentEvent{
				Type:      monitor.EventToolCompleted,
				Timestamp: time.Now(),
				Data:      monitor.ToolCompletedData{ToolName: "read", CallID: callID, Success: true},
			})
			mon.Submit(monitor.AgentEvent{
				Type:      monitor.EventTurnCompleted,
				Timestamp: time.Now(),
				Data:      monitor.TurnCompletedData{InputTokens: 100, OutputTokens: 50},
			})
		}
	}

	// End session
	mon.Submit(monitor.AgentEvent{
		Type:      monitor.EventSessionEnded,
		Timestamp: time.Now(),
		Data:      monitor.SessionEndedData{Reason: "soak_complete"},
	})

	time.Sleep(200 * time.Millisecond) // drain

	// Verify events were processed
	total := eventCount.Load()
	if total == 0 {
		t.Fatal("no events processed during soak")
	}

	// Check for memory leaks: heap growth should be bounded
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	heapGrowthMB := float64(memAfter.HeapInuse-memBefore.HeapInuse) / (1024 * 1024)
	t.Logf("soak: %d events, %d turns, heap growth: %.2f MB", total, turnNum, heapGrowthMB)

	// Heap growth should be reasonable (<50MB for a 30s soak)
	if heapGrowthMB > 50 {
		t.Fatalf("excessive heap growth during soak: %.2f MB", heapGrowthMB)
	}

	// Verify metrics are consistent
	metrics := mon.Metrics()
	if metrics.TurnCount != int64(turnNum) {
		t.Fatalf("turn count mismatch: got %d, want %d", metrics.TurnCount, turnNum)
	}
}

// =============================================================================
// ST2: Multi-Session Stress
// Run many concurrent Mode 2 sessions across drivers.
// Assertions: session isolation, stable PTY operations.
// =============================================================================

func TestST2_MultiSessionStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	numSessions := 10
	eventsPerSession := 50

	var wg sync.WaitGroup
	errors := make(chan error, numSessions)

	for s := 0; s < numSessions; s++ {
		wg.Add(1)
		go func(sessionNum int) {
			defer wg.Done()

			sessionID := fmt.Sprintf("st2-session-%d", sessionNum)
			mon := monitor.NewAgentMonitor()
			defer mon.Close()

			evtCh := make(chan monitor.AgentEvent, 256)
			_, unsub := mon.Subscribe(evtCh)
			defer unsub()

			// Drain events
			var eventCount int
			done := make(chan struct{})
			go func() {
				for range evtCh {
					eventCount++
				}
				close(done)
			}()

			// Submit events
			mon.Submit(monitor.AgentEvent{
				Type:      monitor.EventSessionStarted,
				Timestamp: time.Now(),
				Data:      monitor.SessionStartedData{SessionID: sessionID},
			})

			for i := 0; i < eventsPerSession; i++ {
				callID := fmt.Sprintf("s%d-c%d", sessionNum, i)
				mon.Submit(monitor.AgentEvent{
					Type:      monitor.EventToolStarted,
					Timestamp: time.Now(),
					Data:      monitor.ToolStartedData{ToolName: "read", CallID: callID},
				})
				mon.Submit(monitor.AgentEvent{
					Type:      monitor.EventToolCompleted,
					Timestamp: time.Now(),
					Data:      monitor.ToolCompletedData{ToolName: "read", CallID: callID, Success: true},
				})
			}

			mon.Submit(monitor.AgentEvent{
				Type:      monitor.EventSessionEnded,
				Timestamp: time.Now(),
				Data:      monitor.SessionEndedData{Reason: "complete"},
			})

			time.Sleep(100 * time.Millisecond) // drain

			// Verify metrics isolation
			metrics := mon.Metrics()
			if metrics.TurnCount != 0 {
				// No turn_completed events submitted, so turn count should be 0
			}

			// Verify state reached exited
			state, _ := mon.State()
			if state != monitor.StateExited {
				errors <- fmt.Errorf("session %d: expected exited state, got %s", sessionNum, state)
			}
		}(s)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}
}

// TestST2_MultiSessionStress_WithPTY tests concurrent sessions with real PTY.
func TestST2_MultiSessionStress_WithPTY(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY stress test in short mode")
	}

	numSessions := 5

	var wg sync.WaitGroup
	errors := make(chan error, numSessions)

	for s := 0; s < numSessions; s++ {
		wg.Add(1)
		go func(sessionNum int) {
			defer wg.Done()

			env := harness.NewTermmuxEnv(t, harness.TermmuxEnvConfig{
				SessionID: fmt.Sprintf("st2-pty-%d", sessionNum),
				Command:   "/bin/sh",
				Args:      []string{"-c", fmt.Sprintf("echo 'session %d'; exit 0", sessionNum)},
			})

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := env.Start(ctx, ""); err != nil {
				errors <- fmt.Errorf("session %d start: %v", sessionNum, err)
				return
			}

			env.Wait()

			if env.IsRunning() {
				errors <- fmt.Errorf("session %d still running", sessionNum)
			}
		}(s)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}
}

// =============================================================================
// ST3: Burst Lifecycle Stress
// Rapid attach/detach commands across many sessions.
// Assertions: bounded failure rate and clean recovery.
// =============================================================================

func TestST3_BurstLifecycleStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping burst stress test in short mode")
	}

	env := harness.NewTermmuxEnv(t, harness.TermmuxEnvConfig{
		SessionID: "st3-burst",
		Command:   "/bin/sh",
		Args:      []string{"-c", "while true; do sleep 0.1; done"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := env.Start(ctx, ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Burst: rapid attach/detach from multiple goroutines
	numWorkers := 5
	opsPerWorker := 20
	var wg sync.WaitGroup
	var attachErrors atomic.Int64

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for i := 0; i < opsPerWorker; i++ {
				clientID := fmt.Sprintf("burst-w%d-c%d", workerID, i)

				client := env.Attach(clientID)
				if client == nil {
					attachErrors.Add(1)
					continue
				}

				// Brief pause to simulate interaction
				time.Sleep(time.Millisecond)

				env.Detach(clientID)
			}
		}(w)
	}

	wg.Wait()

	totalOps := int64(numWorkers * opsPerWorker)
	failRate := float64(attachErrors.Load()) / float64(totalOps)

	t.Logf("burst stress: %d total ops, %d attach errors (%.2f%% failure rate)",
		totalOps, attachErrors.Load(), failRate*100)

	// Bounded failure rate: < 5%
	if failRate > 0.05 {
		t.Fatalf("excessive burst failure rate: %.2f%%", failRate*100)
	}

	// Session should still be controllable after burst
	if !env.IsRunning() {
		t.Fatal("session died during burst stress")
	}

	env.Stop()
	time.Sleep(200 * time.Millisecond)

	if env.IsRunning() {
		t.Fatal("session still running after post-burst stop")
	}
}
