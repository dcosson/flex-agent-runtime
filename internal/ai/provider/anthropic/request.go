package anthropic

import (
	"encoding/json"
	"fmt"

	"flex-agent-runtime/internal/ai"
)

func buildRequest(model ai.Model, llmCtx ai.Context, opts ai.StreamOptions, thinking *wireThinking) (wireRequest, error) {
	maxTokens := model.MaxTokens
	if opts.MaxTokens != nil {
		maxTokens = *opts.MaxTokens
	}
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	messages := make([]wireMessage, 0, len(llmCtx.Messages))
	for _, msg := range llmCtx.Messages {
		wm, err := toWireMessage(msg)
		if err != nil {
			return wireRequest{}, err
		}
		if len(wm.Content) == 0 {
			continue
		}
		messages = append(messages, wm)
	}

	tools, err := toWireTools(llmCtx.Tools)
	if err != nil {
		return wireRequest{}, err
	}

	return wireRequest{
		Model:     model.ID,
		MaxTokens: maxTokens,
		TopP:      opts.TopP,
		TopK:      opts.TopK,
		Messages:  messages,
		System:    llmCtx.SystemPrompt,
		Tools:     tools,
		Thinking:  thinking,
		Stream:    true,
		Metadata:  opts.Metadata,
		Headers:   opts.Headers,
	}, nil
}

func toWireTools(tools []ai.Tool) ([]wireTool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	out := make([]wireTool, 0, len(tools))
	for _, t := range tools {
		schema := map[string]any{}
		if len(t.Parameters) > 0 {
			if err := json.Unmarshal(t.Parameters, &schema); err != nil {
				return nil, fmt.Errorf("tool %q has invalid schema: %w", t.Name, err)
			}
		}
		out = append(out, wireTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return out, nil
}

func toWireMessage(msg ai.Message) (wireMessage, error) {
	switch m := msg.(type) {
	case *ai.UserMessage:
		blocks := make([]wireContentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			switch c := b.(type) {
			case *ai.TextContent:
				blocks = append(blocks, wireContentBlock{Type: "text", Text: c.Text})
			case *ai.ImageContent:
				blocks = append(blocks, wireContentBlock{
					Type: "image",
					Source: &wireImageSource{
						Type:      "base64",
						MediaType: c.MimeType,
						Data:      c.Data,
					},
				})
			}
		}
		return wireMessage{Role: "user", Content: blocks}, nil

	case *ai.AssistantMessage:
		blocks := make([]wireContentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			switch c := b.(type) {
			case *ai.TextContent:
				blocks = append(blocks, wireContentBlock{Type: "text", Text: c.Text})
			case *ai.ThinkingContent:
				blocks = append(blocks, wireContentBlock{Type: "thinking", Thinking: c.Thinking, Signature: c.ThinkingSignature})
			case *ai.ToolCall:
				blocks = append(blocks, wireContentBlock{Type: "tool_use", ID: c.ID, Name: c.Name, Input: c.Arguments})
			}
		}
		return wireMessage{Role: "assistant", Content: blocks}, nil

	case *ai.ToolResultMessage:
		content := wireToolResultContent(m.Content)
		block := wireContentBlock{
			Type:      "tool_result",
			ToolUseID: m.ToolCallID,
			IsError:   m.IsError,
			Content:   content,
		}
		return wireMessage{Role: "user", Content: []wireContentBlock{block}}, nil
	}
	return wireMessage{}, fmt.Errorf("unsupported message type %T", msg)
}

func wireToolResultContent(blocks []ai.ContentBlock) any {
	if len(blocks) == 0 {
		return ""
	}
	if len(blocks) == 1 {
		if t, ok := blocks[0].(*ai.TextContent); ok {
			return t.Text
		}
	}
	out := make([]wireContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch c := b.(type) {
		case *ai.TextContent:
			out = append(out, wireContentBlock{Type: "text", Text: c.Text})
		case *ai.ImageContent:
			out = append(out, wireContentBlock{
				Type: "image",
				Source: &wireImageSource{
					Type:      "base64",
					MediaType: c.MimeType,
					Data:      c.Data,
				},
			})
		}
	}
	return out
}
