package sandbox

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/gvisor"
)

var (
	ErrSessionNotFound       = errors.New("sandbox: session not found")
	ErrSessionExists         = errors.New("sandbox: session already exists")
	ErrSessionPaused         = errors.New("sandbox: session is paused")
	ErrSessionDestroying     = errors.New("sandbox: session is destroying")
	ErrProcessNotFound       = errors.New("sandbox: process not found")
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
	processes   map[string]*ManagedProcess
	processSeq  uint64
}

type SnapshotEntry struct {
	Name           string
	IsTurnSnapshot bool
}

type ServiceConfig struct {
	StorageBackend   StorageBackend
	ContainerRuntime ContainerRuntime

	AdvertiseAddr string

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

type LaunchProcessRequest struct {
	SessionID  string
	Binary     string
	Args       []string
	Env        map[string]string
	ExposePort int
}

type LaunchProcessResponse struct {
	ProcessID string
	Address   string
	Status    ProcessStatus
}

type KillProcessRequest struct {
	SessionID string
	ProcessID string
	Signal    int
}

type GetProcessStatusRequest struct {
	SessionID string
	ProcessID string
}

type GetProcessStatusResponse struct {
	Status   ProcessStatus
	ExitCode *int
}

type ProcessStatus string

const (
	ProcessStatusStarting ProcessStatus = "starting"
	ProcessStatusRunning  ProcessStatus = "running"
	ProcessStatusExited   ProcessStatus = "exited"
)

func DefaultServiceConfig() ServiceConfig {
	return ServiceConfig{
		StorageBackend:         StorageBackendZFS,
		ContainerRuntime:       ContainerRuntimeGVisor,
		AdvertiseAddr:          "127.0.0.1",
		SnapshotPrefix:         "turn",
		ToolTimeout:            5 * time.Minute,
		PoolSpaceWarnThreshold: 0.85,
		PoolSpaceCritThreshold: 0.95,
		HealthCheckInterval:    30 * time.Second,
		PauseDrainTimeout:      30 * time.Second,
		ShutdownTimeout:        30 * time.Second,
	}
}
