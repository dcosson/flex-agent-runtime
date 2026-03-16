package scenarios

import (
	"testing"

	"flex-agent-runtime/internal/agent"
	"flex-agent-runtime/internal/ai"
	"flex-agent-runtime/tests/integration/harness"
	"flex-agent-runtime/tests/integration/testutil"
)

// TestScenario_ErrorRecoveryWorkflow validates §4.8:
// Agent encounters a tool failure mid-workflow and recovers by retrying
// with corrected parameters.
func TestScenario_ErrorRecoveryWorkflow(t *testing.T) {
	harness.Run(t, harness.Scenario{
		Name: "error-recovery",
		Script: []testutil.ScriptEntry{
			// Turn 1: Agent tries to edit a nonexistent file
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-1", Name: "edit_file", Arguments: map[string]any{
						"path":       "nonexistent.go",
						"old_string": "old",
						"new_string": "new",
					}},
				},
			},
			// Turn 1 continued: Agent falls back to creating the file
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-2", Name: "write_file", Arguments: map[string]any{
						"path":    "recovered.go",
						"content": "package main\n\nfunc recovered() {}\n",
					}},
				},
			},
			// Turn 1 final: Agent acknowledges recovery
			{
				Text:       "The edit failed because the file didn't exist, so I created recovered.go instead.",
				StopReason: ai.StopReasonStop,
			},
		},
		Setup: func(t *testing.T, root string) string {
			return "Edit nonexistent.go to change 'old' to 'new'. If that fails, create recovered.go with a simple function."
		},
		Assert: func(t *testing.T, result *harness.ScenarioResult) {
			// Verify the edit tool was called and returned an error result
			toolEvents := result.EventsOfType(agent.EventToolCompleted)
			foundEditError := false
			for _, e := range toolEvents {
				if e.ToolName == "edit_file" {
					if e.ToolResult != nil && len(e.ToolResult.Content) > 0 {
						if tc, ok := e.ToolResult.Content[0].(*ai.TextContent); ok {
							if tc.Text != "" {
								foundEditError = true
							}
						}
					}
				}
			}
			if !foundEditError {
				t.Error("expected edit_file to return an error result for nonexistent file")
			}

			// Verify the recovery file was created
			testutil.AssertFiles(t, result.WorkspaceRoot, []testutil.FileExpectation{
				{Path: "recovered.go", Contains: "func recovered()"},
				{Path: "nonexistent.go", Absent: true},
			})

			// Verify the agent completed gracefully (reached idle)
			stateChanges := result.EventsOfType(agent.EventStateChange)
			reachedIdle := false
			for _, e := range stateChanges {
				if e.State == agent.StateIdle {
					reachedIdle = true
				}
			}
			if !reachedIdle {
				t.Error("agent should reach idle state after error recovery")
			}
		},
	})
}
