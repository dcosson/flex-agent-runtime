package envtools

import (
	"context"
	"testing"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment"
	"github.com/dcosson/flex-agent-runtime/internal/tools"
)

type stubEnv struct {
	environment.ExecutionEnvironment
	executeCalled bool
}

func (s *stubEnv) ExecuteTool(ctx context.Context, req tools.ToolRequest, onProgress func(tools.ToolProgress)) (*tools.ToolResponse, error) {
	s.executeCalled = true
	return &tools.ToolResponse{}, nil
}

func TestToolsFuncReturnsTools(t *testing.T) {
	env := &stubEnv{}
	fn := ToolsFunc(env)

	result := fn()
	if len(result) == 0 {
		t.Fatal("expected non-empty tool list from ToolsFunc")
	}

	// Verify at least "read_file" and "bash" are in the catalog.
	names := make(map[string]bool)
	for _, tool := range result {
		names[tool.Name] = true
	}
	for _, expected := range []string{"read_file", "bash", "write_file", "edit_file", "grep", "glob"} {
		if !names[expected] {
			t.Errorf("expected tool %q in catalog, got names: %v", expected, names)
		}
	}
}
