package tier1

import (
	"testing"
	"time"

	"flex-agent-runtime/internal/agent"
	"flex-agent-runtime/internal/ai"
	"flex-agent-runtime/tests/external/common"
	"flex-agent-runtime/tests/integration/harness"
	mh "flex-agent-runtime/tests/integration/mode3/harness"
	"flex-agent-runtime/tests/integration/testutil"
)

// parityScript is the shared scripted response for file ops parity tests.
var fileOpsParityScript = []testutil.ScriptEntry{
	{
		ToolCalls: []testutil.ToolCallSpec{
			{ID: "tc-1", Name: "read_file", Arguments: map[string]any{"path": "source.txt"}},
		},
	},
	{
		ToolCalls: []testutil.ToolCallSpec{
			{ID: "tc-2", Name: "write_file", Arguments: map[string]any{
				"path":    "output.txt",
				"content": "processed content",
			}},
		},
	},
	{
		Text:       "File operations completed.",
		StopReason: ai.StopReasonStop,
	},
}

// fileOpsBaseFiles are the initial files seeded in each backend before running.
var fileOpsBaseFiles = map[string]string{
	"source.txt": "original content",
}

// TestP1_FileOpsParity verifies that the same read/write/edit sequence
// produces the same final file contents on LocalEnvironment and MemorySandboxService.
func TestP1_FileOpsParity(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		localResult := harness.Run(t, harness.Scenario{
			Name:   "P1_FileOpsParity_Local",
			Script: fileOpsParityScript,
			Setup: func(t *testing.T, root string) string {
				harness.WriteFile(t, root, "source.txt", fileOpsBaseFiles["source.txt"])
				return "read source.txt and write the content to output.txt"
			},
			Assert: func(t *testing.T, result *harness.ScenarioResult) {
				testutil.AssertFiles(t, result.WorkspaceRoot, []testutil.FileExpectation{
					{Path: "output.txt", Contains: "processed content"},
				})
			},
		})
		_ = localResult
	})

	t.Run("remote", func(t *testing.T) {
		env := mh.NewRemoteEnv(t, fileOpsParityScript, fileOpsBaseFiles)
		env.PromptAndWait(t, "read source.txt and write the content to output.txt", 5*time.Second)

		mh.SnapshotAssertion{}.AssertFileContent(t, env.Service, env.SessionID, "output.txt", "processed content")
	})

	t.Run("compare_final_files", func(t *testing.T) {
		// Run both backends and compare the output file contents directly.
		localResult := harness.Run(t, harness.Scenario{
			Name:   "P1_Compare_Local",
			Script: fileOpsParityScript,
			Setup: func(t *testing.T, root string) string {
				harness.WriteFile(t, root, "source.txt", fileOpsBaseFiles["source.txt"])
				return "read source.txt and write the content to output.txt"
			},
		})

		env := mh.NewRemoteEnv(t, fileOpsParityScript, fileOpsBaseFiles)
		env.PromptAndWait(t, "read source.txt and write the content to output.txt", 5*time.Second)

		// Compare output.txt between local and remote
		localContent := ""
		data, err := testutil.SnapshotWorkspace(localResult.WorkspaceRoot)
		if err == nil {
			localContent = data.Files["output.txt"]
		}
		remoteContent, _ := env.Service.ReadSessionFile(env.SessionID, "output.txt")

		if localContent != remoteContent {
			t.Errorf("file content parity mismatch for output.txt:\n  local:  %q\n  remote: %q", localContent, remoteContent)
		}
	})
}

// TestP2_EventTypeParity verifies that the same scenario run on both
// LocalEnvironment and MemorySandboxService produces the same event type
// sequence (minus any backend-specific implementation differences).
func TestP2_EventTypeParity(t *testing.T) {
	sharedScript := []testutil.ScriptEntry{
		{
			ToolCalls: []testutil.ToolCallSpec{
				{ID: "tc-1", Name: "read_file", Arguments: map[string]any{"path": "notes.txt"}},
			},
		},
		{
			Text:       "Read notes.txt successfully.",
			StopReason: ai.StopReasonStop,
		},
	}
	baseFiles := map[string]string{"notes.txt": "some notes"}

	var localEventTypes []agent.AgentEventType
	var remoteEventTypes []agent.AgentEventType

	t.Run("local", func(t *testing.T) {
		result := harness.Run(t, harness.Scenario{
			Name:   "P2_EventParity_Local",
			Script: sharedScript,
			Setup: func(t *testing.T, root string) string {
				harness.WriteFile(t, root, "notes.txt", baseFiles["notes.txt"])
				return "read notes.txt"
			},
		})
		localEventTypes = common.EventTypeSequence(result.Events)
	})

	t.Run("remote", func(t *testing.T) {
		env := mh.NewRemoteEnv(t, sharedScript, baseFiles)
		env.PromptAndWait(t, "read notes.txt", 5*time.Second)
		remoteEventTypes = common.EventTypeSequence(env.Events())
	})

	t.Run("compare_event_types", func(t *testing.T) {
		if len(localEventTypes) == 0 {
			t.Skip("local events not collected (run subtests individually to compare)")
		}
		if len(remoteEventTypes) == 0 {
			t.Skip("remote events not collected (run subtests individually to compare)")
		}
		common.AssertEventTypeParity(t, "local vs remote", localEventTypes, remoteEventTypes)
	})
}

// TestP3_SessionStateParity verifies that the same scenario produces
// equivalent session metrics (specifically provider call counts) on both backends.
func TestP3_SessionStateParity(t *testing.T) {
	sharedScript := []testutil.ScriptEntry{
		{
			ToolCalls: []testutil.ToolCallSpec{
				{ID: "tc-1", Name: "read_file", Arguments: map[string]any{"path": "data.txt"}},
			},
		},
		{
			ToolCalls: []testutil.ToolCallSpec{
				{ID: "tc-2", Name: "write_file", Arguments: map[string]any{
					"path":    "result.txt",
					"content": "computed result",
				}},
			},
		},
		{
			Text:       "All done.",
			StopReason: ai.StopReasonStop,
		},
	}
	baseFiles := map[string]string{"data.txt": "input data"}
	prompt := "read data.txt and write a result to result.txt"

	var localCalls int
	var remoteCalls int

	t.Run("local", func(t *testing.T) {
		result := harness.Run(t, harness.Scenario{
			Name:   "P3_SessionState_Local",
			Script: sharedScript,
			Setup: func(t *testing.T, root string) string {
				harness.WriteFile(t, root, "data.txt", baseFiles["data.txt"])
				return prompt
			},
		})
		localCalls = result.ProviderCalls
	})

	t.Run("remote", func(t *testing.T) {
		env := mh.NewRemoteEnv(t, sharedScript, baseFiles)
		env.PromptAndWait(t, prompt, 5*time.Second)
		remoteCalls = env.Provider.Calls()
	})

	t.Run("compare_provider_calls", func(t *testing.T) {
		if localCalls == 0 || remoteCalls == 0 {
			t.Skip("provider call counts not collected (run subtests individually to compare)")
		}
		if localCalls != remoteCalls {
			t.Errorf("provider call count parity mismatch: local=%d remote=%d", localCalls, remoteCalls)
		}
	})
}

// TestP4_ErrorBehaviorParity verifies that the same injected provider error
// causes an equivalent error event on both LocalEnvironment and MemorySandboxService.
func TestP4_ErrorBehaviorParity(t *testing.T) {
	// Script: first turn succeeds, second turn errors out
	errorScript := []testutil.ScriptEntry{
		{
			ToolCalls: []testutil.ToolCallSpec{
				{ID: "tc-1", Name: "read_file", Arguments: map[string]any{"path": "check.txt"}},
			},
		},
		{
			Error: "provider failure: simulated backend error",
		},
	}
	baseFiles := map[string]string{"check.txt": "content to check"}
	prompt := "read check.txt and then summarize it"

	var localHasError bool
	var remoteHasError bool

	t.Run("local", func(t *testing.T) {
		result := harness.Run(t, harness.Scenario{
			Name:   "P4_ErrorParity_Local",
			Script: errorScript,
			Setup: func(t *testing.T, root string) string {
				harness.WriteFile(t, root, "check.txt", baseFiles["check.txt"])
				return prompt
			},
			Assert: func(t *testing.T, result *harness.ScenarioResult) {
				providerErrors := result.EventsOfType(agent.EventProviderError)
				localHasError = len(providerErrors) > 0
				if !localHasError {
					t.Error("expected provider_error event on local backend")
				}
			},
		})
		_ = result
	})

	t.Run("remote", func(t *testing.T) {
		env := mh.NewRemoteEnv(t, errorScript, baseFiles)
		env.PromptAndWait(t, prompt, 5*time.Second)

		events := env.Events()
		remoteErrorCount := common.CountEventType(events, agent.EventProviderError)
		remoteHasError = remoteErrorCount > 0
		if !remoteHasError {
			t.Error("expected provider_error event on remote backend")
		}
	})

	t.Run("compare_error_presence", func(t *testing.T) {
		if localHasError != remoteHasError {
			t.Errorf("error behavior parity mismatch: local has error=%v, remote has error=%v", localHasError, remoteHasError)
		}
	})
}
