package cloud

import (
	"context"
	"errors"
	"testing"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
)

// SC4: CloudSandboxControl placeholder tests.

func TestCloudSandboxControl_InterfaceCompliance(t *testing.T) {
	var _ control.SandboxControl = NewCloudSandboxControl("e2b")
}

func TestCloudSandboxControl_AllMethodsReturnNotImplemented(t *testing.T) {
	providers := []string{"e2b", "daytona", "fly"}
	ctx := context.Background()

	for _, p := range providers {
		t.Run(p, func(t *testing.T) {
			ctrl := NewCloudSandboxControl(p)

			_, err := ctrl.CreateSandbox(ctx, control.CreateSandboxRequest{})
			if !errors.Is(err, ErrNotImplemented) {
				t.Errorf("CreateSandbox: want ErrNotImplemented, got %v", err)
			}

			err = ctrl.DestroySandbox(ctx, "id")
			if !errors.Is(err, ErrNotImplemented) {
				t.Errorf("DestroySandbox: want ErrNotImplemented, got %v", err)
			}

			_, err = ctrl.LaunchProcess(ctx, control.LaunchProcessRequest{})
			if !errors.Is(err, ErrNotImplemented) {
				t.Errorf("LaunchProcess: want ErrNotImplemented, got %v", err)
			}

			err = ctrl.KillProcess(ctx, control.KillProcessRequest{})
			if !errors.Is(err, ErrNotImplemented) {
				t.Errorf("KillProcess: want ErrNotImplemented, got %v", err)
			}

			_, err = ctrl.GetProcessStatus(ctx, control.GetProcessStatusRequest{})
			if !errors.Is(err, ErrNotImplemented) {
				t.Errorf("GetProcessStatus: want ErrNotImplemented, got %v", err)
			}

			err = ctrl.PauseSandbox(ctx, "id")
			if !errors.Is(err, ErrNotImplemented) {
				t.Errorf("PauseSandbox: want ErrNotImplemented, got %v", err)
			}

			err = ctrl.ResumeSandbox(ctx, "id")
			if !errors.Is(err, ErrNotImplemented) {
				t.Errorf("ResumeSandbox: want ErrNotImplemented, got %v", err)
			}

			caps := ctrl.Capabilities()
			if caps.Snapshots || caps.Rollback || caps.Pause || caps.LaunchProcess {
				t.Error("cloud stub should report no capabilities")
			}
		})
	}
}
