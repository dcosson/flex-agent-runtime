package scenarios

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/agent"
	"github.com/anthropics/flex-agent-runtime/internal/agent/agenttest"
	agentapi "github.com/anthropics/flex-agent-runtime/internal/agent/api"
	"github.com/anthropics/flex-agent-runtime/internal/ai"
)

func TestFK1_AgentRPCForkNFromConversation(t *testing.T) {
	stack := agenttest.NewAgentTestStack(t)
	stack.DriverFactory.SetDriver("default", &agenttest.MockAgentDriver{TurnScript: []agent.AgentEvent{
		{Type: agent.EventTurnStarted, Turn: 1},
		{Type: agent.EventAgentMessageDelta, Turn: 1, Delta: "fork"},
		{Type: agent.EventTurnCompleted, Turn: 1},
	}})

	log := buildResumeLog(t, 4)
	const forkCount = 20

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	errCh := make(chan error, forkCount)
	var wg sync.WaitGroup
	for i := 0; i < forkCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := fmt.Sprintf("fork-%d", idx)
			_, err := stack.Client.ResumeSession(ctx, &agentapi.ResumeSessionRequest{
				SessionConfig:   stack.BaseConfig(id),
				SchemaVersion:   agentapi.CurrentConversationSchemaVersion,
				ConversationLog: log,
			})
			if err != nil {
				errCh <- fmt.Errorf("resume %s: %w", id, err)
				return
			}
			recv, err := stack.Client.SendMessage(ctx, &agentapi.SendMessageRequest{SessionID: id, Message: "fork task"})
			if err != nil {
				errCh <- fmt.Errorf("send %s: %w", id, err)
				return
			}
			if err := drainReceiverErr(recv); err != nil {
				errCh <- fmt.Errorf("drain %s: %w", id, err)
				return
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	list, err := stack.Client.ListSessions(context.Background(), &agentapi.ListAgentSessionsRequest{})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(list.Sessions) < forkCount {
		t.Fatalf("sessions = %d, want >= %d", len(list.Sessions), forkCount)
	}
}

func TestFK2_AgentRPCForkUnderLoad(t *testing.T) {
	stack := agenttest.NewAgentTestStack(t)
	stack.DriverFactory.SetDriver("default", &agenttest.MockAgentDriver{TurnScript: []agent.AgentEvent{
		{Type: agent.EventTurnStarted, Turn: 1},
		{Type: agent.EventTurnCompleted, Turn: 1},
	}})

	log := buildResumeLog(t, 2)
	baseG := runtime.NumGoroutine()

	const waves = 5
	const forksPerWave = 10
	for w := 0; w < waves; w++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		var wg sync.WaitGroup
		errCh := make(chan error, forksPerWave)
		for i := 0; i < forksPerWave; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				id := fmt.Sprintf("load-fork-w%d-%d", w, idx)
				_, err := stack.Client.ResumeSession(ctx, &agentapi.ResumeSessionRequest{
					SessionConfig:   stack.BaseConfig(id),
					SchemaVersion:   agentapi.CurrentConversationSchemaVersion,
					ConversationLog: log,
				})
				if err != nil {
					errCh <- err
					return
				}
				recv, err := stack.Client.SendMessage(ctx, &agentapi.SendMessageRequest{SessionID: id, Message: "fork load"})
				if err != nil {
					errCh <- err
					return
				}
				if err := drainReceiverErr(recv); err != nil {
					errCh <- err
					return
				}
				if _, err := stack.Client.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{SessionID: id}); err != nil {
					errCh <- err
				}
			}(i)
		}
		wg.Wait()
		cancel()
		close(errCh)
		for err := range errCh {
			if err != nil {
				t.Fatalf("wave %d: %v", w, err)
			}
		}
	}

	// Give shutdown goroutines a moment to settle and check for large leak deltas.
	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > baseG+20 {
		t.Fatalf("goroutine delta too high: before=%d after=%d", baseG, after)
	}
}

func buildResumeLog(t *testing.T, turns int) []agentapi.AgentMessageRecord {
	t.Helper()
	if turns <= 0 {
		return nil
	}
	out := make([]agentapi.AgentMessageRecord, 0, turns*2)
	for turn := 1; turn <= turns; turn++ {
		user, err := agentapi.AgentMessageToRecord(agentapi.AgentMessage{
			Turn:      turn,
			Message:   &ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: fmt.Sprintf("prompt-%d", turn)}}},
			CreatedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("user record: %v", err)
		}
		assistant, err := agentapi.AgentMessageToRecord(agentapi.AgentMessage{
			Turn:      turn,
			Message:   &ai.AssistantMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: fmt.Sprintf("reply-%d", turn)}}},
			CreatedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("assistant record: %v", err)
		}
		out = append(out, user, assistant)
	}
	return out
}

func drainReceiverErr(recv agentapi.EventReceiver) error {
	for {
		_, err := recv.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
