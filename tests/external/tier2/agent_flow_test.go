//go:build docker

package tier2

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anthropics/flex-agent-runtime/internal/agent"
	"github.com/anthropics/flex-agent-runtime/internal/ai"
	"github.com/anthropics/flex-agent-runtime/internal/ai/provider/anthropic"
	"github.com/anthropics/flex-agent-runtime/internal/rpc/api"
	"github.com/anthropics/flex-agent-runtime/internal/rpc/codec"
	"github.com/anthropics/flex-agent-runtime/internal/tools"
	"github.com/anthropics/flex-agent-runtime/tests/external/common"
)

func stubserverURL() string {
	if v := os.Getenv("STUBSERVER_URL"); v != "" {
		return v
	}
	return "http://localhost:19090"
}

type fixtureSelectorTransport struct {
	base http.RoundTripper

	callCount int64
	mu        sync.Mutex
	bodies    []string
}

func (t *fixtureSelectorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}

	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()

	var body []byte
	if clone.Body != nil {
		body, _ = io.ReadAll(clone.Body)
		clone.Body = io.NopCloser(bytes.NewReader(body))
		clone.ContentLength = int64(len(body))
	}

	n := atomic.AddInt64(&t.callCount, 1)
	if n == 1 {
		clone.Header.Set("X-Fixture", "anthropic-tool-call-bash")
	} else {
		clone.Header.Set("X-Fixture", "anthropic-final-assistant")
	}

	t.mu.Lock()
	t.bodies = append(t.bodies, string(body))
	t.mu.Unlock()

	return base.RoundTrip(clone)
}

func (t *fixtureSelectorTransport) RequestCount() int {
	return int(atomic.LoadInt64(&t.callCount))
}

func (t *fixtureSelectorTransport) RequestBodies() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, len(t.bodies))
	copy(out, t.bodies)
	return out
}

func promptAndWait(t *testing.T, a *agent.Agent, prompt string, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	idle := make(chan struct{}, 1)
	unsub := a.Subscribe(func(evt agent.AgentEvent) {
		if evt.Type == agent.EventStateChange && evt.State == agent.StateIdle {
			select {
			case idle <- struct{}{}:
			default:
			}
		}
	})
	defer unsub()

	if err := a.Prompt(ctx, prompt); err != nil {
		t.Fatalf("agent prompt failed: %v", err)
	}

	select {
	case <-idle:
	case <-ctx.Done():
		t.Fatalf("timed out waiting for idle: %v", ctx.Err())
	}
}

func assistantText(msg *ai.AssistantMessage) string {
	if msg == nil {
		return ""
	}
	var b strings.Builder
	for _, block := range msg.Content {
		if txt, ok := block.(*ai.TextContent); ok {
			b.WriteString(txt.Text)
		}
	}
	return b.String()
}

func TestDockerAgentFlow_StubserverRoundTrip(t *testing.T) {
	common.RequireDocker(t)
	cfg := activeConfig(t)
	sbox := newSandboxClient(t, sandboxHostURL(t))

	created := mustCreateSession(t, sbox, cfg)
	sessionID := created.Session.ID
	defer mustDestroySession(t, sbox, sessionID)

	rt := &fixtureSelectorTransport{}
	provider := anthropic.New(anthropic.Config{
		HTTPClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: rt,
		},
		BaseURL: stubserverURL(),
		APIKey:  "tier2-stub-key",
	})

	sourceID := "tier2-agent-flow-" + t.Name()
	ai.RegisterProvider(provider, sourceID)
	defer ai.UnregisterProviders(sourceID)

	executeFn := func(ctx context.Context, req tools.ToolRequest, _ func(tools.ToolProgress)) (*tools.ToolResponse, error) {
		resp, err := sbox.ExecuteTool.CallUnary(ctx, connect.NewRequest(&api.ExecuteToolRequest{
			SessionID:  sessionID,
			ToolCallID: req.ToolCallID,
			ToolName:   req.ToolName,
			Params:     req.Params,
		}))
		if err != nil {
			return nil, err
		}
		out := resp.Msg
		content := codec.FromAPIContentBlocks(out.ContentBlocks)
		if len(content) == 0 && out.Content != "" {
			content = []ai.ContentBlock{&ai.TextContent{Text: out.Content}}
		}
		return &tools.ToolResponse{
			Content:    content,
			SnapshotID: out.SnapshotID,
			ExitCode:   out.ExitCode,
		}, nil
	}

	driver := agent.NewNativeDriver(agent.DriverConfig{
		Model: ai.Model{
			ID:        "claude-sonnet-4-20250514",
			API:       "anthropic-messages",
			Provider:  "anthropic",
			MaxTokens: 4096,
		},
		Tools:        tools.NewEnvironmentTools(executeFn),
		SystemPrompt: "You are an assistant. Use tools when needed.",
	})

	a := agent.New(driver)
	a.SetSession(&agent.Session{ID: sessionID})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = a.Stop(ctx)
	}()

	var eventsMu sync.Mutex
	var events []agent.AgentEvent
	a.Subscribe(func(evt agent.AgentEvent) {
		eventsMu.Lock()
		events = append(events, evt)
		eventsMu.Unlock()
	})

	promptAndWait(t, a, "Run bash and tell me the result.", 20*time.Second)

	eventsMu.Lock()
	gotEvents := make([]agent.AgentEvent, len(events))
	copy(gotEvents, events)
	eventsMu.Unlock()

	var sawToolStart bool
	var sawToolComplete bool
	var sawTurnCompleted bool
	var finalAssistant string
	for _, evt := range gotEvents {
		switch evt.Type {
		case agent.EventToolStarted:
			sawToolStart = true
		case agent.EventToolCompleted:
			sawToolComplete = true
		case agent.EventTurnCompleted:
			sawTurnCompleted = true
		case agent.EventAgentMessageCompleted:
			finalAssistant = assistantText(evt.Assistant)
		}
	}
	if !sawToolStart {
		t.Fatal("expected tool_started event")
	}
	if !sawToolComplete {
		t.Fatal("expected tool_completed event")
	}
	if !sawTurnCompleted {
		t.Fatal("expected turn_completed event")
	}
	if !strings.Contains(finalAssistant, "tier2-e2e-ok") {
		t.Fatalf("final assistant message missing expected text: %q", finalAssistant)
	}

	if rt.RequestCount() < 2 {
		t.Fatalf("expected at least 2 LLM requests, got %d", rt.RequestCount())
	}
	bodies := rt.RequestBodies()
	if len(bodies) < 2 {
		t.Fatalf("expected at least 2 captured request bodies, got %d", len(bodies))
	}
	if !strings.Contains(bodies[1], "tool_result") {
		t.Fatalf("second LLM request missing tool_result payload: %s", bodies[1])
	}
}
