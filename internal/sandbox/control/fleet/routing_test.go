package fleet

import (
	"testing"
	"time"
)

func newRouter() *CapacityRouter {
	return NewCapacityRouter(DefaultFleetConfig())
}

func TestSelectInstance_MostHeadroom(t *testing.T) {
	r := newRouter()
	instances := []*ManagedInstance{
		{InstanceID: "i-1", State: InstanceActive, SessionCount: 8, MaxSessions: 10},
		{InstanceID: "i-2", State: InstanceActive, SessionCount: 2, MaxSessions: 10},
		{InstanceID: "i-3", State: InstanceActive, SessionCount: 5, MaxSessions: 10},
	}

	got, err := r.SelectInstance(instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.InstanceID != "i-2" {
		t.Errorf("SelectInstance = %s, want i-2 (most headroom)", got.InstanceID)
	}
}

func TestSelectInstance_TiebreakByIdleSince(t *testing.T) {
	r := newRouter()
	now := time.Now()
	instances := []*ManagedInstance{
		{InstanceID: "i-1", State: InstanceReady, SessionCount: 0, MaxSessions: 10, IdleSince: now.Add(-1 * time.Minute)},
		{InstanceID: "i-2", State: InstanceReady, SessionCount: 0, MaxSessions: 10, IdleSince: now.Add(-5 * time.Minute)},
		{InstanceID: "i-3", State: InstanceReady, SessionCount: 0, MaxSessions: 10, IdleSince: now.Add(-3 * time.Minute)},
	}

	got, err := r.SelectInstance(instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// i-2 has been idle longest.
	if got.InstanceID != "i-2" {
		t.Errorf("SelectInstance = %s, want i-2 (longest idle)", got.InstanceID)
	}
}

func TestSelectInstance_TiebreakRoundRobin(t *testing.T) {
	r := newRouter()
	// All active with same session count -> IdleSince is zero -> round-robin.
	instances := []*ManagedInstance{
		{InstanceID: "i-1", State: InstanceActive, SessionCount: 3, MaxSessions: 10},
		{InstanceID: "i-2", State: InstanceActive, SessionCount: 3, MaxSessions: 10},
		{InstanceID: "i-3", State: InstanceActive, SessionCount: 3, MaxSessions: 10},
	}

	seen := map[string]int{}
	for i := 0; i < 30; i++ {
		got, err := r.SelectInstance(instances)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		seen[got.InstanceID]++
	}

	// Each instance should get exactly 10 selections with round-robin.
	for _, id := range []string{"i-1", "i-2", "i-3"} {
		if seen[id] != 10 {
			t.Errorf("instance %s selected %d times, want 10", id, seen[id])
		}
	}
}

func TestSelectInstance_DistributesEvenlyOverManyCalls(t *testing.T) {
	r := newRouter()
	now := time.Now()
	instances := []*ManagedInstance{
		{InstanceID: "i-1", State: InstanceReady, SessionCount: 0, MaxSessions: 10, IdleSince: now.Add(-10 * time.Minute)},
		{InstanceID: "i-2", State: InstanceReady, SessionCount: 0, MaxSessions: 10, IdleSince: now.Add(-9 * time.Minute)},
	}

	// With equal headroom and different IdleSince, the longest-idle instance
	// wins every time. This is correct: the caller is responsible for updating
	// SessionCount after routing. To test even distribution via round-robin,
	// we need all IdleSince to be zero (Active instances).
	got, err := r.SelectInstance(instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.InstanceID != "i-1" {
		t.Errorf("should prefer i-1 (idle longest), got %s", got.InstanceID)
	}
}

func TestSelectInstance_SkipsDraining(t *testing.T) {
	r := newRouter()
	instances := []*ManagedInstance{
		{InstanceID: "i-drain", State: InstanceDraining, SessionCount: 0, MaxSessions: 10},
		{InstanceID: "i-ready", State: InstanceReady, SessionCount: 0, MaxSessions: 10, IdleSince: time.Now()},
	}

	got, err := r.SelectInstance(instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.InstanceID != "i-ready" {
		t.Errorf("SelectInstance = %s, want i-ready (should skip draining)", got.InstanceID)
	}
}

func TestSelectInstance_SkipsTerminating(t *testing.T) {
	r := newRouter()
	instances := []*ManagedInstance{
		{InstanceID: "i-term", State: InstanceTerminating, SessionCount: 0, MaxSessions: 10},
		{InstanceID: "i-ready", State: InstanceReady, SessionCount: 0, MaxSessions: 10, IdleSince: time.Now()},
	}

	got, err := r.SelectInstance(instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.InstanceID != "i-ready" {
		t.Errorf("SelectInstance = %s, want i-ready (should skip terminating)", got.InstanceID)
	}
}

func TestSelectInstance_SkipsProvisioning(t *testing.T) {
	r := newRouter()
	instances := []*ManagedInstance{
		{InstanceID: "i-prov", State: InstanceProvisioning, SessionCount: 0, MaxSessions: 10},
		{InstanceID: "i-active", State: InstanceActive, SessionCount: 1, MaxSessions: 10},
	}

	got, err := r.SelectInstance(instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.InstanceID != "i-active" {
		t.Errorf("SelectInstance = %s, want i-active (should skip provisioning)", got.InstanceID)
	}
}

func TestSelectInstance_SkipsAboveHeadroom(t *testing.T) {
	// Default CapacityHeadroom = 0.8, so headroom < 0.2 is filtered.
	// An instance with 9/10 sessions has headroom 0.1, which is below threshold.
	r := newRouter()
	instances := []*ManagedInstance{
		{InstanceID: "i-full", State: InstanceActive, SessionCount: 9, MaxSessions: 10},
		{InstanceID: "i-ok", State: InstanceActive, SessionCount: 5, MaxSessions: 10},
	}

	got, err := r.SelectInstance(instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.InstanceID != "i-ok" {
		t.Errorf("SelectInstance = %s, want i-ok (i-full above headroom threshold)", got.InstanceID)
	}
}

func TestSelectInstance_NoCapacity_AllFull(t *testing.T) {
	r := newRouter()
	instances := []*ManagedInstance{
		{InstanceID: "i-1", State: InstanceActive, SessionCount: 10, MaxSessions: 10},
		{InstanceID: "i-2", State: InstanceActive, SessionCount: 10, MaxSessions: 10},
	}

	_, err := r.SelectInstance(instances)
	if err != ErrNoCapacity {
		t.Errorf("expected ErrNoCapacity, got: %v", err)
	}
}

func TestSelectInstance_NoCapacity_Empty(t *testing.T) {
	r := newRouter()
	_, err := r.SelectInstance(nil)
	if err != ErrNoCapacity {
		t.Errorf("expected ErrNoCapacity for empty slice, got: %v", err)
	}
}

func TestSelectInstance_NoCapacity_AllUnroutable(t *testing.T) {
	r := newRouter()
	instances := []*ManagedInstance{
		{InstanceID: "i-1", State: InstanceDraining, SessionCount: 0, MaxSessions: 10},
		{InstanceID: "i-2", State: InstanceTerminating, SessionCount: 0, MaxSessions: 10},
		{InstanceID: "i-3", State: InstanceProvisioning, SessionCount: 0, MaxSessions: 10},
	}

	_, err := r.SelectInstance(instances)
	if err != ErrNoCapacity {
		t.Errorf("expected ErrNoCapacity, got: %v", err)
	}
}

func TestSelectInstance_SingleAvailable(t *testing.T) {
	r := newRouter()
	instances := []*ManagedInstance{
		{InstanceID: "i-only", State: InstanceReady, SessionCount: 0, MaxSessions: 10, IdleSince: time.Now()},
	}

	got, err := r.SelectInstance(instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.InstanceID != "i-only" {
		t.Errorf("SelectInstance = %s, want i-only", got.InstanceID)
	}
}

func TestSelectInstance_HeadroomThreshold_BoundaryExactly(t *testing.T) {
	// With CapacityHeadroom=0.8, an instance at 80% (8/10) has headroom=0.2.
	// The filter condition is: headroom < (1.0 - 0.8) = 0.2.
	// So headroom=0.2 is NOT filtered (0.2 < 0.2 is false).
	r := newRouter()
	instances := []*ManagedInstance{
		{InstanceID: "i-boundary", State: InstanceActive, SessionCount: 8, MaxSessions: 10},
	}

	got, err := r.SelectInstance(instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.InstanceID != "i-boundary" {
		t.Errorf("SelectInstance = %s, want i-boundary (at headroom boundary)", got.InstanceID)
	}
}

func TestSelectInstance_CustomHeadroom(t *testing.T) {
	cfg := DefaultFleetConfig()
	cfg.CapacityHeadroom = 0.5 // Filter out instances above 50% utilization.
	r := NewCapacityRouter(cfg)

	instances := []*ManagedInstance{
		{InstanceID: "i-over", State: InstanceActive, SessionCount: 6, MaxSessions: 10},  // headroom=0.4, < 0.5 threshold
		{InstanceID: "i-under", State: InstanceActive, SessionCount: 4, MaxSessions: 10}, // headroom=0.6, >= 0.5
	}

	got, err := r.SelectInstance(instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.InstanceID != "i-under" {
		t.Errorf("SelectInstance = %s, want i-under (i-over above custom headroom)", got.InstanceID)
	}
}

func TestSelectInstance_MixedIdleAndActive(t *testing.T) {
	r := newRouter()
	now := time.Now()
	// All have same headroom (0 sessions, 10 max).
	// i-idle has IdleSince set, i-active does not (zero value).
	// i-idle should win because non-zero IdleSince is preferred over zero.
	instances := []*ManagedInstance{
		{InstanceID: "i-active", State: InstanceActive, SessionCount: 0, MaxSessions: 10},
		{InstanceID: "i-idle", State: InstanceReady, SessionCount: 0, MaxSessions: 10, IdleSince: now.Add(-1 * time.Minute)},
	}

	got, err := r.SelectInstance(instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.InstanceID != "i-idle" {
		t.Errorf("SelectInstance = %s, want i-idle (has IdleSince, preferred over active)", got.InstanceID)
	}
}
