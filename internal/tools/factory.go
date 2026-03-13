package tools

import (
	"context"
	"encoding/json"

	"h2-agent-runtime/internal/agent"
	"h2-agent-runtime/internal/ai"
)

// toolImpl is the internal tool implementation used by backends.
type toolImpl struct {
	name        string
	description string
	schema      json.RawMessage
	execute     func(ctx context.Context, req ToolRequest) (*ToolResponse, error)
}

// LocalToolsOptions configures the local tools factory.
type LocalToolsOptions struct {
	// MaxReadFileSize overrides the default max file size for read_file.
	MaxReadFileSize int64
}

// NewLocalTools creates the standard set of agent tools backed by local
// filesystem execution. All Tier 1 tools run in-process.
func NewLocalTools(rootDir string, _ LocalToolsOptions) []agent.AgentTool {
	backend := NewLocalBackend(rootDir)
	return buildAgentTools(backend)
}

// buildAgentTools wraps each tool implementation as an agent.AgentTool.
func buildAgentTools(backend ToolBackend) []agent.AgentTool {
	lb, ok := backend.(*LocalBackend)
	if !ok {
		return nil
	}

	tools := make([]agent.AgentTool, 0, len(lb.tools))
	for _, impl := range lb.tools {
		impl := impl // capture loop variable
		tools = append(tools, agent.AgentTool{
			Tool: ai.Tool{
				Name:        impl.name,
				Description: impl.description,
				Parameters:  impl.schema,
			},
			Label: impl.name,
			Execute: func(ctx context.Context, toolCallID string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
				req := ToolRequest{
					ToolName:   impl.name,
					ToolCallID: toolCallID,
					Params:     params,
				}
				resp, err := backend.ExecuteTool(ctx, req, func(p ToolProgress) {
					if onUpdate != nil {
						onUpdate(agent.AgentToolResult{
							Content: []ai.ContentBlock{&ai.TextContent{Text: p.Content}},
							IsError: p.IsError,
						})
					}
				})
				if err != nil {
					return agent.AgentToolResult{IsError: true, Content: []ai.ContentBlock{&ai.TextContent{Text: err.Error()}}}, err
				}
				return agent.AgentToolResult{
					Content:    resp.Content,
					SnapshotID: resp.SnapshotID,
					ExitCode:   resp.ExitCode,
				}, nil
			},
		})
	}
	return tools
}

// mustSchema parses a JSON schema string, panicking on error.
func mustSchema(s string) json.RawMessage {
	var m json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		panic("invalid schema: " + err.Error())
	}
	return m
}
