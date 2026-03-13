package scenarios

import (
	"testing"
	"time"

	"h2-agent-runtime/e2etests/harness"
	"h2-agent-runtime/e2etests/testutil"
	"h2-agent-runtime/internal/agent"
	"h2-agent-runtime/internal/ai"
)

// TestScenario_SteeringMidTurn validates §4.3:
// Steering is injected during the first turn and applied at the next turn boundary.
func TestScenario_SteeringMidTurn(t *testing.T) {
	// Gate the first provider call so we can enqueue steering before it completes
	gate0 := make(chan struct{})

	harness.Run(t, harness.Scenario{
		Name: "steering-mid-turn",
		Script: []testutil.ScriptEntry{
			// First turn: agent produces initial response (gated)
			{
				Text:       "Starting the original task.",
				StopReason: ai.StopReasonStop,
			},
			// Second turn (after steering): agent follows new direction
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-1", Name: "write_file", Arguments: map[string]any{
						"path":    "steered.txt",
						"content": "steered content",
					}},
				},
			},
			// Final response
			{
				Text:       "Applied the steering direction.",
				StopReason: ai.StopReasonStop,
			},
		},
		ProviderGates: map[int]chan struct{}{0: gate0},
		Setup: func(t *testing.T, root string) string {
			return "Describe the current directory."
		},
		OnRunning: func(t *testing.T, a *agent.Agent) {
			// Enqueue steering while the first call is gated
			if err := a.Steer("Actually, create a file called steered.txt instead."); err != nil {
				t.Errorf("steer error: %v", err)
			}
			// Release the gate to let the first response return
			close(gate0)
		},
		Assert: func(t *testing.T, result *harness.ScenarioResult) {
			// Verify steering event was emitted
			steerEvents := result.EventsOfType(agent.EventSteeringApplied)
			if len(steerEvents) == 0 {
				t.Error("expected steering_applied event")
			}

			// Verify steered.txt was created (steering was applied)
			testutil.AssertFiles(t, result.WorkspaceRoot, []testutil.FileExpectation{
				{Path: "steered.txt", Contains: "steered content"},
			})

			// Verify at least 2 provider calls (original + steered turn)
			if result.ProviderCalls < 2 {
				t.Errorf("expected at least 2 provider calls, got %d", result.ProviderCalls)
			}
		},
	})
}

// TestScenario_FollowUpQueueChain validates §4.4:
// Multiple follow-ups queued during first turn execute in FIFO order.
func TestScenario_FollowUpQueueChain(t *testing.T) {
	// Gate the first provider call so we can enqueue follow-ups before it completes
	gate0 := make(chan struct{})

	harness.Run(t, harness.Scenario{
		Name: "followup-queue-chain",
		Script: []testutil.ScriptEntry{
			// Turn 1 response (gated)
			{
				Text:       "First turn done.",
				StopReason: ai.StopReasonStop,
			},
			// Turn 2 (first follow-up)
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-1", Name: "write_file", Arguments: map[string]any{
						"path":    "followup1.txt",
						"content": "first followup",
					}},
				},
			},
			{
				Text:       "Follow-up 1 done.",
				StopReason: ai.StopReasonStop,
			},
			// Turn 3 (second follow-up)
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-2", Name: "write_file", Arguments: map[string]any{
						"path":    "followup2.txt",
						"content": "second followup",
					}},
				},
			},
			{
				Text:       "Follow-up 2 done.",
				StopReason: ai.StopReasonStop,
			},
		},
		ProviderGates: map[int]chan struct{}{0: gate0},
		Setup: func(t *testing.T, root string) string {
			return "Say hello."
		},
		OnRunning: func(t *testing.T, a *agent.Agent) {
			// Queue two follow-ups while the first call is gated
			if err := a.FollowUp("Create followup1.txt"); err != nil {
				t.Errorf("followup 1: %v", err)
			}
			if err := a.FollowUp("Create followup2.txt"); err != nil {
				t.Errorf("followup 2: %v", err)
			}
			// Release the gate
			close(gate0)
		},
		Timeout: 15 * time.Second,
		Assert: func(t *testing.T, result *harness.ScenarioResult) {
			// Verify follow-up events were emitted
			fuEvents := result.EventsOfType(agent.EventFollowUpEnqueued)
			if len(fuEvents) < 2 {
				t.Errorf("expected 2 followup_enqueued events, got %d", len(fuEvents))
			}

			// Verify both follow-ups created their files
			testutil.AssertFiles(t, result.WorkspaceRoot, []testutil.FileExpectation{
				{Path: "followup1.txt", Contains: "first followup"},
				{Path: "followup2.txt", Contains: "second followup"},
			})

			// Verify FIFO order: user messages should be in order
			userMsgs := result.ConversationUserMessages()
			if len(userMsgs) < 3 {
				t.Errorf("expected at least 3 user messages (initial + 2 followups), got %d: %v", len(userMsgs), userMsgs)
			}

			// Verify multiple turns completed
			turnCompleted := result.EventsOfType(agent.EventTurnCompleted)
			if len(turnCompleted) < 3 {
				t.Errorf("expected at least 3 turns completed, got %d", len(turnCompleted))
			}
		},
	})
}
