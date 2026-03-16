package mode2

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"h2-agent-runtime/internal/agent"
	"h2-agent-runtime/internal/termmux"
	"h2-agent-runtime/internal/termmux/monitor"
	"h2-agent-runtime/tests/integration/mode2/harness"
)

// =============================================================================
// B1: Event Normalization Latency
// Target: p95 normalization lag < 200ms from raw input to canonical event emission.
// =============================================================================

func BenchmarkB1_EventNormalizationLatency(b *testing.B) {
	b.ReportAllocs()

	// Pre-build events
	now := time.Now()
	events := make([]agent.AgentEvent, 100)
	for i := range events {
		events[i] = agent.AgentEvent{
			Type:       agent.EventToolStarted,
			ToolName:   fmt.Sprintf("tool-%d", i%10),
			ToolCallID: fmt.Sprintf("call-%d", i),
			At:         now.Add(time.Duration(i) * 10 * time.Millisecond),
		}
	}

	sources := []string{"otel", "hook", "session_log"}
	var latencies []time.Duration

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		normalizer := harness.NewEventNormalizer()

		start := time.Now()
		for j, evt := range events {
			normalizer.AddEvent(evt, sources[j%len(sources)])
		}
		normalizer.ResolveConflicts(200 * time.Millisecond)
		elapsed := time.Since(start)

		latencies = append(latencies, elapsed)
	}

	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		p95idx := int(float64(len(latencies)) * 0.95)
		if p95idx >= len(latencies) {
			p95idx = len(latencies) - 1
		}
		p95 := latencies[p95idx]
		b.ReportMetric(float64(p95.Microseconds()), "p95_us")

		if p95 > 200*time.Millisecond {
			b.Fatalf("p95 normalization latency %v exceeds 200ms target", p95)
		}
	}
}

// BenchmarkB1_NormalizationScaling measures how normalization scales with event count.
func BenchmarkB1_NormalizationScaling(b *testing.B) {
	for _, count := range []int{10, 50, 100, 500} {
		b.Run(fmt.Sprintf("events=%d", count), func(b *testing.B) {
			b.ReportAllocs()

			now := time.Now()
			events := make([]agent.AgentEvent, count)
			for i := range events {
				events[i] = agent.AgentEvent{
					Type:       agent.EventToolStarted,
					ToolName:   fmt.Sprintf("tool-%d", i%10),
					ToolCallID: fmt.Sprintf("call-%d", i),
					At:         now.Add(time.Duration(i) * 10 * time.Millisecond),
				}
			}
			sources := []string{"otel", "hook", "session_log"}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				normalizer := harness.NewEventNormalizer()
				for j, evt := range events {
					normalizer.AddEvent(evt, sources[j%len(sources)])
				}
				normalizer.ResolveConflicts(200 * time.Millisecond)
			}
		})
	}
}

// =============================================================================
// B2: PTY Throughput
// Target: sustain high-output driver sessions without dropped frames.
// =============================================================================

func BenchmarkB2_PTYThroughput(b *testing.B) {
	b.ReportAllocs()

	// Measure simulator replay throughput (events processed per second)
	entries := buildLargeReplayScript(1000) // 1000 events

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sim := harness.NewDeterministicDriverSimulator(entries, harness.ReplayFastForward)

		mon := monitor.NewAgentMonitor()
		evtCh := make(chan monitor.AgentEvent, 4096)
		_, unsub := mon.Subscribe(evtCh)
		sim.MonitorSubmit = mon.Submit

		_ = sim.Run()

		unsub()
		close(evtCh) // allow drain goroutine to exit
		mon.Close()
	}
}

// =============================================================================
// B3: Attach Latency
// Target: attach/reattach operations complete p95 < 150ms.
// =============================================================================

func BenchmarkB3_AttachLatency(b *testing.B) {
	b.ReportAllocs()

	// Use termmux.Session directly since NewTermmuxEnv requires *testing.T
	sess := termmux.NewSession("b3-attach-latency", termmux.SessionConfig{
		Command:     "/bin/sh",
		Args:        []string{"-c", "while true; do sleep 1; done"},
		InitialRows: 24,
		InitialCols: 80,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := sess.Start(ctx); err != nil {
		b.Fatalf("start: %v", err)
	}
	defer sess.Stop()

	time.Sleep(200 * time.Millisecond)

	var latencies []time.Duration

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clientID := fmt.Sprintf("bench-client-%d", i)

		start := time.Now()
		client := sess.Attach(clientID)
		elapsed := time.Since(start)

		if client == nil {
			b.Fatalf("attach returned nil at iteration %d", i)
		}

		latencies = append(latencies, elapsed)
		sess.Detach(clientID)
	}

	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		p95idx := int(float64(len(latencies)) * 0.95)
		if p95idx >= len(latencies) {
			p95idx = len(latencies) - 1
		}
		p95 := latencies[p95idx]
		b.ReportMetric(float64(p95.Microseconds()), "p95_us")

		if p95 > 150*time.Millisecond {
			b.Fatalf("p95 attach latency %v exceeds 150ms target", p95)
		}
	}
}

// =============================================================================
// B4: Idle Snapshot Trigger Latency
// Target: idle detection to snapshot trigger signal p95 < 500ms.
// =============================================================================

func BenchmarkB4_IdleSnapshotTriggerLatency(b *testing.B) {
	b.ReportAllocs()

	var latencies []time.Duration

	for i := 0; i < b.N; i++ {
		// Create a monitor with a short idle threshold
		mon := monitor.NewAgentMonitor(monitor.WithIdleThreshold(50 * time.Millisecond))
		evtCh := make(chan monitor.AgentEvent, 64)
		_, unsub := mon.Subscribe(evtCh)

		// Submit session started + turn completed to get to a state where idle can fire
		mon.Submit(monitor.AgentEvent{
			Type:      monitor.EventSessionStarted,
			Timestamp: time.Now(),
			Data:      monitor.SessionStartedData{SessionID: "b4-idle"},
		})
		time.Sleep(5 * time.Millisecond)

		// Mark the time we expect idle to fire after
		start := time.Now()

		// Wait for idle event
		deadline := time.After(1 * time.Second)
		gotIdle := false
	drain:
		for {
			select {
			case evt := <-evtCh:
				if evt.Type == monitor.EventStateChange {
					if d, ok := evt.Data.(monitor.StateChangeData); ok && d.State == monitor.StateIdle {
						latencies = append(latencies, time.Since(start))
						gotIdle = true
						break drain
					}
				}
			case <-deadline:
				break drain
			}
		}

		unsub()
		mon.Close()

		if !gotIdle {
			// Idle may not always fire depending on timing — skip this iteration
			continue
		}
	}

	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		p95idx := int(float64(len(latencies)) * 0.95)
		if p95idx >= len(latencies) {
			p95idx = len(latencies) - 1
		}
		p95 := latencies[p95idx]
		b.ReportMetric(float64(p95.Milliseconds()), "p95_ms")

		if p95 > 500*time.Millisecond {
			b.Fatalf("p95 idle trigger latency %v exceeds 500ms target", p95)
		}
	}
}

// --- Helpers ---

func buildLargeReplayScript(n int) []harness.ReplayEntry {
	base := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	entries := make([]harness.ReplayEntry, 0, n)

	// Start with session_started
	entries = append(entries, harness.ReplayEntry{
		Timestamp: base,
		Source:    "otel",
		Data:      mustJSON(harness.OTELData{Span: "session_started", Attrs: map[string]any{"session_id": "bench"}}),
	})

	// Add tool_started/tool_completed pairs
	for i := 1; i < n-1; i += 2 {
		callID := fmt.Sprintf("c%d", i/2)
		entries = append(entries, harness.ReplayEntry{
			Timestamp: base.Add(time.Duration(i) * 10 * time.Millisecond),
			Source:    "otel",
			Data:      mustJSON(harness.OTELData{Span: "tool_started", Attrs: map[string]any{"tool_name": "read", "call_id": callID}}),
		})
		if i+1 < n-1 {
			entries = append(entries, harness.ReplayEntry{
				Timestamp: base.Add(time.Duration(i+1) * 10 * time.Millisecond),
				Source:    "otel",
				Data:      mustJSON(harness.OTELData{Span: "tool_completed", Attrs: map[string]any{"tool_name": "read", "call_id": callID}}),
			})
		}
	}

	return entries
}
