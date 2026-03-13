package tools

import (
	"context"
	"fmt"
)

// LocalBackend executes tools directly in-process under a workspace root.
type LocalBackend struct {
	rootDir string
	tools   map[string]toolImpl
}

// NewLocalBackend creates a backend that runs all built-in tools in-process.
func NewLocalBackend(rootDir string) *LocalBackend {
	impls := []toolImpl{
		readFileTool(rootDir),
		writeFileTool(rootDir),
		editFileTool(rootDir),
		grepTool(rootDir),
		globTool(rootDir),
		bashTool(rootDir),
		gitStatusTool(rootDir),
		gitDiffTool(rootDir),
		gitLogTool(rootDir),
		gitShowTool(rootDir),
		gitAddTool(rootDir),
		gitCommitTool(rootDir),
	}
	toolMap := make(map[string]toolImpl, len(impls))
	for _, t := range impls {
		toolMap[t.name] = t
	}
	return &LocalBackend{
		rootDir: rootDir,
		tools:   toolMap,
	}
}

func (b *LocalBackend) ExecuteTool(ctx context.Context, req ToolRequest, _ func(ToolProgress)) (*ToolResponse, error) {
	impl, ok := b.tools[req.ToolName]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", req.ToolName)
	}
	return impl.execute(ctx, req)
}
