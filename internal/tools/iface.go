package tools

import (
	"h2-agent-runtime/internal/ai"
)

// ToolRequest carries all parameters needed to dispatch a single tool call.
type ToolRequest struct {
	SessionID  string
	ToolName   string
	ToolCallID string
	Params     map[string]any
	Resources  *ResourceSpec
}

// ToolResponse is the result of a tool execution.
type ToolResponse struct {
	Content    []ai.ContentBlock
	SnapshotID string
	ExitCode   *int
}

// ToolProgress carries incremental output during long-running tool execution.
type ToolProgress struct {
	Content string
	IsError bool
}

// ResourceSpec provides optional resource hints for Tier 2 execution.
type ResourceSpec struct {
	CPUs  int `json:"cpus,omitempty"`
	MemMB int `json:"mem_mb,omitempty"`
}

// ToolTier classifies tools by execution model.
type ToolTier int

const (
	Tier1 ToolTier = 1 // In-process Go function execution
	Tier2 ToolTier = 2 // Isolated process execution
)

// ClassifyTool returns the tier for a given tool name.
// This classifier is centralized and shared across all backends.
func ClassifyTool(name string) ToolTier {
	switch name {
	case "read_file", "write_file", "edit_file", "grep", "glob",
		"git_status", "git_diff", "git_log", "git_show":
		return Tier1
	case "bash", "git_add", "git_commit", "git_push", "git_clone", "git_fetch", "git_pull":
		return Tier2
	default:
		return Tier2 // default to isolated execution for unknown tools
	}
}
