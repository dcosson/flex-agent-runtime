package tools

import (
	"context"
	"encoding/json"

	"github.com/anthropics/flex-agent-runtime/internal/agent"
	"github.com/anthropics/flex-agent-runtime/internal/ai"
	"github.com/anthropics/flex-agent-runtime/internal/tools/codeinterp"
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
	return NewEnvironmentTools(backend.ExecuteTool)
}

// NewEnvironmentTools creates agent tools that delegate execution to the
// provided execute function. This is the unified factory for all environments:
// LocalEnvironment, NativeSandboxEnvironment, etc.
//
// Usage:
//
//	// Local environment
//	env := local.NewLocalEnvironment(rootDir, logger)
//	tools := tools.NewEnvironmentTools(env.ExecuteTool)
//
//	// Native sandbox environment
//	env := native.NewNativeSandboxEnvironment(svc, logger)
//	tools := tools.NewEnvironmentTools(env.ExecuteTool)
func NewEnvironmentTools(executeFn func(ctx context.Context, req ToolRequest, onProgress func(ToolProgress)) (*ToolResponse, error)) []agent.AgentTool {
	schemas := toolSchemas()
	result := make([]agent.AgentTool, 0, len(schemas)+1)
	for _, impl := range schemas {
		impl := impl
		result = append(result, agent.AgentTool{
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
				resp, err := executeFn(ctx, req, func(p ToolProgress) {
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
	result = append(result, codeinterp.NewTool(ai.Model{}, result))
	return result
}

// toolSchemas returns tool metadata (name, description, JSON schema) for building
// AgentTool definitions. It instantiates toolImpl structs via the tool constructors
// with a dummy root — only the name, description, and schema fields are used; the
// execute closures are never called and exist only because the constructors produce
// complete toolImpl values. This is intentional: extracting metadata-only structs
// would require duplicating every tool's name/description/schema outside its
// constructor, which is more error-prone than discarding unused closures.
func toolSchemas() []toolImpl {
	dummyRoot := "/"
	return []toolImpl{
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
}

// mustSchema parses a JSON schema string, panicking on error.
func mustSchema(s string) json.RawMessage {
	var m json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		panic("invalid schema: " + err.Error())
	}
	return m
}
