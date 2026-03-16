package google

import (
	"encoding/base64"

	"flex-agent-runtime/internal/ai"
)

// requestParams holds parameters for building the wire request that don't
// fit naturally into StreamOptions.
type requestParams struct {
	thinkingBudget *int
	thinkingLevel  string
}

func buildRequest(model ai.Model, llmCtx ai.Context, opts ai.StreamOptions, params requestParams) *generateContentRequest {
	req := &generateContentRequest{
		Contents:         convertContents(llmCtx.Messages, model),
		GenerationConfig: buildGenerationConfig(opts, params),
		SafetySettings:   defaultSafetySettings(),
	}

	// System prompt as top-level systemInstruction
	if llmCtx.SystemPrompt != "" {
		req.SystemInstruction = &contentObj{
			Parts: []part{{Text: llmCtx.SystemPrompt}},
		}
	}

	// Tools
	if len(llmCtx.Tools) > 0 {
		req.Tools = []toolObj{convertTools(llmCtx.Tools)}
		req.ToolConfig = &toolConfig{
			FunctionCallingConfig: &functionCallingConfig{Mode: "AUTO"},
		}
	}

	return req
}

func buildGenerationConfig(opts ai.StreamOptions, params requestParams) *generationConfig {
	cfg := &generationConfig{}

	if opts.MaxTokens != nil {
		cfg.MaxOutputTokens = opts.MaxTokens
	}
	if opts.Temperature != nil {
		cfg.Temperature = opts.Temperature
	}
	if opts.TopP != nil {
		cfg.TopP = opts.TopP
	}
	if opts.TopK != nil {
		cfg.TopK = opts.TopK
	}

	// Thinking configuration
	if params.thinkingBudget != nil && *params.thinkingBudget > 0 {
		include := true
		cfg.ThinkingConfig = &thinkingConfig{
			IncludeThoughts: &include,
			ThinkingBudget:  params.thinkingBudget,
			ThinkingLevel:   params.thinkingLevel,
		}
	}

	return cfg
}

func convertContents(messages []ai.Message, model ai.Model) []contentObj {
	var out []contentObj
	for _, msg := range messages {
		switch m := msg.(type) {
		case *ai.UserMessage:
			out = append(out, convertUserContent(m))
		case *ai.AssistantMessage:
			out = append(out, convertModelContent(m))
		case *ai.ToolResultMessage:
			out = append(out, convertToolResultContent(m))
		}
	}
	return out
}

func convertUserContent(m *ai.UserMessage) contentObj {
	var parts []part
	for _, block := range m.Content {
		switch b := block.(type) {
		case *ai.TextContent:
			parts = append(parts, part{Text: b.Text})
		case *ai.ImageContent:
			parts = append(parts, part{
				InlineData: &inlineData{MimeType: b.MimeType, Data: b.Data},
			})
		}
	}
	return contentObj{Role: "user", Parts: parts}
}

func convertModelContent(m *ai.AssistantMessage) contentObj {
	var parts []part
	for _, block := range m.Content {
		switch b := block.(type) {
		case *ai.TextContent:
			parts = append(parts, part{Text: b.Text})
		case *ai.ThinkingContent:
			if b.Thinking != "" {
				t := true
				parts = append(parts, part{Text: b.Thinking, Thought: &t})
			}
			if b.ThinkingSignature != "" {
				sigBytes, err := base64.StdEncoding.DecodeString(b.ThinkingSignature)
				if err == nil {
					parts = append(parts, part{ThoughtSignature: sigBytes})
				}
			}
		case *ai.ToolCall:
			parts = append(parts, part{
				FunctionCall: &functionCallWire{
					Name: b.Name,
					Args: b.Arguments,
					ID:   b.ID,
				},
			})
			if b.ThoughtSignature != "" {
				sigBytes, err := base64.StdEncoding.DecodeString(b.ThoughtSignature)
				if err == nil {
					parts = append(parts, part{ThoughtSignature: sigBytes})
				}
			}
		}
	}
	return contentObj{Role: "model", Parts: parts}
}

func convertToolResultContent(m *ai.ToolResultMessage) contentObj {
	responseObj := map[string]any{}
	for _, block := range m.Content {
		if tb, ok := block.(*ai.TextContent); ok {
			responseObj["result"] = tb.Text
		}
	}
	if m.IsError {
		responseObj["error"] = true
	}

	return contentObj{
		Role: "user",
		Parts: []part{{
			FunctionResponse: &functionResponse{
				Name:     m.ToolName,
				Response: responseObj,
				ID:       m.ToolCallID,
			},
		}},
	}
}

func convertTools(tools []ai.Tool) toolObj {
	decls := make([]functionDeclaration, len(tools))
	for i, t := range tools {
		decls[i] = functionDeclaration{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		}
	}
	return toolObj{FunctionDeclarations: decls}
}
