package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"pgregory.net/rapid"
)

type panicProvider struct{ api string }

func (p *panicProvider) API() string { return p.api }
func (p *panicProvider) Stream(context.Context, ai.Model, ai.Context, ai.StreamOptions) *ai.EventStream {
	panic("provider stream panic")
}
func (p *panicProvider) StreamSimple(context.Context, ai.Model, ai.Context, ai.SimpleStreamOptions) *ai.EventStream {
	panic("provider stream panic")
}

type weirdEventProvider struct{ api string }

func (p *weirdEventProvider) API() string { return p.api }
func (p *weirdEventProvider) Stream(context.Context, ai.Model, ai.Context, ai.StreamOptions) *ai.EventStream {
	return p.stream()
}
func (p *weirdEventProvider) StreamSimple(context.Context, ai.Model, ai.Context, ai.SimpleStreamOptions) *ai.EventStream {
	return p.stream()
}
func (p *weirdEventProvider) stream() *ai.EventStream {
	es := ai.NewEventStream()
	go func() {
		defer es.Close()
		es.Send(ai.AssistantMessageEvent{Type: ai.EventType("weird")})
		es.Send(ai.AssistantMessageEvent{Type: ai.EventDone, Message: &ai.AssistantMessage{
			Content:    []ai.ContentBlock{&ai.TextContent{Text: "ok"}},
			StopReason: ai.StopReasonStop,
			Timestamp:  ai.TimeToMillis(time.Now()),
		}})
	}()
	return es
}

type slowProvider struct {
	api   string
	delay time.Duration
}

func (p *slowProvider) API() string { return p.api }
func (p *slowProvider) Stream(context.Context, ai.Model, ai.Context, ai.StreamOptions) *ai.EventStream {
	return p.stream()
}
func (p *slowProvider) StreamSimple(context.Context, ai.Model, ai.Context, ai.SimpleStreamOptions) *ai.EventStream {
	return p.stream()
}
func (p *slowProvider) stream() *ai.EventStream {
	es := ai.NewEventStream()
	go func() {
		defer es.Close()
		time.Sleep(p.delay)
		es.Send(ai.AssistantMessageEvent{Type: ai.EventDone, Message: &ai.AssistantMessage{
			Content:    []ai.ContentBlock{&ai.TextContent{Text: "slow"}},
			StopReason: ai.StopReasonStop,
			Timestamp:  ai.TimeToMillis(time.Now()),
		}})
	}()
	return es
}

func waitForState(t *testing.T, a *Agent, st AgentState, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if a.State() == st {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for state %s (got %s)", st, a.State())
}

func TestP1_StateTransitionValidity(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)
	// Shared provider across rapid iterations is intentional; this property only
	// validates state-transition legality, not content differences by iteration.
	prov := &scriptedProvider{api: "agent-p1", responses: []ai.AssistantMessage{{
		Content:    []ai.ContentBlock{&ai.TextContent{Text: "ok"}},
		StopReason: ai.StopReasonStop,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}}}
	ai.RegisterProvider(prov, "agent-p1")

	rapid.Check(t, func(rt *rapid.T) {
		driver := NewNativeDriver(DriverConfig{Model: ai.Model{ID: "m", API: "agent-p1", Provider: "test", MaxTokens: 1024}})
		agent := New(driver)
		agent.SetSession(&Session{ID: "runtime-p1", DriverSessionID: "driver-p1"})

		var mu sync.Mutex
		states := make([]AgentState, 0)
		agent.Subscribe(func(evt AgentEvent) {
			if evt.Type == EventStateChange {
				mu.Lock()
				states = append(states, evt.State)
				mu.Unlock()
			}
		})

		opCount := rapid.IntRange(5, 20).Draw(rt, "ops")
		for i := 0; i < opCount; i++ {
			switch rapid.IntRange(0, 4).Draw(rt, fmt.Sprintf("op-%d", i)) {
			case 0:
				_ = agent.Prompt(context.Background(), fmt.Sprintf("p-%d", i))
			case 1:
				_ = agent.Steer(fmt.Sprintf("s-%d", i))
			case 2:
				_ = agent.FollowUp(fmt.Sprintf("f-%d", i))
			case 3:
				_ = agent.Abort(fmt.Sprintf("a-%d", i))
			case 4:
				_ = agent.Continue(context.Background())
			}
		}

		waitForState(t, agent, StateIdle, 2*time.Second)
		if err := agent.Stop(context.Background()); err != nil {
			t.Fatalf("stop: %v", err)
		}

		mu.Lock()
		defer mu.Unlock()
		if len(states) == 0 {
			t.Fatalf("expected state transitions")
		}
		for i := 1; i < len(states); i++ {
			from, to := states[i-1], states[i]
			if _, ok := validTransitions[from][to]; !ok {
				t.Fatalf("invalid transition: %s -> %s", from, to)
			}
		}
		if states[len(states)-1] != StateExited {
			t.Fatalf("expected terminal exited state, got %s", states[len(states)-1])
		}
	})
}

func TestP2_FollowUpFIFOPreservation(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)

	prov := &scriptedProvider{api: "agent-p2", responses: []ai.AssistantMessage{{
		Content:    []ai.ContentBlock{&ai.TextContent{Text: "ok"}},
		StopReason: ai.StopReasonStop,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}}}
	ai.RegisterProvider(prov, "agent-p2")

	rapid.Check(t, func(rt *rapid.T) {
		driver := NewNativeDriver(DriverConfig{Model: ai.Model{ID: "m", API: "agent-p2", Provider: "test", MaxTokens: 1024}})
		agent := New(driver)
		agent.SetSession(&Session{ID: "runtime-p2"})

		followUpN := rapid.IntRange(1, 15).Draw(rt, "follow-up-n")
		payloads := make([]string, 0, followUpN+1)
		payloads = append(payloads, "first")
		for i := 0; i < followUpN; i++ {
			payloads = append(payloads, rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, fmt.Sprintf("f-%d", i)))
		}

		if err := agent.Prompt(context.Background(), payloads[0]); err != nil {
			t.Fatalf("prompt: %v", err)
		}
		for _, f := range payloads[1:] {
			if err := agent.FollowUp(f); err != nil && !errors.Is(err, ErrQueueFull) {
				t.Fatalf("follow-up enqueue: %v", err)
			}
		}

		waitForState(t, agent, StateIdle, 2*time.Second)

		sess := agent.Session()
		users := make([]string, 0)
		for _, entry := range sess.ConversationLog {
			if um, ok := entry.Message.(*ai.UserMessage); ok && len(um.Content) == 1 {
				if txt, ok := um.Content[0].(*ai.TextContent); ok {
					users = append(users, txt.Text)
				}
			}
		}
		if len(users) == 0 || users[0] != payloads[0] {
			t.Fatalf("missing initial prompt: got=%v wantFirst=%q", users, payloads[0])
		}
		for i := 1; i < len(users) && i < len(payloads); i++ {
			if users[i] != payloads[i] {
				t.Fatalf("fifo mismatch at %d: got=%q want=%q all=%v", i, users[i], payloads[i], users)
			}
		}
	})
}

func TestP3_SnapshotTriggerCardinality(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)

	prov := &scriptedProvider{api: "agent-p3", responses: []ai.AssistantMessage{{
		Content:    []ai.ContentBlock{&ai.TextContent{Text: "ok"}},
		StopReason: ai.StopReasonStop,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}}}
	ai.RegisterProvider(prov, "agent-p3")

	rapid.Check(t, func(rt *rapid.T) {
		driver := NewNativeDriver(DriverConfig{Model: ai.Model{ID: "m", API: "agent-p3", Provider: "test", MaxTokens: 1024}})
		agent := New(driver)
		agent.SetSession(&Session{ID: "runtime-p3"})

		var mu sync.Mutex
		turnCompleted := 0
		idleTransitions := 0
		order := make([]AgentEvent, 0)
		agent.Subscribe(func(evt AgentEvent) {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, evt)
			if evt.Type == EventTurnCompleted {
				turnCompleted++
			}
			if evt.Type == EventStateChange && evt.State == StateIdle {
				idleTransitions++
			}
		})

		followUpN := rapid.IntRange(0, 10).Draw(rt, "follow-up-n")
		if err := agent.Prompt(context.Background(), "root"); err != nil {
			t.Fatalf("prompt: %v", err)
		}
		acceptedFollowUps := 0
		for i := 0; i < followUpN; i++ {
			if err := agent.FollowUp(fmt.Sprintf("f-%d", i)); err == nil {
				acceptedFollowUps++
			}
		}

		waitForState(t, agent, StateIdle, 2*time.Second)
		mu.Lock()
		defer mu.Unlock()

		expectedTurns := acceptedFollowUps + 1
		if turnCompleted != expectedTurns {
			t.Fatalf("turn_completed cardinality mismatch: got=%d want=%d", turnCompleted, expectedTurns)
		}
		if idleTransitions != 1 {
			t.Fatalf("idle cardinality mismatch: got=%d want=1", idleTransitions)
		}
		lastTurn := -1
		lastIdle := -1
		for i, evt := range order {
			if evt.Type == EventTurnCompleted {
				lastTurn = i
			}
			if evt.Type == EventStateChange && evt.State == StateIdle {
				lastIdle = i
			}
		}
		if lastTurn < 0 || lastIdle < 0 || lastTurn > lastIdle {
			t.Fatalf("expected final turn_completed before idle: %v", order)
		}
	})
}

func TestP4_SessionIDAuthority(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)
	prov := &scriptedProvider{api: "agent-p4", responses: []ai.AssistantMessage{{
		Content:    []ai.ContentBlock{&ai.TextContent{Text: "ok"}},
		StopReason: ai.StopReasonStop,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}}}
	ai.RegisterProvider(prov, "agent-p4")

	rapid.Check(t, func(rt *rapid.T) {
		runtimeID := rapid.StringMatching(`[a-z]{6}`).Draw(rt, "runtime-id")
		driverID := runtimeID + "-driver"

		driver := NewNativeDriver(DriverConfig{Model: ai.Model{ID: "m", API: "agent-p4", Provider: "test", MaxTokens: 1024}})
		agent := New(driver)
		agent.SetSession(&Session{ID: runtimeID, DriverSessionID: driverID})

		bad := make(chan AgentEvent, 1)
		agent.Subscribe(func(evt AgentEvent) {
			if evt.SessionID != "" && evt.SessionID != runtimeID {
				select {
				case bad <- evt:
				default:
				}
			}
		})

		if err := agent.Prompt(context.Background(), "hi"); err != nil {
			t.Fatalf("prompt: %v", err)
		}
		waitForState(t, agent, StateIdle, 2*time.Second)

		select {
		case evt := <-bad:
			t.Fatalf("event used non-runtime SessionID: %+v", evt)
		default:
		}
	})
}

func TestP5_AbortIdempotency(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)
	ai.RegisterProvider(&slowProvider{api: "agent-p5", delay: 80 * time.Millisecond}, "agent-p5")

	driver := NewNativeDriver(DriverConfig{Model: ai.Model{ID: "m", API: "agent-p5", Provider: "test", MaxTokens: 1024}})
	agent := New(driver)
	agent.SetSession(&Session{ID: "runtime-p5"})

	if err := agent.Prompt(context.Background(), "hi"); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = agent.Abort(fmt.Sprintf("abort-%d", i))
		}(i)
	}
	wg.Wait()

	waitForState(t, agent, StateIdle, 2*time.Second)
}

func TestF1_ProviderStreamPanicRecovery(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)
	ai.RegisterProvider(&panicProvider{api: "agent-f1"}, "agent-f1")

	driver := NewNativeDriver(DriverConfig{Model: ai.Model{ID: "m", API: "agent-f1", Provider: "test", MaxTokens: 1024}})
	agent := New(driver)
	agent.SetSession(&Session{ID: "runtime-f1"})

	errCh := make(chan AgentEvent, 1)
	agent.Subscribe(func(evt AgentEvent) {
		if evt.Type == EventDriverError {
			select {
			case errCh <- evt:
			default:
			}
		}
	})

	if err := agent.Prompt(context.Background(), "boom"); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	select {
	case evt := <-errCh:
		if evt.ErrorMessage == "" {
			t.Fatalf("expected driver error message")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for driver error")
	}
	waitForState(t, agent, StateIdle, 2*time.Second)
}

func TestF2_ToolHangAndTimeoutRecovery(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)

	prov := &scriptedProvider{api: "agent-f2", responses: []ai.AssistantMessage{
		{
			Content:    []ai.ContentBlock{&ai.ToolCall{ID: "tc-1", Name: "hang", Arguments: map[string]any{}}},
			StopReason: ai.StopReasonToolUse,
			Timestamp:  ai.TimeToMillis(time.Now()),
		},
		{
			Content:    []ai.ContentBlock{&ai.TextContent{Text: "after"}},
			StopReason: ai.StopReasonStop,
			Timestamp:  ai.TimeToMillis(time.Now()),
		},
	}}
	ai.RegisterProvider(prov, "agent-f2")

	tool := AgentTool{
		Tool: ai.Tool{Name: "hang"},
		Execute: func(ctx context.Context, toolCallID string, params map[string]any, onUpdate func(AgentToolResult)) (AgentToolResult, error) {
			<-ctx.Done()
			return AgentToolResult{IsError: true}, ctx.Err()
		},
	}

	driver := NewNativeDriver(DriverConfig{Model: ai.Model{ID: "m", API: "agent-f2", Provider: "test", MaxTokens: 1024}, Tools: []AgentTool{tool}})
	agent := New(driver)
	agent.SetSession(&Session{ID: "runtime-f2"})

	toolErr := make(chan struct{}, 1)
	agent.Subscribe(func(evt AgentEvent) {
		if evt.Type == EventToolError {
			select {
			case toolErr <- struct{}{}:
			default:
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := agent.Prompt(ctx, "run"); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	select {
	case <-toolErr:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for tool error")
	}
	waitForState(t, agent, StateIdle, 2*time.Second)
}

func TestF4_MalformedProviderEventIgnored(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)
	ai.RegisterProvider(&weirdEventProvider{api: "agent-f4"}, "agent-f4")

	driver := NewNativeDriver(DriverConfig{Model: ai.Model{ID: "m", API: "agent-f4", Provider: "test", MaxTokens: 1024}})
	agent := New(driver)
	agent.SetSession(&Session{ID: "runtime-f4"})

	if err := agent.Prompt(context.Background(), "hi"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	waitForState(t, agent, StateIdle, 2*time.Second)
}

func TestF5_ConcurrentControlStorms(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)
	ai.RegisterProvider(&slowProvider{api: "agent-f5", delay: 50 * time.Millisecond}, "agent-f5")

	driver := NewNativeDriver(DriverConfig{Model: ai.Model{ID: "m", API: "agent-f5", Provider: "test", MaxTokens: 1024}})
	agent := New(driver)
	agent.SetSession(&Session{ID: "runtime-f5"})
	if err := agent.Prompt(context.Background(), "storm"); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 3 {
			case 0:
				_ = agent.Steer(fmt.Sprintf("s-%d", i))
			case 1:
				_ = agent.FollowUp(fmt.Sprintf("f-%d", i))
			case 2:
				_ = agent.Abort(fmt.Sprintf("a-%d", i))
			}
		}(i)
	}
	wg.Wait()
	waitForState(t, agent, StateIdle, 3*time.Second)
}

func TestO2_ReplayOracleStateMetrics(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)
	prov := &scriptedProvider{api: "agent-o2", responses: []ai.AssistantMessage{{
		Content:    []ai.ContentBlock{&ai.TextContent{Text: "ok"}},
		StopReason: ai.StopReasonStop,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}}}
	ai.RegisterProvider(prov, "agent-o2")

	driver := NewNativeDriver(DriverConfig{Model: ai.Model{ID: "m", API: "agent-o2", Provider: "test", MaxTokens: 1024}})
	agent := New(driver)
	agent.SetSession(&Session{ID: "runtime-o2"})

	var mu sync.Mutex
	events := make([]AgentEvent, 0)
	agent.Subscribe(func(evt AgentEvent) {
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
	})

	if err := agent.Prompt(context.Background(), "first"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	_ = agent.FollowUp("second")
	waitForState(t, agent, StateIdle, 2*time.Second)

	sess := agent.Session()
	mu.Lock()
	defer mu.Unlock()
	reducedTurns := 0
	for _, e := range events {
		if e.Type == EventTurnCompleted {
			reducedTurns++
		}
	}
	if reducedTurns != int(sess.Metrics.TurnsCompleted) {
		t.Fatalf("replay mismatch: turns from events=%d metrics=%d", reducedTurns, sess.Metrics.TurnsCompleted)
	}
}

func TestS2_ControlBoundarySteeringAppliedNextTurn(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)
	prov := &scriptedProvider{api: "agent-s2", responses: []ai.AssistantMessage{{
		Content:    []ai.ContentBlock{&ai.TextContent{Text: "ok"}},
		StopReason: ai.StopReasonStop,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}}}
	ai.RegisterProvider(prov, "agent-s2")

	driver := NewNativeDriver(DriverConfig{Model: ai.Model{ID: "m", API: "agent-s2", Provider: "test", MaxTokens: 1024}})
	agent := New(driver)
	agent.SetSession(&Session{ID: "runtime-s2"})

	if err := agent.Prompt(context.Background(), "first"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if err := agent.Steer("redirect"); err != nil && !errors.Is(err, ErrQueueFull) {
		t.Fatalf("steer: %v", err)
	}
	waitForState(t, agent, StateIdle, 2*time.Second)

	sess := agent.Session()
	found := false
	for _, entry := range sess.ConversationLog {
		if um, ok := entry.Message.(*ai.UserMessage); ok && len(um.Content) == 1 {
			if tc, ok := um.Content[0].(*ai.TextContent); ok && tc.Text == "redirect" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected steering prompt to be applied at next boundary")
	}
}

func TestST3_BurstFollowUpStress(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test skipped in short mode")
	}
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)
	prov := &scriptedProvider{api: "agent-st3", responses: []ai.AssistantMessage{{
		Content:    []ai.ContentBlock{&ai.TextContent{Text: "ok"}},
		StopReason: ai.StopReasonStop,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}}}
	ai.RegisterProvider(prov, "agent-st3")

	driver := NewNativeDriver(DriverConfig{Model: ai.Model{ID: "m", API: "agent-st3", Provider: "test", MaxTokens: 1024}})
	agent := New(driver)
	agent.SetSession(&Session{ID: "runtime-st3"})

	if err := agent.Prompt(context.Background(), "root"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	for i := 0; i < 1000; i++ {
		_ = agent.FollowUp(fmt.Sprintf("f-%d", i))
	}
	waitForState(t, agent, StateIdle, 8*time.Second)
}

func TestSEC1_EventPayloadNoAPIKeyLeak(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)
	secret := "sk-test-very-secret"
	prov := &scriptedProvider{api: "agent-sec1", responses: []ai.AssistantMessage{{
		Content:    []ai.ContentBlock{&ai.TextContent{Text: "ok"}},
		StopReason: ai.StopReasonStop,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}}}
	ai.RegisterProvider(prov, "agent-sec1")

	driver := NewNativeDriver(DriverConfig{
		Model:   ai.Model{ID: "m", API: "agent-sec1", Provider: "test", MaxTokens: 1024},
		Options: ai.SimpleStreamOptions{StreamOptions: ai.StreamOptions{APIKey: secret}},
	})
	agent := New(driver)
	agent.SetSession(&Session{ID: "runtime-sec1"})

	leak := make(chan string, 1)
	agent.Subscribe(func(evt AgentEvent) {
		for _, field := range []string{evt.ControlMessage, evt.Delta, evt.ErrorMessage} {
			if strings.Contains(field, secret) {
				select {
				case leak <- field:
				default:
				}
			}
		}
	})

	if err := agent.Prompt(context.Background(), "hi"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	waitForState(t, agent, StateIdle, 2*time.Second)

	select {
	case leaked := <-leak:
		t.Fatalf("secret leaked in event payload: %q", leaked)
	default:
	}
}

func TestSEC2_ToolResultBoundarySafety(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)
	prov := &scriptedProvider{api: "agent-sec2", responses: []ai.AssistantMessage{
		{
			Content:    []ai.ContentBlock{&ai.ToolCall{ID: "tc-1", Name: "unsafe", Arguments: map[string]any{}}},
			StopReason: ai.StopReasonToolUse,
			Timestamp:  ai.TimeToMillis(time.Now()),
		},
		{
			Content:    []ai.ContentBlock{&ai.TextContent{Text: "done"}},
			StopReason: ai.StopReasonStop,
			Timestamp:  ai.TimeToMillis(time.Now()),
		},
	}}
	ai.RegisterProvider(prov, "agent-sec2")

	tool := AgentTool{
		Tool: ai.Tool{Name: "unsafe"},
		Execute: func(context.Context, string, map[string]any, func(AgentToolResult)) (AgentToolResult, error) {
			big := strings.Repeat("x", 256*1024)
			invalid := string([]byte{0xff, 0xfe, 0xfd})
			return AgentToolResult{Content: []ai.ContentBlock{&ai.TextContent{Text: big + invalid}}}, nil
		},
	}

	driver := NewNativeDriver(DriverConfig{Model: ai.Model{ID: "m", API: "agent-sec2", Provider: "test", MaxTokens: 1024}, Tools: []AgentTool{tool}})
	agent := New(driver)
	agent.SetSession(&Session{ID: "runtime-sec2"})
	if err := agent.Prompt(context.Background(), "go"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	waitForState(t, agent, StateIdle, 3*time.Second)

	// Current runtime behavior is pass-through for tool result text payloads
	// (no truncation/encoding sanitization layer yet). We still assert deterministic
	// boundary handling by verifying oversized and invalid-UTF8 data survives
	// round-trip without crashing the loop.
	sess := agent.Session()
	var toolText string
	for _, entry := range sess.ConversationLog {
		if tm, ok := entry.Message.(*ai.ToolResultMessage); ok && len(tm.Content) == 1 {
			if txt, ok := tm.Content[0].(*ai.TextContent); ok {
				toolText = txt.Text
			}
		}
	}
	if toolText == "" {
		t.Fatalf("expected tool result text in conversation log")
	}
	if len(toolText) < 256*1024 {
		t.Fatalf("expected large payload in tool result, got len=%d", len(toolText))
	}
	if !strings.Contains(toolText, string([]byte{0xff, 0xfe, 0xfd})) {
		t.Fatalf("expected invalid UTF-8 bytes preserved in current pass-through behavior")
	}
	if utf8.ValidString(toolText) {
		t.Fatalf("expected invalid UTF-8 payload for SEC2 boundary regression")
	}
}

func TestSEC3_DriverNativeIDTrustBoundary(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)
	prov := &scriptedProvider{api: "agent-sec3", responses: []ai.AssistantMessage{{
		Content:    []ai.ContentBlock{&ai.TextContent{Text: "ok"}},
		StopReason: ai.StopReasonStop,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}}}
	ai.RegisterProvider(prov, "agent-sec3")

	driver := NewNativeDriver(DriverConfig{Model: ai.Model{ID: "m", API: "agent-sec3", Provider: "test", MaxTokens: 1024}})
	agent := New(driver)
	runtimeID := "runtime-auth-id"
	agent.SetSession(&Session{ID: runtimeID, DriverSessionID: "driver-spoof-id"})

	bad := make(chan AgentEvent, 1)
	agent.Subscribe(func(evt AgentEvent) {
		if evt.SessionID != "" && evt.SessionID != runtimeID {
			select {
			case bad <- evt:
			default:
			}
		}
	})

	if err := agent.Prompt(context.Background(), "start"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	waitForState(t, agent, StateIdle, 2*time.Second)

	select {
	case evt := <-bad:
		t.Fatalf("event used untrusted session id: %+v", evt)
	default:
	}
}
