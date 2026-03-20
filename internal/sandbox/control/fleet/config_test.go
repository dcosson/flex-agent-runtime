package fleet

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestDefaultFleetConfig_Valid(t *testing.T) {
	cfg := DefaultFleetConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("DefaultFleetConfig() should be valid, got: %v", err)
	}
}

func TestDefaultFleetConfig_Values(t *testing.T) {
	cfg := DefaultFleetConfig()

	checks := []struct {
		name string
		got  interface{}
		want interface{}
	}{
		{"MinInstances", cfg.MinInstances, 1},
		{"MaxInstances", cfg.MaxInstances, 50},
		{"WarmPoolTarget", cfg.WarmPoolTarget, 2},
		{"MaxSessionsPerInstance", cfg.MaxSessionsPerInstance, 10},
		{"CapacityHeadroom", cfg.CapacityHeadroom, 0.8},
		{"HealthCheckInterval", cfg.HealthCheckInterval, 15 * time.Second},
		{"UnhealthyThreshold", cfg.UnhealthyThreshold, 3},
		{"HealthyThreshold", cfg.HealthyThreshold, 3},
		{"IdleCooldown", cfg.IdleCooldown, 10 * time.Minute},
		{"DrainTimeout", cfg.DrainTimeout, 30 * time.Minute},
		{"ProvisionTimeout", cfg.ProvisionTimeout, 5 * time.Minute},
		{"MaxConcurrentProvisions", cfg.MaxConcurrentProvisions, 3},
		{"MaxTerminateRetries", cfg.MaxTerminateRetries, 5},
		{"SandboxHostPort", cfg.SandboxHostPort, 9100},
	}

	for _, c := range checks {
		// Use %v to compare regardless of underlying type.
		gotStr := fmt.Sprintf("%v", c.got)
		wantStr := fmt.Sprintf("%v", c.want)
		if gotStr != wantStr {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestValidate_SingleFieldViolations(t *testing.T) {
	// Each test starts from a valid config and breaks exactly one field.
	tests := []struct {
		name    string
		mutate  func(*FleetConfig)
		wantSub string // substring expected in error message
	}{
		{
			name:    "MinInstances negative",
			mutate:  func(c *FleetConfig) { c.MinInstances = -1 },
			wantSub: "MinInstances must be >= 0",
		},
		{
			name:    "MaxInstances zero",
			mutate:  func(c *FleetConfig) { c.MaxInstances = 0 },
			wantSub: "MaxInstances must be >= 1",
		},
		{
			name:    "MinInstances > MaxInstances",
			mutate:  func(c *FleetConfig) { c.MinInstances = 10; c.MaxInstances = 5 },
			wantSub: "MinInstances (10) must be <= MaxInstances (5)",
		},
		{
			name:    "MaxSessionsPerInstance zero",
			mutate:  func(c *FleetConfig) { c.MaxSessionsPerInstance = 0 },
			wantSub: "MaxSessionsPerInstance must be >= 1",
		},
		{
			name:    "CapacityHeadroom zero",
			mutate:  func(c *FleetConfig) { c.CapacityHeadroom = 0 },
			wantSub: "CapacityHeadroom must be in (0, 1.0]",
		},
		{
			name:    "CapacityHeadroom negative",
			mutate:  func(c *FleetConfig) { c.CapacityHeadroom = -0.5 },
			wantSub: "CapacityHeadroom must be in (0, 1.0]",
		},
		{
			name:    "CapacityHeadroom above 1.0",
			mutate:  func(c *FleetConfig) { c.CapacityHeadroom = 1.1 },
			wantSub: "CapacityHeadroom must be in (0, 1.0]",
		},
		{
			name:    "CapacityHeadroom exactly 1.0 is valid",
			mutate:  func(c *FleetConfig) { c.CapacityHeadroom = 1.0 },
			wantSub: "", // no error expected
		},
		{
			name:    "WarmPoolTarget negative",
			mutate:  func(c *FleetConfig) { c.WarmPoolTarget = -1 },
			wantSub: "WarmPoolTarget must be >= 0",
		},
		{
			name:    "HealthCheckInterval zero",
			mutate:  func(c *FleetConfig) { c.HealthCheckInterval = 0 },
			wantSub: "HealthCheckInterval must be > 0",
		},
		{
			name:    "UnhealthyThreshold zero",
			mutate:  func(c *FleetConfig) { c.UnhealthyThreshold = 0 },
			wantSub: "UnhealthyThreshold must be >= 1",
		},
		{
			name:    "HealthyThreshold zero",
			mutate:  func(c *FleetConfig) { c.HealthyThreshold = 0 },
			wantSub: "HealthyThreshold must be >= 1",
		},
		{
			name:    "IdleCooldown zero",
			mutate:  func(c *FleetConfig) { c.IdleCooldown = 0 },
			wantSub: "IdleCooldown must be > 0",
		},
		{
			name:    "DrainTimeout zero",
			mutate:  func(c *FleetConfig) { c.DrainTimeout = 0 },
			wantSub: "DrainTimeout must be > 0",
		},
		{
			name:    "ProvisionTimeout zero",
			mutate:  func(c *FleetConfig) { c.ProvisionTimeout = 0 },
			wantSub: "ProvisionTimeout must be > 0",
		},
		{
			name:    "MaxConcurrentProvisions zero",
			mutate:  func(c *FleetConfig) { c.MaxConcurrentProvisions = 0 },
			wantSub: "MaxConcurrentProvisions must be >= 1",
		},
		{
			name:    "MaxTerminateRetries zero",
			mutate:  func(c *FleetConfig) { c.MaxTerminateRetries = 0 },
			wantSub: "MaxTerminateRetries must be >= 1",
		},
		{
			name:    "SandboxHostPort zero",
			mutate:  func(c *FleetConfig) { c.SandboxHostPort = 0 },
			wantSub: "SandboxHostPort must be in [1, 65535]",
		},
		{
			name:    "SandboxHostPort above 65535",
			mutate:  func(c *FleetConfig) { c.SandboxHostPort = 70000 },
			wantSub: "SandboxHostPort must be in [1, 65535]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultFleetConfig()
			tt.mutate(&cfg)
			err := cfg.Validate()

			if tt.wantSub == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantSub)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantSub)
			}
		})
	}
}

func TestValidate_MultipleViolations(t *testing.T) {
	cfg := FleetConfig{} // zero value violates many constraints
	err := cfg.Validate()
	if err == nil {
		t.Fatal("zero-value FleetConfig should fail validation")
	}

	msg := err.Error()
	// Should report multiple violations separated by semicolons.
	if !strings.Contains(msg, "invalid FleetConfig:") {
		t.Errorf("error should start with 'invalid FleetConfig:', got: %s", msg)
	}
	if strings.Count(msg, ";") < 2 {
		t.Errorf("expected multiple violations joined by ';', got: %s", msg)
	}
}

func TestValidate_MinInstancesZero_Allowed(t *testing.T) {
	cfg := DefaultFleetConfig()
	cfg.MinInstances = 0
	if err := cfg.Validate(); err != nil {
		t.Errorf("MinInstances=0 should be valid (allows scale-to-zero), got: %v", err)
	}
}

func TestFleetOption_WithLogger(t *testing.T) {
	opts := &fleetOptions{}

	// nil logger should be ignored.
	WithLogger(nil)(opts)
	if opts.logger != nil {
		t.Error("WithLogger(nil) should not set logger")
	}
}

func TestFleetOption_WithLeaveInstancesOnClose(t *testing.T) {
	opts := &fleetOptions{}

	WithLeaveInstancesOnClose(true)(opts)
	if !opts.leaveInstancesOnClose {
		t.Error("WithLeaveInstancesOnClose(true) should set leaveInstancesOnClose")
	}

	WithLeaveInstancesOnClose(false)(opts)
	if opts.leaveInstancesOnClose {
		t.Error("WithLeaveInstancesOnClose(false) should clear leaveInstancesOnClose")
	}
}

func TestFleetOption_WithMetricsRegisterer(t *testing.T) {
	opts := &fleetOptions{}
	reg := prometheus.NewRegistry()

	WithMetricsRegisterer(reg)(opts)
	if opts.metricsRegisterer != reg {
		t.Error("WithMetricsRegisterer should set metrics registerer")
	}
}

func TestJoinErrors(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"empty", nil, ""},
		{"single", []string{"a"}, "a"},
		{"multiple", []string{"a", "b", "c"}, "a; b; c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := joinErrors(tt.in)
			if got != tt.want {
				t.Errorf("joinErrors(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
