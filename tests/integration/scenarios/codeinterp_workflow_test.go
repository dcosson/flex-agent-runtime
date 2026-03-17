package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/agent"
	"github.com/anthropics/flex-agent-runtime/internal/ai"
	"github.com/anthropics/flex-agent-runtime/internal/tools"
	"github.com/anthropics/flex-agent-runtime/internal/tools/codeinterp"
	"github.com/anthropics/flex-agent-runtime/tests/integration/harness"
	"github.com/anthropics/flex-agent-runtime/tests/integration/testutil"
)

const codeInterpWorkflowScript = `
def main(args):
  tools = discover("file")
  spec = describe("read_file")
  rf = invoke("read_file", {"path":"notes.txt"})
  store_write("memo", rf["content"])
  memo = store_read("memo")
  summary = llm_call("summarize " + memo, context="call_context=code_interpreter_subcall script_exec_id=exec-001", max_tokens=12)
  invoke("write_file", {"path":"report.txt", "content": summary["response"]})
  return {"tool": spec["name"], "count": len(tools), "summary": summary["response"]}
`

func codeInterpScenario() harness.Scenario {
	return harness.Scenario{
		Name: "code-interpreter-workflow",
		Script: []testutil.ScriptEntry{
			{
				ToolCalls: []testutil.ToolCallSpec{
					{
						ID:   "tc-1",
						Name: "execute_script",
						Arguments: map[string]any{
							"code": codeInterpWorkflowScript,
							"tier": "full",
						},
					},
				},
			},
			{
				Text:       "nested-summary",
				StopReason: ai.StopReasonStop,
			},
			{
				Text:       "Workflow complete with code interpreter.",
				StopReason: ai.StopReasonStop,
			},
		},
		Setup: func(t *testing.T, root string) string {
			harness.WriteFile(t, root, "notes.txt", "alpha beta gamma")
			return "Use execute_script to read notes.txt, summarize, and write report.txt."
		},
		Timeout: 15 * time.Second,
	}
}

func findExecuteScriptResult(t *testing.T, result *harness.ScenarioResult) codeinterp.ExecuteResult {
	t.Helper()
	for _, evt := range result.Events {
		if evt.Type != agent.EventToolCompleted || evt.ToolName != "execute_script" || evt.ToolResult == nil {
			continue
		}
		for _, cb := range evt.ToolResult.Content {
			tc, ok := cb.(*ai.TextContent)
			if !ok {
				continue
			}
			var parsed codeinterp.ExecuteResult
			if err := json.Unmarshal([]byte(tc.Text), &parsed); err == nil {
				return parsed
			}
		}
	}
	t.Fatalf("execute_script result payload not found")
	return codeinterp.ExecuteResult{}
}

func assertCodeInterpMilestones(t *testing.T, result *harness.ScenarioResult) {
	t.Helper()
	if !result.HasEventSequence(
		agent.EventTurnStarted,
		agent.EventToolStarted,
		agent.EventToolCompleted,
		agent.EventAgentMessageCompleted,
		agent.EventTurnCompleted,
		agent.EventStateChange,
	) {
		t.Fatalf("missing required event milestones")
	}
}

// 4.5 core scenario
func TestScenario_CodeInterpreterWorkflow(t *testing.T) {
	scn := codeInterpScenario()
	scn.Assert = func(t *testing.T, result *harness.ScenarioResult) {
		assertCodeInterpMilestones(t, result)
		testutil.AssertFiles(t, result.WorkspaceRoot, []testutil.FileExpectation{
			{Path: "report.txt", Contains: "nested-summary"},
		})
		es := findExecuteScriptResult(t, result)
		if es.Stats.LLMCalls < 1 {
			t.Fatalf("expected llm calls in execute_script stats")
		}
		if es.Stats.StoreOps < 2 {
			t.Fatalf("expected datastore ops in execute_script stats")
		}
		if len(result.ProviderLog) < 3 {
			t.Fatalf("expected nested provider call routing, got %d calls", len(result.ProviderLog))
		}
		if len(result.ProviderLog[0].Tools) == 0 {
			t.Fatalf("top-level provider call should include tools")
		}
		if len(result.ProviderLog[1].Tools) != 0 {
			t.Fatalf("nested codeinterp subcall should not carry top-level tools")
		}
		if !strings.Contains(result.ProviderLog[1].SystemPrompt, "call_context=code_interpreter_subcall") {
			t.Fatalf("nested subcall metadata missing from system prompt: %q", result.ProviderLog[1].SystemPrompt)
		}
	}
	harness.Run(t, scn)
}

func TestP1_EndStateDeterminism(t *testing.T) {
	scn := codeInterpScenario()
	r1 := harness.Run(t, scn)
	r2 := harness.Run(t, scn)
	s1, err := testutil.SnapshotWorkspace(r1.WorkspaceRoot)
	if err != nil {
		t.Fatalf("snapshot1: %v", err)
	}
	s2, err := testutil.SnapshotWorkspace(r2.WorkspaceRoot)
	if err != nil {
		t.Fatalf("snapshot2: %v", err)
	}
	if diff := s1.Diff(s2); diff != "" {
		t.Fatalf("non-deterministic workspace diff:\n%s", diff)
	}
}

func TestP2_EventMilestoneCoverage(t *testing.T) {
	result := harness.Run(t, codeInterpScenario())
	assertCodeInterpMilestones(t, result)
}

func TestP3_FollowUpFIFOPreservation(t *testing.T) {
	gate0 := make(chan struct{})
	scn := codeInterpScenario()
	scn.ProviderGates = map[int]chan struct{}{0: gate0}
	scn.OnRunning = func(t *testing.T, a *agent.Agent) {
		if err := a.FollowUp("first follow-up"); err != nil {
			t.Fatalf("followup1: %v", err)
		}
		if err := a.FollowUp("second follow-up"); err != nil {
			t.Fatalf("followup2: %v", err)
		}
		close(gate0)
	}
	scn.Script = append(scn.Script,
		testutil.ScriptEntry{Text: "follow-up one", StopReason: ai.StopReasonStop},
		testutil.ScriptEntry{Text: "follow-up two", StopReason: ai.StopReasonStop},
	)
	result := harness.Run(t, scn)
	msgs := result.ConversationUserMessages()
	if len(msgs) < 3 {
		t.Fatalf("expected initial + 2 followups, got %v", msgs)
	}
	if msgs[1] != "first follow-up" || msgs[2] != "second follow-up" {
		t.Fatalf("follow-up order mismatch: %v", msgs)
	}
}

func TestP4_CodeInterpreterTraceConsistency(t *testing.T) {
	result := harness.Run(t, codeInterpScenario())
	es := findExecuteScriptResult(t, result)
	pos := map[string]int{}
	for i, step := range es.Trace {
		if _, ok := pos[step.Name]; !ok {
			pos[step.Name] = i
		}
	}
	if !(pos["discover"] < pos["describe"] && pos["describe"] < pos["read_file"]) {
		t.Fatalf("trace ordering mismatch: %+v", pos)
	}
}

func TestP5_TerminalToolShortCircuit(t *testing.T) {
	finalize := agent.AgentTool{
		Tool: ai.Tool{
			Name:        "finalize",
			Description: "terminal artifact",
			Parameters:  []byte(`{"type":"object","properties":{"result":{"type":"string"}},"required":["result"]}`),
		},
		Terminal: true,
		Execute: func(context.Context, string, map[string]any, func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{Content: []ai.ContentBlock{&ai.TextContent{Text: "done"}}}, nil
		},
	}
	result := harness.Run(t, harness.Scenario{
		Name: "codeinterp-terminal-short-circuit",
		Script: []testutil.ScriptEntry{
			{ToolCalls: []testutil.ToolCallSpec{{ID: "tc-1", Name: "finalize", Arguments: map[string]any{"result": "ok"}}}},
			{Text: "should not be consumed", StopReason: ai.StopReasonStop},
		},
		ExtraTools: []agent.AgentTool{finalize},
		Setup:      func(t *testing.T, root string) string { return "finalize now" },
	})
	if result.ProviderCalls != 1 {
		t.Fatalf("expected terminal short-circuit, provider calls=%d", result.ProviderCalls)
	}
}

func TestF1_ProviderInterruption(t *testing.T) {
	scn := codeInterpScenario()
	scn.Script[1] = testutil.ScriptEntry{Error: "provider interrupted mid-turn"}
	result := harness.Run(t, scn)
	es := findExecuteScriptResult(t, result)
	if runtimeErr, ok := es.Result.(map[string]any)["error"].(string); !ok || runtimeErr == "" {
		t.Fatalf("expected execute_script failure after provider interruption")
	}
}

func TestF2_ToolTimeout(t *testing.T) {
	result := harness.Run(t, harness.Scenario{
		Name: "codeinterp-tool-timeout",
		Script: []testutil.ScriptEntry{
			{
				ToolCalls: []testutil.ToolCallSpec{
					{
						ID:   "tc-1",
						Name: "execute_script",
						Arguments: map[string]any{
							"tier": "full",
							"code": `
def main(args):
  invoke("bash", {"cmd":"sleep 1", "timeout_ms": 10})
  return 1
`,
						},
					},
				},
			},
			{Text: "done", StopReason: ai.StopReasonStop},
		},
		Setup: func(t *testing.T, root string) string { return "run timed script" },
	})
	es := findExecuteScriptResult(t, result)
	foundTimeout := false
	for _, step := range es.Trace {
		if strings.Contains(fmt.Sprintf("%v", step.Output), "timed out") {
			foundTimeout = true
			break
		}
	}
	if !foundTimeout {
		t.Fatalf("expected timeout evidence in execute_script trace, got %+v", es.Trace)
	}
}

func TestF3_ConcurrentControlInjection(t *testing.T) {
	gate0 := make(chan struct{})
	scn := codeInterpScenario()
	scn.ProviderGates = map[int]chan struct{}{0: gate0}
	scn.OnRunning = func(t *testing.T, a *agent.Agent) {
		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); _ = a.Steer("steer") }()
		go func() { defer wg.Done(); _ = a.FollowUp("follow-up") }()
		go func() { defer wg.Done(); _ = a.Abort("abort") }()
		wg.Wait()
		close(gate0)
	}
	result := harness.Run(t, scn)
	if len(result.EventsOfType(agent.EventSteeringApplied)) == 0 {
		t.Fatalf("expected steering event")
	}
}

func TestF4_FilesystemConflict(t *testing.T) {
	scn := codeInterpScenario()
	scn.Setup = func(t *testing.T, root string) string {
		harness.WriteFile(t, root, "notes.txt", "alpha")
		harness.WriteFile(t, root, "report.txt", "external-content")
		return "run code interpreter workflow"
	}
	result := harness.Run(t, scn)
	testutil.AssertFiles(t, result.WorkspaceRoot, []testutil.FileExpectation{{Path: "report.txt", Contains: "nested-summary"}})
}

func TestF5_ScriptLimitExceeded(t *testing.T) {
	result := harness.Run(t, harness.Scenario{
		Name: "codeinterp-limit-exceeded",
		Script: []testutil.ScriptEntry{
			{
				ToolCalls: []testutil.ToolCallSpec{
					{
						ID:   "tc-1",
						Name: "execute_script",
						Arguments: map[string]any{
							"code": `
def main(args):
  i = 0
  while i < 100:
    discover("x")
    i += 1
  return i
`,
							"max_steps": 5,
						},
					},
				},
			},
			{Text: "done", StopReason: ai.StopReasonStop},
		},
		Setup: func(t *testing.T, root string) string { return "trigger limit" },
	})
	es := findExecuteScriptResult(t, result)
	if runtimeErr, ok := es.Result.(map[string]any)["error"].(string); !ok || runtimeErr == "" {
		t.Fatalf("expected limit error in execute_script payload")
	}
}

func TestO1_DeterministicSemanticOracle(t *testing.T) {
	r1 := harness.Run(t, codeInterpScenario())
	r2 := harness.Run(t, codeInterpScenario())
	e1 := findExecuteScriptResult(t, r1)
	e2 := findExecuteScriptResult(t, r2)
	if fmt.Sprintf("%v", e1.Result) != fmt.Sprintf("%v", e2.Result) {
		t.Fatalf("semantic oracle mismatch: %v vs %v", e1.Result, e2.Result)
	}
}

func TestO2_LocalVsSandboxOracle(t *testing.T) {
	local := tools.NewLocalTools(".", tools.LocalToolsOptions{})
	sandbox := tools.NewEnvironmentTools(sandboxOracleExecute)
	ln := map[string]bool{}
	for _, tool := range local {
		ln[tool.Name] = true
	}
	for _, tool := range sandbox {
		if !ln[tool.Name] {
			t.Fatalf("sandbox has unexpected tool %q", tool.Name)
		}
	}
}

func TestO3_ReplayBundleOracle(t *testing.T) {
	r1 := harness.Run(t, codeInterpScenario())
	s1, err := testutil.SnapshotWorkspace(r1.WorkspaceRoot)
	if err != nil {
		t.Fatalf("snapshot1: %v", err)
	}
	r2 := harness.Run(t, codeInterpScenario())
	s2, err := testutil.SnapshotWorkspace(r2.WorkspaceRoot)
	if err != nil {
		t.Fatalf("snapshot2: %v", err)
	}
	if diff := s1.Diff(s2); diff != "" {
		t.Fatalf("replay bundle oracle mismatch:\n%s", diff)
	}
}

func TestS1_ScenarioStateMachine(t *testing.T) {
	result := harness.Run(t, codeInterpScenario())
	assertCodeInterpMilestones(t, result)
}

func TestS2_ControlBoundarySimulation(t *testing.T) {
	gate0 := make(chan struct{})
	scn := codeInterpScenario()
	scn.ProviderGates = map[int]chan struct{}{0: gate0}
	scn.OnRunning = func(t *testing.T, a *agent.Agent) {
		_ = a.Steer("boundary update")
		close(gate0)
	}
	result := harness.Run(t, scn)
	if len(result.EventsOfType(agent.EventSteeringApplied)) == 0 {
		t.Fatalf("expected steering boundary event")
	}
}

func TestS3_FixtureMutatorSimulation(t *testing.T) {
	files := []string{"notes.txt", "nested/notes.txt", "deep/path/notes.txt"}
	for _, path := range files {
		path := path
		scn := codeInterpScenario()
		scn.Setup = func(t *testing.T, root string) string {
			harness.WriteFile(t, root, path, "alpha beta")
			return "run scenario"
		}
		scn.Script[0].ToolCalls[0].Arguments["code"] = strings.ReplaceAll(codeInterpWorkflowScript, `"notes.txt"`, fmt.Sprintf("%q", path))
		_ = harness.Run(t, scn)
	}
}

func TestST1_RepeatedScenarioChurn(t *testing.T) {
	if testing.Short() {
		t.Skip("skip churn in short mode")
	}
	for i := 0; i < 50; i++ {
		_ = harness.Run(t, codeInterpScenario())
	}
}

func TestST2_MultiAgentParallelStress(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = harness.Run(t, codeInterpScenario())
		}()
	}
	wg.Wait()
}

func TestST3_LongConversationStress(t *testing.T) {
	scn := codeInterpScenario()
	for i := 0; i < 8; i++ {
		scn.Script = append(scn.Script, testutil.ScriptEntry{Text: fmt.Sprintf("follow-%d", i), StopReason: ai.StopReasonStop})
	}
	_ = harness.Run(t, scn)
}

func TestSEC1_WorkspaceContainment(t *testing.T) {
	var outsidePath string
	const sentinel = "SENTINEL_SAFE"
	result := harness.Run(t, harness.Scenario{
		Name: "sec-workspace-containment",
		Script: []testutil.ScriptEntry{
			{ToolCalls: []testutil.ToolCallSpec{{ID: "tc-1", Name: "execute_script", Arguments: map[string]any{
				"code": `def main(args): return invoke("write_file", {"path":"../outside-sentinel.txt", "content":"HACKED"})["is_error"]`,
			}}},
			},
			{Text: "done", StopReason: ai.StopReasonStop},
		},
		Setup: func(t *testing.T, root string) string {
			outsidePath = filepath.Join(filepath.Dir(root), "outside-sentinel.txt")
			if err := os.WriteFile(outsidePath, []byte(sentinel), 0o644); err != nil {
				t.Fatalf("write sentinel: %v", err)
			}
			return "attempt path traversal"
		},
	})
	es := findExecuteScriptResult(t, result)
	_ = es
	data, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if string(data) != sentinel {
		t.Fatalf("workspace containment violated: outside sentinel was modified to %q", string(data))
	}
}

func TestSEC2_SecretRedaction(t *testing.T) {
	result := harness.Run(t, harness.Scenario{
		Name: "sec-secret-redaction",
		Script: []testutil.ScriptEntry{
			{ToolCalls: []testutil.ToolCallSpec{{ID: "tc-1", Name: "execute_script", Arguments: map[string]any{
				"code": `def main(args): log("SECRET_TOKEN=abc123"); return 1`,
			}}},
			},
			{Text: "done", StopReason: ai.StopReasonStop},
		},
		Setup: func(t *testing.T, root string) string { return "emit secret in log" },
	})
	es := findExecuteScriptResult(t, result)
	traceDump := fmt.Sprintf("%+v", es.Trace)
	if strings.Contains(traceDump, "abc123") {
		t.Fatalf("secret leaked in trace: %s", traceDump)
	}
	if !strings.Contains(traceDump, "[REDACTED]") {
		t.Fatalf("expected redaction marker in trace")
	}
}

func TestSEC3_ScriptSandboxEnforcement(t *testing.T) {
	result := harness.Run(t, harness.Scenario{
		Name: "sec-sandbox-enforcement",
		Script: []testutil.ScriptEntry{
			{ToolCalls: []testutil.ToolCallSpec{{ID: "tc-1", Name: "execute_script", Arguments: map[string]any{
				"code": `load("x.star", "x")` + "\n" + `def main(args): return 1`,
			}}},
			},
			{Text: "done", StopReason: ai.StopReasonStop},
		},
		Setup: func(t *testing.T, root string) string { return "attempt load breakout" },
	})
	es := findExecuteScriptResult(t, result)
	if runtimeErr, ok := es.Result.(map[string]any)["error"].(string); !ok || runtimeErr == "" {
		t.Fatalf("expected sandbox enforcement error")
	}
}

func TestSEC4_CommandSafety(t *testing.T) {
	result := harness.Run(t, harness.Scenario{
		Name: "sec-command-safety",
		Script: []testutil.ScriptEntry{
			{ToolCalls: []testutil.ToolCallSpec{{ID: "tc-1", Name: "bash", Arguments: map[string]any{
				"cmd": "rm -rf /",
			}}},
			},
			{Text: "done", StopReason: ai.StopReasonStop},
		},
		Setup: func(t *testing.T, root string) string { return "run dangerous command" },
	})
	if _, err := testutil.SnapshotWorkspace(result.WorkspaceRoot); err != nil {
		t.Fatalf("workspace inaccessible after dangerous command: %v", err)
	}
}

func TestB1_ScenarioRuntimeUnderBudget(t *testing.T) {
	start := time.Now()
	_ = harness.Run(t, codeInterpScenario())
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("scenario runtime too slow: %s", elapsed)
	}
}

func TestB2_MiniSuiteRuntimeUnderBudget(t *testing.T) {
	start := time.Now()
	_ = harness.Run(t, codeInterpScenario())
	_ = harness.Run(t, codeInterpScenario())
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("mini-suite runtime too slow: %s", elapsed)
	}
}

func TestB3_ArtifactOverheadBounded(t *testing.T) {
	res := harness.Run(t, codeInterpScenario())
	start := time.Now()
	if _, err := testutil.SnapshotWorkspace(res.WorkspaceRoot); err != nil {
		t.Fatalf("snapshot workspace: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("artifact collection overhead too high: %s", elapsed)
	}
}

func TestB4_ParallelScalingSignal(t *testing.T) {
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = harness.Run(t, codeInterpScenario())
		}()
	}
	wg.Wait()
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("parallel scenario execution unexpectedly slow: %s", elapsed)
	}
}

func sandboxOracleExecute(_ context.Context, _ tools.ToolRequest, _ func(tools.ToolProgress)) (*tools.ToolResponse, error) {
	return &tools.ToolResponse{Content: []ai.ContentBlock{&ai.TextContent{Text: "ok"}}}, nil
}
