package direct

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
)

func TestRecover_HappyPath(t *testing.T) {
	prov := &mockInstanceProvisioner{
		listResp: []instance.InstanceInfo{
			{
				InstanceID: "i-run",
				PrivateIP:  "10.0.1.10",
				PublicIP:   "54.1.1.10",
				State:      instance.CloudInstanceRunning,
				Tags: map[string]string{
					"flex-sandbox-id": "direct:i-run",
				},
			},
			{
				InstanceID: "i-stop",
				PrivateIP:  "10.0.1.11",
				State:      instance.CloudInstanceStopped,
			},
		},
	}
	ssmClient := newProcessMockSSM([]ssmSendPlan{
		{
			invocation: &ssm.GetCommandInvocationOutput{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: aws.String("proc-a 111 222\nproc-b 333 444\n"),
			},
		},
	})

	d, err := NewDirectSandboxControl(prov, ssmClient, Config{})
	if err != nil {
		t.Fatalf("NewDirectSandboxControl() error = %v", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.instances) != 2 {
		t.Fatalf("recovered instances = %d, want 2", len(d.instances))
	}
	run := d.instances["direct:i-run"]
	if run == nil {
		t.Fatalf("missing recovered running instance")
	}
	if run.status != statusRunning {
		t.Fatalf("run.status = %q, want %q", run.status, statusRunning)
	}
	if len(run.processes) != 2 {
		t.Fatalf("run.processes = %d, want 2", len(run.processes))
	}
	stopped := d.instances["direct:i-stop"]
	if stopped == nil {
		t.Fatalf("missing recovered stopped instance")
	}
	if stopped.status != statusStopped {
		t.Fatalf("stopped.status = %q, want %q", stopped.status, statusStopped)
	}
	if len(prov.listFilter.Tags) == 0 || prov.listFilter.Tags["ManagedBy"] != "flex-agent-runtime" || prov.listFilter.Tags["adapter"] != "direct" {
		t.Fatalf("ListInstances filter tags = %#v", prov.listFilter.Tags)
	}
}

func TestRecover_ZeroInstances(t *testing.T) {
	prov := &mockInstanceProvisioner{}
	d, err := NewDirectSandboxControl(prov, newProcessMockSSM(nil), Config{})
	if err != nil {
		t.Fatalf("NewDirectSandboxControl() error = %v", err)
	}
	if len(d.instances) != 0 {
		t.Fatalf("recovered instances = %d, want 0", len(d.instances))
	}
}

func TestRecover_ListInstancesFailure(t *testing.T) {
	prov := &mockInstanceProvisioner{
		listErr: errors.New("list failed"),
	}
	_, err := NewDirectSandboxControl(prov, newProcessMockSSM(nil), Config{})
	if err == nil {
		t.Fatalf("NewDirectSandboxControl() error = nil, want non-nil")
	}
}

func TestRecover_SSMProbeFailureBestEffort(t *testing.T) {
	prov := &mockInstanceProvisioner{
		listResp: []instance.InstanceInfo{
			{
				InstanceID: "i-run",
				PrivateIP:  "10.0.1.10",
				State:      instance.CloudInstanceRunning,
			},
		},
	}
	ssmClient := newProcessMockSSM([]ssmSendPlan{
		{
			invocation: &ssm.GetCommandInvocationOutput{
				Status: ssmtypes.CommandInvocationStatusFailed,
			},
		},
	})

	d, err := NewDirectSandboxControl(prov, ssmClient, Config{})
	if err != nil {
		t.Fatalf("NewDirectSandboxControl() error = %v", err)
	}

	d.mu.Lock()
	recovered := d.instances["direct:i-run"]
	d.mu.Unlock()
	if recovered == nil {
		t.Fatalf("missing recovered instance")
	}
	if len(recovered.processes) != 0 {
		t.Fatalf("recovered processes = %d, want 0 on probe failure", len(recovered.processes))
	}
}

func TestPauseSandbox_HappyPath(t *testing.T) {
	prov := &mockInstanceProvisioner{}
	d := mustNewDirect(t, prov, &mockSSMClient{}, Config{})
	d.instances["direct:i-pause"] = &instanceState{
		instanceID: "i-pause",
		status:     statusRunning,
		processes: map[string]*processState{
			"proc-1": {processID: "proc-1", status: control.ProcessRunning},
		},
	}

	if err := d.PauseSandbox(context.Background(), "direct:i-pause"); err != nil {
		t.Fatalf("PauseSandbox() error = %v", err)
	}
	if len(prov.stopCalls) != 1 || prov.stopCalls[0] != "i-pause" {
		t.Fatalf("StopInstance calls = %v, want [i-pause]", prov.stopCalls)
	}

	d.mu.Lock()
	st := d.instances["direct:i-pause"]
	d.mu.Unlock()
	if st.status != statusStopped {
		t.Fatalf("status = %q, want %q", st.status, statusStopped)
	}
	if st.processes["proc-1"].status != control.ProcessExited {
		t.Fatalf("process status = %q, want %q", st.processes["proc-1"].status, control.ProcessExited)
	}
}

func TestResumeSandbox_HappyPath(t *testing.T) {
	prov := &mockInstanceProvisioner{
		startResp: &instance.InstanceInfo{
			InstanceID: "i-resume",
		},
		describeSeq: []*instance.InstanceStatus{
			{
				State:     instance.CloudInstanceRunning,
				PublicIP:  "54.2.2.2",
				PrivateIP: "10.0.2.2",
			},
		},
	}
	ssmClient := &mockSSMClient{
		describeSeq: []*ssm.DescribeInstanceInformationOutput{
			{
				InstanceInformationList: []ssmtypes.InstanceInformation{{InstanceId: aws.String("i-resume")}},
			},
		},
	}
	d := mustNewDirect(t, prov, ssmClient, Config{})
	d.instances["direct:i-resume"] = &instanceState{
		instanceID: "i-resume",
		status:     statusStopped,
		processes: map[string]*processState{
			"proc-old": {processID: "proc-old", status: control.ProcessExited},
		},
	}

	if err := d.ResumeSandbox(context.Background(), "direct:i-resume"); err != nil {
		t.Fatalf("ResumeSandbox() error = %v", err)
	}
	if len(prov.startCalls) != 1 || prov.startCalls[0] != "i-resume" {
		t.Fatalf("StartInstance calls = %v, want [i-resume]", prov.startCalls)
	}

	d.mu.Lock()
	st := d.instances["direct:i-resume"]
	d.mu.Unlock()
	if st.status != statusRunning {
		t.Fatalf("status = %q, want %q", st.status, statusRunning)
	}
	if st.publicIP != "54.2.2.2" || st.privateIP != "10.0.2.2" {
		t.Fatalf("ips = public:%q private:%q", st.publicIP, st.privateIP)
	}
	if len(st.processes) != 0 {
		t.Fatalf("processes should be cleared on resume, got %d", len(st.processes))
	}
}

func TestResumeSandbox_Timeout(t *testing.T) {
	prov := &mockInstanceProvisioner{
		describeSeq: []*instance.InstanceStatus{
			{State: instance.CloudInstancePending},
		},
	}
	d := mustNewDirect(t, prov, &mockSSMClient{}, Config{
		InstanceReadyTimeout: 15 * time.Millisecond,
	})
	d.instances["direct:i-timeout"] = &instanceState{
		instanceID: "i-timeout",
		status:     statusStopped,
		processes:  make(map[string]*processState),
	}

	err := d.ResumeSandbox(context.Background(), "direct:i-timeout")
	if !errors.Is(err, ErrInstanceReadyTimeout) {
		t.Fatalf("ResumeSandbox() error = %v, want %v", err, ErrInstanceReadyTimeout)
	}
}

func TestClose_TerminateOnCloseTrue(t *testing.T) {
	prov := &mockInstanceProvisioner{}
	d := mustNewDirect(t, prov, &mockSSMClient{}, Config{
		TerminateOnClose: true,
	})
	d.instances = map[string]*instanceState{
		"direct:i-1": {instanceID: "i-1"},
		"direct:i-2": {instanceID: "i-2"},
	}

	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if len(prov.terminateCalls) != 2 {
		t.Fatalf("TerminateInstance calls = %v, want 2 calls", prov.terminateCalls)
	}
}

func TestClose_TerminateOnCloseFalse(t *testing.T) {
	prov := &mockInstanceProvisioner{}
	d := mustNewDirect(t, prov, &mockSSMClient{}, Config{
		TerminateOnClose: false,
	})
	d.instances = map[string]*instanceState{
		"direct:i-1": {instanceID: "i-1"},
	}

	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if len(prov.terminateCalls) != 0 {
		t.Fatalf("TerminateInstance calls = %v, want none", prov.terminateCalls)
	}
}

func TestClose_WaitsForInflightCreateSandbox(t *testing.T) {
	blockLaunch := make(chan struct{})
	launchStarted := make(chan struct{})

	prov := &mockInstanceProvisioner{
		launchResp: &instance.InstanceInfo{InstanceID: "i-inflight"},
		launchHook: func() {
			select {
			case launchStarted <- struct{}{}:
			default:
			}
			<-blockLaunch
		},
	}
	d := mustNewDirect(t, prov, &mockSSMClient{}, Config{})

	createErrCh := make(chan error, 1)
	go func() {
		_, err := d.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
		createErrCh <- err
	}()

	select {
	case <-launchStarted:
	case <-time.After(1 * time.Second):
		t.Fatalf("CreateSandbox did not reach LaunchInstance")
	}

	closeDone := make(chan struct{})
	go func() {
		_ = d.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
		t.Fatalf("Close returned before in-flight CreateSandbox completed")
	case <-time.After(50 * time.Millisecond):
	}

	close(blockLaunch)

	select {
	case err := <-createErrCh:
		if !errors.Is(err, ErrDirectClosed) {
			t.Fatalf("CreateSandbox() error = %v, want %v", err, ErrDirectClosed)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("CreateSandbox did not return")
	}

	select {
	case <-closeDone:
	case <-time.After(1 * time.Second):
		t.Fatalf("Close did not return after create completion")
	}

	if len(prov.terminateCalls) != 1 || prov.terminateCalls[0] != "i-inflight" {
		t.Fatalf("TerminateInstance calls = %v, want [i-inflight]", prov.terminateCalls)
	}
}

func TestClose_ThenCreateSandboxReturnsErrDirectClosed(t *testing.T) {
	d := mustNewDirect(t, &mockInstanceProvisioner{}, &mockSSMClient{}, Config{})
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, err := d.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
	if !errors.Is(err, ErrDirectClosed) {
		t.Fatalf("CreateSandbox() error = %v, want %v", err, ErrDirectClosed)
	}
}

func TestConcurrent_CloseAndCreate_NoDeadlock(t *testing.T) {
	prov := &mockInstanceProvisioner{
		launchResp: &instance.InstanceInfo{InstanceID: "i-concurrent"},
	}
	ssmClient := &mockSSMClient{
		describeSeq: []*ssm.DescribeInstanceInformationOutput{
			{
				InstanceInformationList: []ssmtypes.InstanceInformation{{InstanceId: aws.String("i-concurrent")}},
			},
		},
	}
	prov.describeSeq = []*instance.InstanceStatus{
		{State: instance.CloudInstanceRunning, PrivateIP: "10.0.0.20"},
	}

	d := mustNewDirect(t, prov, ssmClient, Config{})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = d.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = d.Close()
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("concurrent close/create operations did not complete")
	}
}
