package direct

import (
	"context"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
)

// PauseSandbox stops the backing EC2 instance. This is a lossy pause:
// processes do not survive and are marked exited.
func (d *DirectSandboxControl) PauseSandbox(ctx context.Context, sandboxID string) error {
	instanceID, err := parseSandboxID(sandboxID)
	if err != nil {
		return err
	}

	d.mu.Lock()
	st, ok := d.instances[sandboxID]
	d.mu.Unlock()
	if !ok {
		return ErrInstanceNotFound
	}
	if st != nil && st.instanceID != "" {
		instanceID = st.instanceID
	}

	if err := d.provisioner.StopInstance(ctx, instanceID); err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	st, ok = d.instances[sandboxID]
	if !ok {
		return ErrInstanceNotFound
	}
	st.status = statusStopped
	for _, proc := range st.processes {
		proc.status = control.ProcessExited
	}

	return nil
}

// ResumeSandbox starts a previously stopped EC2 instance, waits for cloud and
// SSM readiness, refreshes IPs, and clears process state.
func (d *DirectSandboxControl) ResumeSandbox(ctx context.Context, sandboxID string) error {
	instanceID, err := parseSandboxID(sandboxID)
	if err != nil {
		return err
	}

	d.mu.Lock()
	st, ok := d.instances[sandboxID]
	d.mu.Unlock()
	if !ok {
		return ErrInstanceNotFound
	}
	if st != nil && st.instanceID != "" {
		instanceID = st.instanceID
	}

	if _, err := d.provisioner.StartInstance(ctx, instanceID); err != nil {
		return err
	}

	deadline := time.Now().Add(d.config.InstanceReadyTimeout)
	status, err := d.waitForInstanceReady(ctx, instanceID, deadline)
	if err != nil {
		return err
	}
	if err := d.waitForSSMRegistration(ctx, instanceID, deadline); err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	st, ok = d.instances[sandboxID]
	if !ok {
		return ErrInstanceNotFound
	}
	st.publicIP = status.PublicIP
	st.privateIP = status.PrivateIP
	st.status = statusRunning
	st.processes = make(map[string]*processState)
	return nil
}
