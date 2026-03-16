package testutil

import (
	"context"
	"fmt"
	"sync"
	"time"

	"flex-agent-runtime/internal/ai"
)

// ScriptEntry defines one provider response in a scripted conversation.
// If ToolCalls is set, the response will contain tool call content blocks.
// If Text is set, it will contain a text content block.
type ScriptEntry struct {
	Text       string
	ToolCalls  []ToolCallSpec
	StopReason ai.StopReason
	Error      string
}

// ToolCallSpec defines a tool call within a scripted provider response.
type ToolCallSpec struct {
	ID        string
	Name      string
	Arguments map[string]any
}

// ScriptedProvider is a deterministic fake AI provider that replays a
// pre-defined sequence of responses. Each call to Stream/StreamSimple
// returns the next entry in the script. If calls exceed the script length,
// the last entry is repeated.
type ScriptedProvider struct {
	api     string
	mu      sync.Mutex
	script  []ScriptEntry
	calls   int
	CallLog []ScriptedProviderCall

	// Gates allows blocking specific provider calls until signaled.
	// Key is the 0-based call index. The provider will wait on the channel
	// before returning the response for that call.
	Gates map[int]chan struct{}
}

// ScriptedProviderCall records what the provider received.
type ScriptedProviderCall struct {
	Messages     []ai.Message
	Tools        []ai.Tool
	SystemPrompt string
}

// NewScriptedProvider creates a fake provider with the given API name and script.
func NewScriptedProvider(api string, script []ScriptEntry) *ScriptedProvider {
	return &ScriptedProvider{
		api:    api,
		script: script,
	}
}

func (p *ScriptedProvider) API() string { return p.api }

func (p *ScriptedProvider) Stream(ctx context.Context, model ai.Model, llmCtx ai.Context, opts ai.StreamOptions) *ai.EventStream {
	return p.streamNext(ctx, llmCtx)
}

func (p *ScriptedProvider) StreamSimple(ctx context.Context, model ai.Model, llmCtx ai.Context, opts ai.SimpleStreamOptions) *ai.EventStream {
	return p.streamNext(ctx, llmCtx)
}

// Calls returns the number of provider calls made.
func (p *ScriptedProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *ScriptedProvider) streamNext(ctx context.Context, llmCtx ai.Context) *ai.EventStream {
	es := ai.NewEventStream()
	go func() {
		defer es.Close()
		p.mu.Lock()
		idx := p.calls
		p.calls++
		var entry ScriptEntry
		if idx < len(p.script) {
			entry = p.script[idx]
		} else if len(p.script) > 0 {
			entry = p.script[len(p.script)-1]
		}
		// Record the call
		p.CallLog = append(p.CallLog, ScriptedProviderCall{
			Messages:     llmCtx.Messages,
			Tools:        llmCtx.Tools,
			SystemPrompt: llmCtx.SystemPrompt,
		})
		gate := p.Gates[idx]
		p.mu.Unlock()

		// Wait for gate or context cancellation
		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				es.Send(ai.AssistantMessageEvent{
					Type:  ai.EventError,
					Error: &ai.AssistantMessage{StopReason: ai.StopReasonError},
				})
				return
			}
		}
		if ctx.Err() != nil {
			es.Send(ai.AssistantMessageEvent{
				Type:  ai.EventError,
				Error: &ai.AssistantMessage{StopReason: ai.StopReasonError},
			})
			return
		}

		msg := buildMessage(entry)
		if entry.Error != "" {
			es.Send(ai.AssistantMessageEvent{
				Type: ai.EventError,
				Error: &ai.AssistantMessage{
					StopReason:   ai.StopReasonError,
					ErrorMessage: entry.Error,
					Timestamp:    ai.TimeToMillis(time.Now()),
				},
			})
			return
		}

		// Send a delta event for text content
		if entry.Text != "" {
			es.Send(ai.AssistantMessageEvent{Type: ai.EventTextDelta, Delta: entry.Text})
		}
		for _, tc := range entry.ToolCalls {
			es.Send(ai.AssistantMessageEvent{
				Type: ai.EventToolCallStart,
				ToolCall: &ai.ToolCall{
					ID:        tc.ID,
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			})
		}
		es.Send(ai.AssistantMessageEvent{Type: ai.EventDone, Message: &msg})
	}()
	return es
}

func buildMessage(entry ScriptEntry) ai.AssistantMessage {
	var content []ai.ContentBlock
	if entry.Text != "" {
		content = append(content, &ai.TextContent{Text: entry.Text})
	}
	for _, tc := range entry.ToolCalls {
		content = append(content, &ai.ToolCall{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: tc.Arguments,
		})
	}

	stop := entry.StopReason
	if stop == "" {
		if len(entry.ToolCalls) > 0 {
			stop = ai.StopReasonToolUse
		} else {
			stop = ai.StopReasonStop
		}
	}

	return ai.AssistantMessage{
		Content:    content,
		StopReason: stop,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}
}

// UniqueAPI returns a unique API name for test isolation.
var apiCounter int
var apiMu sync.Mutex

func UniqueAPI(prefix string) string {
	apiMu.Lock()
	defer apiMu.Unlock()
	apiCounter++
	return fmt.Sprintf("%s-%d", prefix, apiCounter)
}
