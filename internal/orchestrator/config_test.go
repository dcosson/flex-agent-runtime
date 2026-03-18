package orchestrator

import (
	"errors"
	"testing"
	"time"

	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc"
)

func TestPlacementFromMetadataDefault(t *testing.T) {
	placement, err := placementFromMetadata(nil)
	if err != nil {
		t.Fatalf("placementFromMetadata(nil) error = %v", err)
	}
	if placement != PlacementToolsSandbox {
		t.Fatalf("placement = %q, want %q", placement, PlacementToolsSandbox)
	}
}

func TestPlacementFromMetadataInvalidValue(t *testing.T) {
	_, err := placementFromMetadata(map[string]any{"placement_mode": "bad"})
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected rpc error, got %v", err)
	}
	if rpcErr.Code != rpc.CodeInvalidArgument {
		t.Fatalf("rpc code = %s, want %s", rpcErr.Code, rpc.CodeInvalidArgument)
	}
}

func TestPlacementFromMetadataInvalidType(t *testing.T) {
	_, err := placementFromMetadata(map[string]any{"placement_mode": 1})
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected rpc error, got %v", err)
	}
	if rpcErr.Code != rpc.CodeInvalidArgument {
		t.Fatalf("rpc code = %s, want %s", rpcErr.Code, rpc.CodeInvalidArgument)
	}
}

func TestConfigNormalizeDefaults(t *testing.T) {
	cfg := OrchestratorConfig{DirectControl: &mockSandboxControl{}}
	normalized, err := cfg.normalize()
	if err != nil {
		t.Fatalf("normalize() error = %v", err)
	}
	if normalized.CreateTimeout != defaultCreateTimeout {
		t.Fatalf("CreateTimeout = %s, want %s", normalized.CreateTimeout, defaultCreateTimeout)
	}
	if normalized.HealthInterval != defaultHealthInterval {
		t.Fatalf("HealthInterval = %s, want %s", normalized.HealthInterval, defaultHealthInterval)
	}
	if normalized.ShutdownTimeout != defaultShutdownTimeout {
		t.Fatalf("ShutdownTimeout = %s, want %s", normalized.ShutdownTimeout, defaultShutdownTimeout)
	}
	if normalized.AgentBinary != defaultAgentBinary {
		t.Fatalf("AgentBinary = %q, want %q", normalized.AgentBinary, defaultAgentBinary)
	}
	if normalized.AgentPort != defaultAgentPort {
		t.Fatalf("AgentPort = %d, want %d", normalized.AgentPort, defaultAgentPort)
	}
	if normalized.AgentServiceFactory == nil {
		t.Fatal("AgentServiceFactory should be set")
	}
}

func TestConfigNormalizeValidation(t *testing.T) {
	_, err := (OrchestratorConfig{MaxSessions: -1, DirectControl: &mockSandboxControl{}}).normalize()
	if err == nil {
		t.Fatal("normalize() expected error for negative max sessions")
	}
	_, err = (OrchestratorConfig{HealthInterval: -1 * time.Second, DirectControl: &mockSandboxControl{}}).normalize()
	if err == nil {
		t.Fatal("normalize() expected error for negative health interval")
	}

	_, err = (OrchestratorConfig{}).normalize()
	if err == nil {
		t.Fatal("normalize() expected error when no backends are configured")
	}

	cfg := OrchestratorConfig{
		DirectControl:   &mockSandboxControl{},
		CreateTimeout:   time.Second,
		ShutdownTimeout: time.Second,
		AgentBinary:     "x",
		AgentPort:       42,
		AgentServiceFactory: func(_ string) (agentapi.AgentService, error) {
			return &mockAgentService{}, nil
		},
	}
	normalized, err := cfg.normalize()
	if err != nil {
		t.Fatalf("normalize() unexpected error: %v", err)
	}
	if normalized.AgentBinary != "x" || normalized.AgentPort != 42 {
		t.Fatalf("normalize() changed configured agent launch settings: %#v", normalized)
	}
}
