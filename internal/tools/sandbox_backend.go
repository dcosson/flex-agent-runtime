package tools

import (
	"context"
	"fmt"

	"h2-agent-runtime/internal/agent"
	"h2-agent-runtime/internal/ai"
	"h2-agent-runtime/internal/tools/codeinterp"
)

// SandboxToolClient is the RPC interface that SandboxBackend delegates to.
// The actual implementation will be provided by the RPC layer (plan 13).
type SandboxToolClient interface {
	// ExecuteTool sends a tool request to the sandbox host and returns the response.
	// For Tier 2 tools, progress is streamed via the onProgress callback.
	ExecuteTool(ctx context.Context, sessionID string, req ToolRequest, onProgress func(ToolProgress)) (*ToolResponse, error)
}

// SandboxBackend forwards tool execution to a sandbox host via RPC.
type SandboxBackend struct {
	client    SandboxToolClient
	sessionID string
}

// NewSandboxBackend creates a backend that forwards tool calls to a sandbox host.
func NewSandboxBackend(client SandboxToolClient, sessionID string) *SandboxBackend {
	return &SandboxBackend{
		client:    client,
		sessionID: sessionID,
	}
}

func (b *SandboxBackend) ExecuteTool(ctx context.Context, req ToolRequest, onProgress func(ToolProgress)) (*ToolResponse, error) {
	if b.client == nil {
		return nil, fmt.Errorf("sandbox client not configured")
	}
	req.SessionID = b.sessionID
	return b.client.ExecuteTool(ctx, b.sessionID, req, onProgress)
}

// NewSandboxTools creates the standard set of agent tools backed by sandbox
// execution. Tool schemas and names are identical to NewLocalTools.
func NewSandboxTools(client SandboxToolClient, sessionID string) []agent.AgentTool {
	backend := NewSandboxBackend(client, sessionID)
	tools := buildSandboxAgentTools(backend)
	tools = append(tools, codeinterp.NewTool(ai.Model{}, tools))
	return tools
}

// buildSandboxAgentTools creates AgentTools that delegate to the SandboxBackend.
// Tool schemas are defined once and shared with local tools.
func buildSandboxAgentTools(backend *SandboxBackend) []agent.AgentTool {
	// Use the same tool definitions as LocalBackend for schema consistency
	dummyRoot := "/" // schemas don't depend on root dir
	impls := []toolImpl{
		readFileTool(dummyRoot),
		writeFileTool(dummyRoot),
		editFileTool(dummyRoot),
		grepTool(dummyRoot),
		globTool(dummyRoot),
		bashTool(dummyRoot),
		gitStatusTool(dummyRoot),
		gitDiffTool(dummyRoot),
		gitLogTool(dummyRoot),
		gitShowTool(dummyRoot),
		gitAddTool(dummyRoot),
		gitCommitTool(dummyRoot),
	}

	tools := make([]agent.AgentTool, 0, len(impls))
	for _, impl := range impls {
		impl := impl
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
