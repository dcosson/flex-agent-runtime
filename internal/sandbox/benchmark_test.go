package sandbox

import (
	"context"
	"fmt"
	"testing"
)

// B1. Session Creation Latency
// Target: <10ms per session creation (mock ZFS).
func BenchmarkCreateSession(b *testing.B) {
	svc := newTestService(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sess, err := svc.CreateSession(ctx, CreateSessionRequest{
			BaseSnapshot: "pool/bases/test@v1",
			SessionID:    fmt.Sprintf("bench-create-%d", i),
		})
		if err != nil {
			b.Fatal(err)
		}
		_ = svc.DestroySession(ctx, sess.ID)
	}
}

// B2. Tier 1 Tool Execution Latency
// Target: <1ms per Tier 1 tool call.
func BenchmarkTier1Execution(b *testing.B) {
	svc := newTestService(b)
	ctx := context.Background()
	sess := createTestSession(b, svc, "bench-tier1")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
			SessionID: sess.ID,
			ToolName:  "read_file",
			Params:    map[string]any{"path": "test.txt"},
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// B3. Snapshot Latency
// Target: <5ms per turn completion (mock ZFS).
func BenchmarkTurnComplete(b *testing.B) {
	svc := newTestService(b)
	ctx := context.Background()
	sess := createTestSession(b, svc, "bench-snap")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := svc.TurnComplete(ctx, sess.ID)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// B4. Health Check Latency
// Target: <10ms per health check with 50 sessions.
func BenchmarkHealthCheck(b *testing.B) {
	svc := newTestService(b)
	ctx := context.Background()

	// Create 50 sessions
	for i := 0; i < 50; i++ {
		createTestSession(b, svc, fmt.Sprintf("bench-health-%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := svc.HealthCheck(ctx)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// B5. Session Lookup Under Load
// Target: <500ns per session lookup (sync.Map read).
func BenchmarkGetSessionUnderLoad(b *testing.B) {
	svc := newTestService(b)
	ctx := context.Background()

	ids := make([]string, 100)
	for i := range ids {
		ids[i] = fmt.Sprintf("bench-lookup-%03d", i)
		createTestSession(b, svc, ids[i])
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := svc.GetSession(ctx, ids[i%len(ids)])
		if err != nil {
			b.Fatal(err)
		}
	}
}
