package harness

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/agent"
	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/client"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/server"
	"github.com/dcosson/flex-agent-runtime/internal/tools"
	"github.com/dcosson/flex-agent-runtime/internal/tools/codeinterp"
	"github.com/dcosson/flex-agent-runtime/tests/integration/testutil"
)

type RemoteEnv struct {
	Service      *MemorySandboxService
	Lifecycle    *SessionLifecycle
	Client       *client.SandboxClient
	EventServer  *server.AgentEventServer
	Agent        *agent.Agent
	Provider     *testutil.ScriptedProvider
	SessionID    string
	BaseSnapshot string

	mu     sync.Mutex
	events []agent.AgentEvent
}

func NewRemoteEnv(t *testing.T, script []testutil.ScriptEntry, baseFiles map[string]string) *RemoteEnv {
	t.Helper()

	apiName := testutil.UniqueAPI("mode3")
	provider := testutil.NewScriptedProvider(apiName, script)
	sourceID := "mode3-" + t.Name()
	ai.RegisterProvider(provider, sourceID)
	t.Cleanup(func() { ai.UnregisterProviders(sourceID) })

	baseSnapshot := "memory/base@initial"
	sessionID := fmt.Sprintf("mode3-%d", time.Now().UnixNano())

	svc := NewMemorySandboxService()
	svc.SeedBaseSnapshot(baseSnapshot, baseFiles)
	lifecycle := NewSessionLifecycle(svc)
	if _, err := lifecycle.Create(context.Background(), baseSnapshot, sessionID); err != nil {
		t.Fatalf("create session: %v", err)
	}

	cl := client.NewSandboxClient(svc)
	executeFn := func(ctx context.Context, req tools.ToolRequest, onProgress func(tools.ToolProgress)) (*tools.ToolResponse, error) {
		return cl.ExecuteTool(ctx, sessionID, req, onProgress)
	}
	toolset := tools.NewEnvironmentTools(executeFn)
	toolset = replaceCodeInterpTool(ai.Model{ID: "mode3-model", API: apiName, Provider: "mode3", MaxTokens: 4096}, toolset)
	driver := agent.NewNativeDriver(agent.DriverConfig{
		Model: ai.Model{ID: "mode3-model", API: apiName, Provider: "mode3", MaxTokens: 4096},
		Tools: toolset,
	})
	a := agent.New(driver)
	a.SetSession(&agent.Session{ID: sessionID})

	eventServer := server.NewAgentEventServer()
	env := &RemoteEnv{
		Service:      svc,
		Lifecycle:    lifecycle,
		Client:       cl,
		EventServer:  eventServer,
		Agent:        a,
		Provider:     provider,
		SessionID:    sessionID,
		BaseSnapshot: baseSnapshot,
	}
	a.Subscribe(func(evt agent.AgentEvent) {
		env.mu.Lock()
		env.events = append(env.events, evt)
		env.mu.Unlock()
		eventServer.Publish(sessionID, evt)
	})

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Stop(ctx)
		_ = lifecycle.Destroy(context.Background(), sessionID)
	})

	return env
}

func (e *RemoteEnv) Events() []agent.AgentEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]agent.AgentEvent, len(e.events))
	copy(out, e.events)
	return out
}

func (e *RemoteEnv) PromptAndWait(t *testing.T, prompt string, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	idle := make(chan struct{}, 1)
	unsub := e.Agent.Subscribe(func(evt agent.AgentEvent) {
		if evt.Type == agent.EventStateChange && evt.State == agent.StateIdle {
			select {
			case idle <- struct{}{}:
			default:
			}
		}
	})
	defer unsub()

	if err := e.Agent.Prompt(ctx, prompt); err != nil {
		t.Fatalf("agent prompt failed: %v", err)
	}

	select {
	case <-idle:
	case <-ctx.Done():
		t.Fatalf("timed out waiting for idle")
	}
}

func (e *RemoteEnv) Stop(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := e.Agent.Stop(ctx); err != nil {
		t.Fatalf("agent stop failed: %v", err)
	}
}

func (e *RemoteEnv) SubscribeRemoteEvents(t *testing.T) api.AgentEventReceiver {
	t.Helper()
	recv, err := e.EventServer.StreamAgentEvents(context.Background(), &api.StreamAgentEventsRequest{SessionID: e.SessionID})
	if err != nil {
		t.Fatalf("stream agent events: %v", err)
	}
	t.Cleanup(func() { _ = recv.Close() })
	return recv
}

func CollectRemoteEventsUntilEOF(recv api.AgentEventReceiver) ([]agent.AgentEvent, error) {
	var events []agent.AgentEvent
	for {
		env, err := recv.Recv()
		if err != nil {
			if err == io.EOF {
				return events, nil
			}
			return events, err
		}
		if env == nil {
			continue
		}
		events = append(events, env.Event)
	}
}

func replaceCodeInterpTool(model ai.Model, toolset []agent.AgentTool) []agent.AgentTool {
	filtered := make([]agent.AgentTool, 0, len(toolset))
	for _, tool := range toolset {
		if tool.Name == "execute_script" {
			continue
		}
		filtered = append(filtered, tool)
	}
	filtered = append(filtered, codeinterp.NewTool(model, filtered))
	return filtered
}
