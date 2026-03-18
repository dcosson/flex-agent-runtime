package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/agent"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/server"
	"github.com/dcosson/flex-agent-runtime/internal/tools"
)

// B1. End-to-end tool dispatch latency (Mode3 overhead p95 target <=10ms over local baseline).
func BenchmarkB1_EndToEndToolDispatchLatency(b *testing.B) {
	// Overhead comparison is most meaningful against a real remote host.
	// With the in-memory fake service, remote can appear faster than local
	// filesystem-backed execution, yielding a negative overhead delta.
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "state.txt"), []byte("base"), 0o644); err != nil {
		b.Fatalf("seed file: %v", err)
	}
	local := tools.NewLocalBackend(root)
	svc, sid := newBenchMemoryService(b)
	remote := apiToolClient{svc: svc}

	localDur := make([]time.Duration, 0, b.N)
	remoteDur := make([]time.Duration, 0, b.N)
	for i := 0; i < b.N; i++ {
		start := time.Now()
		_, err := local.ExecuteTool(context.Background(), tools.ToolRequest{ToolCallID: fmt.Sprintf("l-%d", i), ToolName: "read_file", Params: map[string]any{"path": "state.txt"}}, nil)
		if err != nil {
			b.Fatalf("local execute: %v", err)
		}
		localDur = append(localDur, time.Since(start))

		start = time.Now()
		_, err = remote.ExecuteTool(context.Background(), sid, tools.ToolRequest{ToolCallID: fmt.Sprintf("r-%d", i), ToolName: "read_file", Params: map[string]any{"path": "state.txt"}}, nil)
		if err != nil {
			b.Fatalf("remote execute: %v", err)
		}
		remoteDur = append(remoteDur, time.Since(start))
	}
	p95Local := p95(localDur)
	p95Remote := p95(remoteDur)
	delta := p95Remote - p95Local
	b.ReportMetric(float64(p95Local.Microseconds()), "local_p95_us")
	b.ReportMetric(float64(p95Remote.Microseconds()), "remote_p95_us")
	b.ReportMetric(float64(delta.Microseconds()), "overhead_p95_us")
}

// B2. Lifecycle API latency (target p95 <=100ms).
func BenchmarkB2_LifecycleAPILatency(b *testing.B) {
	svc := NewMemorySandboxService()
	base := "memory/base@initial"
	svc.SeedBaseSnapshot(base, map[string]string{"state.txt": "base"})
	lat := make([]time.Duration, 0, b.N*4)
	for i := 0; i < b.N; i++ {
		sid := fmt.Sprintf("bench-lifecycle-%d", i)
		start := time.Now()
		_, err := svc.CreateSession(context.Background(), &api.CreateSessionRequest{SessionID: sid, BaseSnapshot: base})
		if err != nil {
			b.Fatalf("create: %v", err)
		}
		lat = append(lat, time.Since(start))

		start = time.Now()
		_, _ = svc.PauseSession(context.Background(), &api.PauseSessionRequest{SessionID: sid})
		lat = append(lat, time.Since(start))

		start = time.Now()
		_, _ = svc.ResumeSession(context.Background(), &api.ResumeSessionRequest{SessionID: sid})
		lat = append(lat, time.Since(start))

		start = time.Now()
		_, _ = svc.DestroySession(context.Background(), &api.DestroySessionRequest{SessionID: sid})
		lat = append(lat, time.Since(start))
	}
	b.ReportMetric(float64(p95(lat).Microseconds()), "p95_us")
}

// B3. Rollback latency (target p95 <=500ms for small fixture datasets).
func BenchmarkB3_RollbackLatency(b *testing.B) {
	svc, sid := newBenchMemoryService(b)
	for i := 0; i < 32; i++ {
		_, _ = svc.ExecuteTool(context.Background(), &api.ExecuteToolRequest{SessionID: sid, ToolCallID: fmt.Sprintf("seed-%d", i), ToolName: "write_file", Params: map[string]any{"path": fmt.Sprintf("f-%d.txt", i), "content": strings.Repeat("x", 1024)}})
	}
	if _, err := svc.CreateSnapshot(context.Background(), &api.CreateSnapshotRequest{SessionID: sid, Name: "bench-base"}); err != nil {
		b.Fatalf("seed snapshot: %v", err)
	}

	lat := make([]time.Duration, 0, b.N)
	for i := 0; i < b.N; i++ {
		_, _ = svc.ExecuteTool(context.Background(), &api.ExecuteToolRequest{SessionID: sid, ToolCallID: fmt.Sprintf("mut-%d", i), ToolName: "write_file", Params: map[string]any{"path": "state.txt", "content": fmt.Sprintf("v%d", i)}})
		start := time.Now()
		_, err := svc.RollbackSession(context.Background(), &api.RollbackSessionRequest{SessionID: sid, SnapshotID: "bench-base"})
		if err != nil {
			b.Fatalf("rollback: %v", err)
		}
		_, err = svc.ExecuteTool(context.Background(), &api.ExecuteToolRequest{SessionID: sid, ToolCallID: fmt.Sprintf("probe-%d", i), ToolName: "read_file", Params: map[string]any{"path": "state.txt"}})
		if err != nil {
			b.Fatalf("post-rollback probe: %v", err)
		}
		lat = append(lat, time.Since(start))
	}
	b.ReportMetric(float64(p95(lat).Microseconds()), "p95_us")
}

// B4. Event stream throughput (target >=5k events/minute/session).
func BenchmarkB4_EventStreamThroughput(b *testing.B) {
	// Benchmark uses event server directly to avoid provider variability.
	eventSrv := server.NewAgentEventServer()
	sessionID := "bench-events"
	recv, err := eventSrv.StreamAgentEvents(context.Background(), &api.StreamAgentEventsRequest{SessionID: sessionID})
	if err != nil {
		b.Fatalf("stream agent events: %v", err)
	}
	defer recv.Close()

	start := time.Now()
	for i := 0; i < b.N; i++ {
		eventSrv.Publish(sessionID, agent.AgentEvent{Type: agent.EventToolUpdate, ToolCallID: fmt.Sprintf("bench-%d", i)})
		if _, err := recv.Recv(); err != nil {
			b.Fatalf("recv event: %v", err)
		}
	}
	d := time.Since(start)
	eventsPerMin := float64(b.N) / d.Minutes()
	b.ReportMetric(eventsPerMin, "events_per_min")
}

func p95(in []time.Duration) time.Duration {
	if len(in) == 0 {
		return 0
	}
	cp := append([]time.Duration(nil), in...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := int(0.95 * float64(len(cp)-1))
	if idx < 0 {
		idx = 0
	}
	return cp[idx]
}

func newBenchMemoryService(tb testing.TB) (*MemorySandboxService, string) {
	tb.Helper()
	svc := NewMemorySandboxService()
	base := "memory/base@initial"
	svc.SeedBaseSnapshot(base, map[string]string{"state.txt": "base"})
	resp, err := svc.CreateSession(context.Background(), &api.CreateSessionRequest{SessionID: "bench-session", BaseSnapshot: base})
	if err != nil {
		tb.Fatalf("create bench session: %v", err)
	}
	return svc, resp.Session.ID
}
