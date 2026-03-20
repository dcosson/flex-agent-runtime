// Package envtools bridges the circular-import gap between internal/agent,
// internal/tools, and internal/sandbox/environment. It provides a helper
// to create DriverConfig.EnvironmentTools from an ExecutionEnvironment.
package envtools

import (
	"github.com/dcosson/flex-agent-runtime/internal/agent"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment"
	"github.com/dcosson/flex-agent-runtime/internal/tools"
)

// ToolsFunc returns a closure suitable for DriverConfig.EnvironmentTools
// that creates the standard agent tool catalog from an ExecutionEnvironment.
func ToolsFunc(env environment.ExecutionEnvironment) func() []agent.AgentTool {
	return func() []agent.AgentTool {
		return tools.NewEnvironmentTools(env.ExecuteTool)
	}
}
