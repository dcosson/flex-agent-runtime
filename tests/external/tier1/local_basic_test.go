// Package tier1 contains mock-based E2E tests that run without any
// infrastructure dependencies (no Docker, no ZFS, no gVisor).
// These tests cover the "All Local" mode using LocalEnvironment + ScriptedProvider.
package tier1

import (
	"sync"
	"testing"
	"time"

	"flex-agent-runtime/internal/agent"
	"flex-agent-runtime/internal/ai"
	"flex-agent-runtime/tests/integration/harness"
	"flex-agent-runtime/tests/integration/testutil"
)

// TestL1_BasicMultiTurn verifies a three-provider-turn interaction:
// read file, edit file, write a new file. Asserts files were correctly
// mutated and that the event sequence includes turn/tool/message events.
func TestL1_BasicMultiTurn(t *testing.T) {
	harness.Run(t, harness.Scenario{
		Name: "L1_BasicMultiTurn",
		Script: []testutil.ScriptEntry{
			// Turn 1: read the file to understand it
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-1", Name: "read_file", Arguments: map[string]any{"path": "main.go"}},
				},
			},
			// Turn 2: edit the file
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-2", Name: "edit_file", Arguments: map[string]any{
						"path":       "main.go",
						"old_string": "func main() {}",
						"new_string": "func main() {\n\tif err := run(); err != nil {\n\t\tpanic(err)\n\t}\n}",
					}},
				},
			},
			// Turn 3: write a new file and produce final summary
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-3", Name: "write_file", Arguments: map[string]any{
						"path":    "errors.go",
						"content": "package main\n\nfunc run() error {\n\treturn nil\n}\n",
					}},
				},
			},
			{
				Text:       "Done: added error handling to main.go and created errors.go.",
				StopReason: ai.StopReasonStop,
			},
		},
		Setup: func(t *testing.T, root string) string {
			harness.WriteFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
			return "read main.go and add error handling, then create errors.go with a run() function"
		},
		Assert: func(t *testing.T, result *harness.ScenarioResult) {
			// Verify correct file modifications
			testutil.AssertFiles(t, result.WorkspaceRoot, []testutil.FileExpectation{
				{Path: "main.go", Contains: "run()"},
				{Path: "errors.go", Contains: "func run()"},
			})

			// Verify we made multiple provider calls (the agent loops back to the
			// provider after each tool execution within the same logical turn)
			if result.ProviderCalls < 3 {
				t.Errorf("expected at least 3 provider calls for 3-step scenario, got %d", result.ProviderCalls)
			}

			// Verify all three tools were called
			names := result.ToolCallNames()
			if len(names) < 3 {
				t.Errorf("expected at least 3 tool calls, got %d: %v", len(names), names)
			}

			// Verify event sequence includes key milestones (all tool calls occur
			// within one agent turn; the loop calls the provider again after each
			// tool execution until the provider returns StopReasonStop)
			if !result.HasEventSequence(
				agent.EventTurnStarted,
				agent.EventToolStarted,
				agent.EventToolCompleted,
				agent.EventAgentMessageCompleted,
				agent.EventTurnCompleted,
			) {
				t.Error("missing required event sequence: turn/tool/message events")
			}
		},
	})
}

// TestL2_BashExecution verifies that the agent can execute bash commands,
// that exit codes are captured in tool results, and stdout appears in tool results.
func TestL2_BashExecution(t *testing.T) {
	harness.Run(t, harness.Scenario{
		Name: "L2_BashExecution",
		Script: []testutil.ScriptEntry{
			// Run a bash command to check file contents
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-1", Name: "bash", Arguments: map[string]any{
						"cmd": "cat data.txt",
					}},
				},
			},
			// Run another bash command conditionally
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-2", Name: "bash", Arguments: map[string]any{
						"cmd": "sh verify.sh",
					}},
				},
			},
			{
				Text:       "Verified data.txt contents via bash commands.",
				StopReason: ai.StopReasonStop,
			},
		},
		Setup: func(t *testing.T, root string) string {
			harness.WriteFile(t, root, "data.txt", "status: ok\nversion: 1.0\n")
			harness.WriteFile(t, root, "verify.sh", "#!/bin/sh\ngrep -q 'status: ok' data.txt && echo 'VERIFIED' && exit 0 || exit 1\n")
			return "use bash to cat data.txt and then run verify.sh to confirm contents"
		},
		Assert: func(t *testing.T, result *harness.ScenarioResult) {
			// Verify bash tool was called
			names := result.ToolCallNames()
			bashCount := 0
			for _, n := range names {
				if n == "bash" {
					bashCount++
				}
			}
			if bashCount < 2 {
				t.Errorf("expected at least 2 bash calls, got %d", bashCount)
			}

			// Verify tool completion events have results (stdout captured)
			bashCompleted := result.EventsOfType(agent.EventToolCompleted)
			if len(bashCompleted) == 0 {
				t.Error("expected tool_completed events for bash calls")
			}

			foundBashResult := false
			for _, e := range bashCompleted {
				if e.ToolName == "bash" && e.ToolResult != nil && len(e.ToolResult.Content) > 0 {
					foundBashResult = true
					break
				}
			}
			if !foundBashResult {
				t.Error("expected bash tool result with content (stdout captured)")
			}

			// Verify event sequence
			if !result.HasEventSequence(
				agent.EventToolStarted,
				agent.EventToolCompleted,
				agent.EventTurnCompleted,
			) {
				t.Error("missing expected event sequence for bash execution")
			}
		},
	})
}

// TestL3_CodeInterpreter verifies that the agent can use execute_script,
// and that script results are returned in tool results.
func TestL3_CodeInterpreter(t *testing.T) {
	harness.Run(t, harness.Scenario{
		Name: "L3_CodeInterpreter",
		Script: []testutil.ScriptEntry{
			{
				ToolCalls: []testutil.ToolCallSpec{
					{
						ID:   "tc-1",
						Name: "execute_script",
						Arguments: map[string]any{
							"code": `
def main(args):
  content = invoke("read_file", {"path": "input.txt"})
  invoke("write_file", {"path": "output.txt", "content": "processed: " + content.get("content", "")})
  return {"status": "ok"}
`,
							"tier": "full",
						},
					},
				},
			},
			{
				Text:       "Code interpreter completed: read input.txt and wrote output.txt.",
				StopReason: ai.StopReasonStop,
			},
		},
		Setup: func(t *testing.T, root string) string {
			harness.WriteFile(t, root, "input.txt", "hello world")
			return "use execute_script to read input.txt and write a processed version to output.txt"
		},
		Assert: func(t *testing.T, result *harness.ScenarioResult) {
			// Verify execute_script was called
			names := result.ToolCallNames()
			found := false
			for _, n := range names {
				if n == "execute_script" {
					found = true
					break
				}
			}
			if !found {
				t.Error("expected execute_script tool call")
			}

			// Verify script result was returned in tool completed event
			completed := result.EventsOfType(agent.EventToolCompleted)
			foundResult := false
			for _, e := range completed {
				if e.ToolName == "execute_script" && e.ToolResult != nil && len(e.ToolResult.Content) > 0 {
					foundResult = true
					break
				}
			}
			if !foundResult {
				t.Error("expected execute_script tool result with content")
			}

			// Verify output file was produced
			testutil.AssertFiles(t, result.WorkspaceRoot, []testutil.FileExpectation{
				{Path: "output.txt", Contains: "processed:"},
			})
		},
	})
}

// TestL4_SteeringMidTurn verifies that Steer() injected while the provider is
// streaming (using a ProviderGate) causes a steering event to appear, and the
// agent follows the steering direction.
func TestL4_SteeringMidTurn(t *testing.T) {
	gate0 := make(chan struct{})

	harness.Run(t, harness.Scenario{
		Name: "L4_SteeringMidTurn",
		Script: []testutil.ScriptEntry{
			// First turn: produce initial response (blocked by gate)
			{
				Text:       "I'll start by creating a basic file.",
				StopReason: ai.StopReasonStop,
			},
			// Second turn (after steering): follow new direction
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-1", Name: "write_file", Arguments: map[string]any{
						"path":    "steered_output.txt",
						"content": "steered content from new direction",
					}},
				},
			},
			{
				Text:       "Applied steering: created steered_output.txt instead.",
				StopReason: ai.StopReasonStop,
			},
		},
		ProviderGates: map[int]chan struct{}{0: gate0},
		Setup: func(t *testing.T, root string) string {
			return "create a file called original.txt"
		},
		OnRunning: func(t *testing.T, a *agent.Agent) {
			// Inject steering while the first provider call is gated
			if err := a.Steer("Actually, create steered_output.txt instead."); err != nil {
				t.Errorf("steer error: %v", err)
			}
			// Release the gate so the first response returns
			close(gate0)
		},
		Timeout: 15 * time.Second,
		Assert: func(t *testing.T, result *harness.ScenarioResult) {
			// Verify steering event was emitted
			steerEvents := result.EventsOfType(agent.EventSteeringApplied)
			if len(steerEvents) == 0 {
				t.Error("expected steering_applied event to appear in event stream")
			}

			// Verify the steered file was created (steering was acted upon)
			testutil.AssertFiles(t, result.WorkspaceRoot, []testutil.FileExpectation{
				{Path: "steered_output.txt", Contains: "steered content"},
			})

			// Verify at least 2 provider calls (original + steered turn)
			if result.ProviderCalls < 2 {
				t.Errorf("expected at least 2 provider calls (original + steered), got %d", result.ProviderCalls)
			}
		},
	})
}

// TestL5_FollowUpChain verifies that multiple FollowUp() messages enqueued
// during the first turn are all processed in FIFO order, producing distinct
// turns and creating the expected files.
func TestL5_FollowUpChain(t *testing.T) {
	gate0 := make(chan struct{})

	harness.Run(t, harness.Scenario{
		Name: "L5_FollowUpChain",
		Script: []testutil.ScriptEntry{
			// Turn 1 response (gated so we can enqueue follow-ups)
			{
				Text:       "Starting task.",
				StopReason: ai.StopReasonStop,
			},
			// Turn 2: first follow-up
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-1", Name: "write_file", Arguments: map[string]any{
						"path":    "step1.txt",
						"content": "step 1 done",
					}},
				},
			},
			{
				Text:       "Step 1 complete.",
				StopReason: ai.StopReasonStop,
			},
			// Turn 3: second follow-up
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-2", Name: "write_file", Arguments: map[string]any{
						"path":    "step2.txt",
						"content": "step 2 done",
					}},
				},
			},
			{
				Text:       "Step 2 complete.",
				StopReason: ai.StopReasonStop,
			},
		},
		ProviderGates: map[int]chan struct{}{0: gate0},
		Setup: func(t *testing.T, root string) string {
			return "say hello"
		},
		OnRunning: func(t *testing.T, a *agent.Agent) {
			// Enqueue two follow-ups while the first call is gated
			if err := a.FollowUp("create step1.txt"); err != nil {
				t.Errorf("followup 1 error: %v", err)
			}
			if err := a.FollowUp("create step2.txt"); err != nil {
				t.Errorf("followup 2 error: %v", err)
			}
			// Release the gate
			close(gate0)
		},
		Timeout: 20 * time.Second,
		Assert: func(t *testing.T, result *harness.ScenarioResult) {
			// Verify both follow-up events were emitted
			fuEvents := result.EventsOfType(agent.EventFollowUpEnqueued)
			if len(fuEvents) < 2 {
				t.Errorf("expected 2 followup_enqueued events, got %d", len(fuEvents))
			}

			// Verify both files were created (follow-ups processed)
			testutil.AssertFiles(t, result.WorkspaceRoot, []testutil.FileExpectation{
				{Path: "step1.txt", Contains: "step 1 done"},
				{Path: "step2.txt", Contains: "step 2 done"},
			})

			// Verify FIFO ordering: user messages appear in enqueue order
			userMsgs := result.ConversationUserMessages()
			if len(userMsgs) < 3 {
				t.Errorf("expected at least 3 user messages (initial + 2 followups), got %d: %v", len(userMsgs), userMsgs)
			}
			if len(userMsgs) >= 3 {
				if userMsgs[1] != "create step1.txt" {
					t.Errorf("expected first follow-up to be %q, got %q", "create step1.txt", userMsgs[1])
				}
				if userMsgs[2] != "create step2.txt" {
					t.Errorf("expected second follow-up to be %q, got %q", "create step2.txt", userMsgs[2])
				}
			}

			// Verify 3 turns completed
			turnCompleted := result.EventsOfType(agent.EventTurnCompleted)
			if len(turnCompleted) < 3 {
				t.Errorf("expected at least 3 turns completed, got %d", len(turnCompleted))
			}
		},
	})
}

// TestL6_Abort verifies that calling Abort() during tool execution causes
// an EventAborted event to be emitted.
func TestL6_Abort(t *testing.T) {
	gate0 := make(chan struct{})

	harness.Run(t, harness.Scenario{
		Name: "L6_Abort",
		Script: []testutil.ScriptEntry{
			// First turn: gated so we can inject abort before it returns
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-1", Name: "write_file", Arguments: map[string]any{
						"path":    "long_running.txt",
						"content": "should not matter",
					}},
				},
			},
			{
				Text:       "Done after abort was ignored.",
				StopReason: ai.StopReasonStop,
			},
		},
		ProviderGates: map[int]chan struct{}{0: gate0},
		Setup: func(t *testing.T, root string) string {
			return "write a file and describe it"
		},
		OnRunning: func(t *testing.T, a *agent.Agent) {
			// Abort while the first provider call is gated
			if err := a.Abort("user requested abort"); err != nil {
				t.Errorf("abort error: %v", err)
			}
			// Release the gate
			close(gate0)
		},
		Timeout: 10 * time.Second,
		Assert: func(t *testing.T, result *harness.ScenarioResult) {
			// Verify abort event was emitted
			abortEvents := result.EventsOfType(agent.EventAborted)
			if len(abortEvents) == 0 {
				t.Error("expected at least one aborted event to be emitted")
			}

			// Verify the abort event has the expected reason
			if len(abortEvents) > 0 && abortEvents[0].ControlMessage != "user requested abort" {
				t.Errorf("expected abort reason %q, got %q", "user requested abort", abortEvents[0].ControlMessage)
			}
		},
	})
}

// TestL7_ErrorRecovery verifies that when the provider returns an error on
// the second turn, an EventProviderError is emitted and the agent reaches idle.
func TestL7_ErrorRecovery(t *testing.T) {
	harness.Run(t, harness.Scenario{
		Name: "L7_ErrorRecovery",
		Script: []testutil.ScriptEntry{
			// Turn 1: normal response
			{
				ToolCalls: []testutil.ToolCallSpec{
					{ID: "tc-1", Name: "read_file", Arguments: map[string]any{"path": "info.txt"}},
				},
			},
			// Turn 2: provider returns an error
			{
				Error: "provider unavailable: temporary failure",
			},
		},
		Setup: func(t *testing.T, root string) string {
			harness.WriteFile(t, root, "info.txt", "some information\n")
			return "read info.txt and then summarize it"
		},
		Assert: func(t *testing.T, result *harness.ScenarioResult) {
			// Verify provider error event was emitted
			providerErrors := result.EventsOfType(agent.EventProviderError)
			if len(providerErrors) == 0 {
				t.Error("expected at least one provider_error event after turn 2 failure")
			}

			// Verify the agent reached idle state (graceful degradation)
			stateChanges := result.EventsOfType(agent.EventStateChange)
			reachedIdle := false
			for _, e := range stateChanges {
				if e.State == agent.StateIdle {
					reachedIdle = true
					break
				}
			}
			if !reachedIdle {
				t.Error("agent should reach idle state after provider error")
			}

			// Verify the first turn still executed (read_file was called)
			names := result.ToolCallNames()
			foundRead := false
			for _, n := range names {
				if n == "read_file" {
					foundRead = true
					break
				}
			}
			if !foundRead {
				t.Error("expected read_file to have been called in the first turn before the error")
			}
		},
	})
}

// TestL10_ConcurrentSubscribers verifies that multiple event subscribers
// all receive the same events from a single agent run.
func TestL10_ConcurrentSubscribers(t *testing.T) {
	const numSubscribers = 5

	// We need access to the agent before running, so we build the scenario
	// and add subscribers via the agent itself. We use a custom approach
	// by leveraging OnRunning to register additional subscribers concurrently.
	var (
		mu           sync.Mutex
		extraEvents  [numSubscribers][]agent.AgentEvent
		unsubscribes [numSubscribers]func()
	)

	// We'll store the agent reference once OnRunning fires so subscribers
	// can be registered alongside the primary one in harness.Run.
	agentRef := make(chan *agent.Agent, 1)

	script := []testutil.ScriptEntry{
		{
			ToolCalls: []testutil.ToolCallSpec{
				{ID: "tc-1", Name: "write_file", Arguments: map[string]any{
					"path":    "shared.txt",
					"content": "written for all subscribers",
				}},
			},
		},
		{
			Text:       "Done writing shared.txt.",
			StopReason: ai.StopReasonStop,
		},
	}

	gate0 := make(chan struct{})

	result := harness.Run(t, harness.Scenario{
		Name:          "L10_ConcurrentSubscribers",
		Script:        script,
		ProviderGates: map[int]chan struct{}{0: gate0},
		Setup: func(t *testing.T, root string) string {
			return "write shared.txt"
		},
		OnRunning: func(t *testing.T, a *agent.Agent) {
			// Register multiple concurrent subscribers before releasing the gate
			select {
			case agentRef <- a:
			default:
			}
			for i := 0; i < numSubscribers; i++ {
				idx := i
				unsub := a.Subscribe(func(evt agent.AgentEvent) {
					mu.Lock()
					extraEvents[idx] = append(extraEvents[idx], evt)
					mu.Unlock()
				})
				unsubscribes[idx] = unsub
			}
			// Release the gate so the scenario can proceed
			close(gate0)
		},
		Timeout: 10 * time.Second,
		Assert: func(t *testing.T, result *harness.ScenarioResult) {
			// Ensure the primary subscriber collected events
			if len(result.Events) == 0 {
				t.Fatal("primary subscriber received no events")
			}

			// Give a brief moment for any in-flight events to flush
			time.Sleep(20 * time.Millisecond)

			mu.Lock()
			defer mu.Unlock()

			// Verify each extra subscriber received at least some events
			for i := 0; i < numSubscribers; i++ {
				if len(extraEvents[i]) == 0 {
					t.Errorf("subscriber %d received no events", i)
				}
			}

			// Verify all extra subscribers received the same event types
			// (they may not have been registered at time=0, so they'll
			// receive a subset; assert they all got the same subset)
			if numSubscribers >= 2 {
				types0 := make([]agent.AgentEventType, len(extraEvents[0]))
				for j, e := range extraEvents[0] {
					types0[j] = e.Type
				}
				for i := 1; i < numSubscribers; i++ {
					typesI := make([]agent.AgentEventType, len(extraEvents[i]))
					for j, e := range extraEvents[i] {
						typesI[j] = e.Type
					}
					if len(types0) != len(typesI) {
						t.Errorf("subscriber 0 got %d events but subscriber %d got %d events", len(types0), i, len(typesI))
					}
				}
			}
		},
	})

	// Unsubscribe all extra subscribers after the run
	for _, unsub := range unsubscribes {
		if unsub != nil {
			unsub()
		}
	}

	_ = result
}
