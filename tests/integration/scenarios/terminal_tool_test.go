package scenarios

import (
	"context"
	"testing"

	"h2-agent-runtime/internal/agent"
	"h2-agent-runtime/internal/ai"
	"h2-agent-runtime/tests/integration/harness"
	"h2-agent-runtime/tests/integration/testutil"
)

// TestScenario_TerminalToolCompletion validates §4.6:
// A terminal tool produces a structured final artifact and the loop ends
// without any extra LLM continuation.
func TestScenario_TerminalToolCompletion(t *testing.T) {
	finalizeTool := agent.AgentTool{
		Tool: ai.Tool{
			Name:        "finalize",
			Description: "Produce a final structured artifact",
			Parameters:  []byte(`{"type":"object","properties":{"result":{"type":"string"}},"required":["result"]}`),
		},
		Label:    "finalize",
		Terminal: true,
		Execute: func(ctx context.Context, toolCallID string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
			result, _ := params["result"].(string)
			return agent.AgentToolResult{
				Content: []ai.ContentBlock{&ai.TextContent{Text: "Final: " + result}},
			}, nil
		},
	}

	harness.Run(t, harness.Scenario{
		Name: "terminal-tool-completion",
		Script: []testutil.ScriptEntry{
			// Only response: agent calls the terminal tool
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-1", Name: "finalize", Arguments: map[string]any{
						"result": "structured artifact",
					}},
				},
			},
			// This response should NOT be consumed (terminal short-circuit)
			{
				Text:       "This should never appear.",
				StopReason: ai.StopReasonStop,
			},
		},
		ExtraTools: []agent.AgentTool{finalizeTool},
		Setup: func(t *testing.T, root string) string {
			return "Produce the final artifact."
		},
		Assert: func(t *testing.T, result *harness.ScenarioResult) {
			// Verify terminal tool event
			termEvents := result.EventsOfType(agent.EventTerminalToolCompleted)
			if len(termEvents) != 1 {
				t.Errorf("expected 1 terminal_tool_completed event, got %d", len(termEvents))
			}
			if len(termEvents) > 0 && termEvents[0].ToolName != "finalize" {
				t.Errorf("terminal tool name = %q, want finalize", termEvents[0].ToolName)
			}

			// Verify only one provider call (no continuation after terminal tool)
			if result.ProviderCalls != 1 {
				t.Errorf("expected 1 provider call (no continuation after terminal), got %d", result.ProviderCalls)
			}

			// Verify event sequence ends correctly
			if !result.HasEventSequence(
				agent.EventTurnStarted,
				agent.EventToolStarted,
				agent.EventToolCompleted,
				agent.EventTerminalToolCompleted,
				agent.EventTurnCompleted,
			) {
				t.Error("missing expected terminal tool event sequence")
			}
		},
	})
}
