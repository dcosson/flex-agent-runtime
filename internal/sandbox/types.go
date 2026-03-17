package sandbox

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/sandbox/gvisor"
)

var (
	ErrSessionNotFound       = errors.New("sandbox: session not found")
	ErrSessionExists         = errors.New("sandbox: session already exists")
	ErrSessionPaused         = errors.New("sandbox: session is paused")
	ErrSessionDestroying     = errors.New("sandbox: session is destroying")
	ErrRollbackInProgress    = errors.New("sandbox: rollback in progress")
	ErrInvalidState          = errors.New("sandbox: invalid session state")
	ErrMaxSessionsReached    = errors.New("sandbox: max sessions reached")
	ErrToolsInFlight         = errors.New("sandbox: tools in flight")
	ErrSnapshotsNotAvailable = errors.New("sandbox: snapshots not available (requires ZFS storage backend)")
)

type SessionState string

const (
	SessionCreating   SessionState = "creating"
	SessionActive     SessionState = "active"
	SessionPaused     SessionState = "paused"
	SessionDestroying SessionState = "destroying"
	SessionFailed     SessionState = "failed"
)

type Session struct {
	mu          sync.RWMutex
	id          string
	state       SessionState
	dataset     string
	mountpoint  string
	turnCount   int
	created     time.Time
	labels      map[string]string
	activeTools atomic.Int32
	rollingBack bool
	snapshots   []SnapshotEntry
}

type SnapshotEntry struct {
	Name           string
	IsTurnSnapshot bool
}

type ServiceConfig struct {
	StorageBackend   StorageBackend
	ContainerRuntime ContainerRuntime

	PoolName               string
	BasesDataset           string
	SessionsDataset        string
	SessionsRootDir        string
	MaxSessions            int
	DefaultSessionQuota    int64
	SnapshotPrefix         string
	MaxSnapshotsPerSession int
	DefaultResources       gvisor.ResourceSpec
	ToolTimeout            time.Duration
	PoolSpaceWarnThreshold float64
	PoolSpaceCritThreshold float64
	HealthCheckInterval    time.Duration
	PauseDrainTimeout      time.Duration
	ShutdownTimeout        time.Duration
	PerToolSnapshots       bool
}

func DefaultServiceConfig() ServiceConfig {
	return ServiceConfig{
		StorageBackend:         StorageBackendZFS,
		ContainerRuntime:       ContainerRuntimeGVisor,
		SnapshotPrefix:         "turn",
		ToolTimeout:            5 * time.Minute,
		PoolSpaceWarnThreshold: 0.85,
		PoolSpaceCritThreshold: 0.95,
		HealthCheckInterval:    30 * time.Second,
		PauseDrainTimeout:      30 * time.Second,
		ShutdownTimeout:        30 * time.Second,
	}
}
