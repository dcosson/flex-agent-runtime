package direct

import "context"

// DestroySandbox terminates the underlying EC2 instance and removes the
// sandbox from in-memory tracking.
func (d *DirectSandboxControl) DestroySandbox(ctx context.Context, sandboxID string) error {
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

	if err := d.provisioner.TerminateInstance(ctx, instanceID); err != nil {
		return err
	}

	d.mu.Lock()
	delete(d.instances, sandboxID)
	d.mu.Unlock()

	return nil
}
