// Package cloud provides placeholder stubs for cloud-based SandboxControl
// providers (E2B, Daytona, Fly). These are deferred to a future batch.
package cloud

import (
	"context"
	"fmt"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
)

// CloudSandboxControl is a placeholder for cloud-based sandbox providers.
// All methods return ErrNotImplemented.
type CloudSandboxControl struct {
	provider string
}

// ErrNotImplemented is returned by all CloudSandboxControl methods.
var ErrNotImplemented = fmt.Errorf("cloud sandbox control not yet implemented")

// NewCloudSandboxControl creates a placeholder cloud provider stub.
func NewCloudSandboxControl(provider string) *CloudSandboxControl {
	return &CloudSandboxControl{provider: provider}
}

func (c *CloudSandboxControl) CreateSandbox(_ context.Context, _ control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
	return nil, fmt.Errorf("%s: %w", c.provider, ErrNotImplemented)
}

func (c *CloudSandboxControl) DestroySandbox(_ context.Context, _ string) error {
	return fmt.Errorf("%s: %w", c.provider, ErrNotImplemented)
}

func (c *CloudSandboxControl) LaunchProcess(_ context.Context, _ control.LaunchProcessRequest) (*control.LaunchProcessResponse, error) {
	return nil, fmt.Errorf("%s: %w", c.provider, ErrNotImplemented)
}

func (c *CloudSandboxControl) KillProcess(_ context.Context, _ control.KillProcessRequest) error {
	return fmt.Errorf("%s: %w", c.provider, ErrNotImplemented)
}

func (c *CloudSandboxControl) GetProcessStatus(_ context.Context, _ control.GetProcessStatusRequest) (*control.GetProcessStatusResponse, error) {
	return nil, fmt.Errorf("%s: %w", c.provider, ErrNotImplemented)
}

func (c *CloudSandboxControl) PauseSandbox(_ context.Context, _ string) error {
	return fmt.Errorf("%s: %w", c.provider, ErrNotImplemented)
}

func (c *CloudSandboxControl) ResumeSandbox(_ context.Context, _ string) error {
	return fmt.Errorf("%s: %w", c.provider, ErrNotImplemented)
}

func (c *CloudSandboxControl) Capabilities() control.SandboxCapabilities {
	return control.SandboxCapabilities{}
}
