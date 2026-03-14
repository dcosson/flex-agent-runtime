package agent

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"h2-agent-runtime/internal/ai"
)

func BenchmarkB1EventFanoutThroughput(b *testing.B) {
	bus := newEventBus()
	const subscribers = 10
	for i := 0; i < subscribers; i++ {
		_, _ = bus.subscribe(func(AgentEvent) {})
	}

	evt := AgentEvent{Type: EventTurnCompleted, At: time.Now()}
	b.ReportAllocs()
	b.ResetTimer()
	start := time.Now()
	for i := 0; i < b.N; i++ {
		bus.publish(evt)
	}
	dur := time.Since(start)
	if dur <= 0 {
		return
	}
	b.ReportMetric(float64(b.N)/dur.Seconds(), "events/sec")
}

func BenchmarkB2PromptToFirstDeltaOverhead(b *testing.B) {
	ai.ClearProviders()
	b.Cleanup(ai.ClearProviders)

	const apiName = "agent-b2-bench"
	prov := &scriptedProvider{api: apiName, responses: []ai.AssistantMessage{{
		Content:    []ai.ContentBlock{&ai.TextContent{Text: "ok"}},
		StopReason: ai.StopReasonStop,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}}}
	ai.RegisterProvider(prov, apiName)

	overheads := make([]time.Duration, 0, b.N)
	for i := 0; i < b.N; i++ {
		directFirstDelta := measureFirstDeltaDirect(prov)

		driver := NewNativeDriver(DriverConfig{Model: ai.Model{ID: "bench", API: apiName, Provider: "test", MaxTokens: 1024}})
		agent := New(driver)
		agent.SetSession(&Session{ID: fmt.Sprintf("bench-b2-%d", i)})

		firstDelta := make(chan time.Time, 1)
		idle := make(chan struct{}, 1)
		agent.Subscribe(func(evt AgentEvent) {
			switch {
			case evt.Type == EventAgentMessageDelta:
				select {
				case firstDelta <- time.Now():
				default:
				}
			case evt.Type == EventStateChange && evt.State == StateIdle:
				select {
				case idle <- struct{}{}:
				default:
				}
			}
		})

		start := time.Now()
		if err := agent.Prompt(context.Background(), "hello"); err != nil {
			b.Fatalf("prompt failed: %v", err)
		}

		var first time.Time
		select {
		case first = <-firstDelta:
		case <-time.After(2 * time.Second):
			b.Fatal("timeout waiting for first delta")
		}
		select {
		case <-idle:
		case <-time.After(2 * time.Second):
			b.Fatal("timeout waiting for idle")
		}

		overhead := first.Sub(start) - directFirstDelta
		if overhead < 0 {
			overhead = 0
		}
		overheads = append(overheads, overhead)
	}

	p95 := percentileDuration(overheads, 95)
	b.ReportMetric(float64(p95.Microseconds())/1000.0, "p95_ms")
}

func BenchmarkB3ControlQueueLatency(b *testing.B) {
	const workers = 100
	latency := make([]time.Duration, 0, b.N*workers)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q := NewControlQueue(workers + 16)
		runLat := make(chan time.Duration, workers)
		drained := make(chan struct{})
		go func() {
			for j := 0; j < workers; j++ {
				cmd, ok := <-q.C()
				if !ok {
					break
				}
				runLat <- time.Since(cmd.CreatedAt)
			}
			close(drained)
		}()

		var wg sync.WaitGroup
		start := make(chan struct{})
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_ = q.Enqueue(ControlCommand{Type: ControlSteer, Message: "bench", CreatedAt: time.Now()})
			}()
		}
		close(start)
		wg.Wait()
		<-drained
		close(runLat)
		for d := range runLat {
			latency = append(latency, d)
		}
		q.Close()
	}
	b.StopTimer()

	p95 := percentileDuration(latency, 95)
	b.ReportMetric(float64(p95.Microseconds())/1000.0, "p95_ms")
}

func BenchmarkB4MemoryGrowthBound(b *testing.B) {
	bus := newEventBus()
	for i := 0; i < 10; i++ {
		_, _ = bus.subscribe(func(AgentEvent) {})
	}
	evt := AgentEvent{Type: EventAgentMessageDelta, Delta: "x"}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bus.publish(evt)
	}
	b.StopTimer()

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	growth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	b.ReportMetric(float64(growth)/(1024*1024), "heap_growth_mb")
}

func measureFirstDeltaDirect(prov *scriptedProvider) time.Duration {
	stream := prov.streamOnce()
	start := time.Now()
	for evt := range stream.C {
		if evt.Type == ai.EventTextDelta {
			return time.Since(start)
		}
	}
	return 0
}

func percentileDuration(samples []time.Duration, pct int) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	cp := append([]time.Duration(nil), samples...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := (len(cp)*pct + 99) / 100
	if idx <= 0 {
		idx = 1
	}
	if idx > len(cp) {
		idx = len(cp)
	}
	return cp[idx-1]
}
