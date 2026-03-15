package environment

import (
	"time"

	"h2-agent-runtime/internal/tools"
)

// SessionConfig carries parameters for environment creation.
type SessionConfig struct {
	BaseImage string
	SessionID string
	Labels    map[string]string
	Options   any
}

// SnapshotInfo describes a snapshot of the environment state.
type SnapshotInfo struct {
	ID        string
	Name      string
	CreatedAt time.Time
	SpaceUsed int64 // bytes; 0 if environment does not track this
}

// SessionState represents lifecycle state for an execution environment.
type SessionState string

const (
	StateCreating   SessionState = "creating"
	StateActive     SessionState = "active"
	StatePaused     SessionState = "paused"
	StateDestroying SessionState = "destroying"
	StateDestroyed  SessionState = "destroyed"
	StateFailed     SessionState = "failed"
)

type ToolRequest = tools.ToolRequest
type ToolResponse = tools.ToolResponse
type ToolProgress = tools.ToolProgress
