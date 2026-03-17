// Package control defines the SandboxControl interface for orchestrator-level
// sandbox lifecycle management. This is distinct from the agent-loop-level
// ExecutionEnvironment — SandboxControl provisions/destroys sandboxes and
// launches processes, while ExecutionEnvironment executes tools within them.
package control

import (
	"context"
	"fmt"
	"time"
)

// SandboxControl manages sandbox lifecycle. The orchestrator uses this
// to create/destroy sandboxes and launch processes inside them.
type SandboxControl interface {
	// CreateSandbox provisions a new sandbox environment.
	CreateSandbox(ctx context.Context, req CreateSandboxRequest) (*CreateSandboxResponse, error)

	// DestroySandbox tears down a sandbox and releases all resources.
	DestroySandbox(ctx context.Context, sandboxID string) error

	// LaunchProcess starts a long-running process inside an existing sandbox.
	// Returns connectivity info (address, port) for the launched process.
	LaunchProcess(ctx context.Context, req LaunchProcessRequest) (*LaunchProcessResponse, error)

	// KillProcess terminates a previously launched process.
	KillProcess(ctx context.Context, req KillProcessRequest) error

	// GetProcessStatus returns the current status of a launched process.
	GetProcessStatus(ctx context.Context, req GetProcessStatusRequest) (*GetProcessStatusResponse, error)

	// PauseSandbox pauses a sandbox (provider-specific semantics).
	PauseSandbox(ctx context.Context, sandboxID string) error

	// ResumeSandbox resumes a paused sandbox.
	ResumeSandbox(ctx context.Context, sandboxID string) error

	// Capabilities returns what this provider supports.
	Capabilities() SandboxCapabilities
}

// ResourceSpec specifies CPU and memory limits for a sandbox.
type ResourceSpec struct {
	CPUs  float64
	MemMB int
}

// CreateSandboxRequest configures a new sandbox.
type CreateSandboxRequest struct {
	Labels    map[string]string
	Template  string       // Provider-specific template/base image.
	Resources ResourceSpec // CPU, memory limits.
}

// CreateSandboxResponse is returned after sandbox creation.
type CreateSandboxResponse struct {
	SandboxID    string
	Address      string // How to reach this sandbox ("host:port").
	Capabilities SandboxCapabilities
}

// LaunchProcessRequest starts a long-running process in an existing sandbox.
type LaunchProcessRequest struct {
	SandboxID  string
	Binary     string            // Path to binary or command name.
	Args       []string          // Command arguments.
	Env        map[string]string // Environment variables (including API keys).
	ExposePort int               // Port the process will listen on inside sandbox.
}

// LaunchProcessResponse is returned after launching a process.
type LaunchProcessResponse struct {
	ProcessID string
	Address   string        // How to reach the launched process ("host:port").
	Status    ProcessStatus // Initial status.
}

// KillProcessRequest terminates a running process.
type KillProcessRequest struct {
	SandboxID string
	ProcessID string
	Signal    int // Unix signal (default SIGTERM if 0).
}

// GetProcessStatusRequest queries a process's current state.
type GetProcessStatusRequest struct {
	SandboxID string
	ProcessID string
}

// GetProcessStatusResponse returns process status and exit code.
type GetProcessStatusResponse struct {
	Status   ProcessStatus
	ExitCode *int // Only set if Status == ProcessExited.
}

// ProcessStatus indicates the lifecycle state of a launched process.
type ProcessStatus string

const (
	ProcessStarting ProcessStatus = "starting"
	ProcessRunning  ProcessStatus = "running"
	ProcessExited   ProcessStatus = "exited"
)

// SandboxCapabilities reports what a sandbox provider supports.
type SandboxCapabilities struct {
	Snapshots           bool
	Rollback            bool
	Pause               bool
	LaunchProcess       bool
	DeepPause           bool          // ZFS-to-S3 style cold storage (future).
	ConcurrentSandboxes int           // Max concurrent sandbox instances (0 = unlimited).
	MaxSandboxDuration  time.Duration // Max sandbox lifetime (0 = unlimited).
}

// AddressToURL converts a "host:port" address string to an HTTP URL.
func AddressToURL(addr string) string {
	return fmt.Sprintf("http://%s", addr)
}
