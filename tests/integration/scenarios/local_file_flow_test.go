package scenarios

import (
	"testing"

	"h2-agent-runtime/internal/agent"
	"h2-agent-runtime/internal/ai"
	"h2-agent-runtime/tests/integration/harness"
	"h2-agent-runtime/tests/integration/testutil"
)

// TestScenario_MultiTurnLocalFileRefactor validates §4.1:
// Agent inspects and modifies multiple files over multiple turns.
func TestScenario_MultiTurnLocalFileRefactor(t *testing.T) {
	harness.Run(t, harness.Scenario{
		Name: "multi-turn-local-file-refactor",
		Script: []testutil.ScriptEntry{
			// Turn 1: Agent reads files to understand the code
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-1", Name: "read_file", Arguments: map[string]any{"path": "main.go"}},
					{ID: "tc-2", Name: "grep", Arguments: map[string]any{"pattern": "TODO", "include": "*.go"}},
				},
			},
			// Turn 1 continued: Agent edits files based on what it read
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-3", Name: "edit_file", Arguments: map[string]any{
						"path":       "main.go",
						"old_string": "fmt.Println(\"hello\")",
						"new_string": "fmt.Println(\"hello, world\")",
					}},
					{ID: "tc-4", Name: "write_file", Arguments: map[string]any{
						"path":    "utils.go",
						"content": "package main\n\nfunc helper() string {\n\treturn \"helper\"\n}\n",
					}},
				},
			},
			// Turn 1 final: Agent produces summary
			{
				Text:       "I've updated main.go and created utils.go with a helper function.",
				StopReason: ai.StopReasonStop,
			},
		},
		Setup: func(t *testing.T, root string) string {
			harness.WriteFile(t, root, "main.go", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n// TODO: add helper\n")
			return "Inspect main.go, update the println message to say hello world, create a utils.go with a helper function, and remove the TODO."
		},
		Assert: func(t *testing.T, result *harness.ScenarioResult) {
			// Verify event sequence
			if !result.HasEventSequence(
				agent.EventTurnStarted,
				agent.EventToolStarted,
				agent.EventToolCompleted,
				agent.EventToolStarted,
				agent.EventToolCompleted,
				agent.EventAgentMessageCompleted,
				agent.EventTurnCompleted,
			) {
				t.Error("missing expected event sequence")
			}

			// Verify tool calls
			names := result.ToolCallNames()
			if len(names) < 4 {
				t.Errorf("expected at least 4 tool calls, got %d: %v", len(names), names)
			}

			// Verify file mutations
			testutil.AssertFiles(t, result.WorkspaceRoot, []testutil.FileExpectation{
				{Path: "main.go", Contains: "hello, world"},
				{Path: "utils.go", Contains: "func helper()"},
			})

			// Verify provider was called multiple times (multi-turn)
			if result.ProviderCalls < 2 {
				t.Errorf("expected multiple provider calls for multi-turn, got %d", result.ProviderCalls)
			}
		},
	})
}
