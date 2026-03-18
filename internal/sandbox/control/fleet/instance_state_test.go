package fleet

import (
	"errors"
	"testing"
	"time"
)

func TestValidStateTransitions(t *testing.T) {
	// All valid transitions from the state machine (plan 20, section 8.1).
	valid := []struct {
		name string
		from InstanceState
		to   InstanceState
		// drainReason is only relevant for Draining -> Ready.
		drainReason DrainReason
	}{
		{"Provisioning -> Ready", InstanceProvisioning, InstanceReady, ""},
		{"Provisioning -> Terminating", InstanceProvisioning, InstanceTerminating, ""},
		{"Ready -> Active", InstanceReady, InstanceActive, ""},
		{"Ready -> Draining", InstanceReady, InstanceDraining, ""},
		{"Active -> Ready", InstanceActive, InstanceReady, ""},
		{"Active -> Draining", InstanceActive, InstanceDraining, ""},
		{"Draining -> Ready (health)", InstanceDraining, InstanceReady, DrainHealth},
		{"Draining -> Terminating", InstanceDraining, InstanceTerminating, ""},
	}

	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			mi := &ManagedInstance{
				InstanceID:  "i-test",
				State:       tt.from,
				DrainReason: tt.drainReason,
			}

			if err := mi.TransitionTo(tt.to); err != nil {
				t.Errorf("TransitionTo(%s) from %s should succeed, got: %v", tt.to, tt.from, err)
			}
			if mi.State != tt.to {
				t.Errorf("after TransitionTo(%s), State = %s, want %s", tt.to, mi.State, tt.to)
			}
		})
	}
}

func TestInvalidStateTransitions(t *testing.T) {
	invalid := []struct {
		name string
		from InstanceState
		to   InstanceState
	}{
		// Self-transitions
		{"Provisioning -> Provisioning", InstanceProvisioning, InstanceProvisioning},
		{"Ready -> Ready", InstanceReady, InstanceReady},
		{"Active -> Active", InstanceActive, InstanceActive},

		// Skip states
		{"Provisioning -> Active", InstanceProvisioning, InstanceActive},
		{"Provisioning -> Draining", InstanceProvisioning, InstanceDraining},

		// Backward transitions that aren't allowed
		{"Ready -> Provisioning", InstanceReady, InstanceProvisioning},
		{"Active -> Provisioning", InstanceActive, InstanceProvisioning},
		{"Active -> Terminating", InstanceActive, InstanceTerminating},
		{"Ready -> Terminating", InstanceReady, InstanceTerminating},

		// From terminal state
		{"Terminating -> Ready", InstanceTerminating, InstanceReady},
		{"Terminating -> Active", InstanceTerminating, InstanceActive},
		{"Terminating -> Provisioning", InstanceTerminating, InstanceProvisioning},
		{"Terminating -> Draining", InstanceTerminating, InstanceDraining},

		// Draining reverse (non-health)
		{"Draining -> Active", InstanceDraining, InstanceActive},
		{"Draining -> Provisioning", InstanceDraining, InstanceProvisioning},
	}

	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			mi := &ManagedInstance{
				InstanceID: "i-test",
				State:      tt.from,
			}

			err := mi.TransitionTo(tt.to)
			if err == nil {
				t.Errorf("TransitionTo(%s) from %s should fail, but succeeded", tt.to, tt.from)
			}
			if !errors.Is(err, ErrInvalidStateTransition) {
				t.Errorf("expected ErrInvalidStateTransition, got: %v", err)
			}
		})
	}
}

func TestDrainingToReadyRejectedForScaleDown(t *testing.T) {
	mi := &ManagedInstance{
		InstanceID:  "i-test",
		State:       InstanceDraining,
		DrainReason: DrainScaleDown,
	}

	err := mi.TransitionTo(InstanceReady)
	if err == nil {
		t.Fatal("Draining -> Ready should be rejected for scale-down drain reason")
	}
	if !errors.Is(err, ErrInvalidStateTransition) {
		t.Errorf("expected ErrInvalidStateTransition, got: %v", err)
	}
}

func TestDrainingToReadyRejectedForShutdown(t *testing.T) {
	mi := &ManagedInstance{
		InstanceID:  "i-test",
		State:       InstanceDraining,
		DrainReason: DrainShutdown,
	}

	err := mi.TransitionTo(InstanceReady)
	if err == nil {
		t.Fatal("Draining -> Ready should be rejected for shutdown drain reason")
	}
	if !errors.Is(err, ErrInvalidStateTransition) {
		t.Errorf("expected ErrInvalidStateTransition, got: %v", err)
	}
}

func TestDrainingToReadyAllowedForHealth(t *testing.T) {
	mi := &ManagedInstance{
		InstanceID:  "i-test",
		State:       InstanceDraining,
		DrainReason: DrainHealth,
	}

	err := mi.TransitionTo(InstanceReady)
	if err != nil {
		t.Fatalf("Draining -> Ready should be allowed for health drain reason, got: %v", err)
	}
	if mi.State != InstanceReady {
		t.Errorf("State = %s, want %s", mi.State, InstanceReady)
	}
}

func TestClaimSession_ReadyToActive(t *testing.T) {
	mi := &ManagedInstance{
		InstanceID:   "i-test",
		State:        InstanceReady,
		SessionCount: 0,
		MaxSessions:  10,
		IdleSince:    time.Now().Add(-5 * time.Minute),
	}

	if err := mi.ClaimSession(); err != nil {
		t.Fatalf("ClaimSession() unexpected error: %v", err)
	}

	if mi.State != InstanceActive {
		t.Errorf("State = %s, want %s (should transition on first session)", mi.State, InstanceActive)
	}
	if mi.SessionCount != 1 {
		t.Errorf("SessionCount = %d, want 1", mi.SessionCount)
	}
	if !mi.IdleSince.IsZero() {
		t.Error("IdleSince should be cleared after claiming a session")
	}
}

func TestClaimSession_ActiveStaysActive(t *testing.T) {
	mi := &ManagedInstance{
		InstanceID:   "i-test",
		State:        InstanceActive,
		SessionCount: 3,
		MaxSessions:  10,
	}

	if err := mi.ClaimSession(); err != nil {
		t.Fatalf("ClaimSession() unexpected error: %v", err)
	}

	if mi.State != InstanceActive {
		t.Errorf("State = %s, want %s", mi.State, InstanceActive)
	}
	if mi.SessionCount != 4 {
		t.Errorf("SessionCount = %d, want 4", mi.SessionCount)
	}
}

func TestClaimSession_AtCapacity(t *testing.T) {
	mi := &ManagedInstance{
		InstanceID:   "i-test",
		State:        InstanceActive,
		SessionCount: 10,
		MaxSessions:  10,
	}

	err := mi.ClaimSession()
	if err == nil {
		t.Fatal("ClaimSession() should fail at capacity")
	}
	if !errors.Is(err, ErrNoCapacity) {
		t.Errorf("expected ErrNoCapacity, got: %v", err)
	}
}

func TestClaimSession_DrainingRejected(t *testing.T) {
	mi := &ManagedInstance{
		InstanceID:   "i-test",
		State:        InstanceDraining,
		SessionCount: 1,
		MaxSessions:  10,
	}

	err := mi.ClaimSession()
	if err == nil {
		t.Fatal("ClaimSession() should fail on draining instance")
	}
	if !errors.Is(err, ErrInstanceDraining) {
		t.Errorf("expected ErrInstanceDraining, got: %v", err)
	}
}

func TestClaimSession_TerminatingRejected(t *testing.T) {
	mi := &ManagedInstance{
		InstanceID:   "i-test",
		State:        InstanceTerminating,
		SessionCount: 0,
		MaxSessions:  10,
	}

	err := mi.ClaimSession()
	if err == nil {
		t.Fatal("ClaimSession() should fail on terminating instance")
	}
}

func TestClaimSession_ProvisioningRejected(t *testing.T) {
	mi := &ManagedInstance{
		InstanceID:   "i-test",
		State:        InstanceProvisioning,
		SessionCount: 0,
		MaxSessions:  10,
	}

	err := mi.ClaimSession()
	if err == nil {
		t.Fatal("ClaimSession() should fail on provisioning instance")
	}
}

func TestReleaseSession_ActiveToReady(t *testing.T) {
	mi := &ManagedInstance{
		InstanceID:   "i-test",
		State:        InstanceActive,
		SessionCount: 1,
		MaxSessions:  10,
	}

	if err := mi.ReleaseSession(); err != nil {
		t.Fatalf("ReleaseSession() unexpected error: %v", err)
	}

	if mi.State != InstanceReady {
		t.Errorf("State = %s, want %s (should transition on last session)", mi.State, InstanceReady)
	}
	if mi.SessionCount != 0 {
		t.Errorf("SessionCount = %d, want 0", mi.SessionCount)
	}
	if mi.IdleSince.IsZero() {
		t.Error("IdleSince should be set after last session released")
	}
}

func TestReleaseSession_ActiveStaysActive(t *testing.T) {
	mi := &ManagedInstance{
		InstanceID:   "i-test",
		State:        InstanceActive,
		SessionCount: 3,
		MaxSessions:  10,
	}

	if err := mi.ReleaseSession(); err != nil {
		t.Fatalf("ReleaseSession() unexpected error: %v", err)
	}

	if mi.State != InstanceActive {
		t.Errorf("State = %s, want %s (should stay Active with remaining sessions)", mi.State, InstanceActive)
	}
	if mi.SessionCount != 2 {
		t.Errorf("SessionCount = %d, want 2", mi.SessionCount)
	}
}

func TestReleaseSession_DrainingStaysDraining(t *testing.T) {
	// When draining with sessions, releasing one session should NOT
	// transition back. The instance stays in Draining (the control loop
	// handles the Draining -> Terminating transition when count reaches 0).
	mi := &ManagedInstance{
		InstanceID:   "i-test",
		State:        InstanceDraining,
		SessionCount: 2,
		MaxSessions:  10,
		DrainReason:  DrainScaleDown,
	}

	if err := mi.ReleaseSession(); err != nil {
		t.Fatalf("ReleaseSession() unexpected error: %v", err)
	}

	if mi.State != InstanceDraining {
		t.Errorf("State = %s, want %s (draining instance should stay draining)", mi.State, InstanceDraining)
	}
	if mi.SessionCount != 1 {
		t.Errorf("SessionCount = %d, want 1", mi.SessionCount)
	}
}

func TestReleaseSession_ZeroCountError(t *testing.T) {
	mi := &ManagedInstance{
		InstanceID:   "i-test",
		State:        InstanceReady,
		SessionCount: 0,
		MaxSessions:  10,
	}

	err := mi.ReleaseSession()
	if err == nil {
		t.Fatal("ReleaseSession() should fail when session count is already 0")
	}
}
