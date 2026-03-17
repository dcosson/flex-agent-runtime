package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/ai"
)

type scriptedProvider struct {
	api       string
	mu        sync.Mutex
	responses []ai.AssistantMessage
	calls     int
}

func (p *scriptedProvider) API() string { return p.api }

func (p *scriptedProvider) Stream(ctx context.Context, model ai.Model, llmCtx ai.Context, opts ai.StreamOptions) *ai.EventStream {
	return p.streamOnce()
}

func (p *scriptedProvider) StreamSimple(ctx context.Context, model ai.Model, llmCtx ai.Context, opts ai.SimpleStreamOptions) *ai.EventStream {
	return p.streamOnce()
}

func (p *scriptedProvider) streamOnce() *ai.EventStream {
	es := ai.NewEventStream()
	go func() {
		defer es.Close()
		p.mu.Lock()
		idx := p.calls
		p.calls++
		var msg ai.AssistantMessage
		if idx < len(p.responses) {
			msg = p.responses[idx]
		} else if len(p.responses) > 0 {
			msg = p.responses[len(p.responses)-1]
		}
		p.mu.Unlock()

		es.Send(ai.AssistantMessageEvent{Type: ai.EventTextDelta, Delta: "x"})
		es.Send(ai.AssistantMessageEvent{Type: ai.EventDone, Message: &msg})
	}()
	return es
}

func TestNativeDriverToolLoopAndSnapshotBoundary(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)

	prov := &scriptedProvider{api: "native-test", responses: []ai.AssistantMessage{
		{
			Content:    []ai.ContentBlock{&ai.ToolCall{ID: "tc-1", Name: "read_file", Arguments: map[string]any{"path": "a.txt"}}},
			StopReason: ai.StopReasonToolUse,
			Timestamp:  ai.TimeToMillis(time.Now()),
		},
		{
			Content:    []ai.ContentBlock{&ai.TextContent{Text: "final"}},
			StopReason: ai.StopReasonStop,
			Timestamp:  ai.TimeToMillis(time.Now()),
		},
	}}
	ai.RegisterProvider(prov, "agent-loop-test")

	tool := AgentTool{
		Tool: ai.Tool{Name: "read_file"},
		Execute: func(ctx context.Context, toolCallID string, params map[string]any, onUpdate func(AgentToolResult)) (AgentToolResult, error) {
			onUpdate(AgentToolResult{Content: []ai.ContentBlock{&ai.TextContent{Text: "reading"}}})
			return AgentToolResult{Content: []ai.ContentBlock{&ai.TextContent{Text: "contents"}}}, nil
		},
	}

	driver := NewNativeDriver(DriverConfig{
		Model: ai.Model{ID: "test", API: "native-test", Provider: "test", MaxTokens: 1024},
		Tools: []AgentTool{tool},
	})
	agent := New(driver)
	agent.SetSession(&Session{ID: "runtime-1", DriverSessionID: "drv-1"})

	var mu sync.Mutex
	sequence := make([]AgentEventType, 0)
	sawDelta := false
	sawControlLeak := false
	done := make(chan struct{})
	agent.Subscribe(func(evt AgentEvent) {
		mu.Lock()
		sequence = append(sequence, evt.Type)
		if evt.Type == EventAgentMessageDelta {
			if evt.Delta != "" {
				sawDelta = true
			}
			if evt.ControlMessage != "" {
				sawControlLeak = true
			}
		}
		mu.Unlock()
		if evt.Type == EventStateChange && evt.State == StateIdle {
			select {
			case <-done:
			default:
				close(done)
			}
		}
	})

	if err := agent.Prompt(context.Background(), "start"); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for idle state")
	}

	mu.Lock()
	defer mu.Unlock()
	var sawTurnComplete, sawIdle, sawTool bool
	turnIdx, idleIdx := -1, -1
	for i, typ := range sequence {
		if typ == EventToolCompleted {
			sawTool = true
		}
		if typ == EventTurnCompleted {
			sawTurnComplete = true
			turnIdx = i
		}
		if typ == EventStateChange {
			sawIdle = true
			idleIdx = i
		}
	}
	if !sawTool {
		t.Fatalf("expected tool completion event; sequence=%v", sequence)
	}
	if !sawTurnComplete || !sawIdle {
		t.Fatalf("expected turn_completed and idle events; sequence=%v", sequence)
	}
	if turnIdx > idleIdx {
		t.Fatalf("expected turn_completed before idle: %v", sequence)
	}
	if !sawDelta {
		t.Fatalf("expected at least one agent delta event with Delta set")
	}
	if sawControlLeak {
		t.Fatalf("agent delta events must not populate ControlMessage")
	}
}

func TestNativeDriverFollowUpFIFO(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)

	prov := &scriptedProvider{api: "native-followup", responses: []ai.AssistantMessage{{
		Content:    []ai.ContentBlock{&ai.TextContent{Text: "ok"}},
		StopReason: ai.StopReasonStop,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}}}
	ai.RegisterProvider(prov, "agent-followup-test")

	driver := NewNativeDriver(DriverConfig{Model: ai.Model{ID: "m", API: "native-followup", Provider: "test", MaxTokens: 1024}})
	agent := New(driver)
	agent.SetSession(&Session{ID: "runtime-fifo"})

	idle := make(chan struct{})
	agent.Subscribe(func(evt AgentEvent) {
		if evt.Type == EventStateChange && evt.State == StateIdle {
			select {
			case <-idle:
			default:
				close(idle)
			}
		}
	})

	if err := agent.Prompt(context.Background(), "first"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if err := agent.FollowUp("second"); err != nil {
		t.Fatalf("followup second: %v", err)
	}
	if err := agent.FollowUp("third"); err != nil {
		t.Fatalf("followup third: %v", err)
	}

	select {
	case <-idle:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for loop completion")
	}

	sess := agent.Session()
	var users []string
	for _, entry := range sess.ConversationLog {
		if um, ok := entry.Message.(*ai.UserMessage); ok {
			if len(um.Content) == 1 {
				if tc, ok := um.Content[0].(*ai.TextContent); ok {
					users = append(users, tc.Text)
				}
			}
		}
	}
	if len(users) < 3 {
		t.Fatalf("expected 3 user prompts, got %v", users)
	}
	if users[0] != "first" || users[1] != "second" || users[2] != "third" {
		t.Fatalf("followup order mismatch: %v", users)
	}
}

func TestNativeDriverTerminalToolShortCircuit(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)

	prov := &scriptedProvider{api: "native-terminal", responses: []ai.AssistantMessage{
		{
			Content:    []ai.ContentBlock{&ai.ToolCall{ID: "tc-1", Name: "finalize", Arguments: map[string]any{}}},
			StopReason: ai.StopReasonToolUse,
			Timestamp:  ai.TimeToMillis(time.Now()),
		},
		{
			Content:    []ai.ContentBlock{&ai.TextContent{Text: "should not be emitted"}},
			StopReason: ai.StopReasonStop,
			Timestamp:  ai.TimeToMillis(time.Now()),
		},
	}}
	ai.RegisterProvider(prov, "agent-terminal-test")

	tool := AgentTool{
		Tool:     ai.Tool{Name: "finalize"},
		Terminal: true,
		Execute: func(ctx context.Context, toolCallID string, params map[string]any, onUpdate func(AgentToolResult)) (AgentToolResult, error) {
			return AgentToolResult{Content: []ai.ContentBlock{&ai.TextContent{Text: "done"}}}, nil
		},
	}

	driver := NewNativeDriver(DriverConfig{
		Model: ai.Model{ID: "m", API: "native-terminal", Provider: "test", MaxTokens: 1024},
		Tools: []AgentTool{tool},
	})
	agent := New(driver)
	agent.SetSession(&Session{ID: "runtime-terminal"})

	terminal := make(chan struct{})
	agent.Subscribe(func(evt AgentEvent) {
		if evt.Type == EventTerminalToolCompleted {
			select {
			case <-terminal:
			default:
				close(terminal)
			}
		}
	})

	if err := agent.Prompt(context.Background(), "start"); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	select {
	case <-terminal:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for terminal tool event")
	}

	prov.mu.Lock()
	calls := prov.calls
	prov.mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected single provider call for terminal short-circuit, got %d", calls)
	}
}
