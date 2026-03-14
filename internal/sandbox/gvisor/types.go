package gvisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// GVisorManager manages gVisor container lifecycle for Tier 2 tool execution.
// Each Run() call creates a fresh container, executes the command, captures
// output, and destroys the container. Thread-safe for concurrent use.
type GVisorManager interface {
	// Run executes a command in a gVisor container with the given resource limits.
	// Returns ContainerResult on successful execution (even if the command
	// exits non-zero). Returns error only for infrastructure failures.
	Run(ctx context.Context, opts ContainerOptions) (*ContainerResult, error)

	// CleanupStale finds and removes any containers that are no longer tracked
	// by the manager (e.g., from a previous crash). Safe to call periodically.
	CleanupStale(ctx context.Context) (int, error)

	// ActiveContainers returns the number of currently running containers.
	ActiveContainers() int

	// Close shuts down the manager. Idempotent and safe for concurrent callers.
	// Marks manager closed immediately; subsequent Run() returns ErrManagerClosed.
	Close() error
}

// ErrManagerClosed is returned by Run() after the manager has been closed.
var ErrManagerClosed = errors.New("gvisor manager is closed")

// ContainerOptions configures a single container execution.
type ContainerOptions struct {
	// Command is the command and arguments to execute.
	Command []string

	// WorkDir is the working directory inside the container.
	// Must be an absolute path within the rootfs.
	WorkDir string

	// Env is the environment variables for the process.
	// If nil, a minimal default environment is used.
	Env map[string]string

	// RootFS is the host path to use as the container's root filesystem.
	// Typically a ZFS dataset mountpoint.
	RootFS string

	// Resources specifies CPU, memory, PID, and timeout limits.
	Resources ResourceSpec

	// Network controls the container's network access.
	// Default: NetworkNone (no network).
	Network NetworkMode

	// Stdin provides input to the container's stdin.
	// If nil, stdin is /dev/null.
	Stdin io.Reader

	// User specifies the UID:GID to run the process as inside the container.
	// Default: 0:0 (root).
	User *UserSpec

	// ReadOnlyRootFS makes the root filesystem read-only.
	ReadOnlyRootFS bool

	// ExtraMounts adds additional bind mounts or tmpfs mounts.
	ExtraMounts []Mount
}

// Validate checks that the options are valid for container creation.
func (o ContainerOptions) Validate() error {
	if len(o.Command) == 0 {
		return fmt.Errorf("command is required")
	}
	if o.RootFS == "" {
		return fmt.Errorf("rootfs path is required")
	}
	if o.WorkDir == "" {
		return fmt.Errorf("working directory is required")
	}
	if o.WorkDir[0] != '/' {
		return fmt.Errorf("working directory must be absolute, got %q", o.WorkDir)
	}
	return ValidateResources(o.Resources)
}

// ResourceSpec defines resource limits for a container.
type ResourceSpec struct {
	// CPUs is the number of CPU cores to allocate.
	// 0 means no limit (use all available CPUs).
	CPUs float64

	// MemoryMB is the memory limit in megabytes.
	// 0 means no limit.
	MemoryMB int

	// MaxPIDs is the maximum number of processes.
	// 0 means default (1024).
	MaxPIDs int

	// Timeout is the maximum wall-clock execution time.
	// Enforced via context deadline. 0 means no timeout.
	Timeout time.Duration

	// MaxOutputBytes limits stdout and stderr capture size.
	// 0 means default (10 MB).
	MaxOutputBytes int64
}

// NetworkMode controls container network access.
type NetworkMode string

const (
	NetworkNone    NetworkMode = "none"
	NetworkSandbox NetworkMode = "sandbox"
	NetworkHost    NetworkMode = "host"
)

// UserSpec specifies the user identity inside the container.
type UserSpec struct {
	UID uint32
	GID uint32
}

// Mount defines an additional filesystem mount inside the container.
type Mount struct {
	Source      string
	Destination string
	Type        string
	ReadOnly    bool
	Options     []string
}

// ContainerResult holds the outcome of a container execution.
type ContainerResult struct {
	ContainerID     string
	ExitCode        int
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
	Duration        time.Duration
	BootDuration    time.Duration
	Status          ContainerStatus
	OOMKilled       bool
	PeakMemoryBytes int64
}

// ContainerStatus indicates how the container exited.
type ContainerStatus string

const (
	StatusExited    ContainerStatus = "exited"
	StatusTimedOut  ContainerStatus = "timed_out"
	StatusOOMKilled ContainerStatus = "oom_killed"
	StatusKilled    ContainerStatus = "killed"
	StatusError     ContainerStatus = "error"
)

// Constants
const (
	DefaultTimeout      = 120 * time.Second
	DefaultMemoryMB     = 512
	DefaultMaxPIDs      = 1024
	DefaultMaxOutput    = 10 * 1024 * 1024 // 10 MB
	MaxContainerNameLen = 64
)

// ManagerConfig configures the GVisorManager.
type ManagerConfig struct {
	RunscPath               string
	BundleBaseDir           string
	RunscRoot               string
	Platform                string
	MaxConcurrentContainers int
	DefaultNetwork          NetworkMode
	DefaultResources        ResourceSpec
	EnableMetrics           bool
}
