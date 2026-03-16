package mode3

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"h2-agent-runtime/internal/agent"
	mh "h2-agent-runtime/tests/integration/mode3/harness"
	"h2-agent-runtime/tests/integration/testutil"
)

// S6: Event stream remote visibility (ordered milestones).
func TestMode3_S6EventStreamRemoteVisibility(t *testing.T) {
	env := mh.NewRemoteEnv(t, []testutil.ScriptEntry{
		{ToolCalls: []testutil.ToolCallSpec{{ID: "ev-1", Name: "read_file", Arguments: map[string]any{"path": "events.txt"}}}},
		{Text: "done"},
	}, map[string]string{"events.txt": "ok"})

	recv := env.SubscribeRemoteEvents(t)
	var (
		mu     sync.Mutex
		remote []agent.AgentEvent
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			envelope, err := recv.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				return
			}
			if envelope == nil {
				continue
			}
			mu.Lock()
			remote = append(remote, envelope.Event)
			mu.Unlock()
		}
	}()

	env.PromptAndWait(t, "read the events file", 5*time.Second)
	env.Stop(t)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		seenEnded := false
		for _, evt := range remote {
			if evt.Type == agent.EventSessionEnded {
				seenEnded = true
				break
			}
		}
		mu.Unlock()
		if seenEnded {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	_ = recv.Close()
	<-done

	mu.Lock()
	captured := append([]agent.AgentEvent(nil), remote...)
	mu.Unlock()

	if len(captured) == 0 {
		t.Fatal("expected remote events, got none")
	}
	mh.AssertEventSequence(t, captured,
		agent.EventSessionStarted,
		agent.EventToolStarted,
		agent.EventToolCompleted,
		agent.EventTurnCompleted,
		agent.EventSessionEnded,
	)
}
