package rpctest

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"h2-agent-runtime/internal/agent"
	"h2-agent-runtime/internal/rpc/api"
	"h2-agent-runtime/internal/rpc/client"
	"h2-agent-runtime/internal/rpc/server"
	"h2-agent-runtime/internal/sandbox"
	"h2-agent-runtime/internal/sandbox/zfs"
	"h2-agent-runtime/internal/tools"
)

func newBenchStack(b *testing.B) (*server.SandboxServer, *server.AgentEventServer, *client.SandboxClient) {
	b.Helper()
	zm := zfs.NewMockManager()
	cfg := sandbox.DefaultServiceConfig()
	cfg.PoolName = "tank"
	cfg.BasesDataset = "tank/bases"
	cfg.SessionsDataset = "tank/sessions"
	cfg.ToolTimeout = 0
	host, err := sandbox.NewSandboxHostService(cfg, zm, &testGVisor{}, nil)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	if err := zm.CreateDataset(ctx, "tank/bases/repo", zfs.DatasetOptions{}); err != nil {
		b.Fatal(err)
	}
	if _, err := zm.CreateSnapshot(ctx, "tank/bases/repo", "initial"); err != nil {
		b.Fatal(err)
	}

	srv := server.NewSandboxServer(host)
	b.Cleanup(func() { _ = srv.Close() })
	events := server.NewAgentEventServer()
	cl := client.NewSandboxClient(srv)
	return srv, events, cl
}

var benchToolCallCounter atomic.Uint64

// B1. Unary RPC latency — target: p95 < 5ms.
func BenchmarkB1_UnaryRPCLatency(b *testing.B) {
	srv, _, _ := newBenchStack(b)
	ctx := context.Background()

	_, err := srv.CreateSession(ctx, &api.CreateSessionRequest{
		SessionID: "bench-unary", BaseSnapshot: baseSnapshot,
	})
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := srv.GetSession(ctx, &api.GetSessionRequest{SessionID: "bench-unary"})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// B1b. CreateSession latency
func BenchmarkB1b_CreateSessionLatency(b *testing.B) {
	srv, _, _ := newBenchStack(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("bench-create-%d", i)
		_, err := srv.CreateSession(ctx, &api.CreateSessionRequest{
			SessionID: id, BaseSnapshot: baseSnapshot,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// B1c. HealthCheck latency
func BenchmarkB1c_HealthCheckLatency(b *testing.B) {
	srv, _, _ := newBenchStack(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := srv.HealthCheck(ctx, &api.HealthCheckRequest{})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// B2. Tool dispatch throughput — target: >= 2k ExecuteTool RPC/s.
func BenchmarkB2_ToolDispatchThroughput(b *testing.B) {
	srv, _, _ := newBenchStack(b)
	ctx := context.Background()

	_, err := srv.CreateSession(ctx, &api.CreateSessionRequest{
		SessionID: "bench-tool", BaseSnapshot: baseSnapshot,
	})
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tcID := fmt.Sprintf("tc-%d", benchToolCallCounter.Add(1))
		_, err := srv.ExecuteTool(ctx, &api.ExecuteToolRequest{
			SessionID: "bench-tool", ToolCallID: tcID, ToolName: "bash",
			Params: map[string]any{"cmd": "echo bench"},
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// B2b. Tool dispatch via client (SandboxClient)
func BenchmarkB2b_ClientToolDispatch(b *testing.B) {
	srv, _, cl := newBenchStack(b)
	ctx := context.Background()

	_, err := srv.CreateSession(ctx, &api.CreateSessionRequest{
		SessionID: "bench-client", BaseSnapshot: baseSnapshot,
	})
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tcID := fmt.Sprintf("tc-%d", benchToolCallCounter.Add(1))
		_, err := cl.ExecuteTool(ctx, "bench-client", tools.ToolRequest{
			ToolCallID: tcID, ToolName: "bash",
			Params: map[string]any{"cmd": "echo bench"},
		}, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// B3. Streaming throughput — target: >= 100k events/minute per stream.
// Uses synchronous publish-then-consume to avoid channel overflow drops.
func BenchmarkB3_EventStreamThroughput(b *testing.B) {
	_, events, _ := newBenchStack(b)
	ctx := context.Background()

	recv, err := events.StreamAgentEvents(ctx, &api.StreamAgentEventsRequest{SessionID: "bench-events"})
	if err != nil {
		b.Fatal(err)
	}

	// Publish in small batches that fit the 256-element channel buffer,
	// then consume before publishing the next batch.
	b.ResetTimer()
	const batchSize = 200
	remaining := b.N
	for remaining > 0 {
		n := batchSize
		if n > remaining {
			n = remaining
		}
		for i := 0; i < n; i++ {
			events.Publish("bench-events", agent.AgentEvent{
				Type: agent.EventToolStarted, SessionID: "bench-events",
				At: time.Now(),
			})
		}
		for i := 0; i < n; i++ {
			_, err := recv.Recv()
			if err != nil {
				b.Fatal(err)
			}
		}
		remaining -= n
	}
}

// B4. TurnComplete latency (includes snapshot creation)
func BenchmarkB4_TurnCompleteLatency(b *testing.B) {
	srv, _, _ := newBenchStack(b)
	ctx := context.Background()

	_, err := srv.CreateSession(ctx, &api.CreateSessionRequest{
		SessionID: "bench-turn", BaseSnapshot: baseSnapshot,
	})
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := srv.TurnComplete(ctx, &api.TurnCompleteRequest{SessionID: "bench-turn"})
		if err != nil {
			b.Fatal(err)
		}
	}
}
