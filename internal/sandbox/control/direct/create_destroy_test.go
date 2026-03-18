package direct

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
)

type mockInstanceProvisioner struct {
	mu sync.Mutex

	launchResp *instance.InstanceInfo
	launchErr  error
	launchCfgs []instance.InstanceConfig

	describeSeq   []*instance.InstanceStatus
	describeErr   error
	describeCalls int

	terminateErr   error
	terminateCalls []string
}

func (m *mockInstanceProvisioner) LaunchInstance(_ context.Context, cfg instance.InstanceConfig) (*instance.InstanceInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.launchCfgs = append(m.launchCfgs, cloneInstanceConfig(cfg))
	if m.launchErr != nil {
		return nil, m.launchErr
	}
	if m.launchResp == nil {
		return nil, nil
	}
	resp := *m.launchResp
	resp.Tags = cloneStringMap(m.launchResp.Tags)
	return &resp, nil
}

func (m *mockInstanceProvisioner) TerminateInstance(_ context.Context, instanceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.terminateCalls = append(m.terminateCalls, instanceID)
	return m.terminateErr
}

func (m *mockInstanceProvisioner) StopInstance(_ context.Context, _ string) error {
	return nil
}

func (m *mockInstanceProvisioner) StartInstance(_ context.Context, _ string) (*instance.InstanceInfo, error) {
	return nil, nil
}

func (m *mockInstanceProvisioner) DescribeInstance(_ context.Context, instanceID string) (*instance.InstanceStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.describeErr != nil {
		return nil, m.describeErr
	}
	if len(m.describeSeq) == 0 {
		return &instance.InstanceStatus{
			InstanceID: instanceID,
			State:      instance.CloudInstancePending,
		}, nil
	}
	idx := m.describeCalls
	m.describeCalls++
	if idx >= len(m.describeSeq) {
		idx = len(m.describeSeq) - 1
	}
	st := m.describeSeq[idx]
	if st == nil {
		return nil, nil
	}
	cp := *st
	if cp.InstanceID == "" {
		cp.InstanceID = instanceID
	}
	return &cp, nil
}

func (m *mockInstanceProvisioner) ListInstances(_ context.Context, _ instance.InstanceFilter) ([]instance.InstanceInfo, error) {
	return nil, nil
}

type mockSSMClient struct {
	mu sync.Mutex

	describeSeq   []*ssm.DescribeInstanceInformationOutput
	describeErr   error
	describeCalls int
	describeInput *ssm.DescribeInstanceInformationInput
}

func (m *mockSSMClient) SendCommand(_ context.Context, _ *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	return nil, errors.New("unexpected SendCommand call")
}

func (m *mockSSMClient) GetCommandInvocation(_ context.Context, _ *ssm.GetCommandInvocationInput, _ ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	return nil, errors.New("unexpected GetCommandInvocation call")
}

func (m *mockSSMClient) DescribeInstanceInformation(_ context.Context, input *ssm.DescribeInstanceInformationInput, _ ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.describeInput = input
	if m.describeErr != nil {
		return nil, m.describeErr
	}
	if len(m.describeSeq) == 0 {
		return &ssm.DescribeInstanceInformationOutput{}, nil
	}
	idx := m.describeCalls
	m.describeCalls++
	if idx >= len(m.describeSeq) {
		idx = len(m.describeSeq) - 1
	}
	out := m.describeSeq[idx]
	if out == nil {
		return &ssm.DescribeInstanceInformationOutput{}, nil
	}
	return out, nil
}

func TestCreateSandbox_HappyPath(t *testing.T) {
	prov := &mockInstanceProvisioner{
		launchResp: &instance.InstanceInfo{
			InstanceID: "i-abc123",
		},
		describeSeq: []*instance.InstanceStatus{
			{
				State:     instance.CloudInstanceRunning,
				PublicIP:  "54.1.2.3",
				PrivateIP: "10.0.0.3",
			},
		},
	}
	ssmClient := &mockSSMClient{
		describeSeq: []*ssm.DescribeInstanceInformationOutput{
			{
				InstanceInformationList: []ssmtypes.InstanceInformation{
					{InstanceId: aws.String("i-abc123")},
				},
			},
		},
	}

	d := NewDirectSandboxControl(prov, ssmClient, Config{
		AMIID:               "ami-default",
		DefaultInstanceType: "t3.medium",
		RootVolumeSizeGB:    64,
		UserDataTemplate:    "#!/bin/bash\necho {{index .Labels \"env\"}} > /tmp/env\necho {{.WorkspaceDir}} > /tmp/ws",
		Tags:                map[string]string{"team": "runtime"},
	})

	resp, err := d.CreateSandbox(context.Background(), control.CreateSandboxRequest{
		Labels: map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatalf("CreateSandbox() error = %v", err)
	}
	if resp.SandboxID != "direct:i-abc123" {
		t.Fatalf("SandboxID = %q, want %q", resp.SandboxID, "direct:i-abc123")
	}
	if resp.Address != "54.1.2.3:0" {
		t.Fatalf("Address = %q, want %q", resp.Address, "54.1.2.3:0")
	}

	if len(prov.launchCfgs) != 1 {
		t.Fatalf("LaunchInstance calls = %d, want 1", len(prov.launchCfgs))
	}
	launchCfg := prov.launchCfgs[0]
	if launchCfg.Image != "ami-default" {
		t.Errorf("Image = %q, want %q", launchCfg.Image, "ami-default")
	}
	if launchCfg.InstanceType != "t3.medium" {
		t.Errorf("InstanceType = %q, want %q", launchCfg.InstanceType, "t3.medium")
	}
	if launchCfg.DiskSizeGB != 64 {
		t.Errorf("DiskSizeGB = %d, want %d", launchCfg.DiskSizeGB, 64)
	}
	if launchCfg.Tags["ManagedBy"] != "flex-agent-runtime" {
		t.Errorf("ManagedBy tag = %q, want %q", launchCfg.Tags["ManagedBy"], "flex-agent-runtime")
	}
	if launchCfg.Tags["adapter"] != "direct" {
		t.Errorf("adapter tag = %q, want %q", launchCfg.Tags["adapter"], "direct")
	}
	if launchCfg.Tags["team"] != "runtime" {
		t.Errorf("team tag = %q, want %q", launchCfg.Tags["team"], "runtime")
	}
	if launchCfg.Tags["env"] != "test" {
		t.Errorf("env tag = %q, want %q", launchCfg.Tags["env"], "test")
	}
	if !strings.Contains(launchCfg.UserData, "test") {
		t.Errorf("UserData = %q, want to contain env label", launchCfg.UserData)
	}
	if !strings.Contains(launchCfg.UserData, "/workspace") {
		t.Errorf("UserData = %q, want to contain default workspace", launchCfg.UserData)
	}

	if ssmClient.describeInput == nil || len(ssmClient.describeInput.Filters) != 1 {
		t.Fatalf("DescribeInstanceInformation filters missing")
	}
	filter := ssmClient.describeInput.Filters[0]
	if filter.Key == nil || *filter.Key != "InstanceIds" {
		t.Fatalf("DescribeInstanceInformation filter key = %v, want InstanceIds", filter.Key)
	}
	if len(filter.Values) != 1 || filter.Values[0] != "i-abc123" {
		t.Fatalf("DescribeInstanceInformation filter values = %v, want [i-abc123]", filter.Values)
	}

	d.mu.Lock()
	state := d.instances["direct:i-abc123"]
	d.mu.Unlock()
	if state == nil {
		t.Fatalf("instance state missing for %q", "direct:i-abc123")
	}
	if state.status != statusRunning {
		t.Errorf("state.status = %q, want %q", state.status, statusRunning)
	}
}

func TestCreateSandbox_InstanceTypeMapping(t *testing.T) {
	prov := &mockInstanceProvisioner{
		launchResp: &instance.InstanceInfo{InstanceID: "i-mapping"},
		describeSeq: []*instance.InstanceStatus{
			{State: instance.CloudInstanceRunning, PrivateIP: "10.0.0.10"},
		},
	}
	ssmClient := &mockSSMClient{
		describeSeq: []*ssm.DescribeInstanceInformationOutput{
			{
				InstanceInformationList: []ssmtypes.InstanceInformation{{InstanceId: aws.String("i-mapping")}},
			},
		},
	}
	d := NewDirectSandboxControl(prov, ssmClient, Config{
		AMIID:               "ami-default",
		DefaultInstanceType: "t3.medium",
	})

	_, err := d.CreateSandbox(context.Background(), control.CreateSandboxRequest{
		Resources: control.ResourceSpec{CPUs: 7, MemMB: 16000},
	})
	if err != nil {
		t.Fatalf("CreateSandbox() error = %v", err)
	}
	if len(prov.launchCfgs) != 1 {
		t.Fatalf("LaunchInstance calls = %d, want 1", len(prov.launchCfgs))
	}
	if prov.launchCfgs[0].InstanceType != "t3.xlarge" {
		t.Fatalf("InstanceType = %q, want %q", prov.launchCfgs[0].InstanceType, "t3.xlarge")
	}
}

func TestCreateSandbox_TemplateOverride(t *testing.T) {
	prov := &mockInstanceProvisioner{
		launchResp: &instance.InstanceInfo{InstanceID: "i-template"},
		describeSeq: []*instance.InstanceStatus{
			{State: instance.CloudInstanceRunning, PrivateIP: "10.0.0.11"},
		},
	}
	ssmClient := &mockSSMClient{
		describeSeq: []*ssm.DescribeInstanceInformationOutput{
			{
				InstanceInformationList: []ssmtypes.InstanceInformation{{InstanceId: aws.String("i-template")}},
			},
		},
	}
	d := NewDirectSandboxControl(prov, ssmClient, Config{
		AMIID:               "ami-default",
		DefaultInstanceType: "t3.medium",
	})

	_, err := d.CreateSandbox(context.Background(), control.CreateSandboxRequest{
		Template: "ami-override",
	})
	if err != nil {
		t.Fatalf("CreateSandbox() error = %v", err)
	}
	if len(prov.launchCfgs) != 1 {
		t.Fatalf("LaunchInstance calls = %d, want 1", len(prov.launchCfgs))
	}
	if prov.launchCfgs[0].Image != "ami-override" {
		t.Fatalf("Image = %q, want %q", prov.launchCfgs[0].Image, "ami-override")
	}
}

func TestCreateSandbox_InstanceReadyTimeout(t *testing.T) {
	prov := &mockInstanceProvisioner{
		launchResp: &instance.InstanceInfo{InstanceID: "i-timeout"},
		describeSeq: []*instance.InstanceStatus{
			{State: instance.CloudInstancePending},
		},
	}
	ssmClient := &mockSSMClient{}
	d := NewDirectSandboxControl(prov, ssmClient, Config{
		AMIID:                "ami-default",
		DefaultInstanceType:  "t3.medium",
		InstanceReadyTimeout: 15 * time.Millisecond,
	})

	_, err := d.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
	if !errors.Is(err, ErrInstanceReadyTimeout) {
		t.Fatalf("CreateSandbox() error = %v, want %v", err, ErrInstanceReadyTimeout)
	}
	if len(prov.terminateCalls) != 1 || prov.terminateCalls[0] != "i-timeout" {
		t.Fatalf("TerminateInstance calls = %v, want [i-timeout]", prov.terminateCalls)
	}
}

func TestCreateSandbox_SSMUnavailable(t *testing.T) {
	prov := &mockInstanceProvisioner{
		launchResp: &instance.InstanceInfo{InstanceID: "i-ssm"},
		describeSeq: []*instance.InstanceStatus{
			{State: instance.CloudInstanceRunning, PrivateIP: "10.0.0.12"},
		},
	}
	ssmClient := &mockSSMClient{
		describeSeq: []*ssm.DescribeInstanceInformationOutput{
			{InstanceInformationList: nil},
		},
	}
	d := NewDirectSandboxControl(prov, ssmClient, Config{
		AMIID:                "ami-default",
		DefaultInstanceType:  "t3.medium",
		InstanceReadyTimeout: 15 * time.Millisecond,
	})

	_, err := d.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
	if !errors.Is(err, ErrSSMUnavailable) {
		t.Fatalf("CreateSandbox() error = %v, want %v", err, ErrSSMUnavailable)
	}
	if len(prov.terminateCalls) != 1 || prov.terminateCalls[0] != "i-ssm" {
		t.Fatalf("TerminateInstance calls = %v, want [i-ssm]", prov.terminateCalls)
	}
}

func TestCreateSandbox_ResourcesExceedMaximum(t *testing.T) {
	prov := &mockInstanceProvisioner{}
	ssmClient := &mockSSMClient{}
	d := NewDirectSandboxControl(prov, ssmClient, Config{
		AMIID:               "ami-default",
		DefaultInstanceType: "t3.medium",
	})

	_, err := d.CreateSandbox(context.Background(), control.CreateSandboxRequest{
		Resources: control.ResourceSpec{CPUs: 64, MemMB: 131072},
	})
	if !errors.Is(err, ErrResourcesExceedMaximum) {
		t.Fatalf("CreateSandbox() error = %v, want %v", err, ErrResourcesExceedMaximum)
	}
	if len(prov.launchCfgs) != 0 {
		t.Fatalf("LaunchInstance calls = %d, want 0", len(prov.launchCfgs))
	}
}

func TestDestroySandbox_HappyPath(t *testing.T) {
	prov := &mockInstanceProvisioner{}
	d := NewDirectSandboxControl(prov, &mockSSMClient{}, Config{})
	d.instances["direct:i-destroy"] = &instanceState{
		instanceID: "i-destroy",
		status:     statusRunning,
		processes:  make(map[string]*processState),
	}

	if err := d.DestroySandbox(context.Background(), "direct:i-destroy"); err != nil {
		t.Fatalf("DestroySandbox() error = %v", err)
	}
	if len(prov.terminateCalls) != 1 || prov.terminateCalls[0] != "i-destroy" {
		t.Fatalf("TerminateInstance calls = %v, want [i-destroy]", prov.terminateCalls)
	}

	d.mu.Lock()
	_, ok := d.instances["direct:i-destroy"]
	d.mu.Unlock()
	if ok {
		t.Fatalf("instance state still present after DestroySandbox")
	}
}

func TestDestroySandbox_UnknownSandbox(t *testing.T) {
	prov := &mockInstanceProvisioner{}
	d := NewDirectSandboxControl(prov, &mockSSMClient{}, Config{})

	err := d.DestroySandbox(context.Background(), "direct:i-missing")
	if !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("DestroySandbox() error = %v, want %v", err, ErrInstanceNotFound)
	}
}

func cloneInstanceConfig(cfg instance.InstanceConfig) instance.InstanceConfig {
	clone := cfg
	clone.Tags = cloneStringMap(cfg.Tags)
	return clone
}
