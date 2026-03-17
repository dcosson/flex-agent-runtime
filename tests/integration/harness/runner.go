package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/agent"
	"github.com/anthropics/flex-agent-runtime/internal/ai"
	"github.com/anthropics/flex-agent-runtime/internal/tools"
	"github.com/anthropics/flex-agent-runtime/internal/tools/codeinterp"
	"github.com/anthropics/flex-agent-runtime/tests/integration/testutil"
)

// Scenario defines a complete E2E test scenario.
type Scenario struct {
	Name   string
	Script []testutil.ScriptEntry

	// Setup is called before the scenario runs. It receives the workspace
	// root and should create any files/repos needed. Returns the prompt.
	Setup func(t *testing.T, root string) string

	// ExtraTools adds additional tools beyond the standard local tool set.
	ExtraTools []agent.AgentTool

	// SystemPrompt overrides the default system prompt.
	SystemPrompt string

	// OnRunning is called after the agent starts but before waiting for
	// completion. Use for injecting steering or follow-ups mid-run.
	OnRunning func(t *testing.T, a *agent.Agent)

	// ProviderGates blocks specific provider calls (0-indexed) until closed.
	// Use to synchronize mid-turn actions like steering/follow-ups.
	ProviderGates map[int]chan struct{}

	// Timeout for the entire scenario. Defaults to 10s.
	Timeout time.Duration

	// Assert is called after the scenario completes to verify outcomes.
	Assert func(t *testing.T, result *ScenarioResult)
}

// ScenarioResult captures all observable outcomes from a scenario run.
type ScenarioResult struct {
	// Events is the ordered sequence of all agent events.
	Events []agent.AgentEvent

	// Session is the final agent session state.
	Session *agent.Session

	// WorkspaceRoot is the path to the workspace directory.
	WorkspaceRoot string

	// ProviderCalls is the number of calls made to the provider.
	ProviderCalls int
	ProviderLog   []testutil.ScriptedProviderCall

	// Error is set if the scenario failed to start or errored.
	Error error
}

// EventsOfType returns all events matching the given type.
func (r *ScenarioResult) EventsOfType(typ agent.AgentEventType) []agent.AgentEvent {
	var out []agent.AgentEvent
	for _, e := range r.Events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// ToolCallNames returns an ordered list of tool names that were started.
func (r *ScenarioResult) ToolCallNames() []string {
	var names []string
	for _, e := range r.Events {
		if e.Type == agent.EventToolStarted {
			names = append(names, e.ToolName)
		}
	}
	return names
}

// HasEventSequence checks that the given event types appear in order
// (not necessarily contiguously) in the event stream.
func (r *ScenarioResult) HasEventSequence(types ...agent.AgentEventType) bool {
	idx := 0
	for _, e := range r.Events {
		if idx < len(types) && e.Type == types[idx] {
			idx++
		}
	}
	return idx == len(types)
}

// ConversationUserMessages returns all user message texts from the session.
func (r *ScenarioResult) ConversationUserMessages() []string {
	if r.Session == nil {
		return nil
	}
	var msgs []string
	for _, entry := range r.Session.ConversationLog {
		if um, ok := entry.Message.(*ai.UserMessage); ok {
			for _, cb := range um.Content {
				if tc, ok := cb.(*ai.TextContent); ok {
					msgs = append(msgs, tc.Text)
				}
			}
		}
	}
	return msgs
}

// Run executes a single scenario and returns the result.
func Run(t *testing.T, scenario Scenario) *ScenarioResult {
	t.Helper()

	timeout := scenario.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	// Create isolated workspace
	root := t.TempDir()

	// Run setup to populate workspace and get prompt
	prompt := ""
	if scenario.Setup != nil {
		prompt = scenario.Setup(t, root)
	}
	if prompt == "" {
		t.Fatal("scenario setup must return a non-empty prompt")
	}

	// Register isolated provider
	api := testutil.UniqueAPI("e2e")
	sourceID := "e2e-" + t.Name()
	provider := testutil.NewScriptedProvider(api, scenario.Script)
	if scenario.ProviderGates != nil {
		provider.Gates = scenario.ProviderGates
	}
	ai.RegisterProvider(provider, sourceID)
	t.Cleanup(func() {
		ai.UnregisterProviders(sourceID)
	})

	// Build tools
	agentModel := ai.Model{ID: "e2e-model", API: api, Provider: "e2e", MaxTokens: 4096}
	agentTools := tools.NewLocalTools(root, tools.LocalToolsOptions{})
	agentTools = append(agentTools, scenario.ExtraTools...)
	agentTools = configureCodeInterpTool(agentModel, agentTools)

	// Create agent
	driver := agent.NewNativeDriver(agent.DriverConfig{
		Model:        agentModel,
		Tools:        agentTools,
		SystemPrompt: scenario.SystemPrompt,
	})
	a := agent.New(driver)
	a.SetSession(&agent.Session{ID: fmt.Sprintf("e2e-%s", t.Name())})

	// Collect events
	var mu sync.Mutex
	var events []agent.AgentEvent
	// Use a debounced idle detection: the loop may transition to idle
	// between follow-up turns. We wait until idle with no further events.
	idleCh := make(chan struct{}, 1)
	var idleTimer *time.Timer
	a.Subscribe(func(evt agent.AgentEvent) {
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
		if evt.Type == agent.EventStateChange && evt.State == agent.StateIdle {
			// Debounce: wait 50ms after last idle before signaling completion.
			// If the loop picks up follow-ups, a new turn will start and
			// we won't fire the channel prematurely.
			if idleTimer != nil {
				idleTimer.Stop()
			}
			idleTimer = time.AfterFunc(100*time.Millisecond, func() {
				select {
				case idleCh <- struct{}{}:
				default:
				}
			})
		} else if evt.Type == agent.EventTurnStarted {
			// Cancel any pending idle signal — a new turn started
			if idleTimer != nil {
				idleTimer.Stop()
			}
		}
	})

	// Start the scenario
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err := a.Prompt(ctx, prompt)
	if err != nil {
		return &ScenarioResult{Error: err, WorkspaceRoot: root}
	}

	// Run mid-scenario actions (steering, follow-ups)
	if scenario.OnRunning != nil {
		// Brief pause to let the loop goroutine start
		time.Sleep(5 * time.Millisecond)
		scenario.OnRunning(t, a)
	}

	// Wait for idle or timeout
	select {
	case <-idleCh:
	case <-ctx.Done():
		t.Logf("scenario %s timed out after %v", scenario.Name, timeout)
	}

	mu.Lock()
	eventsCopy := make([]agent.AgentEvent, len(events))
	copy(eventsCopy, events)
	mu.Unlock()

	result := &ScenarioResult{
		Events:        eventsCopy,
		Session:       a.Session(),
		WorkspaceRoot: root,
		ProviderCalls: provider.Calls(),
		ProviderLog:   append([]testutil.ScriptedProviderCall(nil), provider.CallLog...),
	}

	// Run assertions
	if scenario.Assert != nil {
		scenario.Assert(t, result)
	}

	// On failure, dump diagnostics
	if t.Failed() {
		dumpDiagnostics(t, result)
	}

	return result
}

func configureCodeInterpTool(model ai.Model, toolset []agent.AgentTool) []agent.AgentTool {
	filtered := make([]agent.AgentTool, 0, len(toolset))
	for _, t := range toolset {
		if t.Name == "execute_script" {
			continue
		}
		filtered = append(filtered, t)
	}
	filtered = append(filtered, codeinterp.NewTool(model, filtered))
	return filtered
}

// dumpDiagnostics emits detailed failure information.
func dumpDiagnostics(t *testing.T, result *ScenarioResult) {
	t.Helper()

	t.Logf("=== Scenario Diagnostics ===")
	t.Logf("Workspace: %s", result.WorkspaceRoot)
	t.Logf("Provider calls: %d", result.ProviderCalls)
	t.Logf("Total events: %d", len(result.Events))

	// Event timeline
	t.Logf("--- Event Timeline ---")
	for i, e := range result.Events {
		extra := ""
		if e.ToolName != "" {
			extra = fmt.Sprintf(" tool=%s", e.ToolName)
		}
		if e.Delta != "" {
			delta := e.Delta
			if len(delta) > 50 {
				delta = delta[:50] + "..."
			}
			extra += fmt.Sprintf(" delta=%q", delta)
		}
		if e.State != "" {
			extra += fmt.Sprintf(" state=%s", e.State)
		}
		if e.ErrorMessage != "" {
			extra += fmt.Sprintf(" err=%q", e.ErrorMessage)
		}
		t.Logf("  [%d] %s%s", i, e.Type, extra)
	}

	// Workspace contents
	snap, err := testutil.SnapshotWorkspace(result.WorkspaceRoot)
	if err != nil {
		t.Logf("(workspace snapshot error: %v)", err)
		return
	}
	t.Logf("--- Workspace Files ---")
	for path, content := range snap.Files {
		preview := content
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		t.Logf("  %s: %q", path, preview)
	}
}

// WriteFile is a helper to create files in the workspace during setup.
func WriteFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	abs := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// InitGitRepo initializes a git repo with an initial commit.
func InitGitRepo(t *testing.T, root string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "e2e@test.com"},
		{"git", "config", "user.name", "E2E Test"},
	}
	for _, args := range cmds {
		runCmd(t, root, args...)
	}
}

// GitCommitAll stages and commits all files.
func GitCommitAll(t *testing.T, root, message string) {
	t.Helper()
	runCmd(t, root, "git", "add", ".")
	runCmd(t, root, "git", "commit", "-m", message)
}

func runCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := execCommand(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v failed: %v\n%s", args, err, out)
	}
}
