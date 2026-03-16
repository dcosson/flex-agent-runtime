package openai

import (
	"encoding/json"
	"fmt"

	"flex-agent-runtime/internal/ai"
)

// requestParams holds parameters for building the wire request that don't
// fit naturally into StreamOptions (which has no reasoning-effort field).
type requestParams struct {
	reasoningEffort string
}

func buildRequest(model ai.Model, llmCtx ai.Context, opts ai.StreamOptions, params requestParams) (chatRequest, error) {
	maxTokens := model.MaxTokens
	if opts.MaxTokens != nil {
		maxTokens = *opts.MaxTokens
	}
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	messages := convertMessages(llmCtx.Messages, model, llmCtx.SystemPrompt)
	tools := convertTools(llmCtx.Tools, model)

	req := chatRequest{
		Model:    model.ID,
		Messages: messages,
		Tools:    tools,
		Stream:   true,
		Metadata: opts.Metadata,
	}

	// Max tokens field selection
	if useMaxCompletionTokens(model) {
		req.MaxCompletionTokens = &maxTokens
	} else {
		req.MaxTokens = &maxTokens
	}

	// Temperature
	if opts.Temperature != nil {
		req.Temperature = opts.Temperature
	}
	if opts.TopP != nil {
		req.TopP = opts.TopP
	}

	// Reasoning effort
	if params.reasoningEffort != "" {
		req.ReasoningEffort = params.reasoningEffort
	}

	// Store
	if shouldIncludeStore(model) {
		store := true
		req.Store = &store
	}

	// Stream usage
	if shouldRequestStreamUsage(model) {
		req.StreamOptions = &streamOptions{IncludeUsage: true}
	}

	return req, nil
}

func convertMessages(messages []ai.Message, model ai.Model, system string) []chatMessage {
	var out []chatMessage

	if system != "" {
		out = append(out, chatMessage{Role: systemRole(model), Content: system})
	}

	// Track tool call ID mappings for Mistral normalization so tool results
	// reference the same normalized IDs as their corresponding tool calls.
	toolIDMap := make(map[string]string)

	for _, msg := range messages {
		switch m := msg.(type) {
		case *ai.UserMessage:
			out = append(out, convertUserMessage(m))
		case *ai.AssistantMessage:
			out = append(out, convertAssistantMessage(m, model, toolIDMap))
		case *ai.ToolResultMessage:
			out = append(out, convertToolResult(m, model, toolIDMap)...)
		}
	}
	return out
}

func convertUserMessage(m *ai.UserMessage) chatMessage {
	if len(m.Content) == 1 {
		if t, ok := m.Content[0].(*ai.TextContent); ok {
			return chatMessage{Role: "user", Content: t.Text}
		}
	}
	parts := make([]contentPart, 0, len(m.Content))
	for _, b := range m.Content {
		switch c := b.(type) {
		case *ai.TextContent:
			parts = append(parts, contentPart{Type: "text", Text: c.Text})
		case *ai.ImageContent:
			parts = append(parts, contentPart{
				Type:     "image_url",
				ImageURL: &imageURL{URL: fmt.Sprintf("data:%s;base64,%s", c.MimeType, c.Data)},
			})
		}
	}
	return chatMessage{Role: "user", Content: parts}
}

func convertAssistantMessage(m *ai.AssistantMessage, model ai.Model, toolIDMap map[string]string) chatMessage {
	var textContent string
	var contentParts []contentPart
	var toolCalls []toolCall
	hasMultipleText := false

	for _, b := range m.Content {
		switch c := b.(type) {
		case *ai.TextContent:
			if textContent == "" && !hasMultipleText {
				textContent = c.Text
			} else {
				if !hasMultipleText {
					contentParts = append(contentParts, contentPart{Type: "text", Text: textContent})
					hasMultipleText = true
				}
				contentParts = append(contentParts, contentPart{Type: "text", Text: c.Text})
			}
		case *ai.ThinkingContent:
			if requiresThinkingAsText(model) {
				part := contentPart{Type: "text", Text: c.Thinking}
				if hasMultipleText {
					contentParts = append(contentParts, part)
				} else if textContent != "" {
					contentParts = append(contentParts, contentPart{Type: "text", Text: textContent})
					contentParts = append(contentParts, part)
					hasMultipleText = true
				} else {
					contentParts = append(contentParts, part)
					hasMultipleText = true
				}
			}
			// else: drop thinking blocks (default) or format per ThinkingFormat
		case *ai.ToolCall:
			argsJSON, _ := json.Marshal(c.Arguments)
			tcID := normalizeToolCallID(c.ID, model)
			if tcID != c.ID {
				toolIDMap[c.ID] = tcID
			}
			toolCalls = append(toolCalls, toolCall{
				ID:   tcID,
				Type: "function",
				Function: functionCall{
					Name:      c.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}

	msg := chatMessage{Role: "assistant", ToolCalls: toolCalls}
	if hasMultipleText {
		msg.Content = contentParts
	} else if textContent != "" {
		msg.Content = textContent
	}
	return msg
}

func convertToolResult(m *ai.ToolResultMessage, model ai.Model, toolIDMap map[string]string) []chatMessage {
	content := toolResultContent(m.Content)
	tcID := m.ToolCallID
	if mapped, ok := toolIDMap[tcID]; ok {
		tcID = mapped
	}
	msg := chatMessage{
		Role:       "tool",
		ToolCallID: tcID,
		Content:    content,
	}
	if requiresToolResultName(model) {
		msg.Name = m.ToolName
	}

	result := []chatMessage{msg}
	if requiresAssistantAfterToolResult(model) {
		result = append(result, chatMessage{
			Role:    "assistant",
			Content: "Acknowledged.",
		})
	}
	return result
}

func toolResultContent(blocks []ai.ContentBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	if len(blocks) == 1 {
		if t, ok := blocks[0].(*ai.TextContent); ok {
			return t.Text
		}
	}
	// For multiple blocks, concatenate text content
	var result string
	for _, b := range blocks {
		if t, ok := b.(*ai.TextContent); ok {
			if result != "" {
				result += "\n"
			}
			result += t.Text
		}
	}
	return result
}

func convertTools(tools []ai.Tool, model ai.Model) []chatTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]chatTool, len(tools))
	for i, t := range tools {
		ct := chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		}
		if supportsStrictMode(model) {
			strict := true
			ct.Function.Strict = &strict
		}
		out[i] = ct
	}
	return out
}
