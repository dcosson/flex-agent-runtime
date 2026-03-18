package fleet

import (
	"fmt"
	"sync"
	"time"
)

// InstanceState represents the lifecycle state of a managed instance in the fleet.
type InstanceState string

const (
	// InstanceProvisioning indicates the cloud instance has been launched but
	// the sandbox-host has not yet passed health checks.
	InstanceProvisioning InstanceState = "provisioning"

	// InstanceReady indicates the sandbox-host is healthy with 0 sessions,
	// available for routing new sandboxes.
	InstanceReady InstanceState = "ready"

	// InstanceActive indicates the sandbox-host is healthy with >= 1 active
	// session.
	InstanceActive InstanceState = "active"

	// InstanceDraining indicates no new sessions should be assigned. The
	// instance is waiting for existing sessions to complete before
	// termination (or recovering health if DrainReason is DrainHealth).
	InstanceDraining InstanceState = "draining"

	// InstanceTerminating indicates TerminateInstance has been called and
	// we are waiting for cloud confirmation.
	InstanceTerminating InstanceState = "terminating"
)

// DrainReason records why an instance entered the Draining state. This
// determines whether Draining -> Ready recovery is permitted.
type DrainReason string

const (
	// DrainHealth indicates the instance was drained because it failed
	// consecutive health checks. Recovery to Ready is permitted if the
	// instance subsequently passes HealthyThreshold consecutive checks.
	DrainHealth DrainReason = "health"

	// DrainScaleDown indicates the instance was drained as part of scale-down
	// (idle too long, fleet can shrink). Recovery to Ready is NOT permitted.
	DrainScaleDown DrainReason = "scale-down"

	// DrainShutdown indicates the instance was drained as part of fleet
	// shutdown (Close was called). Recovery to Ready is NOT permitted.
	DrainShutdown DrainReason = "shutdown"
)

// ManagedInstance tracks the state of a single instance in the fleet.
//
// Lock ordering discipline: FleetSandboxControl.mu (fleet-level lock) must
// ALWAYS be acquired BEFORE ManagedInstance.mu (per-instance lock). No code
// path may acquire FleetSandboxControl.mu while holding any ManagedInstance.mu.
// This prevents deadlocks between the control loop and concurrent callers.
// See plan 20, section 4.1.2.
type ManagedInstance struct {
	// mu guards all mutable fields below. See lock ordering note above.
	mu sync.RWMutex

	// InstanceID is the cloud provider's unique identifier.
	InstanceID string

	// State is the current lifecycle state.
	State InstanceState

	// Client is the combined SandboxControl + HealthCheck client for this
	// instance. Set after the instance is provisioned and the client factory
	// creates a connection.
	Client FleetNodeClient

	// SessionCount tracks the number of active sandbox sessions on this
	// instance. Updated via the atomic claim-slot protocol (section 4.1.1)
	// and reconciled against HealthCheck-reported counts.
	SessionCount int64

	// MaxSessions is the maximum number of sessions this instance can host,
	// typically set from FleetConfig.MaxSessionsPerInstance.
	MaxSessions int

	// IP is the instance's private IP address used for RPC connections.
	IP string

	// ConsecutiveHealthSuccesses counts consecutive successful health checks.
	// Used for Draining -> Ready recovery when DrainReason is DrainHealth.
	// Reset to 0 on health check failure.
	ConsecutiveHealthSuccesses int

	// ConsecutiveHealthFailures counts consecutive failed health checks.
	// Used to trigger Draining transition when >= UnhealthyThreshold.
	// Reset to 0 on health check success.
	ConsecutiveHealthFailures int

	// DrainReason records why this instance entered the Draining state.
	// Only meaningful when State == InstanceDraining.
	DrainReason DrainReason

	// IdleSince records when the session count last reached 0 (in Ready state).
	// Used by the scale-down logic to determine if the instance has been idle
	// longer than IdleCooldown.
	IdleSince time.Time

	// ProvisionStarted records when provisioning began. Used by the control
	// loop to detect provision timeouts.
	ProvisionStarted time.Time

	// DrainStarted records when draining began. Used by the control loop
	// to detect drain timeouts and force-terminate.
	DrainStarted time.Time

	// TerminateRetries counts consecutive TerminateInstance failures for
	// this instance. Used to trigger an alert after MaxTerminateRetries.
	TerminateRetries int
}

// validTransitions defines the allowed state transitions for the instance
// lifecycle state machine. See plan 20, section 8.1.
var validTransitions = map[InstanceState]map[InstanceState]bool{
	InstanceProvisioning: {
		InstanceReady:       true, // sandbox-host HealthCheck passes
		InstanceTerminating: true, // Provision timeout exceeded
	},
	InstanceReady: {
		InstanceActive:   true, // First session created (CreateSandbox)
		InstanceDraining: true, // Idle > IdleCooldown OR unhealthy
	},
	InstanceActive: {
		InstanceReady:    true, // Last session destroyed (DestroySandbox)
		InstanceDraining: true, // Unhealthy (consecutive health failures)
	},
	InstanceDraining: {
		InstanceReady:       true, // Health-drained instance recovers (health reason only, validated separately)
		InstanceTerminating: true, // Session count == 0 OR DrainTimeout exceeded
	},
	InstanceTerminating: {
		// Terminal state: instance is removed from the registry after
		// TerminateInstance completes. No further transitions.
	},
}

// TransitionTo attempts to change the instance state. Returns an error if
// the transition is not valid according to the state machine.
//
// Caller must hold mi.mu for writing.
//
// Special rules:
//   - Draining -> Ready is only permitted when DrainReason is DrainHealth.
//     Scale-down and shutdown drains are irrecoverable.
func (mi *ManagedInstance) TransitionTo(newState InstanceState) error {
	allowed, ok := validTransitions[mi.State]
	if !ok || !allowed[newState] {
		return fmt.Errorf("%w: %s -> %s for instance %s",
			ErrInvalidStateTransition, mi.State, newState, mi.InstanceID)
	}

	// Draining -> Ready recovery is only permitted for health-related drains.
	if mi.State == InstanceDraining && newState == InstanceReady {
		if mi.DrainReason != DrainHealth {
			return fmt.Errorf("%w: %s -> %s not permitted for drain reason %q (instance %s)",
				ErrInvalidStateTransition, mi.State, newState, mi.DrainReason, mi.InstanceID)
		}
	}

	mi.State = newState
	return nil
}

// ClaimSession atomically increments the session count and transitions
// Ready -> Active if this is the first session. Returns an error if the
// instance is not in a routable state (Ready or Active) or is at capacity.
//
// Caller must hold mi.mu for writing.
func (mi *ManagedInstance) ClaimSession() error {
	if mi.State != InstanceReady && mi.State != InstanceActive {
		return fmt.Errorf("%w: cannot claim session on instance %s in state %s",
			ErrInstanceDraining, mi.InstanceID, mi.State)
	}
	if mi.SessionCount >= int64(mi.MaxSessions) {
		return fmt.Errorf("%w: instance %s at capacity (%d/%d)",
			ErrNoCapacity, mi.InstanceID, mi.SessionCount, mi.MaxSessions)
	}

	mi.SessionCount++

	// Transition Ready -> Active on first session.
	if mi.State == InstanceReady {
		mi.State = InstanceActive
		mi.IdleSince = time.Time{} // Clear idle timer
	}

	return nil
}

// ReleaseSession decrements the session count and transitions Active -> Ready
// if this was the last session. Returns an error if the session count would
// go negative.
//
// Caller must hold mi.mu for writing.
func (mi *ManagedInstance) ReleaseSession() error {
	if mi.SessionCount <= 0 {
		return fmt.Errorf("fleet: cannot release session on instance %s: session count already 0", mi.InstanceID)
	}

	mi.SessionCount--

	// Transition Active -> Ready on last session.
	if mi.SessionCount == 0 && mi.State == InstanceActive {
		mi.State = InstanceReady
		mi.IdleSince = time.Now()
	}

	return nil
}
