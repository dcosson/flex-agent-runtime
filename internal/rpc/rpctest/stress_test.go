package rpctest

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/anthropics/flex-agent-runtime/internal/rpc/api"
)

// ST1. Continuous mixed traffic soak (short version for CI).
func TestST1_MixedTrafficSoak(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}

	stack := newTestStack(t)
	ctx := context.Background()

	const sessions = 5
	const turnsPerSession = 50
	const toolsPerTurn = 3

	var wg sync.WaitGroup
	var toolCallID atomic.Uint64
	errs := make(chan error, sessions*turnsPerSession)

	for s := 0; s < sessions; s++ {
		wg.Add(1)
		go func(sid int) {
			defer wg.Done()
			sessID := fmt.Sprintf("soak-%d", sid)
			_, err := stack.Server.CreateSession(ctx, &api.CreateSessionRequest{
				SessionID: sessID, BaseSnapshot: baseSnapshot,
			})
			if err != nil {
				errs <- fmt.Errorf("create %s: %v", sessID, err)
				return
			}

			for turn := 0; turn < turnsPerSession; turn++ {
				for tool := 0; tool < toolsPerTurn; tool++ {
					tcID := fmt.Sprintf("tc-%d", toolCallID.Add(1))
					_, err := stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
						SessionID: sessID, ToolCallID: tcID,
						ToolName: "bash", Params: map[string]any{"cmd": "echo soak"},
					})
					if err != nil {
						errs <- fmt.Errorf("%s turn %d tool %d: %v", sessID, turn, tool, err)
						return
					}
				}
				_, err := stack.Server.TurnComplete(ctx, &api.TurnCompleteRequest{SessionID: sessID})
				if err != nil {
					errs <- fmt.Errorf("%s TurnComplete %d: %v", sessID, turn, err)
					return
				}
			}

			_, err = stack.Server.DestroySession(ctx, &api.DestroySessionRequest{SessionID: sessID})
			if err != nil {
				errs <- fmt.Errorf("destroy %s: %v", sessID, err)
			}
		}(s)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}

	// HealthCheck should be healthy after soak
	health, err := stack.Server.HealthCheck(ctx, &api.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if health.Status != "healthy" {
		t.Fatalf("post-soak health = %q, want healthy", health.Status)
	}
}

// ST2. High-concurrency agent simulation.
func TestST2_HighConcurrencyAgents(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}

	stack := newTestStack(t)
	ctx := context.Background()

	const agents = 20
	const opsPerAgent = 100
	var toolCallID atomic.Uint64

	var wg sync.WaitGroup
	errs := make(chan error, agents)

	for a := 0; a < agents; a++ {
		wg.Add(1)
		go func(aid int) {
			defer wg.Done()
			sessID := fmt.Sprintf("agent-%d", aid)
			_, err := stack.Server.CreateSession(ctx, &api.CreateSessionRequest{
				SessionID: sessID, BaseSnapshot: baseSnapshot,
			})
			if err != nil {
				errs <- fmt.Errorf("create agent %d: %v", aid, err)
				return
			}

			for op := 0; op < opsPerAgent; op++ {
				tcID := fmt.Sprintf("tc-%d", toolCallID.Add(1))
				_, err := stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
					SessionID: sessID, ToolCallID: tcID,
					ToolName: "bash", Params: map[string]any{"cmd": "echo agent"},
				})
				if err != nil {
					errs <- fmt.Errorf("agent %d op %d: %v", aid, op, err)
					return
				}
			}

			_, err = stack.Server.DestroySession(ctx, &api.DestroySessionRequest{SessionID: sessID})
			if err != nil {
				errs <- fmt.Errorf("destroy agent %d: %v", aid, err)
			}
		}(a)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

// ST3. Burst-failure resilience — repeated transient outage bursts.
func TestST3_BurstFailureResilience(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}

	stack := newTestStack(t)
	ctx := context.Background()

	const bursts = 5
	const opsPerBurst = 50
	var toolCallID atomic.Uint64

	sess := createRPCSession(t, stack.Server, "burst")

	for burst := 0; burst < bursts; burst++ {
		// Inject failures for half the burst
		var wg sync.WaitGroup
		successCount := atomic.Int32{}
		failCount := atomic.Int32{}

		for op := 0; op < opsPerBurst; op++ {
			if op < opsPerBurst/2 {
				stack.GVisor.SetError(fmt.Errorf("burst failure"))
			} else {
				stack.GVisor.SetError(nil)
			}

			wg.Add(1)
			go func() {
				defer wg.Done()
				tcID := fmt.Sprintf("tc-%d", toolCallID.Add(1))
				_, err := stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
					SessionID: sess.ID, ToolCallID: tcID,
					ToolName: "bash", Params: map[string]any{"cmd": "echo burst"},
				})
				if err != nil {
					failCount.Add(1)
				} else {
					successCount.Add(1)
				}
			}()
		}
		wg.Wait()

		// Clear failures for next burst
		stack.GVisor.SetError(nil)

		// Session should still be active after each burst
		get, err := stack.Server.GetSession(ctx, &api.GetSessionRequest{SessionID: sess.ID})
		if err != nil {
			t.Fatalf("burst %d: GetSession: %v", burst, err)
		}
		if get.Session.State != "active" {
			t.Fatalf("burst %d: state = %s, want active", burst, get.Session.State)
		}
	}
}
