package anthropic

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/ai/sse"
)

type toolStreamState struct {
	id     string
	name   string
	parser *toolJSONParser
}

type streamAccumulator struct {
	api        string
	provider   string
	modelID    string
	usage      ai.Usage
	stopReason ai.StopReason

	blocks     map[int]ai.ContentBlock
	toolStates map[int]*toolStreamState
}

func newAccumulator(model ai.Model) *streamAccumulator {
	return &streamAccumulator{
		api:        model.API,
		provider:   model.Provider,
		modelID:    model.ID,
		stopReason: ai.StopReasonStop,
		blocks:     make(map[int]ai.ContentBlock),
		toolStates: make(map[int]*toolStreamState),
	}
}

func (a *streamAccumulator) orderedContent() []ai.ContentBlock {
	if len(a.blocks) == 0 {
		return nil
	}
	idx := make([]int, 0, len(a.blocks))
	for i := range a.blocks {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	out := make([]ai.ContentBlock, 0, len(idx))
	for _, i := range idx {
		if b, ok := a.blocks[i]; ok {
			out = append(out, b)
		}
	}
	return out
}

func (a *streamAccumulator) partial() *ai.AssistantMessage {
	return &ai.AssistantMessage{
		Content:    a.orderedContent(),
		API:        a.api,
		Provider:   a.provider,
		Model:      a.modelID,
		Usage:      a.usage,
		StopReason: a.stopReason,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}
}

type sseProcessor struct {
	model ai.Model
	es    *ai.EventStream
	acc   *streamAccumulator
	done  bool
}

func newSSEProcessor(model ai.Model, es *ai.EventStream) *sseProcessor {
	return &sseProcessor{model: model, es: es, acc: newAccumulator(model)}
}

func (p *sseProcessor) process(ev sse.Event) error {
	if strings.TrimSpace(ev.Data) == "" {
		return nil
	}
	var env wireEventEnvelope
	if err := unmarshalEvent(ev.Data, &env); err != nil {
		return fmt.Errorf("decode event envelope: %w", err)
	}

	switch env.Type {
	case "ping":
		return nil
	case "message_start":
		var e wireMessageStartEvent
		if err := unmarshalEvent(ev.Data, &e); err != nil {
			return fmt.Errorf("decode message_start: %w", err)
		}
		if e.Message.Model != "" {
			p.acc.modelID = e.Message.Model
		}
		p.acc.usage = mapUsage(e.Message.Usage)
		p.es.Send(ai.AssistantMessageEvent{Type: ai.EventStart, Partial: p.acc.partial()})
		return nil

	case "content_block_start":
		var e wireContentBlockStartEvent
		if err := unmarshalEvent(ev.Data, &e); err != nil {
			return fmt.Errorf("decode content_block_start: %w", err)
		}
		return p.onBlockStart(e)

	case "content_block_delta":
		var e wireContentBlockDeltaEvent
		if err := unmarshalEvent(ev.Data, &e); err != nil {
			return fmt.Errorf("decode content_block_delta: %w", err)
		}
		return p.onBlockDelta(e)

	case "content_block_stop":
		var e wireContentBlockStopEvent
		if err := unmarshalEvent(ev.Data, &e); err != nil {
			return fmt.Errorf("decode content_block_stop: %w", err)
		}
		return p.onBlockStop(e)

	case "message_delta":
		var e wireMessageDeltaEvent
		if err := unmarshalEvent(ev.Data, &e); err != nil {
			return fmt.Errorf("decode message_delta: %w", err)
		}
		mergeUsage(&p.acc.usage, e.Usage)
		p.acc.stopReason = mapStopReason(e.Delta.StopReason)
		return nil

	case "message_stop":
		return p.finish(nil)

	case "error":
		var e wireErrorEvent
		if err := unmarshalEvent(ev.Data, &e); err != nil {
			return fmt.Errorf("decode error event: %w", err)
		}
		return providerError(0, e.Error.Message)
	default:
		return nil
	}
}

func (p *sseProcessor) onBlockStart(e wireContentBlockStartEvent) error {
	switch e.ContentBlock.Type {
	case "text":
		p.acc.blocks[e.Index] = &ai.TextContent{Text: e.ContentBlock.Text}
		p.es.Send(ai.AssistantMessageEvent{Type: ai.EventTextStart, ContentIndex: e.Index, Partial: p.acc.partial()})
	case "thinking":
		p.acc.blocks[e.Index] = &ai.ThinkingContent{Thinking: e.ContentBlock.Thinking, ThinkingSignature: e.ContentBlock.Signature}
		p.es.Send(ai.AssistantMessageEvent{Type: ai.EventThinkingStart, ContentIndex: e.Index, Partial: p.acc.partial()})
	case "tool_use":
		state := &toolStreamState{id: e.ContentBlock.ID, name: e.ContentBlock.Name, parser: newToolJSONParser()}
		p.acc.toolStates[e.Index] = state
		if len(e.ContentBlock.Input) > 0 {
			if b, err := json.Marshal(e.ContentBlock.Input); err == nil {
				_, _, _ = state.parser.AppendDelta(string(b))
			}
		}
		p.acc.blocks[e.Index] = &ai.ToolCall{ID: state.id, Name: state.name, Arguments: map[string]any{}}
		p.es.Send(ai.AssistantMessageEvent{Type: ai.EventToolCallStart, ContentIndex: e.Index, Partial: p.acc.partial()})
	}
	return nil
}

func (p *sseProcessor) onBlockDelta(e wireContentBlockDeltaEvent) error {
	switch e.Delta.Type {
	case "text_delta":
		b, ok := p.acc.blocks[e.Index].(*ai.TextContent)
		if !ok {
			b = &ai.TextContent{}
			p.acc.blocks[e.Index] = b
		}
		b.Text += e.Delta.Text
		p.es.Send(ai.AssistantMessageEvent{Type: ai.EventTextDelta, ContentIndex: e.Index, Delta: e.Delta.Text, Partial: p.acc.partial()})
	case "thinking_delta":
		b, ok := p.acc.blocks[e.Index].(*ai.ThinkingContent)
		if !ok {
			b = &ai.ThinkingContent{}
			p.acc.blocks[e.Index] = b
		}
		b.Thinking += e.Delta.Thinking
		if e.Delta.Signature != "" {
			b.ThinkingSignature = e.Delta.Signature
		}
		p.es.Send(ai.AssistantMessageEvent{Type: ai.EventThinkingDelta, ContentIndex: e.Index, Delta: e.Delta.Thinking, Partial: p.acc.partial()})
	case "signature_delta":
		b, ok := p.acc.blocks[e.Index].(*ai.ThinkingContent)
		if !ok {
			b = &ai.ThinkingContent{}
			p.acc.blocks[e.Index] = b
		}
		if e.Delta.Signature != "" {
			b.ThinkingSignature = e.Delta.Signature
		}
	case "input_json_delta":
		state, ok := p.acc.toolStates[e.Index]
		if !ok {
			return fmt.Errorf("tool delta for unknown index %d", e.Index)
		}
		partial, valid, err := state.parser.AppendDelta(e.Delta.PartialJSON)
		if err != nil {
			return err
		}
		ev := ai.AssistantMessageEvent{Type: ai.EventToolCallDelta, ContentIndex: e.Index, Delta: e.Delta.PartialJSON, Partial: p.acc.partial()}
		if valid {
			ev.ToolCall = &ai.ToolCall{ID: state.id, Name: state.name, Arguments: partial}
		}
		p.es.Send(ev)
	}
	return nil
}

func (p *sseProcessor) onBlockStop(e wireContentBlockStopEvent) error {
	if text, ok := p.acc.blocks[e.Index].(*ai.TextContent); ok {
		p.es.Send(ai.AssistantMessageEvent{Type: ai.EventTextEnd, ContentIndex: e.Index, Content: text.Text, Partial: p.acc.partial()})
		return nil
	}
	if thinking, ok := p.acc.blocks[e.Index].(*ai.ThinkingContent); ok {
		p.es.Send(ai.AssistantMessageEvent{Type: ai.EventThinkingEnd, ContentIndex: e.Index, Content: thinking.Thinking, Partial: p.acc.partial()})
		return nil
	}
	state, ok := p.acc.toolStates[e.Index]
	if !ok {
		return nil
	}
	args, err := state.parser.ParseFinal()
	if err != nil {
		return providerError(0, "tool argument parse failure: "+err.Error())
	}
	tc := &ai.ToolCall{ID: state.id, Name: state.name, Arguments: args}
	p.acc.blocks[e.Index] = tc
	delete(p.acc.toolStates, e.Index)
	p.es.Send(ai.AssistantMessageEvent{Type: ai.EventToolCallEnd, ContentIndex: e.Index, ToolCall: tc, Partial: p.acc.partial()})
	return nil
}

func (p *sseProcessor) finish(err error) error {
	if p.done {
		return nil
	}
	p.done = true
	if err != nil {
		return err
	}
	msg := ai.AssistantMessage{
		Content:    p.acc.orderedContent(),
		API:        p.acc.api,
		Provider:   p.acc.provider,
		Model:      p.acc.modelID,
		Usage:      p.acc.usage,
		StopReason: p.acc.stopReason,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}
	ai.CalculateCost(p.model, &msg.Usage)
	p.es.Send(ai.AssistantMessageEvent{Type: ai.EventDone, Reason: msg.StopReason, Message: &msg})
	return nil
}
