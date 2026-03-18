package direct

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
)

type ssmSendPlan struct {
	commandID  string
	invocation *ssm.GetCommandInvocationOutput
}

type processMockSSM struct {
	mu sync.Mutex

	sendPlans  []ssmSendPlan
	sendCalls  int
	sendInputs []*ssm.SendCommandInput

	defaultInvocation *ssm.GetCommandInvocationOutput
	invocations       map[string]*ssm.GetCommandInvocationOutput
}

func newProcessMockSSM(plans []ssmSendPlan) *processMockSSM {
	return &processMockSSM{
		sendPlans:   plans,
		invocations: make(map[string]*ssm.GetCommandInvocationOutput),
	}
}

func (m *processMockSSM) SendCommand(_ context.Context, input *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	call := m.sendCalls
	m.sendCalls++
	m.sendInputs = append(m.sendInputs, input)

	commandID := fmt.Sprintf("cmd-%d", call+1)
	if call < len(m.sendPlans) && m.sendPlans[call].commandID != "" {
		commandID = m.sendPlans[call].commandID
	}

	if call < len(m.sendPlans) {
		m.invocations[commandID] = m.sendPlans[call].invocation
	}

	return &ssm.SendCommandOutput{
		Command: &ssmtypes.Command{
			CommandId: aws.String(commandID),
		},
	}, nil
}

func (m *processMockSSM) GetCommandInvocation(_ context.Context, input *ssm.GetCommandInvocationInput, _ ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	commandID := aws.ToString(input.CommandId)
	if inv, ok := m.invocations[commandID]; ok && inv != nil {
		return inv, nil
	}
	if m.defaultInvocation != nil {
		return m.defaultInvocation, nil
	}

	return &ssm.GetCommandInvocationOutput{
		Status:                ssmtypes.CommandInvocationStatusSuccess,
		StandardOutputContent: aws.String(""),
	}, nil
}

func (m *processMockSSM) DescribeInstanceInformation(_ context.Context, _ *ssm.DescribeInstanceInformationInput, _ ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error) {
	return &ssm.DescribeInstanceInformationOutput{}, nil
}

func setupDirectWithRunningSandbox(t *testing.T, ssmClient *processMockSSM) (*DirectSandboxControl, string) {
	t.Helper()

	d := NewDirectSandboxControl(&mockInstanceProvisioner{}, ssmClient, Config{
		IPSelectionMode:     "private",
		ProcessReadyTimeout: 25 * time.Millisecond,
	})
	sandboxID := "direct:i-proc"
	d.instances[sandboxID] = &instanceState{
		instanceID: "i-proc",
		privateIP:  "10.0.1.50",
		publicIP:   "54.0.0.2",
		status:     statusRunning,
		processes:  make(map[string]*processState),
	}
	return d, sandboxID
}

func TestLaunchProcess_HappyPath(t *testing.T) {
	ssmClient := newProcessMockSSM([]ssmSendPlan{
		{
			commandID: "launch",
			invocation: &ssm.GetCommandInvocationOutput{
				Status: ssmtypes.CommandInvocationStatusSuccess,
			},
		},
		{
			commandID: "pid",
			invocation: &ssm.GetCommandInvocationOutput{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: aws.String("1234\n5678\n"),
			},
		},
		{
			commandID: "health",
			invocation: &ssm.GetCommandInvocationOutput{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: aws.String("200"),
			},
		},
	})
	d, sandboxID := setupDirectWithRunningSandbox(t, ssmClient)

	resp, err := d.LaunchProcess(context.Background(), control.LaunchProcessRequest{
		SandboxID:  sandboxID,
		Binary:     "/usr/local/bin/flexagent",
		Args:       []string{"serve", "agent", "--listen=:8081"},
		ExposePort: 8081,
	})
	if err != nil {
		t.Fatalf("LaunchProcess() error = %v", err)
	}
	if resp.ProcessID == "" {
		t.Fatalf("ProcessID is empty")
	}
	if !strings.HasPrefix(resp.ProcessID, "proc-") {
		t.Fatalf("ProcessID = %q, want prefix %q", resp.ProcessID, "proc-")
	}
	if resp.Address != "10.0.1.50:8081" {
		t.Fatalf("Address = %q, want %q", resp.Address, "10.0.1.50:8081")
	}
	if resp.Status != control.ProcessRunning {
		t.Fatalf("Status = %q, want %q", resp.Status, control.ProcessRunning)
	}

	if len(ssmClient.sendInputs) < 3 {
		t.Fatalf("SendCommand calls = %d, want at least 3", len(ssmClient.sendInputs))
	}
	firstCmd := ssmClient.sendInputs[0].Parameters["commands"][0]
	if !strings.Contains(firstCmd, "nohup '/usr/local/bin/flexagent' 'serve' 'agent' '--listen=:8081'") {
		t.Fatalf("launch command missing expected nohup command: %q", firstCmd)
	}

	d.mu.Lock()
	proc := d.instances[sandboxID].processes[resp.ProcessID]
	d.mu.Unlock()
	if proc == nil {
		t.Fatalf("process state not stored for %q", resp.ProcessID)
	}
	if proc.pid != 1234 || proc.startTime != 5678 {
		t.Fatalf("stored pid/start = %d/%d, want 1234/5678", proc.pid, proc.startTime)
	}
}

func TestLaunchProcess_EnvVarsEscaped(t *testing.T) {
	ssmClient := newProcessMockSSM([]ssmSendPlan{
		{
			invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess},
		},
		{
			invocation: &ssm.GetCommandInvocationOutput{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: aws.String("1000\n2000\n"),
			},
		},
		{
			invocation: &ssm.GetCommandInvocationOutput{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: aws.String("200"),
			},
		},
	})
	d, sandboxID := setupDirectWithRunningSandbox(t, ssmClient)

	_, err := d.LaunchProcess(context.Background(), control.LaunchProcessRequest{
		SandboxID: sandboxID,
		Binary:    "/bin/echo",
		Args:      []string{"hello"},
		Env: map[string]string{
			"API_KEY": "value'withquote",
		},
		ExposePort: 8082,
	})
	if err != nil {
		t.Fatalf("LaunchProcess() error = %v", err)
	}

	launchCmd := ssmClient.sendInputs[0].Parameters["commands"][0]
	if !strings.Contains(launchCmd, "export API_KEY='value'\\''withquote'") {
		t.Fatalf("launch command missing escaped env var: %q", launchCmd)
	}
}

func TestLaunchProcess_CommandFailure(t *testing.T) {
	ssmClient := newProcessMockSSM([]ssmSendPlan{
		{
			invocation: &ssm.GetCommandInvocationOutput{
				Status: ssmtypes.CommandInvocationStatusFailed,
			},
		},
	})
	d, sandboxID := setupDirectWithRunningSandbox(t, ssmClient)

	_, err := d.LaunchProcess(context.Background(), control.LaunchProcessRequest{
		SandboxID:  sandboxID,
		Binary:     "/bad/bin",
		ExposePort: 8081,
	})
	if !errors.Is(err, ErrProcessLaunchFailed) {
		t.Fatalf("LaunchProcess() error = %v, want %v", err, ErrProcessLaunchFailed)
	}
}

func TestLaunchProcess_ReadyTimeout(t *testing.T) {
	ssmClient := newProcessMockSSM([]ssmSendPlan{
		{
			invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess},
		},
		{
			invocation: &ssm.GetCommandInvocationOutput{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: aws.String("1234\n5678\n"),
			},
		},
		{
			invocation: &ssm.GetCommandInvocationOutput{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: aws.String("503"),
			},
		},
		{
			invocation: &ssm.GetCommandInvocationOutput{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: aws.String("503"),
			},
		},
	})
	d, sandboxID := setupDirectWithRunningSandbox(t, ssmClient)

	_, err := d.LaunchProcess(context.Background(), control.LaunchProcessRequest{
		SandboxID:  sandboxID,
		Binary:     "/usr/local/bin/flexagent",
		ExposePort: 8081,
	})
	if !errors.Is(err, ErrProcessReadyTimeout) {
		t.Fatalf("LaunchProcess() error = %v, want %v", err, ErrProcessReadyTimeout)
	}
}

func TestKillProcess_HappyPath(t *testing.T) {
	ssmClient := newProcessMockSSM([]ssmSendPlan{
		{
			invocation: &ssm.GetCommandInvocationOutput{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: aws.String("5678"),
			},
		},
		{
			invocation: &ssm.GetCommandInvocationOutput{
				Status: ssmtypes.CommandInvocationStatusSuccess,
			},
		},
		{
			invocation: &ssm.GetCommandInvocationOutput{
				Status: ssmtypes.CommandInvocationStatusSuccess,
			},
		},
	})
	d, sandboxID := setupDirectWithRunningSandbox(t, ssmClient)
	d.instances[sandboxID].processes["proc-1"] = &processState{
		processID: "proc-1",
		pid:       1234,
		startTime: 5678,
		status:    control.ProcessRunning,
	}

	err := d.KillProcess(context.Background(), control.KillProcessRequest{
		SandboxID: sandboxID,
		ProcessID: "proc-1",
	})
	if err != nil {
		t.Fatalf("KillProcess() error = %v", err)
	}

	killCmd := ssmClient.sendInputs[1].Parameters["commands"][0]
	if !strings.Contains(killCmd, "kill -15 1234") {
		t.Fatalf("kill command = %q, want default SIGTERM", killCmd)
	}

	d.mu.Lock()
	status := d.instances[sandboxID].processes["proc-1"].status
	d.mu.Unlock()
	if status != control.ProcessExited {
		t.Fatalf("process status = %q, want %q", status, control.ProcessExited)
	}
}

func TestKillProcess_CustomSignal(t *testing.T) {
	ssmClient := newProcessMockSSM([]ssmSendPlan{
		{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: aws.String("5678")}},
		{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess}},
		{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess}},
	})
	d, sandboxID := setupDirectWithRunningSandbox(t, ssmClient)
	d.instances[sandboxID].processes["proc-2"] = &processState{
		processID: "proc-2",
		pid:       2222,
		startTime: 5678,
		status:    control.ProcessRunning,
	}

	err := d.KillProcess(context.Background(), control.KillProcessRequest{
		SandboxID: sandboxID,
		ProcessID: "proc-2",
		Signal:    9,
	})
	if err != nil {
		t.Fatalf("KillProcess() error = %v", err)
	}

	killCmd := ssmClient.sendInputs[1].Parameters["commands"][0]
	if !strings.Contains(killCmd, "kill -9 2222") {
		t.Fatalf("kill command = %q, want custom signal", killCmd)
	}
}

func TestKillProcess_PIDRecycled(t *testing.T) {
	ssmClient := newProcessMockSSM([]ssmSendPlan{
		{
			invocation: &ssm.GetCommandInvocationOutput{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: aws.String("9999"),
			},
		},
	})
	d, sandboxID := setupDirectWithRunningSandbox(t, ssmClient)
	d.instances[sandboxID].processes["proc-3"] = &processState{
		processID: "proc-3",
		pid:       3333,
		startTime: 5678,
		status:    control.ProcessRunning,
	}

	err := d.KillProcess(context.Background(), control.KillProcessRequest{
		SandboxID: sandboxID,
		ProcessID: "proc-3",
	})
	if !errors.Is(err, ErrProcessNotFound) {
		t.Fatalf("KillProcess() error = %v, want %v", err, ErrProcessNotFound)
	}

	d.mu.Lock()
	status := d.instances[sandboxID].processes["proc-3"].status
	d.mu.Unlock()
	if status != control.ProcessExited {
		t.Fatalf("process status = %q, want %q", status, control.ProcessExited)
	}
	if len(ssmClient.sendInputs) != 1 {
		t.Fatalf("SendCommand calls = %d, want 1", len(ssmClient.sendInputs))
	}
}

func TestGetProcessStatus_Running(t *testing.T) {
	ssmClient := newProcessMockSSM([]ssmSendPlan{
		{
			invocation: &ssm.GetCommandInvocationOutput{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: aws.String("running 12345"),
			},
		},
	})
	d, sandboxID := setupDirectWithRunningSandbox(t, ssmClient)
	d.instances[sandboxID].processes["proc-4"] = &processState{
		processID: "proc-4",
		pid:       4444,
		startTime: 12345,
		status:    control.ProcessRunning,
	}

	resp, err := d.GetProcessStatus(context.Background(), control.GetProcessStatusRequest{
		SandboxID: sandboxID,
		ProcessID: "proc-4",
	})
	if err != nil {
		t.Fatalf("GetProcessStatus() error = %v", err)
	}
	if resp.Status != control.ProcessRunning {
		t.Fatalf("Status = %q, want %q", resp.Status, control.ProcessRunning)
	}
}

func TestGetProcessStatus_ExitedCached(t *testing.T) {
	ssmClient := newProcessMockSSM(nil)
	d, sandboxID := setupDirectWithRunningSandbox(t, ssmClient)
	d.instances[sandboxID].processes["proc-5"] = &processState{
		processID: "proc-5",
		pid:       5555,
		startTime: 12345,
		status:    control.ProcessExited,
	}

	resp, err := d.GetProcessStatus(context.Background(), control.GetProcessStatusRequest{
		SandboxID: sandboxID,
		ProcessID: "proc-5",
	})
	if err != nil {
		t.Fatalf("GetProcessStatus() error = %v", err)
	}
	if resp.Status != control.ProcessExited {
		t.Fatalf("Status = %q, want %q", resp.Status, control.ProcessExited)
	}
	if len(ssmClient.sendInputs) != 0 {
		t.Fatalf("SendCommand calls = %d, want 0 for cached exited status", len(ssmClient.sendInputs))
	}
}

func TestGetProcessStatus_PIDRecycled(t *testing.T) {
	ssmClient := newProcessMockSSM([]ssmSendPlan{
		{
			invocation: &ssm.GetCommandInvocationOutput{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: aws.String("running 99999"),
			},
		},
	})
	d, sandboxID := setupDirectWithRunningSandbox(t, ssmClient)
	d.instances[sandboxID].processes["proc-6"] = &processState{
		processID: "proc-6",
		pid:       6666,
		startTime: 12345,
		status:    control.ProcessRunning,
	}

	resp, err := d.GetProcessStatus(context.Background(), control.GetProcessStatusRequest{
		SandboxID: sandboxID,
		ProcessID: "proc-6",
	})
	if err != nil {
		t.Fatalf("GetProcessStatus() error = %v", err)
	}
	if resp.Status != control.ProcessExited {
		t.Fatalf("Status = %q, want %q", resp.Status, control.ProcessExited)
	}

	d.mu.Lock()
	status := d.instances[sandboxID].processes["proc-6"].status
	d.mu.Unlock()
	if status != control.ProcessExited {
		t.Fatalf("cached process status = %q, want %q", status, control.ProcessExited)
	}
}
