package direct

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
)

const processRecoverCommand = `
for f in /var/run/flex-agent-*.pid; do
	[ -f "$f" ] || continue
	PROC_ID="${f##*/flex-agent-}"
	PROC_ID="${PROC_ID%.pid}"
	PID="$(sed -n '1p' "$f" 2>/dev/null)"
	START="$(sed -n '2p' "$f" 2>/dev/null)"
	[ -n "$PID" ] && [ -n "$START" ] || continue
	if [ -f "/proc/$PID/stat" ]; then
		CUR="$(awk '{print $22}' "/proc/$PID/stat" 2>/dev/null || true)"
		if [ "$CUR" = "$START" ]; then
			echo "$PROC_ID $PID $START"
		fi
	fi
done
`

// Recover rebuilds in-memory instance/process state from cloud inventory.
func (d *DirectSandboxControl) Recover(ctx context.Context) error {
	infos, err := d.provisioner.ListInstances(ctx, instance.InstanceFilter{
		Tags: map[string]string{
			"ManagedBy": "flex-agent-runtime",
			"adapter":   "direct",
		},
		States: []instance.CloudInstanceState{
			instance.CloudInstanceRunning,
			instance.CloudInstanceStopped,
		},
	})
	if err != nil {
		return fmt.Errorf("direct: recover list instances: %w", err)
	}

	recovered := make(map[string]*instanceState, len(infos))
	totalRecoveredProcesses := 0
	for _, info := range infos {
		if info.InstanceID == "" {
			continue
		}

		sandboxID := recoverSandboxID(info)
		st := &instanceState{
			instanceID: info.InstanceID,
			publicIP:   info.PublicIP,
			privateIP:  info.PrivateIP,
			status:     recoverInstanceStatus(info.State),
			processes:  make(map[string]*processState),
		}

		if info.State == instance.CloudInstanceRunning && d.ssmClient != nil {
			procs, procErr := d.recoverProcesses(ctx, info.InstanceID)
			if procErr != nil {
				d.logger.Warn("failed to recover instance processes", "instance_id", info.InstanceID, "error", procErr)
			} else {
				st.processes = procs
				totalRecoveredProcesses += len(procs)
			}
		}

		recovered[sandboxID] = st
	}

	d.mu.Lock()
	d.instances = recovered
	d.mu.Unlock()

	d.logger.Info("recovered direct adapter state",
		"instances", len(recovered),
		"processes", totalRecoveredProcesses,
	)
	return nil
}

func recoverSandboxID(info instance.InstanceInfo) string {
	if info.Tags != nil {
		if id := info.Tags["flex-sandbox-id"]; id != "" {
			if strings.HasPrefix(id, sandboxIDPrefix) {
				return id
			}
			return encodeSandboxID(id)
		}
	}
	return encodeSandboxID(info.InstanceID)
}

func recoverInstanceStatus(state instance.CloudInstanceState) instanceStatus {
	switch state {
	case instance.CloudInstanceRunning:
		return statusRunning
	case instance.CloudInstancePending:
		return statusProvisioning
	default:
		return statusStopped
	}
}

func (d *DirectSandboxControl) recoverProcesses(ctx context.Context, instanceID string) (map[string]*processState, error) {
	inv, err := d.runShellCommand(ctx, instanceID, processRecoverCommand, processLaunchCommandTimeout)
	if err != nil {
		return nil, err
	}
	if inv.Status != ssmtypes.CommandInvocationStatusSuccess {
		return nil, fmt.Errorf("%w: process recovery command failed", ErrSSMUnavailable)
	}

	processes := make(map[string]*processState)
	lines := strings.Split(strings.TrimSpace(aws.ToString(inv.StandardOutputContent)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}

		pid, pidErr := strconv.Atoi(fields[1])
		startTime, stErr := strconv.ParseInt(fields[2], 10, 64)
		if pidErr != nil || stErr != nil {
			continue
		}

		processID := fields[0]
		processes[processID] = &processState{
			processID: processID,
			pid:       pid,
			startTime: startTime,
			status:    control.ProcessRunning,
		}
	}
	return processes, nil
}
