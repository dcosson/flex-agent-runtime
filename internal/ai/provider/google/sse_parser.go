package google

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/ai/sse"
)

type toolCallState struct {
	id   string
	name string
	args map[string]any
}

type streamAccumulator struct {
	api      string
	provider string
	modelID  string
	usage    ai.Usage

	textParts         []string
	thinkingParts     []string
	toolCalls         []*toolCallState
	thoughtSignatures [][]byte
	stopReason        ai.StopReason
	safetyBlocked     bool
	safetyDetails     string
	contentIndex      int
	started           bool
}

func newAccumulator(model ai.Model) *streamAccumulator {
	return &streamAccumulator{
		api:        model.API,
		provider:   model.Provider,
		modelID:    model.ID,
		stopReason: ai.StopReasonStop,
	}
}

func processStream(ctx context.Context, body io.Reader, model ai.Model, es *ai.EventStream) error {
	acc := newAccumulator(model)
	scanner := sse.NewScanner(body)

	for scanner.Next() {
		select {
		case <-ctx.Done():
			return &ai.ProviderError{Code: ai.ErrUnknown, Message: "request canceled", Provider: "google"}
		default:
		}

		ev := scanner.UnsafeEvent()
		data := strings.TrimSpace(ev.Data)
		if data == "" {
			continue
		}

		var chunk generateContentResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return providerError(0, fmt.Sprintf("parse chunk: %v", err))
		}

		// Emit start event on first chunk
		if !acc.started {
			acc.started = true
			es.Send(ai.AssistantMessageEvent{Type: ai.EventStart, Partial: acc.partial()})
		}

		// Check for prompt-level blocking
		if chunk.PromptFeedback != nil && chunk.PromptFeedback.BlockReason != "" {
			return &ai.ProviderError{
				Code:     ai.ErrUnknown,
				Message:  "prompt blocked: " + chunk.PromptFeedback.BlockReason,
				Provider: "google",
			}
		}

		// Process candidates (we only use candidate 0)
		if len(chunk.Candidates) > 0 {
			cand := &chunk.Candidates[0]

			// Check safety status BEFORE emitting content to avoid
			// leaking partial content from blocked responses.
			if cand.FinishReason != "" {
				acc.stopReason = mapFinishReason(cand.FinishReason)
				if isSafetyBlock(cand.FinishReason) {
					acc.safetyBlocked = true
					acc.safetyDetails = formatSafetyRatings(cand.SafetyRatings)
					continue // Do NOT process parts from safety-blocked candidates
				}
			}

			processCandidateParts(es, acc, cand)
		}

		// Usage from final chunk
		if chunk.UsageMetadata != nil {
			acc.usage = mapUsage(chunk.UsageMetadata)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("sse scan: %w", err)
	}

	if acc.safetyBlocked {
		return &ai.ProviderError{
			Code:     ai.ErrUnknown,
			Message:  "response blocked by safety filter: " + acc.safetyDetails,
			Provider: "google",
		}
	}

	return emitDone(es, acc, model)
}

// processCandidateParts handles all parts from a streaming candidate chunk.
// Gemini delivers function calls complete (not delta-streamed), so no streaming
// JSON lexer is needed — a key simplification compared to Anthropic/OpenAI.
// Text and thinking parts arrive as deltas with `thought: true` flag distinguishing them.
func processCandidateParts(es *ai.EventStream, acc *streamAccumulator, cand *candidate) {
	if cand.Content == nil {
		return
	}

	for _, p := range cand.Content.Parts {
		switch {
		case p.FunctionCall != nil:
			tc := &toolCallState{
				id:   p.FunctionCall.ID,
				name: p.FunctionCall.Name,
				args: p.FunctionCall.Args,
			}
			if tc.id == "" {
				tc.id = generateToolCallID(tc.name, len(acc.toolCalls))
			}
			acc.toolCalls = append(acc.toolCalls, tc)
			idx := acc.contentIndex
			acc.contentIndex++

			// Emit start + end immediately (complete call)
			es.Send(ai.AssistantMessageEvent{
				Type:         ai.EventToolCallStart,
				ContentIndex: idx,
				ToolCall:     &ai.ToolCall{ID: tc.id, Name: tc.name},
			})
			es.Send(ai.AssistantMessageEvent{
				Type:         ai.EventToolCallEnd,
				ContentIndex: idx,
				ToolCall: &ai.ToolCall{
					ID:        tc.id,
					Name:      tc.name,
					Arguments: tc.args,
				},
			})

		case p.ThoughtSignature != nil:
			acc.thoughtSignatures = append(acc.thoughtSignatures, p.ThoughtSignature)

		case p.Thought != nil && *p.Thought:
			acc.thinkingParts = append(acc.thinkingParts, p.Text)
			es.Send(ai.AssistantMessageEvent{
				Type:  ai.EventThinkingDelta,
				Delta: p.Text,
			})

		case p.Text != "":
			acc.textParts = append(acc.textParts, p.Text)
			es.Send(ai.AssistantMessageEvent{
				Type:  ai.EventTextDelta,
				Delta: p.Text,
			})
		}
	}
}

func emitDone(es *ai.EventStream, acc *streamAccumulator, model ai.Model) error {
	msg := &ai.AssistantMessage{
		API:        acc.api,
		Provider:   acc.provider,
		Model:      acc.modelID,
		Usage:      acc.usage,
		StopReason: acc.stopReason,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}

	// Assemble content blocks: thinking first, then text, then tool calls
	if len(acc.thinkingParts) > 0 {
		msg.Content = append(msg.Content, &ai.ThinkingContent{
			Thinking: strings.Join(acc.thinkingParts, ""),
		})
	}
	if len(acc.textParts) > 0 {
		msg.Content = append(msg.Content, &ai.TextContent{
			Text: strings.Join(acc.textParts, ""),
		})
	}
	for _, tc := range acc.toolCalls {
		msg.Content = append(msg.Content, &ai.ToolCall{
			ID:        tc.id,
			Name:      tc.name,
			Arguments: tc.args,
		})
	}

	// Attach thought signatures
	attachThoughtSignatures(msg, acc.thoughtSignatures)

	ai.CalculateCost(model, &msg.Usage)
	es.Send(ai.AssistantMessageEvent{Type: ai.EventDone, Reason: msg.StopReason, Message: msg})
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

// generateToolCallID creates a synthetic ID when Gemini doesn't provide one.
func generateToolCallID(name string, index int) string {
	return fmt.Sprintf("call_%s_%d", name, index)
}
