package local

import (
	"context"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment"
	"github.com/dcosson/flex-agent-runtime/internal/tools"
)

// executeLocalTool is an implementation seam so LocalEnvironment does not
// structurally depend on tools.LocalBackend as a field type.
func executeLocalTool(ctx context.Context, workDir string, req environment.ToolRequest, onProgress func(environment.ToolProgress)) (*environment.ToolResponse, error) {
	backend := tools.NewLocalBackend(workDir)
	return backend.ExecuteTool(ctx, req, onProgress)
}
