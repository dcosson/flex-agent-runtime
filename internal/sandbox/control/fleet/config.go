// Package fleet implements FleetSandboxControl, a SandboxControl adapter that
// manages a fleet of sandbox-host instances with auto-scaling, health monitoring,
// warm pool maintenance, and capacity-aware routing.
package fleet

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
)

// FleetConfig controls fleet behavior. All durations with zero values use the
// defaults from DefaultFleetConfig().
type FleetConfig struct {
	// Instance provisioning
	InstanceConfig instance.InstanceConfig // Template for launching new instances (from shared package)

	// Fleet sizing
	MinInstances   int // Floor -- never scale below this (default: 1)
	MaxInstances   int // Ceiling -- never scale above this (default: 50)
	WarmPoolTarget int // Number of idle instances to keep ready (default: 2)

	// Capacity thresholds
	MaxSessionsPerInstance int     // Max sandboxes per instance (default: 10)
	CapacityHeadroom       float64 // Don't route above this fraction (default: 0.8)

	// Health checking
	HealthCheckInterval time.Duration // How often to poll (default: 15s)
	UnhealthyThreshold  int           // Consecutive failures before marking unhealthy (default: 3)
	HealthyThreshold    int           // Consecutive successes before marking healthy again during drain recovery (default: 3)

	// Scaling
	IdleCooldown            time.Duration // How long an instance must be idle before scale-down (default: 10m)
	DrainTimeout            time.Duration // Max time to wait for active sandboxes during drain (default: 30m)
	ProvisionTimeout        time.Duration // Max time to wait for instance to become healthy (default: 5m)
	MaxConcurrentProvisions int           // Max simultaneous LaunchInstance calls (default: 3)

	// Termination
	MaxTerminateRetries int // Max consecutive TerminateInstance failures before alerting (default: 5)

	// Shutdown behavior
	LeaveInstancesOnClose bool // If true, skip instance termination on Close for re-adoption on restart

	// Sandbox-host port
	SandboxHostPort int // Port sandbox-host listens on (default: 9100)
}

// DefaultFleetConfig returns a FleetConfig with sensible production defaults.
func DefaultFleetConfig() FleetConfig {
	return FleetConfig{
		MinInstances:            1,
		MaxInstances:            50,
		WarmPoolTarget:          2,
		MaxSessionsPerInstance:  10,
		CapacityHeadroom:        0.8,
		HealthCheckInterval:     15 * time.Second,
		UnhealthyThreshold:      3,
		HealthyThreshold:        3,
		IdleCooldown:            10 * time.Minute,
		DrainTimeout:            30 * time.Minute,
		ProvisionTimeout:        5 * time.Minute,
		MaxConcurrentProvisions: 3,
		MaxTerminateRetries:     5,
		SandboxHostPort:         9100,
	}
}

// Validate checks config invariants and returns an error describing all
// violations found. Returns nil if the config is valid.
func (c FleetConfig) Validate() error {
	var errs []string

	if c.MinInstances < 0 {
		errs = append(errs, "MinInstances must be >= 0")
	}
	if c.MaxInstances < 1 {
		errs = append(errs, "MaxInstances must be >= 1")
	}
	if c.MinInstances > c.MaxInstances {
		errs = append(errs, fmt.Sprintf("MinInstances (%d) must be <= MaxInstances (%d)", c.MinInstances, c.MaxInstances))
	}
	if c.MaxSessionsPerInstance < 1 {
		errs = append(errs, "MaxSessionsPerInstance must be >= 1")
	}
	if c.CapacityHeadroom <= 0 || c.CapacityHeadroom > 1.0 {
		errs = append(errs, fmt.Sprintf("CapacityHeadroom must be in (0, 1.0], got %f", c.CapacityHeadroom))
	}
	if c.WarmPoolTarget < 0 {
		errs = append(errs, "WarmPoolTarget must be >= 0")
	}
	if c.HealthCheckInterval <= 0 {
		errs = append(errs, "HealthCheckInterval must be > 0")
	}
	if c.UnhealthyThreshold < 1 {
		errs = append(errs, "UnhealthyThreshold must be >= 1")
	}
	if c.HealthyThreshold < 1 {
		errs = append(errs, "HealthyThreshold must be >= 1")
	}
	if c.IdleCooldown <= 0 {
		errs = append(errs, "IdleCooldown must be > 0")
	}
	if c.DrainTimeout <= 0 {
		errs = append(errs, "DrainTimeout must be > 0")
	}
	if c.ProvisionTimeout <= 0 {
		errs = append(errs, "ProvisionTimeout must be > 0")
	}
	if c.MaxConcurrentProvisions < 1 {
		errs = append(errs, "MaxConcurrentProvisions must be >= 1")
	}
	if c.MaxTerminateRetries < 1 {
		errs = append(errs, "MaxTerminateRetries must be >= 1")
	}
	if c.SandboxHostPort <= 0 || c.SandboxHostPort > 65535 {
		errs = append(errs, fmt.Sprintf("SandboxHostPort must be in [1, 65535], got %d", c.SandboxHostPort))
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("invalid FleetConfig: %s", joinErrors(errs))
}

// joinErrors joins error strings with "; " separator.
func joinErrors(errs []string) string {
	result := ""
	for i, e := range errs {
		if i > 0 {
			result += "; "
		}
		result += e
	}
	return result
}

// FleetOption configures optional behavior on FleetSandboxControl.
type FleetOption func(*fleetOptions)

// fleetOptions holds optional configuration applied via FleetOption.
type fleetOptions struct {
	logger                *slog.Logger
	leaveInstancesOnClose bool
}

// WithLogger sets the structured logger for fleet operations.
func WithLogger(logger *slog.Logger) FleetOption {
	return func(o *fleetOptions) {
		if logger != nil {
			o.logger = logger
		}
	}
}

// WithLeaveInstancesOnClose configures the fleet to skip instance termination
// on Close. This is used when the fleet manager is restarting and instances
// should be re-adopted by the new manager via crash recovery.
func WithLeaveInstancesOnClose(leave bool) FleetOption {
	return func(o *fleetOptions) {
		o.leaveInstancesOnClose = leave
	}
}
