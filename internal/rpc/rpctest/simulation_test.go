package rpctest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"h2-agent-runtime/internal/agent"
	"h2-agent-runtime/internal/rpc"
	"h2-agent-runtime/internal/rpc/api"
)

// S1. Session lifecycle FSM simulation — all valid and invalid transitions via RPC.
func TestS1_SessionLifecycleFSM(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()

	sess := createRPCSession(t, stack.Server, "fsm")

	// Active → tool execution works
	_, err := stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
		SessionID: sess.ID, ToolCallID: "tc-1", ToolName: "bash",
		Params: map[string]any{"cmd": "echo test"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Active → TurnComplete
	_, err = stack.Server.TurnComplete(ctx, &api.TurnCompleteRequest{SessionID: sess.ID})
	if err != nil {
		t.Fatal(err)
	}

	// Active → Pause
	_, err = stack.Server.PauseSession(ctx, &api.PauseSessionRequest{SessionID: sess.ID})
	if err != nil {
		t.Fatal(err)
	}

	// Paused → ExecuteTool should fail with FailedPrecondition
	_, err = stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
		SessionID: sess.ID, ToolCallID: "tc-paused", ToolName: "bash",
		Params: map[string]any{"cmd": "echo fail"},
	})
	assertRPCError(t, err, rpc.CodeFailedPrecondition)

	// Paused → Pause should fail
	_, err = stack.Server.PauseSession(ctx, &api.PauseSessionRequest{SessionID: sess.ID})
	assertRPCError(t, err, rpc.CodeFailedPrecondition)

	// Paused → Resume
	_, err = stack.Server.ResumeSession(ctx, &api.ResumeSessionRequest{SessionID: sess.ID})
	if err != nil {
		t.Fatal(err)
	}

	// Active → Resume should fail
	_, err = stack.Server.ResumeSession(ctx, &api.ResumeSessionRequest{SessionID: sess.ID})
	assertRPCError(t, err, rpc.CodeFailedPrecondition)

	// Active → Destroy
	_, err = stack.Server.DestroySession(ctx, &api.DestroySessionRequest{SessionID: sess.ID})
	if err != nil {
		t.Fatal(err)
	}

	// Destroyed → all operations should fail with NotFound
	_, err = stack.Server.GetSession(ctx, &api.GetSessionRequest{SessionID: sess.ID})
	assertRPCError(t, err, rpc.CodeNotFound)

	_, err = stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
		SessionID: sess.ID, ToolCallID: "tc-dead", ToolName: "bash",
	})
	assertRPCError(t, err, rpc.CodeNotFound)
}

// S2. Snapshot workflow simulation — create, list, rollback, verify propagation.
func TestS2_SnapshotWorkflow(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()
	sess := createRPCSession(t, stack.Server, "snap-wf")

	// Create 5 turns
	snapIDs := make([]string, 5)
	for i := 0; i < 5; i++ {
		// Tool call before turn complete
		_, err := stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
			SessionID: sess.ID, ToolCallID: fmt.Sprintf("tc-%d", i), ToolName: "bash",
			Params: map[string]any{"cmd": fmt.Sprintf("echo turn-%d", i)},
		})
		if err != nil {
			t.Fatalf("tool %d: %v", i, err)
		}

		resp, err := stack.Server.TurnComplete(ctx, &api.TurnCompleteRequest{SessionID: sess.ID})
		if err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
		snapIDs[i] = resp.SnapshotID
		if resp.TurnNumber != i+1 {
			t.Fatalf("turn %d: TurnNumber = %d", i, resp.TurnNumber)
		}
	}

	// List should return 5
	list, err := stack.Server.ListSnapshots(ctx, &api.ListSnapshotsRequest{SessionID: sess.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Snapshots) != 5 {
		t.Fatalf("snapshot count = %d, want 5", len(list.Snapshots))
	}

	// Rollback to snapshot 2 (third one, index 2)
	_, err = stack.Server.RollbackSession(ctx, &api.RollbackSessionRequest{
		SessionID: sess.ID, SnapshotID: snapIDs[2],
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify turn count
	get, _ := stack.Server.GetSession(ctx, &api.GetSessionRequest{SessionID: sess.ID})
	if get.Session.TurnCount != 3 {
		t.Fatalf("turnCount = %d, want 3", get.Session.TurnCount)
	}

	// List should return 3 snapshots
	listAfter, _ := stack.Server.ListSnapshots(ctx, &api.ListSnapshotsRequest{SessionID: sess.ID})
	if len(listAfter.Snapshots) != 3 {
		t.Fatalf("after rollback: snapshot count = %d, want 3", len(listAfter.Snapshots))
	}

	// Verify the target snapshot is still present
	found := false
	for _, s := range listAfter.Snapshots {
		if s.Name == snapIDs[2] {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("rollback target snapshot not found after rollback")
	}
}

// S3. Stream reconnection simulation — producer restarts, consumer re-subscribes.
func TestS3_StreamReconnection(t *testing.T) {
	stack := newTestStack(t)

	// First subscription
	ctx1, cancel1 := context.WithCancel(context.Background())
	recv1, err := stack.Events.StreamAgentEvents(ctx1, &api.StreamAgentEventsRequest{SessionID: "recon"})
	if err != nil {
		t.Fatal(err)
	}

	// Publish an event
	stack.Events.Publish("recon", agent.AgentEvent{Type: agent.EventToolStarted, SessionID: "recon"})
	evt, err := recv1.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if evt.Event.Type != agent.EventToolStarted {
		t.Fatalf("event type = %s, want tool_started", evt.Event.Type)
	}

	// Cancel first subscription (simulate disconnect)
	cancel1()
	time.Sleep(10 * time.Millisecond) // allow cleanup goroutine to run

	// Re-subscribe
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	recv2, err := stack.Events.StreamAgentEvents(ctx2, &api.StreamAgentEventsRequest{SessionID: "recon"})
	if err != nil {
		t.Fatal(err)
	}

	// Publish another event — only the new subscriber should get it
	stack.Events.Publish("recon", agent.AgentEvent{Type: agent.EventToolCompleted, SessionID: "recon"})
	evt2, err := recv2.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if evt2.Event.Type != agent.EventToolCompleted {
		t.Fatalf("event type = %s, want tool_completed", evt2.Event.Type)
	}

	// Old receiver should be closed
	_, err = recv1.Recv()
	if err == nil {
		t.Fatal("old receiver should be closed")
	}
}

// S3b. Multiple concurrent subscribers
func TestS3b_MultipleSubscribers(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()

	const subscribers = 5
	receivers := make([]api.AgentEventReceiver, subscribers)
	for i := 0; i < subscribers; i++ {
		recv, err := stack.Events.StreamAgentEvents(ctx, &api.StreamAgentEventsRequest{SessionID: "multi-sub"})
		if err != nil {
			t.Fatal(err)
		}
		receivers[i] = recv
	}

	// Publish one event — all subscribers should receive it
	stack.Events.Publish("multi-sub", agent.AgentEvent{Type: agent.EventTurnCompleted, SessionID: "multi-sub"})

	for i, recv := range receivers {
		evt, err := recv.Recv()
		if err != nil {
			t.Fatalf("subscriber %d: %v", i, err)
		}
		if evt.Event.Type != agent.EventTurnCompleted {
			t.Fatalf("subscriber %d: type = %s", i, evt.Event.Type)
		}
	}
}

// S1b. Full end-to-end simulation: create → tools → turns → rollback → destroy
func TestS1b_EndToEndSimulation(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()

	// Create
	sess := createRPCSession(t, stack.Server, "e2e")

	// Execute tools + turns
	for turn := 0; turn < 5; turn++ {
		for tool := 0; tool < 3; tool++ {
			stream, err := stack.Server.ExecuteToolStream(ctx, &api.ExecuteToolRequest{
				SessionID: sess.ID, ToolCallID: fmt.Sprintf("tc-%d-%d", turn, tool),
				ToolName: "bash", Params: map[string]any{"cmd": fmt.Sprintf("echo t%d-t%d", turn, tool)},
			})
			if err != nil {
				t.Fatalf("turn %d tool %d: %v", turn, tool, err)
			}
			for {
				_, recvErr := stream.Recv()
				if errors.Is(recvErr, io.EOF) {
					break
				}
				if recvErr != nil {
					t.Fatal(recvErr)
				}
			}
		}
		_, err := stack.Server.TurnComplete(ctx, &api.TurnCompleteRequest{SessionID: sess.ID})
		if err != nil {
			t.Fatalf("TurnComplete %d: %v", turn, err)
		}
	}

	// Rollback to turn 2
	snaps, _ := stack.Server.ListSnapshots(ctx, &api.ListSnapshotsRequest{SessionID: sess.ID})
	if len(snaps.Snapshots) < 2 {
		t.Fatal("not enough snapshots for rollback")
	}
	_, err := stack.Server.RollbackSession(ctx, &api.RollbackSessionRequest{
		SessionID: sess.ID, SnapshotID: snaps.Snapshots[1].Name,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Pause + Resume
	stack.Server.PauseSession(ctx, &api.PauseSessionRequest{SessionID: sess.ID})
	stack.Server.ResumeSession(ctx, &api.ResumeSessionRequest{SessionID: sess.ID})

	// More tools
	_, err = stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
		SessionID: sess.ID, ToolCallID: "tc-post-rollback",
		ToolName: "bash", Params: map[string]any{"cmd": "echo post-rollback"},
	})
	if err != nil {
		t.Fatalf("post-rollback tool: %v", err)
	}

	// Destroy
	_, err = stack.Server.DestroySession(ctx, &api.DestroySessionRequest{SessionID: sess.ID})
	if err != nil {
		t.Fatal(err)
	}

	// Verify gone
	_, err = stack.Server.GetSession(ctx, &api.GetSessionRequest{SessionID: sess.ID})
	assertRPCError(t, err, rpc.CodeNotFound)
}

// S2b. Concurrent multi-session simulation
func TestS2b_ConcurrentMultiSession(t *testing.T) {
	stack := newTestStack(t)
	ctx := context.Background()

	const sessions = 10
	var wg sync.WaitGroup
	errs := make(chan error, sessions)

	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sessID := fmt.Sprintf("concurrent-%d", id)
			_, err := stack.Server.CreateSession(ctx, &api.CreateSessionRequest{
				SessionID: sessID, BaseSnapshot: baseSnapshot,
			})
			if err != nil {
				errs <- fmt.Errorf("create %d: %v", id, err)
				return
			}

			for turn := 0; turn < 3; turn++ {
				_, err := stack.Server.ExecuteTool(ctx, &api.ExecuteToolRequest{
					SessionID: sessID, ToolCallID: fmt.Sprintf("tc-%d-%d", id, turn),
					ToolName: "bash", Params: map[string]any{"cmd": "echo concurrent"},
				})
				if err != nil {
					errs <- fmt.Errorf("tool %d/%d: %v", id, turn, err)
					return
				}
				_, err = stack.Server.TurnComplete(ctx, &api.TurnCompleteRequest{SessionID: sessID})
				if err != nil {
					errs <- fmt.Errorf("turn %d/%d: %v", id, turn, err)
					return
				}
			}

			_, err = stack.Server.DestroySession(ctx, &api.DestroySessionRequest{SessionID: sessID})
			if err != nil {
				errs <- fmt.Errorf("destroy %d: %v", id, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}
