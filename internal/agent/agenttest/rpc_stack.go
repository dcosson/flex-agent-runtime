package agenttest

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/anthropics/flex-agent-runtime/internal/agent"
	agentapi "github.com/anthropics/flex-agent-runtime/internal/agent/api"
	"github.com/anthropics/flex-agent-runtime/internal/ai"
	rpcclient "github.com/anthropics/flex-agent-runtime/internal/rpc/client"
	rpcserver "github.com/anthropics/flex-agent-runtime/internal/rpc/server"
	"github.com/anthropics/flex-agent-runtime/internal/rpc/transport"
)

// AgentTestStack wires AgentLoopService + AgentRPCServer + AgentServiceClient.
type AgentTestStack struct {
	Service       *agent.AgentLoopService
	RPCServer     *rpcserver.AgentRPCServer
	Client        *rpcclient.AgentServiceClient
	Publisher     *MockEventPublisher
	DriverFactory *MockDriverFactory

	Provider string
	Model    string

	transport *httptest.Server
}

func NewAgentTestStack(t testing.TB, opts ...agent.ServiceOption) *AgentTestStack {
	t.Helper()

	provider, model, err := pickAnyRegisteredModel()
	if err != nil {
		t.Fatalf("pick model: %v", err)
	}

	publisher := &MockEventPublisher{}
	drivers := NewMockDriverFactory()
	service := agent.NewAgentLoopService(publisher, opts...)
	service.SetDriverFactory(drivers.Create)

	rpcAdapter := rpcserver.NewAgentRPCServer(service)
	rpcTransport := transport.NewServer(
		transport.ServerConfig{},
		transport.WithAgentService(service),
	)

	ts := httptest.NewUnstartedServer(rpcTransport.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()

	stack := &AgentTestStack{
		Service:       service,
		RPCServer:     rpcAdapter,
		Client:        rpcclient.NewAgentServiceClient(ts.Client(), ts.URL, transport.ClientConfig{APIVersion: "v1"}),
		Publisher:     publisher,
		DriverFactory: drivers,
		Provider:      provider,
		Model:         model,
		transport:     ts,
	}

	t.Cleanup(func() {
		_ = stack.Close()
	})
	return stack
}

func (s *AgentTestStack) Close() error {
	var serviceErr error
	if s.Service != nil {
		serviceErr = s.Service.Close()
		s.Service = nil
	}
	if s.transport != nil {
		s.transport.Close()
		s.transport = nil
	}
	return serviceErr
}

func (s *AgentTestStack) BaseConfig(sessionID string) agentapi.SessionConfig {
	return agentapi.SessionConfig{
		SessionID: sessionID,
		Driver:    "mock",
		Model:     s.Model,
		Provider:  s.Provider,
		Tools:     []string{"bash", "read_file", "write_file"},
		Metadata:  map[string]any{"team": "agenttest"},
		ToolEnvironment: agentapi.ToolEnvironmentConfig{
			Type: agentapi.ToolEnvLocal,
		},
	}
}

func (s *AgentTestStack) CreateAgentSession(ctx context.Context, sessionID string) (*agentapi.CreateAgentSessionResponse, error) {
	return s.Client.CreateSession(ctx, &agentapi.CreateAgentSessionRequest{SessionConfig: s.BaseConfig(sessionID)})
}

// MockEventPublisher records all published events by session.
type MockEventPublisher struct {
	mu     sync.Mutex
	events map[string][]agent.AgentEvent
}

func (p *MockEventPublisher) Publish(sessionID string, evt agent.AgentEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.events == nil {
		p.events = make(map[string][]agent.AgentEvent)
	}
	p.events[sessionID] = append(p.events[sessionID], evt)
}

func (p *MockEventPublisher) EventsForSession(sessionID string) []agent.AgentEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	src := p.events[sessionID]
	out := make([]agent.AgentEvent, len(src))
	copy(out, src)
	return out
}

// MockDriverFactory routes session IDs to configurable MockAgentDrivers.
type MockDriverFactory struct {
	mu      sync.Mutex
	drivers map[string]*MockAgentDriver
}

func NewMockDriverFactory() *MockDriverFactory {
	return &MockDriverFactory{drivers: make(map[string]*MockAgentDriver)}
}

func (f *MockDriverFactory) SetDriver(sessionID string, driver *MockAgentDriver) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.drivers[sessionID] = driver
}

func (f *MockDriverFactory) Create(name string, cfg agent.DriverConfig) (agent.AgentDriver, error) {
	if name != "mock" {
		return nil, fmt.Errorf("unknown driver: %s", name)
	}
	sessionID, _ := cfg.Metadata["session_id"].(string)
	f.mu.Lock()
	defer f.mu.Unlock()
	if driver, ok := f.drivers[sessionID]; ok {
		return driver, nil
	}
	if driver, ok := f.drivers["default"]; ok {
		return driver, nil
	}
	driver := &MockAgentDriver{}
	f.drivers[sessionID] = driver
	return driver, nil
}

func pickAnyRegisteredModel() (provider string, model string, err error) {
	providers := ai.GetModelProviders()
	for _, p := range providers {
		models := ai.GetModels(p)
		if len(models) == 0 {
			continue
		}
		return p, models[0].ID, nil
	}
	return "", "", fmt.Errorf("no registered models")
}
