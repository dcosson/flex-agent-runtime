package direct

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
)

const (
	processLaunchCommandTimeout = 30 * time.Second
	ssmPollInitialDelay         = 250 * time.Millisecond
	ssmPollMaxDelay             = 2 * time.Second
	healthPollInitialDelay      = 500 * time.Millisecond
	healthPollMaxDelay          = 5 * time.Second
	defaultSIGTERM              = 15
)

// LaunchProcess starts a long-running process inside an existing sandbox using SSM.
func (d *DirectSandboxControl) LaunchProcess(ctx context.Context, req control.LaunchProcessRequest) (*control.LaunchProcessResponse, error) {
	instanceID, err := parseSandboxID(req.SandboxID)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	state, ok := d.instances[req.SandboxID]
	if !ok {
		d.mu.Unlock()
		return nil, ErrInstanceNotFound
	}
	if state.status != statusRunning {
		d.mu.Unlock()
		return nil, ErrInstanceNotReady
	}
	d.mu.Unlock()

	processID := newProcessID()

	launchCommand := buildSSMCommand(processID, req.Binary, req.Args, req.Env)
	launchInv, err := d.runShellCommand(ctx, instanceID, launchCommand, processLaunchCommandTimeout)
	if err != nil {
		return nil, err
	}
	if launchInv.Status != ssmtypes.CommandInvocationStatusSuccess {
		return nil, ErrProcessLaunchFailed
	}

	pid, startTime, err := d.readPIDFile(ctx, instanceID, processID)
	if err != nil {
		return nil, err
	}

	if err := d.waitForProcessHealth(ctx, instanceID, req.ExposePort); err != nil {
		return nil, err
	}

	d.mu.Lock()
	state, ok = d.instances[req.SandboxID]
	if !ok {
		d.mu.Unlock()
		return nil, ErrInstanceNotFound
	}
	if state.processes == nil {
		state.processes = make(map[string]*processState)
	}
	state.processes[processID] = &processState{
		processID: processID,
		pid:       pid,
		startTime: startTime,
		binary:    req.Binary,
		args:      append([]string(nil), req.Args...),
		port:      req.ExposePort,
		status:    control.ProcessRunning,
	}
	addressIP := selectAddressIP(state.publicIP, state.privateIP, d.config.IPSelectionMode)
	d.mu.Unlock()

	return &control.LaunchProcessResponse{
		ProcessID: processID,
		Address:   fmt.Sprintf("%s:%d", addressIP, req.ExposePort),
		Status:    control.ProcessRunning,
	}, nil
}

// KillProcess terminates a launched process using SSM.
func (d *DirectSandboxControl) KillProcess(ctx context.Context, req control.KillProcessRequest) error {
	instanceID, err := parseSandboxID(req.SandboxID)
	if err != nil {
		return err
	}

	d.mu.Lock()
	state, ok := d.instances[req.SandboxID]
	if !ok {
		d.mu.Unlock()
		return ErrInstanceNotFound
	}
	proc, ok := state.processes[req.ProcessID]
	if !ok {
		d.mu.Unlock()
		return ErrProcessNotFound
	}
	expectedStart := proc.startTime
	pid := proc.pid
	d.mu.Unlock()

	currentStart, exists, err := d.readProcStartTime(ctx, instanceID, pid)
	if err != nil {
		return err
	}
	if !exists || currentStart != expectedStart {
		d.markProcessExited(req.SandboxID, req.ProcessID)
		return ErrProcessNotFound
	}

	sig := req.Signal
	if sig == 0 {
		sig = defaultSIGTERM
	}

	killCmd := fmt.Sprintf("kill -%d %d", sig, pid)
	killInv, err := d.runShellCommand(ctx, instanceID, killCmd, processLaunchCommandTimeout)
	if err != nil {
		return err
	}
	if killInv.Status != ssmtypes.CommandInvocationStatusSuccess {
		return fmt.Errorf("%w: kill command failed", ErrSSMUnavailable)
	}

	cleanupCmd := fmt.Sprintf("rm -f %s %s", shellQuote(pidFilePath(req.ProcessID)), shellQuote(statusFilePath(req.ProcessID)))
	if cleanupInv, cleanupErr := d.runShellCommand(ctx, instanceID, cleanupCmd, processLaunchCommandTimeout); cleanupErr != nil || cleanupInv.Status != ssmtypes.CommandInvocationStatusSuccess {
		d.logger.Warn("failed to clean up process marker files", "sandbox_id", req.SandboxID, "process_id", req.ProcessID, "error", cleanupErr)
	}

	d.markProcessExited(req.SandboxID, req.ProcessID)
	return nil
}

// GetProcessStatus returns the current status of a launched process.
func (d *DirectSandboxControl) GetProcessStatus(ctx context.Context, req control.GetProcessStatusRequest) (*control.GetProcessStatusResponse, error) {
	instanceID, err := parseSandboxID(req.SandboxID)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	state, ok := d.instances[req.SandboxID]
	if !ok {
		d.mu.Unlock()
		return nil, ErrInstanceNotFound
	}
	proc, ok := state.processes[req.ProcessID]
	if !ok {
		d.mu.Unlock()
		return nil, ErrProcessNotFound
	}
	if proc.status == control.ProcessExited {
		d.mu.Unlock()
		return &control.GetProcessStatusResponse{Status: control.ProcessExited}, nil
	}
	pid := proc.pid
	expectedStart := proc.startTime
	d.mu.Unlock()

	checkCmd := fmt.Sprintf(
		"if [ -f /proc/%d/stat ]; then echo \"running $(awk '{print $22}' /proc/%d/stat)\"; else echo exited; fi",
		pid, pid,
	)
	inv, err := d.runShellCommand(ctx, instanceID, checkCmd, processLaunchCommandTimeout)
	if err != nil {
		return nil, err
	}
	if inv.Status != ssmtypes.CommandInvocationStatusSuccess {
		return nil, fmt.Errorf("%w: status query failed", ErrSSMUnavailable)
	}

	fields := strings.Fields(strings.TrimSpace(aws.ToString(inv.StandardOutputContent)))
	if len(fields) == 0 || strings.EqualFold(fields[0], "exited") {
		d.markProcessExited(req.SandboxID, req.ProcessID)
		return &control.GetProcessStatusResponse{Status: control.ProcessExited}, nil
	}

	if len(fields) < 2 || !strings.EqualFold(fields[0], "running") {
		d.markProcessExited(req.SandboxID, req.ProcessID)
		return &control.GetProcessStatusResponse{Status: control.ProcessExited}, nil
	}

	currentStart, parseErr := strconv.ParseInt(fields[1], 10, 64)
	if parseErr != nil || currentStart != expectedStart {
		d.markProcessExited(req.SandboxID, req.ProcessID)
		return &control.GetProcessStatusResponse{Status: control.ProcessExited}, nil
	}

	return &control.GetProcessStatusResponse{Status: control.ProcessRunning}, nil
}

func (d *DirectSandboxControl) waitForProcessHealth(ctx context.Context, instanceID string, port int) error {
	deadline := time.Now().Add(d.config.ProcessReadyTimeout)
	delay := healthPollInitialDelay
	checkCmd := fmt.Sprintf("curl -s -o /dev/null -w '%%{http_code}' http://localhost:%d/health || true", port)

	for {
		inv, err := d.runShellCommand(ctx, instanceID, checkCmd, processLaunchCommandTimeout)
		if err == nil && inv.Status == ssmtypes.CommandInvocationStatusSuccess {
			code := strings.TrimSpace(aws.ToString(inv.StandardOutputContent))
			if code == "200" {
				return nil
			}
		}

		if time.Now().After(deadline) {
			return ErrProcessReadyTimeout
		}
		if err := d.processPollSleep(ctx, delay, deadline); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return ErrProcessReadyTimeout
		}
		delay = nextBackoffDelay(delay, healthPollMaxDelay)
	}
}

func (d *DirectSandboxControl) processPollSleep(ctx context.Context, baseDelay time.Duration, deadline time.Time) error {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return ErrProcessReadyTimeout
	}

	sleepFor := jitterDelay(baseDelay)
	if sleepFor > remaining {
		sleepFor = remaining
	}
	if sleepFor <= 0 {
		return nil
	}

	timer := time.NewTimer(sleepFor)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (d *DirectSandboxControl) runShellCommand(ctx context.Context, instanceID, command string, timeout time.Duration) (*ssm.GetCommandInvocationOutput, error) {
	timeoutSeconds := int32(timeout / time.Second)
	if timeoutSeconds <= 0 {
		timeoutSeconds = 1
	}

	sendOut, err := d.ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
		DocumentName:   aws.String("AWS-RunShellScript"),
		InstanceIds:    []string{instanceID},
		Parameters:     map[string][]string{"commands": {command}},
		TimeoutSeconds: aws.Int32(timeoutSeconds),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: send command: %v", ErrSSMUnavailable, err)
	}
	if sendOut == nil || sendOut.Command == nil || sendOut.Command.CommandId == nil || *sendOut.Command.CommandId == "" {
		return nil, fmt.Errorf("%w: missing command id", ErrSSMUnavailable)
	}

	return d.waitForCommandInvocation(ctx, instanceID, *sendOut.Command.CommandId, timeout)
}

func (d *DirectSandboxControl) waitForCommandInvocation(ctx context.Context, instanceID, commandID string, timeout time.Duration) (*ssm.GetCommandInvocationOutput, error) {
	deadline := time.Now().Add(timeout)
	delay := ssmPollInitialDelay

	for {
		inv, err := d.ssmClient.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
			CommandId:  aws.String(commandID),
			InstanceId: aws.String(instanceID),
		})
		if err == nil && inv != nil && isInvocationTerminal(inv.Status) {
			return inv, nil
		}

		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, ErrSSMTimeout
		}

		if sleepErr := d.pollSleep(ctx, delay, deadline); sleepErr != nil {
			if errors.Is(sleepErr, ErrInstanceReadyTimeout) {
				return nil, ErrSSMTimeout
			}
			return nil, sleepErr
		}
		delay = nextBackoffDelay(delay, ssmPollMaxDelay)
	}
}

func isInvocationTerminal(status ssmtypes.CommandInvocationStatus) bool {
	switch status {
	case ssmtypes.CommandInvocationStatusSuccess,
		ssmtypes.CommandInvocationStatusCancelled,
		ssmtypes.CommandInvocationStatusTimedOut,
		ssmtypes.CommandInvocationStatusFailed:
		return true
	default:
		return false
	}
}

func (d *DirectSandboxControl) readPIDFile(ctx context.Context, instanceID, processID string) (int, int64, error) {
	readCmd := fmt.Sprintf("cat %s", shellQuote(pidFilePath(processID)))
	inv, err := d.runShellCommand(ctx, instanceID, readCmd, processLaunchCommandTimeout)
	if err != nil {
		return 0, 0, err
	}
	if inv.Status != ssmtypes.CommandInvocationStatusSuccess {
		return 0, 0, ErrProcessLaunchFailed
	}

	fields := strings.Fields(strings.TrimSpace(aws.ToString(inv.StandardOutputContent)))
	if len(fields) < 2 {
		return 0, 0, ErrProcessLaunchFailed
	}

	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, ErrProcessLaunchFailed
	}
	startTime, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, 0, ErrProcessLaunchFailed
	}

	return pid, startTime, nil
}

func (d *DirectSandboxControl) readProcStartTime(ctx context.Context, instanceID string, pid int) (int64, bool, error) {
	checkCmd := fmt.Sprintf("if [ -f /proc/%d/stat ]; then awk '{print $22}' /proc/%d/stat; else echo exited; fi", pid, pid)
	inv, err := d.runShellCommand(ctx, instanceID, checkCmd, processLaunchCommandTimeout)
	if err != nil {
		return 0, false, err
	}
	if inv.Status != ssmtypes.CommandInvocationStatusSuccess {
		return 0, false, fmt.Errorf("%w: start-time check failed", ErrSSMUnavailable)
	}

	out := strings.TrimSpace(aws.ToString(inv.StandardOutputContent))
	if out == "" || strings.EqualFold(out, "exited") {
		return 0, false, nil
	}

	startTime, parseErr := strconv.ParseInt(out, 10, 64)
	if parseErr != nil {
		return 0, false, nil
	}
	return startTime, true, nil
}

func (d *DirectSandboxControl) markProcessExited(sandboxID, processID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	state, ok := d.instances[sandboxID]
	if !ok {
		return
	}
	proc, ok := state.processes[processID]
	if !ok {
		return
	}
	proc.status = control.ProcessExited
}

func pidFilePath(processID string) string {
	return fmt.Sprintf("/var/run/flex-agent-%s.pid", processID)
}

func statusFilePath(processID string) string {
	return fmt.Sprintf("/var/run/flex-agent-%s.status", processID)
}

func newProcessID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("proc-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("proc-%x", buf[:])
}
