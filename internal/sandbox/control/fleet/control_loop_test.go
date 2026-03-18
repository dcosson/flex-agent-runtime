package fleet

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
)

// ---------- fakeClock ----------

// fakeClock implements Clock with controllable time for deterministic testing.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{now: t}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// ---------- Test helper ----------

func newTestFleetWithClock(t *testing.T) (*FleetSandboxControl, *mockProvisioner, *fakeClock) {
	t.Helper()
	mp := &mockProvisioner{}
	cfg := DefaultFleetConfig()
	cfg.MaxSessionsPerInstance = 10

	f, err := NewFleetSandboxControl(mp, func(addr string) (FleetNodeClient, error) {
		return &mockNodeClient{}, nil
	}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	clk := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	f.clock = clk
	return f, mp, clk
}

// ---------- Phase 1: Health Check tests ----------

func TestPhaseHealthCheck_FailureIncrementsCounter(t *testing.T) {
	f, _, _ := newTestFleetWithClock(t)
	ctx := context.Background()

	mc := &mockNodeClient{
		healthCheckFn: func(context.Context) (*HealthCheckResult, error) {
			return nil, errors.New("connection refused")
		},
	}
	mi := addInstance(f, "i-1", "10.0.1.1", InstanceReady, 0, mc)

	f.phaseHealthCheck(ctx)

	mi.mu.RLock()
	defer mi.mu.RUnlock()
	if mi.ConsecutiveHealthFailures != 1 {
		t.Errorf("ConsecutiveHealthFailures = %d, want 1", mi.ConsecutiveHealthFailures)
	}
	if mi.ConsecutiveHealthSuccesses != 0 {
		t.Errorf("ConsecutiveHealthSuccesses = %d, want 0", mi.ConsecutiveHealthSuccesses)
	}
}

func TestPhaseHealthCheck_SuccessResetsFailureCounter(t *testing.T) {
	f, _, _ := newTestFleetWithClock(t)
	ctx := context.Background()

	mc := &mockNodeClient{
		healthCheckFn: func(context.Context) (*HealthCheckResult, error) {
			return &HealthCheckResult{Status: "healthy", SessionCount: 0}, nil
		},
	}
	mi := addInstance(f, "i-1", "10.0.1.1", InstanceReady, 0, mc)
	mi.mu.Lock()
	mi.ConsecutiveHealthFailures = 2
	mi.mu.Unlock()

	f.phaseHealthCheck(ctx)

	mi.mu.RLock()
	defer mi.mu.RUnlock()
	if mi.ConsecutiveHealthFailures != 0 {
		t.Errorf("ConsecutiveHealthFailures = %d, want 0 (should be reset)", mi.ConsecutiveHealthFailures)
	}
	if mi.ConsecutiveHealthSuccesses != 1 {
		t.Errorf("ConsecutiveHealthSuccesses = %d, want 1", mi.ConsecutiveHealthSuccesses)
	}
}

func TestPhaseHealthCheck_SkipsProvisioningInstances(t *testing.T) {
	f, _, _ := newTestFleetWithClock(t)
	ctx := context.Background()

	called := false
	mc := &mockNodeClient{
		healthCheckFn: func(context.Context) (*HealthCheckResult, error) {
			called = true
			return &HealthCheckResult{Status: "healthy"}, nil
		},
	}
	addInstance(f, "i-prov", "10.0.1.1", InstanceProvisioning, 0, mc)

	f.phaseHealthCheck(ctx)

	if called {
		t.Error("phaseHealthCheck should skip Provisioning instances")
	}
}

func TestPhaseHealthCheck_SessionCountReconciliation_Overcount(t *testing.T) {
	f, _, _ := newTestFleetWithClock(t)
	ctx := context.Background()

	// Local count is 5, remote reports 3.
	mc := &mockNodeClient{
		healthCheckFn: func(context.Context) (*HealthCheckResult, error) {
			return &HealthCheckResult{Status: "healthy", SessionCount: 3}, nil
		},
	}
	mi := addInstance(f, "i-1", "10.0.1.1", InstanceActive, 5, mc)

	f.phaseHealthCheck(ctx)

	mi.mu.RLock()
	defer mi.mu.RUnlock()
	if mi.SessionCount != 3 {
		t.Errorf("SessionCount = %d, want 3 (reconciled to remote)", mi.SessionCount)
	}
}

func TestPhaseHealthCheck_SessionCountReconciliation_Undercount(t *testing.T) {
	f, _, _ := newTestFleetWithClock(t)
	ctx := context.Background()

	// Local count is 2, remote reports 5 (undercount drift).
	mc := &mockNodeClient{
		healthCheckFn: func(context.Context) (*HealthCheckResult, error) {
			return &HealthCheckResult{Status: "healthy", SessionCount: 5}, nil
		},
	}
	mi := addInstance(f, "i-1", "10.0.1.1", InstanceActive, 2, mc)

	f.phaseHealthCheck(ctx)

	mi.mu.RLock()
	defer mi.mu.RUnlock()
	if mi.SessionCount != 5 {
		t.Errorf("SessionCount = %d, want 5 (reconciled to remote)", mi.SessionCount)
	}
}

func TestPhaseHealthCheck_SessionCountReconciliation_ActiveToReady(t *testing.T) {
	f, _, clk := newTestFleetWithClock(t)
	ctx := context.Background()

	// Active instance, remote reports 0 sessions -> should transition to Ready.
	mc := &mockNodeClient{
		healthCheckFn: func(context.Context) (*HealthCheckResult, error) {
			return &HealthCheckResult{Status: "healthy", SessionCount: 0}, nil
		},
	}
	mi := addInstance(f, "i-1", "10.0.1.1", InstanceActive, 3, mc)

	f.phaseHealthCheck(ctx)

	mi.mu.RLock()
	defer mi.mu.RUnlock()
	if mi.State != InstanceReady {
		t.Errorf("State = %s, want Ready (remote reports 0 sessions)", mi.State)
	}
	if mi.SessionCount != 0 {
		t.Errorf("SessionCount = %d, want 0", mi.SessionCount)
	}
	if mi.IdleSince != clk.Now() {
		t.Error("IdleSince should be set to current clock time")
	}
}

func TestPhaseHealthCheck_SessionCountReconciliation_ReadyToActive(t *testing.T) {
	f, _, _ := newTestFleetWithClock(t)
	ctx := context.Background()

	// Ready instance, remote reports 2 sessions -> should transition to Active.
	mc := &mockNodeClient{
		healthCheckFn: func(context.Context) (*HealthCheckResult, error) {
			return &HealthCheckResult{Status: "healthy", SessionCount: 2}, nil
		},
	}
	mi := addInstance(f, "i-1", "10.0.1.1", InstanceReady, 0, mc)

	f.phaseHealthCheck(ctx)

	mi.mu.RLock()
	defer mi.mu.RUnlock()
	if mi.State != InstanceActive {
		t.Errorf("State = %s, want Active (remote reports 2 sessions)", mi.State)
	}
	if mi.SessionCount != 2 {
		t.Errorf("SessionCount = %d, want 2", mi.SessionCount)
	}
	if !mi.IdleSince.IsZero() {
		t.Error("IdleSince should be zero for Active instance")
	}
}

// ---------- Phase 2: Handle Unhealthy tests ----------

func TestPhaseHandleUnhealthy_TransitionsToDraining(t *testing.T) {
	f, _, clk := newTestFleetWithClock(t)

	mc := &mockNodeClient{}
	mi := addInstance(f, "i-1", "10.0.1.1", InstanceReady, 0, mc)
	mi.mu.Lock()
	mi.ConsecutiveHealthFailures = f.config.UnhealthyThreshold // 3
	mi.mu.Unlock()

	f.phaseHandleUnhealthy()

	mi.mu.RLock()
	defer mi.mu.RUnlock()
	if mi.State != InstanceDraining {
		t.Errorf("State = %s, want Draining", mi.State)
	}
	if mi.DrainReason != DrainHealth {
		t.Errorf("DrainReason = %q, want %q", mi.DrainReason, DrainHealth)
	}
	if mi.DrainStarted != clk.Now() {
		t.Error("DrainStarted should be set to current clock time")
	}
}

func TestPhaseHandleUnhealthy_BelowThresholdStaysReady(t *testing.T) {
	f, _, _ := newTestFleetWithClock(t)

	mc := &mockNodeClient{}
	mi := addInstance(f, "i-1", "10.0.1.1", InstanceReady, 0, mc)
	mi.mu.Lock()
	mi.ConsecutiveHealthFailures = f.config.UnhealthyThreshold - 1 // 2
	mi.mu.Unlock()

	f.phaseHandleUnhealthy()

	mi.mu.RLock()
	defer mi.mu.RUnlock()
	if mi.State != InstanceReady {
		t.Errorf("State = %s, want Ready (below threshold)", mi.State)
	}
}

func TestPhaseHandleUnhealthy_HealthDrainRecovery(t *testing.T) {
	f, _, clk := newTestFleetWithClock(t)

	mc := &mockNodeClient{}
	mi := addInstance(f, "i-1", "10.0.1.1", InstanceDraining, 0, mc)
	mi.mu.Lock()
	mi.DrainReason = DrainHealth
	mi.ConsecutiveHealthSuccesses = f.config.HealthyThreshold // 3
	mi.mu.Unlock()

	f.phaseHandleUnhealthy()

	mi.mu.RLock()
	defer mi.mu.RUnlock()
	if mi.State != InstanceReady {
		t.Errorf("State = %s, want Ready (health drain recovery)", mi.State)
	}
	if mi.DrainReason != "" {
		t.Errorf("DrainReason = %q, want empty (cleared on recovery)", mi.DrainReason)
	}
	if mi.DrainStarted != (time.Time{}) {
		t.Error("DrainStarted should be cleared on recovery")
	}
	if mi.IdleSince != clk.Now() {
		t.Error("IdleSince should be set on recovery")
	}
}

func TestPhaseHandleUnhealthy_ScaleDownDrainDoesNotRecover(t *testing.T) {
	f, _, _ := newTestFleetWithClock(t)

	mc := &mockNodeClient{}
	mi := addInstance(f, "i-1", "10.0.1.1", InstanceDraining, 0, mc)
	mi.mu.Lock()
	mi.DrainReason = DrainScaleDown
	mi.ConsecutiveHealthSuccesses = f.config.HealthyThreshold // 3
	mi.mu.Unlock()

	f.phaseHandleUnhealthy()

	mi.mu.RLock()
	defer mi.mu.RUnlock()
	if mi.State != InstanceDraining {
		t.Errorf("State = %s, want Draining (scale-down drain is irrecoverable)", mi.State)
	}
}

// ---------- Phase 3: Scale-Up tests ----------

func TestPhaseScaleUp_TriggeredWhenBelowWarmPoolTarget(t *testing.T) {
	f, mp, _ := newTestFleetWithClock(t)
	ctx := context.Background()

	// No instances at all, WarmPoolTarget=2 -> should provision.
	f.phaseScaleUp(ctx)

	mp.mu.Lock()
	calls := len(mp.launchCalls)
	mp.mu.Unlock()

	if calls != 1 {
		t.Errorf("LaunchInstance calls = %d, want 1 (warm pool below target)", calls)
	}

	// Verify the new instance is in Provisioning state.
	f.mu.RLock()
	total := len(f.instances)
	f.mu.RUnlock()
	if total != 1 {
		t.Errorf("total instances = %d, want 1", total)
	}
}

func TestPhaseScaleUp_RespectsMaxInstances(t *testing.T) {
	f, mp, _ := newTestFleetWithClock(t)
	ctx := context.Background()
	f.config.MaxInstances = 1

	mc := &mockNodeClient{}
	addInstance(f, "i-1", "10.0.1.1", InstanceActive, 10, mc) // Full, but counts toward total.

	f.phaseScaleUp(ctx)

	mp.mu.Lock()
	calls := len(mp.launchCalls)
	mp.mu.Unlock()

	if calls != 0 {
		t.Errorf("LaunchInstance calls = %d, want 0 (at MaxInstances)", calls)
	}
}

func TestPhaseScaleUp_RespectsMaxConcurrentProvisions(t *testing.T) {
	f, mp, _ := newTestFleetWithClock(t)
	ctx := context.Background()

	f.pendingLaunches.Store(int32(f.config.MaxConcurrentProvisions))

	f.phaseScaleUp(ctx)

	mp.mu.Lock()
	calls := len(mp.launchCalls)
	mp.mu.Unlock()

	if calls != 0 {
		t.Errorf("LaunchInstance calls = %d, want 0 (at MaxConcurrentProvisions)", calls)
	}
}

func TestPhaseScaleUp_SkipsWhenWarmPoolSatisfied(t *testing.T) {
	f, mp, _ := newTestFleetWithClock(t)
	ctx := context.Background()

	// Add enough idle Ready instances to satisfy WarmPoolTarget (2).
	mc := &mockNodeClient{}
	addInstance(f, "i-1", "10.0.1.1", InstanceReady, 0, mc)
	addInstance(f, "i-2", "10.0.1.2", InstanceReady, 0, mc)

	f.phaseScaleUp(ctx)

	mp.mu.Lock()
	calls := len(mp.launchCalls)
	mp.mu.Unlock()

	if calls != 0 {
		t.Errorf("LaunchInstance calls = %d, want 0 (warm pool satisfied)", calls)
	}
}

func TestPhaseScaleUp_WarmInstanceIsProvisioning(t *testing.T) {
	f, _, _ := newTestFleetWithClock(t)
	ctx := context.Background()

	f.phaseScaleUp(ctx)

	// Find the newly created instance and verify it's Provisioning (not Ready).
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, mi := range f.instances {
		mi.mu.RLock()
		state := mi.State
		mi.mu.RUnlock()
		if state != InstanceProvisioning {
			t.Errorf("warm-provisioned instance State = %s, want Provisioning", state)
		}
	}
}

// ---------- Phase 4: Scale-Down tests ----------

func TestPhaseScaleDown_DrainsIdleInstance(t *testing.T) {
	f, _, clk := newTestFleetWithClock(t)

	mc := &mockNodeClient{}
	// Add 3 Ready instances (above WarmPoolTarget=2), all idle past cooldown.
	idleSince := clk.Now().Add(-f.config.IdleCooldown - time.Minute)
	for i := 0; i < 3; i++ {
		mi := addInstance(f, fmt.Sprintf("i-%d", i), fmt.Sprintf("10.0.1.%d", i), InstanceReady, 0, mc)
		mi.mu.Lock()
		mi.IdleSince = idleSince
		mi.mu.Unlock()
	}

	f.phaseScaleDown()

	// Should drain exactly 1 instance (idleCount=3, WarmPoolTarget=2, so drain 1).
	drainingCount := 0
	f.mu.RLock()
	for _, mi := range f.instances {
		mi.mu.RLock()
		if mi.State == InstanceDraining {
			drainingCount++
			if mi.DrainReason != DrainScaleDown {
				t.Errorf("DrainReason = %q, want %q", mi.DrainReason, DrainScaleDown)
			}
		}
		mi.mu.RUnlock()
	}
	f.mu.RUnlock()

	if drainingCount != 1 {
		t.Errorf("draining instances = %d, want 1", drainingCount)
	}
}

func TestPhaseScaleDown_RespectsMinInstances(t *testing.T) {
	f, _, clk := newTestFleetWithClock(t)
	f.config.MinInstances = 2
	f.config.WarmPoolTarget = 0

	mc := &mockNodeClient{}
	idleSince := clk.Now().Add(-f.config.IdleCooldown - time.Minute)
	for i := 0; i < 2; i++ {
		mi := addInstance(f, fmt.Sprintf("i-%d", i), fmt.Sprintf("10.0.1.%d", i), InstanceReady, 0, mc)
		mi.mu.Lock()
		mi.IdleSince = idleSince
		mi.mu.Unlock()
	}

	f.phaseScaleDown()

	// total=2, MinInstances=2 -> should not drain any.
	f.mu.RLock()
	for _, mi := range f.instances {
		mi.mu.RLock()
		if mi.State != InstanceReady {
			t.Errorf("instance %s State = %s, want Ready (min instances guard)", mi.InstanceID, mi.State)
		}
		mi.mu.RUnlock()
	}
	f.mu.RUnlock()
}

func TestPhaseScaleDown_RespectsWarmPoolTarget(t *testing.T) {
	f, _, clk := newTestFleetWithClock(t)
	f.config.WarmPoolTarget = 2

	mc := &mockNodeClient{}
	idleSince := clk.Now().Add(-f.config.IdleCooldown - time.Minute)
	// Only 2 idle instances, WarmPoolTarget=2 -> should not drain any.
	for i := 0; i < 2; i++ {
		mi := addInstance(f, fmt.Sprintf("i-%d", i), fmt.Sprintf("10.0.1.%d", i), InstanceReady, 0, mc)
		mi.mu.Lock()
		mi.IdleSince = idleSince
		mi.mu.Unlock()
	}

	f.phaseScaleDown()

	f.mu.RLock()
	for _, mi := range f.instances {
		mi.mu.RLock()
		if mi.State != InstanceReady {
			t.Errorf("instance %s State = %s, want Ready (warm pool target guard)", mi.InstanceID, mi.State)
		}
		mi.mu.RUnlock()
	}
	f.mu.RUnlock()
}

func TestPhaseScaleDown_SkipsRecentlyIdleInstances(t *testing.T) {
	f, _, clk := newTestFleetWithClock(t)
	f.config.WarmPoolTarget = 0

	mc := &mockNodeClient{}
	// Instance idle for less than IdleCooldown -> should not drain.
	mi := addInstance(f, "i-1", "10.0.1.1", InstanceReady, 0, mc)
	mi.mu.Lock()
	mi.IdleSince = clk.Now().Add(-f.config.IdleCooldown + time.Minute) // Not past cooldown.
	mi.mu.Unlock()

	f.phaseScaleDown()

	mi.mu.RLock()
	if mi.State != InstanceReady {
		t.Errorf("State = %s, want Ready (idle cooldown not exceeded)", mi.State)
	}
	mi.mu.RUnlock()
}

// ---------- Phase 5: Drain Completion tests ----------

func TestPhaseDrainCompletion_TerminatesAtZeroSessions(t *testing.T) {
	f, mp, _ := newTestFleetWithClock(t)
	ctx := context.Background()

	mc := &mockNodeClient{}
	mi := addInstance(f, "i-drain", "10.0.1.1", InstanceDraining, 0, mc)
	mi.mu.Lock()
	mi.DrainReason = DrainScaleDown
	mi.DrainStarted = time.Now()
	mi.mu.Unlock()

	f.phaseDrainCompletion(ctx)

	mp.mu.Lock()
	terminated := false
	for _, id := range mp.terminateCalls {
		if id == "i-drain" {
			terminated = true
		}
	}
	mp.mu.Unlock()

	if !terminated {
		t.Error("TerminateInstance should have been called for drained instance")
	}

	// Instance should be removed from fleet.
	f.mu.RLock()
	_, exists := f.instances["i-drain"]
	f.mu.RUnlock()
	if exists {
		t.Error("drained instance should be removed from fleet after termination")
	}
}

func TestPhaseDrainCompletion_FailureLeavesForRetry(t *testing.T) {
	f, mp, _ := newTestFleetWithClock(t)
	ctx := context.Background()

	mp.terminateFn = func(_ context.Context, _ string) error {
		return errors.New("cloud API error")
	}

	mc := &mockNodeClient{}
	mi := addInstance(f, "i-drain", "10.0.1.1", InstanceDraining, 0, mc)
	mi.mu.Lock()
	mi.DrainReason = DrainScaleDown
	mi.DrainStarted = time.Now()
	mi.mu.Unlock()

	f.phaseDrainCompletion(ctx)

	// Instance should still exist.
	f.mu.RLock()
	_, exists := f.instances["i-drain"]
	f.mu.RUnlock()
	if !exists {
		t.Error("instance should remain in fleet after terminate failure")
	}

	mi.mu.RLock()
	retries := mi.TerminateRetries
	mi.mu.RUnlock()
	if retries != 1 {
		t.Errorf("TerminateRetries = %d, want 1", retries)
	}
}

func TestPhaseDrainCompletion_AlertsAfterMaxTerminateRetries(t *testing.T) {
	f, mp, _ := newTestFleetWithClock(t)
	ctx := context.Background()

	mp.terminateFn = func(_ context.Context, _ string) error {
		return errors.New("persistent failure")
	}

	mc := &mockNodeClient{}
	mi := addInstance(f, "i-drain", "10.0.1.1", InstanceDraining, 0, mc)
	mi.mu.Lock()
	mi.DrainReason = DrainScaleDown
	mi.DrainStarted = time.Now()
	mi.TerminateRetries = f.config.MaxTerminateRetries - 1
	mi.mu.Unlock()

	f.phaseDrainCompletion(ctx)

	mi.mu.RLock()
	retries := mi.TerminateRetries
	mi.mu.RUnlock()
	if retries != f.config.MaxTerminateRetries {
		t.Errorf("TerminateRetries = %d, want %d (should trigger alert)", retries, f.config.MaxTerminateRetries)
	}
}

func TestPhaseDrainCompletion_DrainTimeoutForceTerminates(t *testing.T) {
	f, mp, clk := newTestFleetWithClock(t)
	ctx := context.Background()

	mc := &mockNodeClient{}
	mi := addInstance(f, "i-drain", "10.0.1.1", InstanceDraining, 5, mc) // Still has sessions!
	mi.mu.Lock()
	mi.DrainReason = DrainHealth
	mi.DrainStarted = clk.Now().Add(-f.config.DrainTimeout - time.Minute)
	mi.mu.Unlock()

	f.phaseDrainCompletion(ctx)

	mp.mu.Lock()
	terminated := false
	for _, id := range mp.terminateCalls {
		if id == "i-drain" {
			terminated = true
		}
	}
	mp.mu.Unlock()

	if !terminated {
		t.Error("should force-terminate instance after drain timeout")
	}

	// Instance should be removed.
	f.mu.RLock()
	_, exists := f.instances["i-drain"]
	f.mu.RUnlock()
	if exists {
		t.Error("force-terminated instance should be removed from fleet")
	}
}

func TestPhaseDrainCompletion_WaitsForSessionsBeforeTimeout(t *testing.T) {
	f, mp, clk := newTestFleetWithClock(t)
	ctx := context.Background()

	mc := &mockNodeClient{}
	mi := addInstance(f, "i-drain", "10.0.1.1", InstanceDraining, 3, mc)
	mi.mu.Lock()
	mi.DrainReason = DrainHealth
	// Drain started recently, well within timeout.
	mi.DrainStarted = clk.Now().Add(-time.Minute)
	mi.mu.Unlock()

	f.phaseDrainCompletion(ctx)

	// Should NOT terminate since sessions > 0 and timeout not reached.
	mp.mu.Lock()
	calls := len(mp.terminateCalls)
	mp.mu.Unlock()

	if calls != 0 {
		t.Errorf("TerminateInstance calls = %d, want 0 (waiting for sessions to drain)", calls)
	}
}

func TestPhaseDrainCompletion_Phase5b_CleansStuckTerminating(t *testing.T) {
	f, mp, _ := newTestFleetWithClock(t)
	ctx := context.Background()

	mc := &mockNodeClient{}
	addInstance(f, "i-stuck", "10.0.1.1", InstanceTerminating, 0, mc)

	f.phaseDrainCompletion(ctx)

	mp.mu.Lock()
	terminated := false
	for _, id := range mp.terminateCalls {
		if id == "i-stuck" {
			terminated = true
		}
	}
	mp.mu.Unlock()

	if !terminated {
		t.Error("should retry TerminateInstance for stuck Terminating instance")
	}

	f.mu.RLock()
	_, exists := f.instances["i-stuck"]
	f.mu.RUnlock()
	if exists {
		t.Error("stuck Terminating instance should be removed on successful termination")
	}
}

// ---------- Phase 6: Provisioning Completion tests ----------

func TestPhaseProvisioningCompletion_TransitionsToReady(t *testing.T) {
	f, _, clk := newTestFleetWithClock(t)
	ctx := context.Background()

	mc := &mockNodeClient{
		healthCheckFn: func(context.Context) (*HealthCheckResult, error) {
			return &HealthCheckResult{Status: "healthy"}, nil
		},
	}
	mi := addInstance(f, "i-prov", "10.0.1.1", InstanceProvisioning, 0, mc)
	mi.mu.Lock()
	mi.ProvisionStarted = clk.Now()
	mi.mu.Unlock()

	f.phaseProvisioningCompletion(ctx)

	mi.mu.RLock()
	defer mi.mu.RUnlock()
	if mi.State != InstanceReady {
		t.Errorf("State = %s, want Ready (healthy check passed)", mi.State)
	}
	if mi.IdleSince != clk.Now() {
		t.Error("IdleSince should be set when transitioning to Ready")
	}
}

func TestPhaseProvisioningCompletion_StaysProvisioningOnHealthFailure(t *testing.T) {
	f, _, clk := newTestFleetWithClock(t)
	ctx := context.Background()

	mc := &mockNodeClient{
		healthCheckFn: func(context.Context) (*HealthCheckResult, error) {
			return nil, errors.New("not ready yet")
		},
	}
	mi := addInstance(f, "i-prov", "10.0.1.1", InstanceProvisioning, 0, mc)
	mi.mu.Lock()
	mi.ProvisionStarted = clk.Now()
	mi.mu.Unlock()

	f.phaseProvisioningCompletion(ctx)

	mi.mu.RLock()
	defer mi.mu.RUnlock()
	if mi.State != InstanceProvisioning {
		t.Errorf("State = %s, want Provisioning (health check failed, still booting)", mi.State)
	}
}

func TestPhaseProvisioningCompletion_TimeoutTerminates(t *testing.T) {
	f, mp, clk := newTestFleetWithClock(t)
	ctx := context.Background()

	mc := &mockNodeClient{
		healthCheckFn: func(context.Context) (*HealthCheckResult, error) {
			return nil, errors.New("still not ready")
		},
	}
	mi := addInstance(f, "i-prov", "10.0.1.1", InstanceProvisioning, 0, mc)
	mi.mu.Lock()
	mi.ProvisionStarted = clk.Now().Add(-f.config.ProvisionTimeout - time.Minute)
	mi.mu.Unlock()

	f.phaseProvisioningCompletion(ctx)

	mp.mu.Lock()
	terminated := false
	for _, id := range mp.terminateCalls {
		if id == "i-prov" {
			terminated = true
		}
	}
	mp.mu.Unlock()

	if !terminated {
		t.Error("should terminate instance after provision timeout")
	}

	f.mu.RLock()
	_, exists := f.instances["i-prov"]
	f.mu.RUnlock()
	if exists {
		t.Error("timed-out provisioning instance should be removed from fleet")
	}
}

// ---------- Full iteration test ----------

func TestRunIteration_AllPhasesExecute(t *testing.T) {
	f, _, clk := newTestFleetWithClock(t)
	ctx := context.Background()

	// Set up a scenario that exercises multiple phases:
	// - A Ready instance for health check
	// - WarmPoolTarget not met -> triggers scale-up
	mc := &mockNodeClient{
		healthCheckFn: func(context.Context) (*HealthCheckResult, error) {
			return &HealthCheckResult{Status: "healthy", SessionCount: 0}, nil
		},
	}
	mi := addInstance(f, "i-1", "10.0.1.1", InstanceReady, 0, mc)
	mi.mu.Lock()
	mi.IdleSince = clk.Now()
	mi.mu.Unlock()

	f.runIteration(ctx)

	// Health check should have incremented successes.
	mi.mu.RLock()
	successes := mi.ConsecutiveHealthSuccesses
	mi.mu.RUnlock()
	if successes != 1 {
		t.Errorf("ConsecutiveHealthSuccesses = %d, want 1 (health check ran)", successes)
	}

	// Scale-up should have added a new instance (readyCount=1, WarmPoolTarget=2).
	f.mu.RLock()
	total := len(f.instances)
	f.mu.RUnlock()
	if total != 2 {
		t.Errorf("total instances = %d, want 2 (scale-up should have provisioned one)", total)
	}
}

// ---------- Crash Recovery tests ----------

func TestRecoverInstances_RediscoversInstances(t *testing.T) {
	f, mp, clk := newTestFleetWithClock(t)
	ctx := context.Background()

	mp.listFn = func(_ context.Context, _ instance.InstanceFilter) ([]instance.InstanceInfo, error) {
		return []instance.InstanceInfo{
			{InstanceID: "i-recovered-1", PrivateIP: "10.0.2.1", State: instance.CloudInstanceRunning},
			{InstanceID: "i-recovered-2", PrivateIP: "10.0.2.2", State: instance.CloudInstancePending},
		}, nil
	}

	err := f.RecoverInstances(ctx)
	if err != nil {
		t.Fatalf("RecoverInstances error: %v", err)
	}

	f.mu.RLock()
	total := len(f.instances)
	f.mu.RUnlock()
	if total != 2 {
		t.Errorf("total instances = %d, want 2", total)
	}

	for _, id := range []string{"i-recovered-1", "i-recovered-2"} {
		f.mu.RLock()
		mi, ok := f.instances[id]
		f.mu.RUnlock()
		if !ok {
			t.Errorf("instance %s not found in fleet", id)
			continue
		}
		mi.mu.RLock()
		if mi.State != InstanceProvisioning {
			t.Errorf("instance %s State = %s, want Provisioning", id, mi.State)
		}
		if mi.ProvisionStarted != clk.Now() {
			t.Errorf("instance %s ProvisionStarted not set to clock time", id)
		}
		mi.mu.RUnlock()
	}
}

func TestRecoverInstances_SkipsAlreadyKnown(t *testing.T) {
	f, mp, _ := newTestFleetWithClock(t)
	ctx := context.Background()

	mc := &mockNodeClient{}
	addInstance(f, "i-existing", "10.0.1.1", InstanceReady, 0, mc)

	mp.listFn = func(_ context.Context, _ instance.InstanceFilter) ([]instance.InstanceInfo, error) {
		return []instance.InstanceInfo{
			{InstanceID: "i-existing", PrivateIP: "10.0.1.1", State: instance.CloudInstanceRunning},
			{InstanceID: "i-new", PrivateIP: "10.0.2.1", State: instance.CloudInstanceRunning},
		}, nil
	}

	err := f.RecoverInstances(ctx)
	if err != nil {
		t.Fatalf("RecoverInstances error: %v", err)
	}

	f.mu.RLock()
	total := len(f.instances)
	f.mu.RUnlock()
	if total != 2 {
		t.Errorf("total instances = %d, want 2 (1 existing + 1 new)", total)
	}

	// Existing instance should keep its original state, not be overwritten.
	f.mu.RLock()
	mi := f.instances["i-existing"]
	f.mu.RUnlock()
	mi.mu.RLock()
	if mi.State != InstanceReady {
		t.Errorf("existing instance State = %s, want Ready (should not be overwritten)", mi.State)
	}
	mi.mu.RUnlock()
}

func TestRecoverInstances_ListFailure(t *testing.T) {
	f, mp, _ := newTestFleetWithClock(t)
	ctx := context.Background()

	mp.listFn = func(_ context.Context, _ instance.InstanceFilter) ([]instance.InstanceInfo, error) {
		return nil, errors.New("cloud API unavailable")
	}

	err := f.RecoverInstances(ctx)
	if err == nil {
		t.Fatal("expected error from ListInstances failure")
	}
}

// ---------- StartControlLoop / StopControlLoop test ----------

func TestStartControlLoop_CanBeStopped(t *testing.T) {
	f, _, _ := newTestFleetWithClock(t)
	f.config.HealthCheckInterval = 10 * time.Millisecond

	f.StartControlLoop()

	// Give the loop a moment to tick.
	time.Sleep(50 * time.Millisecond)

	// Close should stop the loop cleanly.
	err := f.Close()
	if err != nil {
		t.Fatalf("Close error: %v", err)
	}
}
