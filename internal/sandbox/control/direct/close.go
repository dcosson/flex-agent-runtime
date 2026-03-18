package direct

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Close shuts down the adapter. It blocks until in-flight CreateSandbox calls
// finish. If TerminateOnClose is set, managed instances are terminated.
func (d *DirectSandboxControl) Close() error {
	d.mu.Lock()
	d.closing = true
	terminateOnClose := d.config.TerminateOnClose
	d.mu.Unlock()

	d.inflightWg.Wait()

	var terminateErrs []error
	if terminateOnClose {
		instanceIDs := d.snapshotInstanceIDs()
		for _, instanceID := range instanceIDs {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := d.provisioner.TerminateInstance(ctx, instanceID)
			cancel()
			if err != nil {
				terminateErrs = append(terminateErrs, fmt.Errorf("terminate %s: %w", instanceID, err))
			}
		}
	}

	d.mu.Lock()
	d.instances = nil
	d.mu.Unlock()

	if len(terminateErrs) > 0 {
		return errors.Join(terminateErrs...)
	}
	return nil
}

func (d *DirectSandboxControl) snapshotInstanceIDs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	ids := make([]string, 0, len(d.instances))
	for _, st := range d.instances {
		if st == nil || st.instanceID == "" {
			continue
		}
		ids = append(ids, st.instanceID)
	}
	return ids
}
