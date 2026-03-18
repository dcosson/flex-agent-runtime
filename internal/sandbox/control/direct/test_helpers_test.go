package direct

import (
	"testing"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
)

func mustNewDirect(t *testing.T, provisioner instance.InstanceProvisioner, ssmClient SSMAPI, cfg Config, opts ...Option) *DirectSandboxControl {
	t.Helper()
	d, err := NewDirectSandboxControl(provisioner, ssmClient, cfg, opts...)
	if err != nil {
		t.Fatalf("NewDirectSandboxControl() error = %v", err)
	}
	return d
}
