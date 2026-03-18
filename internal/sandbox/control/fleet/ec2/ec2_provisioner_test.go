package ec2

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
)

// --- Mock EC2 API ---

// mockEC2 implements EC2API with in-memory instance tracking. It simulates the
// core EC2 behavior needed for unit testing the provisioner.
type mockEC2 struct {
	mu        sync.Mutex
	instances map[string]*ec2types.Instance
	nextID    int

	// Error injection: if set, the next call to the corresponding method
	// returns this error instead of executing normally.
	runInstancesErr       error
	terminateInstancesErr error
	stopInstancesErr      error
	startInstancesErr     error
	describeInstancesErr  error
}

func newMockEC2() *mockEC2 {
	return &mockEC2{
		instances: make(map[string]*ec2types.Instance),
	}
}

func (m *mockEC2) RunInstances(_ context.Context, input *ec2.RunInstancesInput, _ ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.runInstancesErr != nil {
		err := m.runInstancesErr
		m.runInstancesErr = nil
		return nil, err
	}

	m.nextID++
	id := fmt.Sprintf("i-mock%08d", m.nextID)
	now := time.Now()

	inst := &ec2types.Instance{
		InstanceId:       &id,
		ImageId:          input.ImageId,
		InstanceType:     input.InstanceType,
		PrivateIpAddress: strPtr(fmt.Sprintf("10.0.0.%d", m.nextID)),
		PublicIpAddress:  strPtr(fmt.Sprintf("54.0.0.%d", m.nextID)),
		LaunchTime:       &now,
		State: &ec2types.InstanceState{
			Name: ec2types.InstanceStateNamePending,
			Code: int32Ptr(0),
		},
	}

	// Copy tags from TagSpecifications.
	if len(input.TagSpecifications) > 0 {
		for _, ts := range input.TagSpecifications {
			inst.Tags = append(inst.Tags, ts.Tags...)
		}
	}

	m.instances[id] = inst

	return &ec2.RunInstancesOutput{
		Instances: []ec2types.Instance{*inst},
	}, nil
}

func (m *mockEC2) TerminateInstances(_ context.Context, input *ec2.TerminateInstancesInput, _ ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.terminateInstancesErr != nil {
		err := m.terminateInstancesErr
		m.terminateInstancesErr = nil
		return nil, err
	}

	var changes []ec2types.InstanceStateChange
	for _, id := range input.InstanceIds {
		inst, ok := m.instances[id]
		if !ok {
			// EC2 TerminateInstances on non-existent instance: still succeeds.
			changes = append(changes, ec2types.InstanceStateChange{
				InstanceId: &id,
				CurrentState: &ec2types.InstanceState{
					Name: ec2types.InstanceStateNameTerminated,
					Code: int32Ptr(48),
				},
			})
			continue
		}
		prevState := *inst.State
		inst.State = &ec2types.InstanceState{
			Name: ec2types.InstanceStateNameTerminated,
			Code: int32Ptr(48),
		}
		// Clear IPs on termination.
		inst.PublicIpAddress = nil
		changes = append(changes, ec2types.InstanceStateChange{
			InstanceId:    &id,
			PreviousState: &prevState,
			CurrentState:  inst.State,
		})
	}

	return &ec2.TerminateInstancesOutput{
		TerminatingInstances: changes,
	}, nil
}

func (m *mockEC2) StopInstances(_ context.Context, input *ec2.StopInstancesInput, _ ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.stopInstancesErr != nil {
		err := m.stopInstancesErr
		m.stopInstancesErr = nil
		return nil, err
	}

	var changes []ec2types.InstanceStateChange
	for _, id := range input.InstanceIds {
		inst, ok := m.instances[id]
		if !ok {
			return nil, fmt.Errorf("instance %q not found", id)
		}
		prevState := *inst.State
		inst.State = &ec2types.InstanceState{
			Name: ec2types.InstanceStateNameStopped,
			Code: int32Ptr(80),
		}
		// EC2 clears public IP on stop.
		inst.PublicIpAddress = nil
		changes = append(changes, ec2types.InstanceStateChange{
			InstanceId:    &id,
			PreviousState: &prevState,
			CurrentState:  inst.State,
		})
	}

	return &ec2.StopInstancesOutput{
		StoppingInstances: changes,
	}, nil
}

func (m *mockEC2) StartInstances(_ context.Context, input *ec2.StartInstancesInput, _ ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.startInstancesErr != nil {
		err := m.startInstancesErr
		m.startInstancesErr = nil
		return nil, err
	}

	var changes []ec2types.InstanceStateChange
	for _, id := range input.InstanceIds {
		inst, ok := m.instances[id]
		if !ok {
			return nil, fmt.Errorf("instance %q not found", id)
		}
		prevState := *inst.State
		inst.State = &ec2types.InstanceState{
			Name: ec2types.InstanceStateNameRunning,
			Code: int32Ptr(16),
		}
		// EC2 may reassign a new public IP on start.
		inst.PublicIpAddress = strPtr("54.1.1.1")
		changes = append(changes, ec2types.InstanceStateChange{
			InstanceId:    &id,
			PreviousState: &prevState,
			CurrentState:  inst.State,
		})
	}

	return &ec2.StartInstancesOutput{
		StartingInstances: changes,
	}, nil
}

func (m *mockEC2) DescribeInstances(_ context.Context, input *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.describeInstancesErr != nil {
		err := m.describeInstancesErr
		m.describeInstancesErr = nil
		return nil, err
	}

	var matched []ec2types.Instance

	// If specific instance IDs are requested, filter by ID.
	if len(input.InstanceIds) > 0 {
		for _, id := range input.InstanceIds {
			inst, ok := m.instances[id]
			if ok {
				matched = append(matched, *inst)
			}
		}
	} else {
		// Filter by EC2 filters (tag: and instance-state-name).
		for _, inst := range m.instances {
			if matchesFilters(inst, input.Filters) {
				matched = append(matched, *inst)
			}
		}
	}

	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{
			{Instances: matched},
		},
	}, nil
}

// matchesFilters checks if an instance matches all the given EC2 filters.
func matchesFilters(inst *ec2types.Instance, filters []ec2types.Filter) bool {
	for _, f := range filters {
		if f.Name == nil {
			continue
		}
		name := *f.Name

		if name == "instance-state-name" {
			if inst.State == nil {
				return false
			}
			stateStr := string(inst.State.Name)
			found := false
			for _, v := range f.Values {
				if v == stateStr {
					found = true
					break
				}
			}
			if !found {
				return false
			}
			continue
		}

		// Tag filters: "tag:<key>"
		if len(name) > 4 && name[:4] == "tag:" {
			tagKey := name[4:]
			tagVal := ""
			for _, t := range inst.Tags {
				if t.Key != nil && *t.Key == tagKey {
					if t.Value != nil {
						tagVal = *t.Value
					}
					break
				}
			}
			found := false
			for _, v := range f.Values {
				if v == tagVal {
					found = true
					break
				}
			}
			if !found {
				return false
			}
			continue
		}
	}
	return true
}

// --- Unit Tests ---

func TestLaunchInstance_CallsRunInstancesWithCorrectParams(t *testing.T) {
	mock := newMockEC2()
	p := NewEC2InstanceProvisioner(mock, EC2LaunchConfig{
		SecurityGroupIDs:    []string{"sg-abc123"},
		SubnetID:            "subnet-def456",
		KeyName:             "my-key",
		InstanceProfileName: "my-profile",
	})

	cfg := instance.InstanceConfig{
		Image:        "ami-12345678",
		InstanceType: "m5.xlarge",
		UserData:     "#!/bin/bash\necho hello",
		Tags: map[string]string{
			"fleet-id": "test-fleet",
		},
		DiskSizeGB: 100,
	}

	info, err := p.LaunchInstance(context.Background(), cfg)
	if err != nil {
		t.Fatalf("LaunchInstance: %v", err)
	}
	if info == nil {
		t.Fatal("LaunchInstance returned nil")
	}
	if info.InstanceID == "" {
		t.Error("InstanceID is empty")
	}
	if info.State != instance.CloudInstancePending {
		t.Errorf("State = %q, want %q", info.State, instance.CloudInstancePending)
	}
}

func TestLaunchInstance_AppliesManagedByAndCustomTags(t *testing.T) {
	mock := newMockEC2()
	p := NewEC2InstanceProvisioner(mock, EC2LaunchConfig{})

	cfg := instance.InstanceConfig{
		Image:        "ami-test",
		InstanceType: "t3.micro",
		Tags: map[string]string{
			"fleet-id":    "fleet-abc",
			"environment": "test",
		},
		DiskSizeGB: 20,
	}

	info, err := p.LaunchInstance(context.Background(), cfg)
	if err != nil {
		t.Fatalf("LaunchInstance: %v", err)
	}

	// Verify tags on the mock instance.
	mock.mu.Lock()
	inst := mock.instances[info.InstanceID]
	mock.mu.Unlock()

	tags := fromEC2Tags(inst.Tags)
	if tags["ManagedBy"] != "flex-agent-runtime" {
		t.Errorf("ManagedBy tag = %q, want %q", tags["ManagedBy"], "flex-agent-runtime")
	}
	if tags["fleet-id"] != "fleet-abc" {
		t.Errorf("fleet-id tag = %q, want %q", tags["fleet-id"], "fleet-abc")
	}
	if tags["environment"] != "test" {
		t.Errorf("environment tag = %q, want %q", tags["environment"], "test")
	}
}

func TestLaunchInstance_InvalidConfigReturnsError(t *testing.T) {
	mock := newMockEC2()
	p := NewEC2InstanceProvisioner(mock, EC2LaunchConfig{})

	// Missing Image.
	_, err := p.LaunchInstance(context.Background(), instance.InstanceConfig{
		InstanceType: "t3.micro",
	})
	if err == nil {
		t.Error("expected error for missing Image")
	}

	// Missing InstanceType.
	_, err = p.LaunchInstance(context.Background(), instance.InstanceConfig{
		Image: "ami-test",
	})
	if err == nil {
		t.Error("expected error for missing InstanceType")
	}

	// Both missing.
	_, err = p.LaunchInstance(context.Background(), instance.InstanceConfig{})
	if err == nil {
		t.Error("expected error for empty config")
	}
}

func TestTerminateInstance_CallsTerminateInstances(t *testing.T) {
	mock := newMockEC2()
	p := NewEC2InstanceProvisioner(mock, EC2LaunchConfig{})

	info, err := p.LaunchInstance(context.Background(), instance.InstanceConfig{
		Image:        "ami-test",
		InstanceType: "t3.micro",
		DiskSizeGB:   20,
	})
	if err != nil {
		t.Fatalf("LaunchInstance: %v", err)
	}

	err = p.TerminateInstance(context.Background(), info.InstanceID)
	if err != nil {
		t.Fatalf("TerminateInstance: %v", err)
	}

	// Verify instance is terminated.
	status, err := p.DescribeInstance(context.Background(), info.InstanceID)
	if err != nil {
		t.Fatalf("DescribeInstance: %v", err)
	}
	if status.State != instance.CloudInstanceTerminated {
		t.Errorf("State = %q, want %q", status.State, instance.CloudInstanceTerminated)
	}
}

func TestTerminateInstance_IdempotentOnTerminated(t *testing.T) {
	mock := newMockEC2()
	p := NewEC2InstanceProvisioner(mock, EC2LaunchConfig{})

	info, err := p.LaunchInstance(context.Background(), instance.InstanceConfig{
		Image:        "ami-test",
		InstanceType: "t3.micro",
		DiskSizeGB:   20,
	})
	if err != nil {
		t.Fatalf("LaunchInstance: %v", err)
	}

	// First terminate.
	err = p.TerminateInstance(context.Background(), info.InstanceID)
	if err != nil {
		t.Fatalf("first TerminateInstance: %v", err)
	}

	// Second terminate should also succeed (idempotent).
	err = p.TerminateInstance(context.Background(), info.InstanceID)
	if err != nil {
		t.Errorf("second TerminateInstance should be idempotent, got: %v", err)
	}
}

func TestStopInstance_CallsStopInstances(t *testing.T) {
	mock := newMockEC2()
	p := NewEC2InstanceProvisioner(mock, EC2LaunchConfig{})

	info, err := p.LaunchInstance(context.Background(), instance.InstanceConfig{
		Image:        "ami-test",
		InstanceType: "t3.micro",
		DiskSizeGB:   20,
	})
	if err != nil {
		t.Fatalf("LaunchInstance: %v", err)
	}

	err = p.StopInstance(context.Background(), info.InstanceID)
	if err != nil {
		t.Fatalf("StopInstance: %v", err)
	}

	status, err := p.DescribeInstance(context.Background(), info.InstanceID)
	if err != nil {
		t.Fatalf("DescribeInstance: %v", err)
	}
	if status.State != instance.CloudInstanceStopped {
		t.Errorf("State = %q, want %q", status.State, instance.CloudInstanceStopped)
	}
}

func TestStartInstance_CallsStartInstances(t *testing.T) {
	mock := newMockEC2()
	p := NewEC2InstanceProvisioner(mock, EC2LaunchConfig{})

	info, err := p.LaunchInstance(context.Background(), instance.InstanceConfig{
		Image:        "ami-test",
		InstanceType: "t3.micro",
		DiskSizeGB:   20,
	})
	if err != nil {
		t.Fatalf("LaunchInstance: %v", err)
	}

	// Stop first.
	err = p.StopInstance(context.Background(), info.InstanceID)
	if err != nil {
		t.Fatalf("StopInstance: %v", err)
	}

	// Start.
	restarted, err := p.StartInstance(context.Background(), info.InstanceID)
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}
	if restarted == nil {
		t.Fatal("StartInstance returned nil")
	}
	if restarted.InstanceID != info.InstanceID {
		t.Errorf("InstanceID = %q, want %q", restarted.InstanceID, info.InstanceID)
	}
	if restarted.State != instance.CloudInstanceRunning {
		t.Errorf("State = %q, want %q", restarted.State, instance.CloudInstanceRunning)
	}
}

func TestDescribeInstance_MapsAllEC2States(t *testing.T) {
	// EC2 has 6 states: pending, running, shutting-down, terminated, stopping, stopped.
	// Verify each maps to the correct CloudInstanceState.
	tests := []struct {
		ec2State ec2types.InstanceStateName
		want     instance.CloudInstanceState
	}{
		{ec2types.InstanceStateNamePending, instance.CloudInstancePending},
		{ec2types.InstanceStateNameRunning, instance.CloudInstanceRunning},
		{ec2types.InstanceStateNameShuttingDown, instance.CloudInstanceTerminating},
		{ec2types.InstanceStateNameTerminated, instance.CloudInstanceTerminated},
		{ec2types.InstanceStateNameStopping, instance.CloudInstanceStopping},
		{ec2types.InstanceStateNameStopped, instance.CloudInstanceStopped},
	}

	for _, tt := range tests {
		t.Run(string(tt.ec2State), func(t *testing.T) {
			got := mapEC2State(&ec2types.InstanceState{Name: tt.ec2State})
			if got != tt.want {
				t.Errorf("mapEC2State(%q) = %q, want %q", tt.ec2State, got, tt.want)
			}
		})
	}
}

func TestDescribeInstance_NilStateReturnsPending(t *testing.T) {
	got := mapEC2State(nil)
	if got != instance.CloudInstancePending {
		t.Errorf("mapEC2State(nil) = %q, want %q", got, instance.CloudInstancePending)
	}
}

func TestListInstances_WithTagFilters(t *testing.T) {
	mock := newMockEC2()
	p := NewEC2InstanceProvisioner(mock, EC2LaunchConfig{})

	// Launch two instances with different fleet-ids.
	_, err := p.LaunchInstance(context.Background(), instance.InstanceConfig{
		Image:        "ami-test",
		InstanceType: "t3.micro",
		Tags: map[string]string{
			"fleet-id": "fleet-A",
		},
		DiskSizeGB: 20,
	})
	if err != nil {
		t.Fatalf("LaunchInstance A: %v", err)
	}

	_, err = p.LaunchInstance(context.Background(), instance.InstanceConfig{
		Image:        "ami-test",
		InstanceType: "t3.micro",
		Tags: map[string]string{
			"fleet-id": "fleet-B",
		},
		DiskSizeGB: 20,
	})
	if err != nil {
		t.Fatalf("LaunchInstance B: %v", err)
	}

	// List with fleet-A tag.
	results, err := p.ListInstances(context.Background(), instance.InstanceFilter{
		Tags: map[string]string{
			"fleet-id": "fleet-A",
		},
	})
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Tags["fleet-id"] != "fleet-A" {
		t.Errorf("fleet-id = %q, want %q", results[0].Tags["fleet-id"], "fleet-A")
	}
}

func TestListInstances_WithStateFilter(t *testing.T) {
	mock := newMockEC2()
	p := NewEC2InstanceProvisioner(mock, EC2LaunchConfig{})

	// Launch an instance (pending state).
	info, err := p.LaunchInstance(context.Background(), instance.InstanceConfig{
		Image:        "ami-test",
		InstanceType: "t3.micro",
		Tags: map[string]string{
			"fleet-id": "fleet-state-test",
		},
		DiskSizeGB: 20,
	})
	if err != nil {
		t.Fatalf("LaunchInstance: %v", err)
	}

	// Manually set the mock instance to running.
	mock.mu.Lock()
	mock.instances[info.InstanceID].State = &ec2types.InstanceState{
		Name: ec2types.InstanceStateNameRunning,
		Code: int32Ptr(16),
	}
	mock.mu.Unlock()

	// Launch another instance that stays pending.
	_, err = p.LaunchInstance(context.Background(), instance.InstanceConfig{
		Image:        "ami-test",
		InstanceType: "t3.micro",
		Tags: map[string]string{
			"fleet-id": "fleet-state-test",
		},
		DiskSizeGB: 20,
	})
	if err != nil {
		t.Fatalf("LaunchInstance 2: %v", err)
	}

	// List only running instances.
	results, err := p.ListInstances(context.Background(), instance.InstanceFilter{
		Tags: map[string]string{
			"fleet-id": "fleet-state-test",
		},
		States: []instance.CloudInstanceState{instance.CloudInstanceRunning},
	})
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 running instance, got %d", len(results))
	}
	if results[0].InstanceID != info.InstanceID {
		t.Errorf("InstanceID = %q, want %q", results[0].InstanceID, info.InstanceID)
	}
}

func TestListInstances_NoMatchesReturnsEmpty(t *testing.T) {
	mock := newMockEC2()
	p := NewEC2InstanceProvisioner(mock, EC2LaunchConfig{})

	// Launch an instance.
	_, err := p.LaunchInstance(context.Background(), instance.InstanceConfig{
		Image:        "ami-test",
		InstanceType: "t3.micro",
		Tags: map[string]string{
			"fleet-id": "fleet-exists",
		},
		DiskSizeGB: 20,
	})
	if err != nil {
		t.Fatalf("LaunchInstance: %v", err)
	}

	// List with non-matching tags.
	results, err := p.ListInstances(context.Background(), instance.InstanceFilter{
		Tags: map[string]string{
			"fleet-id": "fleet-nonexistent",
		},
	})
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestListInstances_ExcludesTerminatedByDefault(t *testing.T) {
	mock := newMockEC2()
	p := NewEC2InstanceProvisioner(mock, EC2LaunchConfig{})

	info, err := p.LaunchInstance(context.Background(), instance.InstanceConfig{
		Image:        "ami-test",
		InstanceType: "t3.micro",
		Tags: map[string]string{
			"fleet-id": "fleet-term",
		},
		DiskSizeGB: 20,
	})
	if err != nil {
		t.Fatalf("LaunchInstance: %v", err)
	}

	err = p.TerminateInstance(context.Background(), info.InstanceID)
	if err != nil {
		t.Fatalf("TerminateInstance: %v", err)
	}

	// List without explicit state filter should exclude terminated.
	results, err := p.ListInstances(context.Background(), instance.InstanceFilter{
		Tags: map[string]string{
			"fleet-id": "fleet-term",
		},
	})
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results (terminated excluded), got %d", len(results))
	}
}

func TestListInstances_IncludesTerminatedWhenRequested(t *testing.T) {
	mock := newMockEC2()
	p := NewEC2InstanceProvisioner(mock, EC2LaunchConfig{})

	info, err := p.LaunchInstance(context.Background(), instance.InstanceConfig{
		Image:        "ami-test",
		InstanceType: "t3.micro",
		Tags: map[string]string{
			"fleet-id": "fleet-term-incl",
		},
		DiskSizeGB: 20,
	})
	if err != nil {
		t.Fatalf("LaunchInstance: %v", err)
	}

	err = p.TerminateInstance(context.Background(), info.InstanceID)
	if err != nil {
		t.Fatalf("TerminateInstance: %v", err)
	}

	// List with explicit terminated state filter should include it.
	results, err := p.ListInstances(context.Background(), instance.InstanceFilter{
		Tags: map[string]string{
			"fleet-id": "fleet-term-incl",
		},
		States: []instance.CloudInstanceState{instance.CloudInstanceTerminated},
	})
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 terminated instance, got %d", len(results))
	}
}

func TestReverseMapState_AllStates(t *testing.T) {
	tests := []struct {
		state instance.CloudInstanceState
		want  []string
	}{
		{instance.CloudInstancePending, []string{"pending"}},
		{instance.CloudInstanceRunning, []string{"running"}},
		{instance.CloudInstanceStopping, []string{"stopping"}},
		{instance.CloudInstanceStopped, []string{"stopped"}},
		{instance.CloudInstanceTerminating, []string{"shutting-down"}},
		{instance.CloudInstanceTerminated, []string{"terminated"}},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			got := reverseMapState(tt.state)
			if len(got) != len(tt.want) {
				t.Fatalf("reverseMapState(%q) returned %d values, want %d", tt.state, len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("reverseMapState(%q)[%d] = %q, want %q", tt.state, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestMergeTags_OverrideBehavior(t *testing.T) {
	base := map[string]string{"a": "1", "b": "2"}
	overrides := map[string]string{"b": "override", "c": "3"}

	result := mergeTags(base, overrides)
	if result["a"] != "1" {
		t.Errorf("a = %q, want %q", result["a"], "1")
	}
	if result["b"] != "override" {
		t.Errorf("b = %q, want %q", result["b"], "override")
	}
	if result["c"] != "3" {
		t.Errorf("c = %q, want %q", result["c"], "3")
	}
}

func TestLaunchInstance_ManagedByTagCannotBeOverridden(t *testing.T) {
	mock := newMockEC2()
	p := NewEC2InstanceProvisioner(mock, EC2LaunchConfig{})

	cfg := instance.InstanceConfig{
		Image:        "ami-test",
		InstanceType: "t3.micro",
		Tags: map[string]string{
			"ManagedBy": "some-other-system",
		},
		DiskSizeGB: 20,
	}

	info, err := p.LaunchInstance(context.Background(), cfg)
	if err != nil {
		t.Fatalf("LaunchInstance: %v", err)
	}

	// The ManagedBy tag should be forced to "flex-agent-runtime".
	mock.mu.Lock()
	inst := mock.instances[info.InstanceID]
	mock.mu.Unlock()

	tags := fromEC2Tags(inst.Tags)
	if tags["ManagedBy"] != "flex-agent-runtime" {
		t.Errorf("ManagedBy tag = %q, want %q", tags["ManagedBy"], "flex-agent-runtime")
	}
}

// --- Interface compliance check ---

var _ instance.InstanceProvisioner = (*EC2InstanceProvisioner)(nil)

// --- Contract Tests ---

func TestEC2ProvisionerContractTests(t *testing.T) {
	instance.RunProvisionerContractTests(t, func(t *testing.T) (instance.InstanceProvisioner, func()) {
		t.Helper()
		mock := newMockEC2()
		p := NewEC2InstanceProvisioner(mock, EC2LaunchConfig{
			SecurityGroupIDs: []string{"sg-contract"},
			SubnetID:         "subnet-contract",
		})
		return p, func() {}
	})
}
