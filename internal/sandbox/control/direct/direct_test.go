package direct

import (
	"testing"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
)

func TestCapabilities(t *testing.T) {
	d := mustNewDirect(t, nil, nil, Config{
		DefaultInstanceType: "t3.medium",
	})

	caps := d.Capabilities()

	if caps.Snapshots {
		t.Error("Capabilities().Snapshots should be false")
	}
	if caps.Rollback {
		t.Error("Capabilities().Rollback should be false")
	}
	if !caps.Pause {
		t.Error("Capabilities().Pause should be true")
	}
	if !caps.LaunchProcess {
		t.Error("Capabilities().LaunchProcess should be true")
	}
	if caps.DeepPause {
		t.Error("Capabilities().DeepPause should be false")
	}
}

func TestNewDirectSandboxControl_Defaults(t *testing.T) {
	d := mustNewDirect(t, nil, nil, Config{})

	if d.config.InstanceReadyTimeout != defaultInstanceReadyTimeout {
		t.Errorf("default InstanceReadyTimeout = %v, want %v", d.config.InstanceReadyTimeout, defaultInstanceReadyTimeout)
	}
	if d.config.ProcessReadyTimeout != defaultProcessReadyTimeout {
		t.Errorf("default ProcessReadyTimeout = %v, want %v", d.config.ProcessReadyTimeout, defaultProcessReadyTimeout)
	}
	if d.config.RootVolumeType != "gp3" {
		t.Errorf("default RootVolumeType = %q, want %q", d.config.RootVolumeType, "gp3")
	}
	if d.config.IPSelectionMode != "public" {
		t.Errorf("default IPSelectionMode = %q, want %q", d.config.IPSelectionMode, "public")
	}
	if d.instances == nil {
		t.Error("instances map should be initialized")
	}
	if d.closing {
		t.Error("closing should be false initially")
	}
}

func TestNewDirectSandboxControl_WithLogger(t *testing.T) {
	// Verify WithLogger option does not panic with nil
	d := mustNewDirect(t, nil, nil, Config{}, WithLogger(nil))
	if d.logger == nil {
		t.Error("logger should not be nil after WithLogger(nil)")
	}
}

func TestInstanceStatus_Constants(t *testing.T) {
	// Verify instance status constants are distinct.
	statuses := []instanceStatus{statusProvisioning, statusRunning, statusStopped}
	seen := make(map[instanceStatus]bool)
	for _, s := range statuses {
		if seen[s] {
			t.Errorf("duplicate instanceStatus value: %q", s)
		}
		seen[s] = true
	}
}

func TestInstanceState_Structure(t *testing.T) {
	// Verify instanceState can be constructed with all fields.
	is := &instanceState{
		instanceID: "i-0abc123",
		publicIP:   "1.2.3.4",
		privateIP:  "10.0.1.5",
		status:     statusRunning,
		processes: map[string]*processState{
			"proc-1": {
				processID: "proc-1",
				pid:       1234,
				startTime: 9876543210,
				binary:    "/usr/local/bin/flexagent",
				args:      []string{"serve", "agent"},
				port:      8081,
				status:    control.ProcessRunning,
			},
		},
	}

	if is.instanceID != "i-0abc123" {
		t.Errorf("instanceID = %q, want %q", is.instanceID, "i-0abc123")
	}
	if is.status != statusRunning {
		t.Errorf("status = %q, want %q", is.status, statusRunning)
	}
	if len(is.processes) != 1 {
		t.Errorf("processes count = %d, want 1", len(is.processes))
	}
	proc := is.processes["proc-1"]
	if proc.pid != 1234 {
		t.Errorf("process pid = %d, want 1234", proc.pid)
	}
}

func TestUserDataTemplateData(t *testing.T) {
	td := UserDataTemplateData{
		SandboxID:    "direct:i-0abc123",
		Labels:       map[string]string{"env": "staging"},
		WorkspaceDir: "/workspace",
	}

	if td.SandboxID != "direct:i-0abc123" {
		t.Errorf("SandboxID = %q, want %q", td.SandboxID, "direct:i-0abc123")
	}
	if td.WorkspaceDir != "/workspace" {
		t.Errorf("WorkspaceDir = %q, want %q", td.WorkspaceDir, "/workspace")
	}
}

func TestDirectSandboxControl_InternalState(t *testing.T) {
	// Verify the mutex, closing flag, and inflightWg are accessible
	// from internal tests (used by CreateSandbox/Close in later tasks).
	d := mustNewDirect(t, nil, nil, Config{})

	d.mu.Lock()
	d.closing = true
	d.mu.Unlock()

	d.mu.Lock()
	if !d.closing {
		t.Error("closing flag should be true after setting it")
	}
	d.mu.Unlock()

	// inflightWg should be usable
	d.inflightWg.Add(1)
	d.inflightWg.Done()
}
