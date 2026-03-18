package instance

import (
	"context"
	"testing"
)

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
