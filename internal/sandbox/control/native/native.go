// Package native implements NativeSandboxControl, which wraps our
// sandbox-host RPC service to satisfy the SandboxControl interface.
package native

import (
	"context"
	"log/slog"

	"github.com/anthropics/flex-agent-runtime/internal/rpc/api"
	"github.com/anthropics/flex-agent-runtime/internal/sandbox/control"
)

// Option configures a NativeSandboxControl.
type Option func(*NativeSandboxControl)

// WithLogger sets the structured logger.
func WithLogger(logger *slog.Logger) Option {
	return func(n *NativeSandboxControl) {
		if logger != nil {
			n.logger = logger
		}
	}
}

// WithDefaultQuota sets the default disk quota (bytes) for new sandboxes.
func WithDefaultQuota(bytes int64) Option {
	return func(n *NativeSandboxControl) { n.defaultQuota = bytes }
}

// NativeSandboxControl wraps a sandbox-host RPC client to provide
// SandboxControl for native (on-premise gVisor + ZFS) sandboxes.
type NativeSandboxControl struct {
	client       api.SandboxService
	logger       *slog.Logger
	defaultQuota int64
}

// NewNativeSandboxControl creates a NativeSandboxControl wrapping the given
// sandbox-host RPC client.
func NewNativeSandboxControl(client api.SandboxService, opts ...Option) *NativeSandboxControl {
	n := &NativeSandboxControl{
		client: client,
		logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(n)
	}
	return n
}

// CreateSandbox provisions a new sandbox by calling CreateSession on the
// sandbox-host. Field mapping:
//   - Template  → BaseSnapshot
//   - Labels    → Labels
//   - Resources → logged (resource limits set at sandbox-host level)
func (n *NativeSandboxControl) CreateSandbox(ctx context.Context, req control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
	n.logger.DebugContext(ctx, "creating sandbox",
		"template", req.Template,
		"cpus", req.Resources.CPUs,
		"mem_mb", req.Resources.MemMB,
	)

	resp, err := n.client.CreateSession(ctx, &api.CreateSessionRequest{
		BaseSnapshot: req.Template,
		Labels:       req.Labels,
		Quota:        n.defaultQuota,
	})
	if err != nil {
		return nil, err
	}

	return &control.CreateSandboxResponse{
		SandboxID:    resp.Session.ID,
		Address:      resp.Session.Mountpoint,
		Capabilities: n.Capabilities(),
	}, nil
}

// DestroySandbox tears down a sandbox by calling DestroySession.
func (n *NativeSandboxControl) DestroySandbox(ctx context.Context, sandboxID string) error {
	_, err := n.client.DestroySession(ctx, &api.DestroySessionRequest{
		SessionID: sandboxID,
	})
	return err
}

// LaunchProcess starts a long-running process inside the sandbox by calling
// the sandbox-host LaunchProcess RPC.
func (n *NativeSandboxControl) LaunchProcess(ctx context.Context, req control.LaunchProcessRequest) (*control.LaunchProcessResponse, error) {
	resp, err := n.client.LaunchProcess(ctx, &api.LaunchProcessRequest{
		SessionID:  req.SandboxID,
		Binary:     req.Binary,
		Args:       req.Args,
		Env:        req.Env,
		ExposePort: req.ExposePort,
	})
	if err != nil {
		return nil, err
	}

	return &control.LaunchProcessResponse{
		ProcessID: resp.ProcessID,
		Address:   resp.Address,
		Status:    control.ProcessStatus(resp.Status),
	}, nil
}

// KillProcess sends a signal to a running process via the sandbox-host.
func (n *NativeSandboxControl) KillProcess(ctx context.Context, req control.KillProcessRequest) error {
	_, err := n.client.KillProcess(ctx, &api.KillProcessRequest{
		SessionID: req.SandboxID,
		ProcessID: req.ProcessID,
		Signal:    req.Signal,
	})
	return err
}

// GetProcessStatus queries the status of a launched process.
func (n *NativeSandboxControl) GetProcessStatus(ctx context.Context, req control.GetProcessStatusRequest) (*control.GetProcessStatusResponse, error) {
	resp, err := n.client.GetProcessStatus(ctx, &api.GetProcessStatusRequest{
		SessionID: req.SandboxID,
		ProcessID: req.ProcessID,
	})
	if err != nil {
		return nil, err
	}

	return &control.GetProcessStatusResponse{
		Status:   control.ProcessStatus(resp.Status),
		ExitCode: resp.ExitCode,
	}, nil
}

// PauseSandbox pauses a sandbox by calling PauseSession.
func (n *NativeSandboxControl) PauseSandbox(ctx context.Context, sandboxID string) error {
	_, err := n.client.PauseSession(ctx, &api.PauseSessionRequest{
		SessionID: sandboxID,
	})
	return err
}

// ResumeSandbox resumes a paused sandbox by calling ResumeSession.
func (n *NativeSandboxControl) ResumeSandbox(ctx context.Context, sandboxID string) error {
	_, err := n.client.ResumeSession(ctx, &api.ResumeSessionRequest{
		SessionID: sandboxID,
	})
	return err
}

// Capabilities returns the native sandbox provider capabilities.
func (n *NativeSandboxControl) Capabilities() control.SandboxCapabilities {
	return control.SandboxCapabilities{
		Snapshots:     true,
		Rollback:      true,
		Pause:         true,
		LaunchProcess: true,
	}
}
