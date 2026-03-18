package orchestrator

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/client"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/transport"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
)

const (
	defaultCreateTimeout   = 3 * time.Minute
	defaultShutdownTimeout = 30 * time.Second
	defaultAgentBinary     = "flexagent"
	defaultAgentPort       = 8081
)

// PlacementMode controls where and how a session runs.
type PlacementMode string

const (
	PlacementAgentDirect  PlacementMode = "agent-direct"
	PlacementAgentSandbox PlacementMode = "agent-sandbox"
	PlacementToolsSandbox PlacementMode = "tools-sandbox"
)

// AgentServiceFactory creates an AgentService bound to a launched remote agent address.
type AgentServiceFactory func(address string) (agentapi.AgentService, error)

// OrchestratorConfig configures orchestrator behavior.
type OrchestratorConfig struct {
	DirectControl    control.SandboxControl
	NodeControl      control.SandboxControl
	AgentLoopService agentapi.AgentService

	// SandboxHostAddrs are the configured --sandbox-host-addr values.
	// Used by resolveToolsSandboxHostAddr in Node mode (single host)
	// where CreateSandboxResponse.Address is a mountpoint, not an RPC addr.
	SandboxHostAddrs []string

	MaxSessions     int
	CreateTimeout   time.Duration
	ShutdownTimeout time.Duration
	Logger          *slog.Logger

	AgentBinary string
	AgentPort   int
	AgentArgs   []string
	AgentEnv    map[string]string

	AgentServiceFactory AgentServiceFactory
}

func (cfg OrchestratorConfig) normalize() (OrchestratorConfig, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxSessions < 0 {
		return cfg, fmt.Errorf("max sessions must be >= 0")
	}
	if cfg.CreateTimeout <= 0 {
		cfg.CreateTimeout = defaultCreateTimeout
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = defaultShutdownTimeout
	}
	if strings.TrimSpace(cfg.AgentBinary) == "" {
		cfg.AgentBinary = defaultAgentBinary
	}
	if cfg.AgentPort <= 0 {
		cfg.AgentPort = defaultAgentPort
	}
	if cfg.AgentServiceFactory == nil {
		cfg.AgentServiceFactory = defaultAgentServiceFactory()
	}
	if cfg.AgentEnv == nil {
		cfg.AgentEnv = map[string]string{}
	}
	if cfg.DirectControl == nil && cfg.NodeControl == nil && cfg.AgentLoopService == nil {
		return cfg, fmt.Errorf("at least one placement backend must be configured")
	}
	return cfg, nil
}

func defaultAgentServiceFactory() AgentServiceFactory {
	return func(address string) (agentapi.AgentService, error) {
		if strings.TrimSpace(address) == "" {
			return nil, fmt.Errorf("remote agent address is required")
		}
		return client.NewAgentServiceClient(
			http.DefaultClient,
			control.AddressToURL(address),
			transport.ClientConfig{},
		), nil
	}
}

func placementFromMetadata(metadata map[string]any) (PlacementMode, error) {
	if len(metadata) == 0 {
		return PlacementToolsSandbox, nil
	}
	raw, ok := metadata["placement_mode"]
	if !ok || raw == nil {
		return PlacementToolsSandbox, nil
	}
	mode, ok := raw.(string)
	if !ok {
		return "", rpc.NewRPCError(rpc.CodeInvalidArgument, "session metadata placement_mode must be a string", nil)
	}
	placement := PlacementMode(strings.TrimSpace(mode))
	switch placement {
	case PlacementAgentDirect, PlacementAgentSandbox, PlacementToolsSandbox:
		return placement, nil
	default:
		return "", rpc.NewRPCError(rpc.CodeInvalidArgument, fmt.Sprintf("unsupported placement_mode %q", mode), nil)
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func metadataStringLabels(metadata map[string]any) map[string]string {
	if len(metadata) == 0 {
		return map[string]string{}
	}
	labels := make(map[string]string)
	for k, v := range metadata {
		s, ok := v.(string)
		if ok {
			labels[k] = s
		}
	}
	return labels
}

func joinErrors(errs ...error) error {
	filtered := make([]error, 0, len(errs))
	for _, err := range errs {
		if err != nil {
			filtered = append(filtered, err)
		}
	}
	return errors.Join(filtered...)
}
