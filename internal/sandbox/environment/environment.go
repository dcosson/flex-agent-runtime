package environment

import "context"

// ExecutionEnvironment is the unified interface for tool execution environments.
// It covers lifecycle management, tool execution, environment state, and
// optional capabilities like snapshots and rollback.
type ExecutionEnvironment interface {
	Create(ctx context.Context, config SessionConfig) error
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	Destroy(ctx context.Context) error

	ExecuteTool(ctx context.Context, req ToolRequest, onProgress func(ToolProgress)) (*ToolResponse, error)

	State() SessionState
	Capabilities() Capabilities

	CreateSnapshot(ctx context.Context, name string) (*SnapshotInfo, error)
	Rollback(ctx context.Context, snapshotID string) error
}
