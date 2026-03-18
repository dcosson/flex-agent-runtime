package agent

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/termmux/monitor"
)

func TestO1_NativeVsAdapterEventParity(t *testing.T) {
	ai.ClearProviders()
	t.Cleanup(ai.ClearProviders)

	const apiName = "agent-o1-parity"
	prov := &scriptedProvider{api: apiName, responses: []ai.AssistantMessage{{
		Content:    []ai.ContentBlock{&ai.TextContent{Text: "ok"}},
		StopReason: ai.StopReasonStop,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}}}
	ai.RegisterProvider(prov, apiName)

	driver := NewNativeDriver(DriverConfig{Model: ai.Model{ID: "m", API: apiName, Provider: "test", MaxTokens: 1024}})
	nativeAgent := New(driver)
	nativeAgent.SetSession(&Session{ID: "parity-s1"})

	var mu sync.Mutex
	nativeEvents := make([]AgentEvent, 0, 16)
	idle := make(chan struct{}, 1)
	nativeAgent.Subscribe(func(evt AgentEvent) {
		mu.Lock()
		nativeEvents = append(nativeEvents, evt)
		mu.Unlock()
		if evt.Type == EventStateChange && evt.State == StateIdle {
			select {
			case idle <- struct{}{}:
			default:
			}
		}
	})

	if err := nativeAgent.Prompt(context.Background(), "hello"); err != nil {
		t.Fatalf("prompt failed: %v", err)
	}
	select {
	case <-idle:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for native idle")
	}

	adapterMonitorTrace := []monitor.AgentEvent{
		{Type: monitor.EventSessionStarted, Timestamp: time.Now(), Data: monitor.SessionStartedData{SessionID: "parity-s1"}},
		{Type: monitor.EventAgentMessage, Timestamp: time.Now(), Data: monitor.AgentMessageData{Content: "x"}},
		{Type: monitor.EventTurnCompleted, Timestamp: time.Now(), Data: monitor.TurnCompletedData{InputTokens: 1, OutputTokens: 1}},
		{Type: monitor.EventStateChange, Timestamp: time.Now(), Data: monitor.StateChangeData{State: monitor.StateIdle}},
	}
	adapterEvents := make([]AgentEvent, 0, len(adapterMonitorTrace))
	for _, evt := range adapterMonitorTrace {
		adapterEvents = append(adapterEvents, adaptMonitorEvent(evt, "parity-s1"))
	}

	gotNative := normalizeParityClasses(nativeEvents)
	gotAdapter := normalizeParityClasses(adapterEvents)
	if !reflect.DeepEqual(gotNative, gotAdapter) {
		t.Fatalf("event parity mismatch\nnative:  %v\nadapter: %v", gotNative, gotAdapter)
	}
}

func normalizeParityClasses(events []AgentEvent) []string {
	out := make([]string, 0, len(events))
	for _, evt := range events {
		switch evt.Type {
		case EventSessionStarted:
			out = append(out, "session_started")
		case EventAgentMessageCompleted:
			out = append(out, "agent_message_completed")
		case EventTurnCompleted:
			out = append(out, "turn_completed")
		case EventStateChange:
			if evt.State == StateIdle {
				out = append(out, "state_idle")
			}
		}
	}
	return out
}
