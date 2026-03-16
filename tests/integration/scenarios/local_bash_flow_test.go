package scenarios

import (
	"testing"

	"flex-agent-runtime/internal/agent"
	"flex-agent-runtime/internal/ai"
	"flex-agent-runtime/tests/integration/harness"
	"flex-agent-runtime/tests/integration/testutil"
)

// TestScenario_BashBuildAndFixLoop validates §4.2:
// Agent runs a bash command that fails, edits the file, then re-runs successfully.
func TestScenario_BashBuildAndFixLoop(t *testing.T) {
	harness.Run(t, harness.Scenario{
		Name: "bash-build-and-fix-loop",
		Script: []testutil.ScriptEntry{
			// Turn 1: Agent runs the test
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-1", Name: "bash", Arguments: map[string]any{
						"cmd": "sh test.sh",
					}},
				},
			},
			// Turn 1 continued: Agent reads and fixes the file
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-2", Name: "read_file", Arguments: map[string]any{"path": "code.txt"}},
				},
			},
			// Turn 1 continued: Agent applies the fix
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-3", Name: "edit_file", Arguments: map[string]any{
						"path":       "code.txt",
						"old_string": "broken",
						"new_string": "fixed",
					}},
				},
			},
			// Turn 1 continued: Agent re-runs the test
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-4", Name: "bash", Arguments: map[string]any{
						"cmd": "sh test.sh",
					}},
				},
			},
			// Turn 1 final: Agent confirms fix
			{
				Text:       "The test now passes after fixing the code.",
				StopReason: ai.StopReasonStop,
			},
		},
		Setup: func(t *testing.T, root string) string {
			harness.WriteFile(t, root, "code.txt", "status: broken\n")
			harness.WriteFile(t, root, "test.sh", "#!/bin/sh\ngrep -q 'fixed' code.txt && echo PASS && exit 0 || echo FAIL && exit 1\n")
			return "Run test.sh, fix any failures in code.txt, then verify the fix by re-running."
		},
		Assert: func(t *testing.T, result *harness.ScenarioResult) {
			// Verify bash was called twice
			names := result.ToolCallNames()
			bashCount := 0
			for _, n := range names {
				if n == "bash" {
					bashCount++
				}
			}
			if bashCount < 2 {
				t.Errorf("expected at least 2 bash calls (fail then pass), got %d", bashCount)
			}

			// Verify the file was fixed
			testutil.AssertFiles(t, result.WorkspaceRoot, []testutil.FileExpectation{
				{Path: "code.txt", Contains: "fixed"},
			})

			// Verify event sequence has tool calls
			if !result.HasEventSequence(
				agent.EventToolStarted,
				agent.EventToolCompleted,
				agent.EventTurnCompleted,
			) {
				t.Error("missing expected event sequence")
			}
		},
	})
}
