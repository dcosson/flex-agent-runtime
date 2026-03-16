package local

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"flex-agent-runtime/internal/sandbox/environment"
)

// LocalEnvironment executes tools directly on the local filesystem/processes.
// Lifecycle methods are no-ops except Destroy, which marks the environment as
// inactive to prevent further execution.
type LocalEnvironment struct {
	workDir   string
	logger    *slog.Logger
	execute   func(context.Context, string, environment.ToolRequest, func(environment.ToolProgress)) (*environment.ToolResponse, error)
	destroyed atomic.Bool
}

func NewLocalEnvironment(workDir string, logger *slog.Logger) *LocalEnvironment {
	return &LocalEnvironment{
		workDir: workDir,
		logger:  logger,
		execute: executeLocalTool,
	}
}

func (e *LocalEnvironment) Create(_ context.Context, config environment.SessionConfig) error {
	if config.SessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	if e.destroyed.Load() {
		return environment.ErrNotActive
	}
	return nil
}

func (e *LocalEnvironment) Pause(context.Context) error {
	return nil
}

func (e *LocalEnvironment) Resume(context.Context) error {
	return nil
}

func (e *LocalEnvironment) Destroy(context.Context) error {
	e.destroyed.Store(true)
	return nil
}

func (e *LocalEnvironment) ExecuteTool(ctx context.Context, req environment.ToolRequest, onProgress func(environment.ToolProgress)) (*environment.ToolResponse, error) {
	if e.destroyed.Load() {
		return nil, environment.ErrNotActive
	}
	return e.execute(ctx, e.workDir, req, onProgress)
}

func (e *LocalEnvironment) State() environment.SessionState {
	if e.destroyed.Load() {
		return environment.StateDestroyed
	}
	return environment.StateActive
}

func (e *LocalEnvironment) Capabilities() environment.Capabilities {
	return environment.LocalCapabilities
}

func (e *LocalEnvironment) CreateSnapshot(context.Context, string) (*environment.SnapshotInfo, error) {
	return nil, environment.ErrCapabilityNotSupported
}

func (e *LocalEnvironment) Rollback(context.Context, string) error {
	return environment.ErrCapabilityNotSupported
}
