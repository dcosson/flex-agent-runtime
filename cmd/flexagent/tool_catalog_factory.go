package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/agent"
	agentapi "github.com/anthropics/flex-agent-runtime/internal/agent/api"
	rpcclient "github.com/anthropics/flex-agent-runtime/internal/rpc/client"
	"github.com/anthropics/flex-agent-runtime/internal/rpc/transport"
	"github.com/anthropics/flex-agent-runtime/internal/sandbox/control"
	localenv "github.com/anthropics/flex-agent-runtime/internal/sandbox/environment/local"
	nativeenv "github.com/anthropics/flex-agent-runtime/internal/sandbox/environment/native"
	"github.com/anthropics/flex-agent-runtime/internal/tools"
)

func newRuntimeToolCatalogFactory(logger *slog.Logger) agent.ToolCatalogFactory {
	if logger == nil {
		logger = slog.Default()
	}
	return func(cfg agentapi.SessionConfig) ([]agent.AgentTool, error) {
		catalog, err := buildEnvironmentCatalog(logger, cfg)
		if err != nil {
			return nil, err
		}
		return filterCatalogByName(cfg.Tools, catalog)
	}
}

func buildEnvironmentCatalog(logger *slog.Logger, cfg agentapi.SessionConfig) ([]agent.AgentTool, error) {
	switch cfg.ToolEnvironment.Type {
	case "", agentapi.ToolEnvLocal:
		rootDir := strings.TrimSpace(cfg.ToolEnvironment.LocalRootDir)
		if rootDir == "" {
			rootDir = "."
		}
		env := localenv.NewLocalEnvironment(rootDir, logger)
		return tools.NewEnvironmentTools(env.ExecuteTool), nil
	case agentapi.ToolEnvSandbox:
		hostAddr := strings.TrimSpace(cfg.ToolEnvironment.SandboxHostAddr)
		sessionID := strings.TrimSpace(cfg.ToolEnvironment.SandboxSessionID)
		if hostAddr == "" {
			return nil, fmt.Errorf("sandbox_host_addr is required for sandbox tool environment")
		}
		if sessionID == "" {
			return nil, fmt.Errorf("sandbox_session_id is required for sandbox tool environment")
		}

		baseURL := hostAddr
		if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
			baseURL = control.AddressToURL(baseURL)
		}
		sandboxSvc := rpcclient.NewSandboxServiceClient(
			&http.Client{Timeout: 30 * time.Second},
			baseURL,
			transport.ClientConfig{APIVersion: "v1"},
		)
		env := nativeenv.NewNativeSandboxEnvironment(sandboxSvc, nativeenv.DefaultConfig(), logger)
		if err := env.AttachSession(sessionID); err != nil {
			return nil, err
		}
		return tools.NewEnvironmentTools(env.ExecuteTool), nil
	default:
		return nil, fmt.Errorf("unknown tool environment type %q", cfg.ToolEnvironment.Type)
	}
}

func filterCatalogByName(requested []string, catalog []agent.AgentTool) ([]agent.AgentTool, error) {
	if len(requested) == 0 {
		return nil, nil
	}
	index := make(map[string]agent.AgentTool, len(catalog))
	for _, tool := range catalog {
		index[tool.Name] = tool
	}

	out := make([]agent.AgentTool, 0, len(requested))
	for _, name := range requested {
		tool, ok := index[name]
		if !ok {
			return nil, fmt.Errorf("unknown tool %q", name)
		}
		out = append(out, tool)
	}
	return out, nil
}
