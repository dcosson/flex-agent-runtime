package scenarios

import (
	"strings"
	"testing"

	"github.com/anthropics/flex-agent-runtime/internal/agent"
	"github.com/anthropics/flex-agent-runtime/internal/ai"
	"github.com/anthropics/flex-agent-runtime/tests/integration/harness"
	"github.com/anthropics/flex-agent-runtime/tests/integration/testutil"
)

// TestScenario_GitReadWorkflow validates §4.7:
// Agent uses git_status, git_log, and git_diff tools to summarize repository state.
func TestScenario_GitReadWorkflow(t *testing.T) {
	harness.Run(t, harness.Scenario{
		Name: "git-read-workflow",
		Script: []testutil.ScriptEntry{
			// Turn 1: Agent checks repo status
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-1", Name: "git_status", Arguments: map[string]any{}},
					{ID: "tc-2", Name: "git_log", Arguments: map[string]any{"n": float64(5)}},
				},
			},
			// Turn 1 continued: Agent checks diff
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-3", Name: "git_diff", Arguments: map[string]any{}},
				},
			},
			// Turn 1 final: Agent summarizes
			{
				Text:       "The repository has 2 commits. There is one modified file (readme.md) with uncommitted changes.",
				StopReason: ai.StopReasonStop,
			},
		},
		Setup: func(t *testing.T, root string) string {
			// Initialize git repo with commit history
			harness.WriteFile(t, root, "readme.md", "# Project\nInitial version\n")
			harness.InitGitRepo(t, root)
			harness.GitCommitAll(t, root, "initial commit")

			harness.WriteFile(t, root, "main.go", "package main\n")
			harness.GitCommitAll(t, root, "add main.go")

			// Make uncommitted changes
			harness.WriteFile(t, root, "readme.md", "# Project\nUpdated version\n")

			return "Summarize the current branch state and recent changes."
		},
		Assert: func(t *testing.T, result *harness.ScenarioResult) {
			// Verify all three git tools were used
			names := result.ToolCallNames()
			toolSet := make(map[string]bool)
			for _, n := range names {
				toolSet[n] = true
			}
			for _, required := range []string{"git_status", "git_log", "git_diff"} {
				if !toolSet[required] {
					t.Errorf("expected %s tool call, got tools: %v", required, names)
				}
			}

			// Verify tool results contain expected data (check events)
			for _, e := range result.Events {
				if e.Type == agent.EventToolCompleted && e.ToolName == "git_status" {
					if e.ToolResult != nil && len(e.ToolResult.Content) > 0 {
						if tc, ok := e.ToolResult.Content[0].(*ai.TextContent); ok {
							if !strings.Contains(tc.Text, "readme.md") {
								t.Errorf("git_status should show modified readme.md, got %q", tc.Text)
							}
						}
					}
				}
				if e.Type == agent.EventToolCompleted && e.ToolName == "git_log" {
					if e.ToolResult != nil && len(e.ToolResult.Content) > 0 {
						if tc, ok := e.ToolResult.Content[0].(*ai.TextContent); ok {
							if !strings.Contains(tc.Text, "initial commit") {
								t.Errorf("git_log should contain 'initial commit', got %q", tc.Text)
							}
						}
					}
				}
			}
		},
	})
}
