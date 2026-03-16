package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"flex-agent-runtime/internal/ai"
	"flex-agent-runtime/internal/ai/sse"
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

	contentBuf     strings.Builder
	reasoningBuf   strings.Builder
	toolStates     map[int]*toolStreamState
	finalToolCalls []*ai.ToolCall // populated by finalizeToolCalls
	started        bool
}

func newAccumulator(model ai.Model) *streamAccumulator {
	return &streamAccumulator{
		api:        model.API,
		provider:   model.Provider,
		modelID:    model.ID,
		stopReason: ai.StopReasonStop,
		toolStates: make(map[int]*toolStreamState),
	}
}

func processStream(ctx context.Context, body io.Reader, model ai.Model, es *ai.EventStream) error {
	acc := newAccumulator(model)
	scanner := sse.NewScanner(body)

	for scanner.Next() {
		select {
		case <-ctx.Done():
			return &ai.ProviderError{Code: ai.ErrUnknown, Message: "request canceled", Provider: "openai"}
		default:
		}

		ev := scanner.UnsafeEvent()
		data := strings.TrimSpace(ev.Data)
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}

		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return providerError(0, fmt.Sprintf("parse chunk: %v", err))
		}

		// Emit start event on first chunk
		if !acc.started {
			acc.started = true
			if chunk.ID != "" {
				acc.modelID = model.ID // keep canonical model ID
			}
			es.Send(ai.AssistantMessageEvent{Type: ai.EventStart, Partial: acc.partial()})
		}

		// Usage (typically in final chunk)
		if chunk.Usage != nil {
			acc.usage = mapUsage(chunk.Usage)
		}

		for i := range chunk.Choices {
			if err := processChoice(es, acc, &chunk.Choices[i]); err != nil {
				return err
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("sse scan: %w", err)
	}

	return finishStream(es, acc, model)
}

// processChoice handles a single choice from a streaming chunk.
// Note: Unlike Anthropic, OpenAI's wire format has no explicit content_block_start/stop
// framing, so text and thinking deltas are emitted without corresponding start/end
// lifecycle events. Tool calls do get start/end events because OpenAI provides
// index-based tool call lifecycle. Consumers tracking content block lifecycle across
// providers should handle this asymmetry.
func processChoice(es *ai.EventStream, acc *streamAccumulator, choice *chunkChoice) error {
	d := &choice.Delta

	// Text content delta (no EventTextStart/End — see note above)
	if d.Content != nil && *d.Content != "" {
		acc.contentBuf.WriteString(*d.Content)
		es.Send(ai.AssistantMessageEvent{
			Type:  ai.EventTextDelta,
			Delta: *d.Content,
		})
	}

	// Reasoning content delta (no EventThinkingStart/End — see note above)
	if d.Reasoning != nil && *d.Reasoning != "" {
		acc.reasoningBuf.WriteString(*d.Reasoning)
		es.Send(ai.AssistantMessageEvent{
			Type:  ai.EventThinkingDelta,
			Delta: *d.Reasoning,
		})
	}

	// Tool call deltas
	for i := range d.ToolCalls {
		if err := processToolCallDelta(es, acc, &d.ToolCalls[i]); err != nil {
			return err
		}
	}

	// Finish reason
	if choice.FinishReason != nil {
		acc.stopReason = mapStopReason(choice.FinishReason)
	}

	return nil
}

func processToolCallDelta(es *ai.EventStream, acc *streamAccumulator, tc *toolCall) error {
	idx := 0
	if tc.Index != nil {
		idx = *tc.Index
	}

	state, exists := acc.toolStates[idx]
	if !exists {
		// New tool call — first chunk has id and function.name
		state = &toolStreamState{
			id:     tc.ID,
			name:   tc.Function.Name,
			parser: newToolJSONParser(),
		}
		acc.toolStates[idx] = state
		es.Send(ai.AssistantMessageEvent{
			Type:         ai.EventToolCallStart,
			ContentIndex: idx,
			ToolCall:     &ai.ToolCall{ID: tc.ID, Name: tc.Function.Name},
		})
	}

	// Argument delta
	if tc.Function.Arguments != "" {
		partial, valid, err := state.parser.AppendDelta(tc.Function.Arguments)
		if err != nil {
			return err
		}
		ev := ai.AssistantMessageEvent{
			Type:         ai.EventToolCallDelta,
			ContentIndex: idx,
			Delta:        tc.Function.Arguments,
		}
		if valid {
			ev.ToolCall = &ai.ToolCall{ID: state.id, Name: state.name, Arguments: partial}
		}
		es.Send(ev)
	}

	return nil
}

func finishStream(es *ai.EventStream, acc *streamAccumulator, model ai.Model) error {
	// Finalize tool calls if any
	if len(acc.toolStates) > 0 {
		if err := finalizeToolCalls(es, acc); err != nil {
			return err
		}
	}

	// Build final message content
	var content []ai.ContentBlock
	if acc.reasoningBuf.Len() > 0 {
		content = append(content, &ai.ThinkingContent{Thinking: acc.reasoningBuf.String()})
	}
	if acc.contentBuf.Len() > 0 {
		content = append(content, &ai.TextContent{Text: acc.contentBuf.String()})
	}
	for _, tc := range acc.finalToolCalls {
		content = append(content, tc)
	}

	msg := ai.AssistantMessage{
		Content:    content,
		API:        acc.api,
		Provider:   acc.provider,
		Model:      acc.modelID,
		Usage:      acc.usage,
		StopReason: acc.stopReason,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}
	ai.CalculateCost(model, &msg.Usage)
	es.Send(ai.AssistantMessageEvent{Type: ai.EventDone, Reason: msg.StopReason, Message: &msg})
	return nil
}

func finalizeToolCalls(es *ai.EventStream, acc *streamAccumulator) error {
	// Sort indices for deterministic finalization order
	indices := make([]int, 0, len(acc.toolStates))
	for idx := range acc.toolStates {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	for _, idx := range indices {
		state := acc.toolStates[idx]
		finalArgs, err := state.parser.ParseFinal()
		if err != nil {
			return providerError(0, fmt.Sprintf("tool %q (index %d): final argument JSON parse failed: %v", state.name, idx, err))
		}
		tc := &ai.ToolCall{ID: state.id, Name: state.name, Arguments: finalArgs}
		acc.finalToolCalls = append(acc.finalToolCalls, tc)
		es.Send(ai.AssistantMessageEvent{
			Type:         ai.EventToolCallEnd,
			ContentIndex: idx,
			ToolCall:     tc,
		})
	}
	return nil
}

func (a *streamAccumulator) partial() *ai.AssistantMessage {
	return &ai.AssistantMessage{
		API:        a.api,
		Provider:   a.provider,
		Model:      a.modelID,
		Usage:      a.usage,
		StopReason: a.stopReason,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}
}
