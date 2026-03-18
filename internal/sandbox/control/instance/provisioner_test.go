package instance

import (
	"context"
	"testing"
	"time"
)

// --- CloudInstanceState string stability tests ---

func TestCloudInstanceState_StringValues(t *testing.T) {
	// These string values are part of the public API contract. If they change,
	// serialized state (logs, metrics, persisted fleet state) will break.
	// This test ensures nobody accidentally changes a constant value.
	tests := []struct {
		state CloudInstanceState
		want  string
	}{
		{CloudInstancePending, "pending"},
		{CloudInstanceRunning, "running"},
		{CloudInstanceStopping, "stopping"},
		{CloudInstanceStopped, "stopped"},
		{CloudInstanceTerminating, "terminating"},
		{CloudInstanceTerminated, "terminated"},
	}

	for _, tt := range tests {
		if got := string(tt.state); got != tt.want {
			t.Errorf("CloudInstanceState = %q, want %q", got, tt.want)
		}
	}
}

func TestCloudInstanceState_AllStatesExhaustive(t *testing.T) {
	// Verify AllCloudInstanceStates returns exactly the expected set.
	all := AllCloudInstanceStates()
	expected := map[CloudInstanceState]bool{
		CloudInstancePending:     true,
		CloudInstanceRunning:     true,
		CloudInstanceStopping:    true,
		CloudInstanceStopped:     true,
		CloudInstanceTerminating: true,
		CloudInstanceTerminated:  true,
	}

	if len(all) != len(expected) {
		t.Fatalf("AllCloudInstanceStates returned %d states, want %d", len(all), len(expected))
	}

	seen := make(map[CloudInstanceState]bool)
	for _, s := range all {
		if !expected[s] {
			t.Errorf("unexpected state in AllCloudInstanceStates: %q", s)
		}
		if seen[s] {
			t.Errorf("duplicate state in AllCloudInstanceStates: %q", s)
		}
		seen[s] = true
	}
}

func TestCloudInstanceState_StableOrdering(t *testing.T) {
	// AllCloudInstanceStates must return states in a consistent order across calls.
	first := AllCloudInstanceStates()
	second := AllCloudInstanceStates()

	if len(first) != len(second) {
		t.Fatalf("length mismatch: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("index %d: %q != %q", i, first[i], second[i])
		}
	}
}

// --- InstanceFilter construction tests ---

func TestInstanceFilter_EmptyFilter(t *testing.T) {
	f := InstanceFilter{}
	if f.Tags != nil {
		t.Error("zero-value InstanceFilter should have nil Tags")
	}
	if f.States != nil {
		t.Error("zero-value InstanceFilter should have nil States")
	}
}

func TestInstanceFilter_WithTags(t *testing.T) {
	f := InstanceFilter{
		Tags: map[string]string{
			"ManagedBy": "flex-agent-runtime",
			"fleet-id":  "test-fleet-1",
		},
	}
	if len(f.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(f.Tags))
	}
	if f.Tags["ManagedBy"] != "flex-agent-runtime" {
		t.Errorf("ManagedBy = %q, want %q", f.Tags["ManagedBy"], "flex-agent-runtime")
	}
	if f.Tags["fleet-id"] != "test-fleet-1" {
		t.Errorf("fleet-id = %q, want %q", f.Tags["fleet-id"], "test-fleet-1")
	}
}

func TestInstanceFilter_WithStates(t *testing.T) {
	f := InstanceFilter{
		States: []CloudInstanceState{CloudInstanceRunning, CloudInstancePending},
	}
	if len(f.States) != 2 {
		t.Fatalf("expected 2 states, got %d", len(f.States))
	}
	if f.States[0] != CloudInstanceRunning {
		t.Errorf("States[0] = %q, want %q", f.States[0], CloudInstanceRunning)
	}
	if f.States[1] != CloudInstancePending {
		t.Errorf("States[1] = %q, want %q", f.States[1], CloudInstancePending)
	}
}

func TestInstanceFilter_FullFilter(t *testing.T) {
	f := InstanceFilter{
		Tags: map[string]string{
			"ManagedBy": "flex-agent-runtime",
		},
		States: []CloudInstanceState{
			CloudInstanceRunning,
			CloudInstanceStopped,
		},
	}
	if len(f.Tags) != 1 {
		t.Errorf("expected 1 tag, got %d", len(f.Tags))
	}
	if len(f.States) != 2 {
		t.Errorf("expected 2 states, got %d", len(f.States))
	}
}

// --- InstanceConfig construction tests ---

func TestInstanceConfig_ZeroValue(t *testing.T) {
	cfg := InstanceConfig{}
	if cfg.Image != "" {
		t.Error("zero-value Image should be empty")
	}
	if cfg.InstanceType != "" {
		t.Error("zero-value InstanceType should be empty")
	}
	if cfg.DiskSizeGB != 0 {
		t.Error("zero-value DiskSizeGB should be 0")
	}
}

func TestInstanceConfig_WithAllFields(t *testing.T) {
	cfg := InstanceConfig{
		Image:        "ami-12345678",
		InstanceType: "m5.xlarge",
		UserData:     "#!/bin/bash\necho hello",
		Tags: map[string]string{
			"ManagedBy": "flex-agent-runtime",
			"fleet-id":  "fleet-abc",
		},
		DiskSizeGB: 100,
	}
	if cfg.Image != "ami-12345678" {
		t.Errorf("Image = %q", cfg.Image)
	}
	if cfg.InstanceType != "m5.xlarge" {
		t.Errorf("InstanceType = %q", cfg.InstanceType)
	}
	if cfg.UserData != "#!/bin/bash\necho hello" {
		t.Errorf("UserData = %q", cfg.UserData)
	}
	if len(cfg.Tags) != 2 {
		t.Errorf("len(Tags) = %d", len(cfg.Tags))
	}
	if cfg.DiskSizeGB != 100 {
		t.Errorf("DiskSizeGB = %d", cfg.DiskSizeGB)
	}
}

// --- InstanceInfo tests ---

func TestInstanceInfo_WithLaunchTime(t *testing.T) {
	now := time.Now()
	info := InstanceInfo{
		InstanceID: "i-abc123",
		PrivateIP:  "10.0.1.5",
		PublicIP:   "54.123.45.67",
		State:      CloudInstanceRunning,
		LaunchTime: now,
		Tags: map[string]string{
			"Name": "test-instance",
		},
	}
	if info.InstanceID != "i-abc123" {
		t.Errorf("InstanceID = %q", info.InstanceID)
	}
	if info.State != CloudInstanceRunning {
		t.Errorf("State = %q", info.State)
	}
	if info.LaunchTime != now {
		t.Error("LaunchTime mismatch")
	}
	if info.Tags["Name"] != "test-instance" {
		t.Errorf("Tags[Name] = %q", info.Tags["Name"])
	}
}

// --- InstanceStatus tests ---

func TestInstanceStatus_WithStateReason(t *testing.T) {
	status := InstanceStatus{
		InstanceID:  "i-abc123",
		State:       CloudInstanceStopped,
		StateReason: "Client.UserInitiatedShutdown",
		PrivateIP:   "10.0.1.5",
		PublicIP:    "",
	}
	if status.StateReason != "Client.UserInitiatedShutdown" {
		t.Errorf("StateReason = %q", status.StateReason)
	}
	if status.PublicIP != "" {
		t.Errorf("PublicIP should be empty for stopped instance, got %q", status.PublicIP)
	}
}

// --- Contract test helpers for InstanceProvisioner implementations ---

// ProvisionerFactory creates an InstanceProvisioner for contract testing and
// returns a cleanup function. The factory should set up any required test
// infrastructure (mock cloud APIs, test accounts, etc.). The cleanup function
// is called after all contract tests complete.
type ProvisionerFactory func(t *testing.T) (provisioner InstanceProvisioner, cleanup func())

// RunProvisionerContractTests runs the shared contract test suite against any
// InstanceProvisioner implementation. Every concrete provisioner (EC2, GCP,
// Azure, mock) should call this from its own test file to verify it satisfies
// the interface contract.
//
// The factory function should create a provisioner configured for the test
// environment. For integration tests against real cloud APIs, the factory
// should set up test resources and the cleanup function should tear them down.
// For unit tests, the factory should provide a mock/fake implementation.
func RunProvisionerContractTests(t *testing.T, factory ProvisionerFactory) {
	t.Helper()

	t.Run("LaunchInstance_returns_valid_info", func(t *testing.T) {
		p, cleanup := factory(t)
		defer cleanup()

		ctx := context.Background()
		cfg := InstanceConfig{
			Image:        "test-image",
			InstanceType: "test-type",
			Tags: map[string]string{
				"ManagedBy": "flex-agent-runtime",
				"fleet-id":  "contract-test",
			},
			DiskSizeGB: 20,
		}

		info, err := p.LaunchInstance(ctx, cfg)
		if err != nil {
			t.Fatalf("LaunchInstance: %v", err)
		}
		if info == nil {
			t.Fatal("LaunchInstance returned nil InstanceInfo")
		}
		if info.InstanceID == "" {
			t.Error("LaunchInstance returned empty InstanceID")
		}
	})

	t.Run("DescribeInstance_returns_consistent_state", func(t *testing.T) {
		p, cleanup := factory(t)
		defer cleanup()

		ctx := context.Background()
		cfg := InstanceConfig{
			Image:        "test-image",
			InstanceType: "test-type",
			Tags: map[string]string{
				"ManagedBy": "flex-agent-runtime",
				"fleet-id":  "contract-test",
			},
			DiskSizeGB: 20,
		}

		info, err := p.LaunchInstance(ctx, cfg)
		if err != nil {
			t.Fatalf("LaunchInstance: %v", err)
		}

		status, err := p.DescribeInstance(ctx, info.InstanceID)
		if err != nil {
			t.Fatalf("DescribeInstance: %v", err)
		}
		if status == nil {
			t.Fatal("DescribeInstance returned nil")
		}
		if status.InstanceID != info.InstanceID {
			t.Errorf("DescribeInstance.InstanceID = %q, want %q", status.InstanceID, info.InstanceID)
		}
		// State should be one of the valid cloud instance states.
		validStates := map[CloudInstanceState]bool{
			CloudInstancePending:     true,
			CloudInstanceRunning:     true,
			CloudInstanceStopping:    true,
			CloudInstanceStopped:     true,
			CloudInstanceTerminating: true,
			CloudInstanceTerminated:  true,
		}
		if !validStates[status.State] {
			t.Errorf("DescribeInstance.State = %q, not a valid CloudInstanceState", status.State)
		}
	})

	t.Run("TerminateInstance_makes_instance_terminated", func(t *testing.T) {
		p, cleanup := factory(t)
		defer cleanup()

		ctx := context.Background()
		cfg := InstanceConfig{
			Image:        "test-image",
			InstanceType: "test-type",
			Tags: map[string]string{
				"ManagedBy": "flex-agent-runtime",
				"fleet-id":  "contract-test",
			},
			DiskSizeGB: 20,
		}

		info, err := p.LaunchInstance(ctx, cfg)
		if err != nil {
			t.Fatalf("LaunchInstance: %v", err)
		}

		err = p.TerminateInstance(ctx, info.InstanceID)
		if err != nil {
			t.Fatalf("TerminateInstance: %v", err)
		}

		status, err := p.DescribeInstance(ctx, info.InstanceID)
		if err != nil {
			t.Fatalf("DescribeInstance after terminate: %v", err)
		}
		if status.State != CloudInstanceTerminated && status.State != CloudInstanceTerminating {
			t.Errorf("state after terminate = %q, want terminated or terminating", status.State)
		}
	})

	t.Run("TerminateInstance_idempotent", func(t *testing.T) {
		p, cleanup := factory(t)
		defer cleanup()

		ctx := context.Background()
		cfg := InstanceConfig{
			Image:        "test-image",
			InstanceType: "test-type",
			Tags: map[string]string{
				"ManagedBy": "flex-agent-runtime",
				"fleet-id":  "contract-test",
			},
			DiskSizeGB: 20,
		}

		info, err := p.LaunchInstance(ctx, cfg)
		if err != nil {
			t.Fatalf("LaunchInstance: %v", err)
		}

		err = p.TerminateInstance(ctx, info.InstanceID)
		if err != nil {
			t.Fatalf("first TerminateInstance: %v", err)
		}

		// Second terminate should also succeed (idempotent).
		err = p.TerminateInstance(ctx, info.InstanceID)
		if err != nil {
			t.Errorf("second TerminateInstance should be idempotent, got: %v", err)
		}
	})

	t.Run("ListInstances_matches_by_tags", func(t *testing.T) {
		p, cleanup := factory(t)
		defer cleanup()

		ctx := context.Background()
		fleetID := "contract-test-list"
		cfg := InstanceConfig{
			Image:        "test-image",
			InstanceType: "test-type",
			Tags: map[string]string{
				"ManagedBy": "flex-agent-runtime",
				"fleet-id":  fleetID,
			},
			DiskSizeGB: 20,
		}

		info, err := p.LaunchInstance(ctx, cfg)
		if err != nil {
			t.Fatalf("LaunchInstance: %v", err)
		}

		// List with matching tags should find the instance.
		instances, err := p.ListInstances(ctx, InstanceFilter{
			Tags: map[string]string{
				"ManagedBy": "flex-agent-runtime",
				"fleet-id":  fleetID,
			},
		})
		if err != nil {
			t.Fatalf("ListInstances: %v", err)
		}

		found := false
		for _, inst := range instances {
			if inst.InstanceID == info.InstanceID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ListInstances with matching tags did not return launched instance %q", info.InstanceID)
		}
	})

	t.Run("ListInstances_nonmatching_tags_returns_empty", func(t *testing.T) {
		p, cleanup := factory(t)
		defer cleanup()

		ctx := context.Background()
		cfg := InstanceConfig{
			Image:        "test-image",
			InstanceType: "test-type",
			Tags: map[string]string{
				"ManagedBy": "flex-agent-runtime",
				"fleet-id":  "contract-test-nomatch",
			},
			DiskSizeGB: 20,
		}

		_, err := p.LaunchInstance(ctx, cfg)
		if err != nil {
			t.Fatalf("LaunchInstance: %v", err)
		}

		// List with completely different tags should not find the instance.
		instances, err := p.ListInstances(ctx, InstanceFilter{
			Tags: map[string]string{
				"ManagedBy": "some-other-system",
				"fleet-id":  "nonexistent-fleet",
			},
		})
		if err != nil {
			t.Fatalf("ListInstances: %v", err)
		}
		if len(instances) != 0 {
			t.Errorf("ListInstances with non-matching tags returned %d instances, want 0", len(instances))
		}
	})

	t.Run("LaunchInstance_invalid_config_returns_error", func(t *testing.T) {
		p, cleanup := factory(t)
		defer cleanup()

		ctx := context.Background()
		// Empty config -- implementations should reject this.
		_, err := p.LaunchInstance(ctx, InstanceConfig{})
		if err == nil {
			t.Error("LaunchInstance with empty config should return an error")
		}
	})

	t.Run("StopInstance_and_StartInstance_lifecycle", func(t *testing.T) {
		p, cleanup := factory(t)
		defer cleanup()

		ctx := context.Background()
		cfg := InstanceConfig{
			Image:        "test-image",
			InstanceType: "test-type",
			Tags: map[string]string{
				"ManagedBy": "flex-agent-runtime",
				"fleet-id":  "contract-test-stopstart",
			},
			DiskSizeGB: 20,
		}

		info, err := p.LaunchInstance(ctx, cfg)
		if err != nil {
			t.Fatalf("LaunchInstance: %v", err)
		}

		err = p.StopInstance(ctx, info.InstanceID)
		if err != nil {
			t.Fatalf("StopInstance: %v", err)
		}

		status, err := p.DescribeInstance(ctx, info.InstanceID)
		if err != nil {
			t.Fatalf("DescribeInstance after stop: %v", err)
		}
		if status.State != CloudInstanceStopped && status.State != CloudInstanceStopping {
			t.Errorf("state after stop = %q, want stopped or stopping", status.State)
		}

		restarted, err := p.StartInstance(ctx, info.InstanceID)
		if err != nil {
			t.Fatalf("StartInstance: %v", err)
		}
		if restarted == nil {
			t.Fatal("StartInstance returned nil")
		}
		if restarted.InstanceID != info.InstanceID {
			t.Errorf("StartInstance.InstanceID = %q, want %q", restarted.InstanceID, info.InstanceID)
		}
	})
}
