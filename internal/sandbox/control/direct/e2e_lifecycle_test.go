package direct

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
)

type compositeSSM struct {
	describe *mockSSMClient
	process  *processMockSSM
}

func (c *compositeSSM) SendCommand(ctx context.Context, input *ssm.SendCommandInput, opts ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	return c.process.SendCommand(ctx, input, opts...)
}

func (c *compositeSSM) GetCommandInvocation(ctx context.Context, input *ssm.GetCommandInvocationInput, opts ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	return c.process.GetCommandInvocation(ctx, input, opts...)
}

func (c *compositeSSM) DescribeInstanceInformation(ctx context.Context, input *ssm.DescribeInstanceInformationInput, opts ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error) {
	return c.describe.DescribeInstanceInformation(ctx, input, opts...)
}

func TestE2E_FullLifecycleWithMocks(t *testing.T) {
	prov := &mockInstanceProvisioner{
		launchResp: &instance.InstanceInfo{InstanceID: "i-e2e-1"},
		describeSeq: []*instance.InstanceStatus{
			{State: instance.CloudInstanceRunning, PrivateIP: "10.0.1.21"},
		},
	}
	ssmAPI := &compositeSSM{
		describe: &mockSSMClient{
			describeSeq: []*ssm.DescribeInstanceInformationOutput{
				{
					InstanceInformationList: []ssmtypes.InstanceInformation{{InstanceId: aws.String("i-e2e-1")}},
				},
			},
		},
		process: newProcessMockSSM([]ssmSendPlan{
			{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess}},
			{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: aws.String("501\n1501\n")}},
			{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: aws.String("200")}},
			{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: aws.String("running 1501")}},
			{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: aws.String("1501")}},
			{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess}},
			{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess}},
		}),
	}
	d := mustNewDirect(t, prov, ssmAPI, Config{
		AMIID:               "ami-test",
		DefaultInstanceType: "t3.medium",
		IPSelectionMode:     "private",
	})

	createResp, err := d.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
	if err != nil {
		t.Fatalf("CreateSandbox() error = %v", err)
	}
	if createResp.SandboxID != "direct:i-e2e-1" {
		t.Fatalf("SandboxID = %q, want %q", createResp.SandboxID, "direct:i-e2e-1")
	}

	launchResp, err := d.LaunchProcess(context.Background(), control.LaunchProcessRequest{
		SandboxID:  createResp.SandboxID,
		Binary:     "/usr/local/bin/flexagent",
		Args:       []string{"serve", "agent", "--listen=:8081"},
		ExposePort: 8081,
	})
	if err != nil {
		t.Fatalf("LaunchProcess() error = %v", err)
	}

	statusResp, err := d.GetProcessStatus(context.Background(), control.GetProcessStatusRequest{
		SandboxID: createResp.SandboxID,
		ProcessID: launchResp.ProcessID,
	})
	if err != nil {
		t.Fatalf("GetProcessStatus() error = %v", err)
	}
	if statusResp.Status != control.ProcessRunning {
		t.Fatalf("status = %q, want %q", statusResp.Status, control.ProcessRunning)
	}

	if err := d.KillProcess(context.Background(), control.KillProcessRequest{
		SandboxID: createResp.SandboxID,
		ProcessID: launchResp.ProcessID,
	}); err != nil {
		t.Fatalf("KillProcess() error = %v", err)
	}

	if err := d.DestroySandbox(context.Background(), createResp.SandboxID); err != nil {
		t.Fatalf("DestroySandbox() error = %v", err)
	}
	if len(prov.terminateCalls) != 1 || prov.terminateCalls[0] != "i-e2e-1" {
		t.Fatalf("TerminateInstance calls = %v, want [i-e2e-1]", prov.terminateCalls)
	}
}

func TestE2E_MultiAgentLifecycleWithMocks(t *testing.T) {
	prov := &mockInstanceProvisioner{
		launchResp: &instance.InstanceInfo{InstanceID: "i-e2e-2"},
		describeSeq: []*instance.InstanceStatus{
			{State: instance.CloudInstanceRunning, PrivateIP: "10.0.1.22"},
		},
	}
	plans := make([]ssmSendPlan, 0, 1+3*3+3+3*3)
	// 3x launch (launch cmd + pid file + health)
	for i := 0; i < 3; i++ {
		pid := 600 + i
		start := 2600 + i
		plans = append(plans,
			ssmSendPlan{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess}},
			ssmSendPlan{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: aws.String(fmt.Sprintf("%d\n%d\n", pid, start))}},
			ssmSendPlan{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: aws.String("200")}},
		)
	}
	// 3x status checks
	for i := 0; i < 3; i++ {
		start := 2600 + i
		plans = append(plans, ssmSendPlan{
			invocation: &ssm.GetCommandInvocationOutput{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: aws.String(fmt.Sprintf("running %d", start)),
			},
		})
	}
	// 3x kills (start check + kill + cleanup)
	for i := 0; i < 3; i++ {
		start := 2600 + i
		plans = append(plans,
			ssmSendPlan{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: aws.String(fmt.Sprintf("%d", start))}},
			ssmSendPlan{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess}},
			ssmSendPlan{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess}},
		)
	}
	ssmAPI := &compositeSSM{
		describe: &mockSSMClient{
			describeSeq: []*ssm.DescribeInstanceInformationOutput{
				{InstanceInformationList: []ssmtypes.InstanceInformation{{InstanceId: aws.String("i-e2e-2")}}},
			},
		},
		process: newProcessMockSSM(plans),
	}
	d := mustNewDirect(t, prov, ssmAPI, Config{
		AMIID:               "ami-test",
		DefaultInstanceType: "t3.medium",
		IPSelectionMode:     "private",
	})

	createResp, err := d.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
	if err != nil {
		t.Fatalf("CreateSandbox() error = %v", err)
	}

	processIDs := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		launchResp, launchErr := d.LaunchProcess(context.Background(), control.LaunchProcessRequest{
			SandboxID:  createResp.SandboxID,
			Binary:     "/usr/local/bin/flexagent",
			Args:       []string{"serve", "agent", fmt.Sprintf("--listen=:%d", 8081+i)},
			ExposePort: 8081 + i,
		})
		if launchErr != nil {
			t.Fatalf("LaunchProcess #%d error = %v", i, launchErr)
		}
		processIDs = append(processIDs, launchResp.ProcessID)
	}

	for _, processID := range processIDs {
		st, stErr := d.GetProcessStatus(context.Background(), control.GetProcessStatusRequest{
			SandboxID: createResp.SandboxID,
			ProcessID: processID,
		})
		if stErr != nil {
			t.Fatalf("GetProcessStatus(%s) error = %v", processID, stErr)
		}
		if st.Status != control.ProcessRunning {
			t.Fatalf("status(%s) = %q, want running", processID, st.Status)
		}
	}

	for _, processID := range processIDs {
		if killErr := d.KillProcess(context.Background(), control.KillProcessRequest{
			SandboxID: createResp.SandboxID,
			ProcessID: processID,
		}); killErr != nil {
			t.Fatalf("KillProcess(%s) error = %v", processID, killErr)
		}
	}

	if err := d.DestroySandbox(context.Background(), createResp.SandboxID); err != nil {
		t.Fatalf("DestroySandbox() error = %v", err)
	}
}

func TestE2E_PauseResumeLifecycleWithMocks(t *testing.T) {
	prov := &mockInstanceProvisioner{
		launchResp: &instance.InstanceInfo{InstanceID: "i-e2e-3"},
		startResp:  &instance.InstanceInfo{InstanceID: "i-e2e-3"},
		describeSeq: []*instance.InstanceStatus{
			{State: instance.CloudInstanceRunning, PrivateIP: "10.0.1.23"},
			{State: instance.CloudInstanceRunning, PrivateIP: "10.0.9.23"},
		},
	}
	ssmAPI := &compositeSSM{
		describe: &mockSSMClient{
			describeSeq: []*ssm.DescribeInstanceInformationOutput{
				{InstanceInformationList: []ssmtypes.InstanceInformation{{InstanceId: aws.String("i-e2e-3")}}},
				{InstanceInformationList: []ssmtypes.InstanceInformation{{InstanceId: aws.String("i-e2e-3")}}},
			},
		},
		process: newProcessMockSSM([]ssmSendPlan{
			// First launch
			{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess}},
			{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: aws.String("701\n1701\n")}},
			{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: aws.String("200")}},
			// Second launch after resume
			{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess}},
			{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: aws.String("702\n1702\n")}},
			{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: aws.String("200")}},
		}),
	}
	d := mustNewDirect(t, prov, ssmAPI, Config{
		AMIID:               "ami-test",
		DefaultInstanceType: "t3.medium",
		IPSelectionMode:     "private",
	})

	createResp, err := d.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
	if err != nil {
		t.Fatalf("CreateSandbox() error = %v", err)
	}
	firstLaunch, err := d.LaunchProcess(context.Background(), control.LaunchProcessRequest{
		SandboxID:  createResp.SandboxID,
		Binary:     "/usr/local/bin/flexagent",
		Args:       []string{"serve", "agent", "--listen=:8081"},
		ExposePort: 8081,
	})
	if err != nil {
		t.Fatalf("LaunchProcess(first) error = %v", err)
	}

	if err := d.PauseSandbox(context.Background(), createResp.SandboxID); err != nil {
		t.Fatalf("PauseSandbox() error = %v", err)
	}

	d.mu.Lock()
	beforeResume := d.instances[createResp.SandboxID]
	d.mu.Unlock()
	if beforeResume.processes[firstLaunch.ProcessID].status != control.ProcessExited {
		t.Fatalf("process should be marked exited after pause")
	}

	if err := d.ResumeSandbox(context.Background(), createResp.SandboxID); err != nil {
		t.Fatalf("ResumeSandbox() error = %v", err)
	}
	d.mu.Lock()
	afterResume := d.instances[createResp.SandboxID]
	d.mu.Unlock()
	if afterResume.privateIP != "10.0.9.23" {
		t.Fatalf("private IP after resume = %q, want %q", afterResume.privateIP, "10.0.9.23")
	}
	if len(afterResume.processes) != 0 {
		t.Fatalf("process map should be cleared after resume, got %d entries", len(afterResume.processes))
	}

	if _, err := d.LaunchProcess(context.Background(), control.LaunchProcessRequest{
		SandboxID:  createResp.SandboxID,
		Binary:     "/usr/local/bin/flexagent",
		Args:       []string{"serve", "agent", "--listen=:8082"},
		ExposePort: 8082,
	}); err != nil {
		t.Fatalf("LaunchProcess(second) error = %v", err)
	}

	if err := d.DestroySandbox(context.Background(), createResp.SandboxID); err != nil {
		t.Fatalf("DestroySandbox() error = %v", err)
	}
}

func TestE2E_CrashRecoveryLifecycleWithMocks(t *testing.T) {
	prov := &mockInstanceProvisioner{
		launchResp: &instance.InstanceInfo{InstanceID: "i-e2e-4"},
		describeSeq: []*instance.InstanceStatus{
			{State: instance.CloudInstanceRunning, PrivateIP: "10.0.1.24"},
		},
	}

	ssmCreate := &compositeSSM{
		describe: &mockSSMClient{
			describeSeq: []*ssm.DescribeInstanceInformationOutput{
				{InstanceInformationList: []ssmtypes.InstanceInformation{{InstanceId: aws.String("i-e2e-4")}}},
			},
		},
		process: newProcessMockSSM([]ssmSendPlan{
			{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess}},
			{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: aws.String("801\n1801\n")}},
			{invocation: &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: aws.String("200")}},
		}),
	}
	d1 := mustNewDirect(t, prov, ssmCreate, Config{
		AMIID:               "ami-test",
		DefaultInstanceType: "t3.medium",
		IPSelectionMode:     "private",
	})

	createResp, err := d1.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
	if err != nil {
		t.Fatalf("CreateSandbox() error = %v", err)
	}
	launchResp, err := d1.LaunchProcess(context.Background(), control.LaunchProcessRequest{
		SandboxID:  createResp.SandboxID,
		Binary:     "/usr/local/bin/flexagent",
		Args:       []string{"serve", "agent", "--listen=:8081"},
		ExposePort: 8081,
	})
	if err != nil {
		t.Fatalf("LaunchProcess() error = %v", err)
	}

	// Simulate process restart by creating a new adapter against provider state.
	prov.listResp = []instance.InstanceInfo{
		{
			InstanceID: "i-e2e-4",
			PrivateIP:  "10.0.1.24",
			State:      instance.CloudInstanceRunning,
			Tags: map[string]string{
				"ManagedBy":       "flex-agent-runtime",
				"adapter":         "direct",
				"flex-sandbox-id": createResp.SandboxID,
			},
		},
	}
	ssmRecover := &mockSSMClient{} // not used by recovery path
	ssmRecoverComposite := &compositeSSM{
		describe: ssmRecover,
		process: newProcessMockSSM([]ssmSendPlan{
			{
				invocation: &ssm.GetCommandInvocationOutput{
					Status:                ssmtypes.CommandInvocationStatusSuccess,
					StandardOutputContent: aws.String(fmt.Sprintf("%s %d %d\n", launchResp.ProcessID, 801, 1801)),
				},
			},
		}),
	}
	d2 := mustNewDirect(t, prov, ssmRecoverComposite, Config{
		AMIID:               "ami-test",
		DefaultInstanceType: "t3.medium",
		IPSelectionMode:     "private",
	})

	d2.mu.Lock()
	recovered := d2.instances[createResp.SandboxID]
	d2.mu.Unlock()
	if recovered == nil {
		t.Fatalf("recovered instance missing for %q", createResp.SandboxID)
	}
	if _, ok := recovered.processes[launchResp.ProcessID]; !ok {
		t.Fatalf("recovered process missing for %q", launchResp.ProcessID)
	}

	if err := d2.DestroySandbox(context.Background(), createResp.SandboxID); err != nil {
		t.Fatalf("DestroySandbox() error = %v", err)
	}
}
