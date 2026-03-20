package fleet

import (
	"context"
	"fmt"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
)

// Clock abstracts time for deterministic testing.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// StartControlLoop begins the background control loop goroutine. Must be
// called after NewFleetSandboxControl. The loop runs every HealthCheckInterval.
func (f *FleetSandboxControl) StartControlLoop() {
	f.loopDone = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	f.cancelLoop = cancel
	go f.runControlLoop(ctx)
}

func (f *FleetSandboxControl) runControlLoop(ctx context.Context) {
	defer close(f.loopDone)
	ticker := time.NewTicker(f.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.runIteration(ctx)
		}
	}
}

// runIteration executes one iteration of the 6-phase control loop.
// Exported for testing.
func (f *FleetSandboxControl) runIteration(ctx context.Context) {
	defer f.updateMetricsSnapshot()

	f.phaseHealthCheck(ctx)
	f.phaseHandleUnhealthy()
	f.phaseScaleUp(ctx)
	f.phaseScaleDown()
	f.phaseDrainCompletion(ctx)
	f.phaseProvisioningCompletion(ctx)
}

// ---------- Phase 1: Health Check + Session Count Reconciliation ----------

func (f *FleetSandboxControl) phaseHealthCheck(ctx context.Context) {
	f.mu.RLock()
	targets := make([]*ManagedInstance, 0, len(f.instances))
	for _, mi := range f.instances {
		targets = append(targets, mi)
	}
	f.mu.RUnlock()

	for _, mi := range targets {
		mi.mu.RLock()
		state := mi.State
		client := mi.Client
		mi.mu.RUnlock()

		if state != InstanceReady && state != InstanceActive && state != InstanceDraining {
			continue
		}
		if client == nil {
			continue
		}

		hcStart := time.Now()
		result, err := client.HealthCheck(ctx)
		f.metrics.observeHealthCheckDuration(time.Since(hcStart))

		mi.mu.Lock()
		if err != nil {
			mi.ConsecutiveHealthFailures++
			mi.ConsecutiveHealthSuccesses = 0
			f.metrics.healthChecksTotal.WithLabelValues("error").Inc()
		} else {
			mi.ConsecutiveHealthFailures = 0
			mi.ConsecutiveHealthSuccesses++
			f.metrics.healthChecksTotal.WithLabelValues("healthy").Inc()

			// Reconcile session count (section 4.1.3).
			remoteCount := int64(result.SessionCount)
			if remoteCount != mi.SessionCount {
				direction := "down"
				if remoteCount > mi.SessionCount {
					direction = "up"
				}
				f.metrics.reconciliationsTotal.WithLabelValues(direction).Inc()
				f.logger.Warn("session count reconciled",
					"instance_id", mi.InstanceID,
					"local", mi.SessionCount, "remote", remoteCount)
				mi.SessionCount = remoteCount

				if mi.SessionCount == 0 && mi.State == InstanceActive {
					mi.State = InstanceReady
					mi.IdleSince = f.clock.Now()
				} else if mi.SessionCount > 0 && mi.State == InstanceReady {
					mi.State = InstanceActive
					mi.IdleSince = time.Time{}
				}
			}
		}
		mi.mu.Unlock()
	}
}

// ---------- Phase 2: Handle Unhealthy + Drain Recovery ----------

func (f *FleetSandboxControl) phaseHandleUnhealthy() {
	f.mu.RLock()
	targets := make([]*ManagedInstance, 0, len(f.instances))
	for _, mi := range f.instances {
		targets = append(targets, mi)
	}
	f.mu.RUnlock()

	for _, mi := range targets {
		mi.mu.Lock()

		// Transition unhealthy instances to Draining.
		if (mi.State == InstanceReady || mi.State == InstanceActive) &&
			mi.ConsecutiveHealthFailures >= f.config.UnhealthyThreshold {
			if err := mi.TransitionTo(InstanceDraining); err == nil {
				mi.DrainReason = DrainHealth
				mi.DrainStarted = f.clock.Now()
				f.logger.Warn("instance marked unhealthy, draining",
					"instance_id", mi.InstanceID,
					"failures", mi.ConsecutiveHealthFailures)
			}
		}

		// Drain recovery: health-drained instances can return to Ready.
		if mi.State == InstanceDraining && mi.DrainReason == DrainHealth &&
			mi.ConsecutiveHealthSuccesses >= f.config.HealthyThreshold {
			if err := mi.TransitionTo(InstanceReady); err == nil {
				mi.DrainReason = ""
				mi.DrainStarted = time.Time{}
				mi.IdleSince = f.clock.Now()
				f.metrics.drainRecoveriesTotal.Inc()
				f.logger.Info("health-drained instance recovered",
					"instance_id", mi.InstanceID)
			}
		}

		mi.mu.Unlock()
	}
}

// ---------- Phase 3: Scale-Up ----------

func (f *FleetSandboxControl) phaseScaleUp(ctx context.Context) {
	f.mu.RLock()
	total := len(f.instances)
	readyCount := 0
	for _, mi := range f.instances {
		mi.mu.RLock()
		if (mi.State == InstanceReady || mi.State == InstanceActive) &&
			mi.SessionCount < int64(mi.MaxSessions) {
			readyCount++
		}
		mi.mu.RUnlock()
	}
	f.mu.RUnlock()

	if readyCount >= f.config.WarmPoolTarget || total >= f.config.MaxInstances {
		return
	}
	if int(f.pendingLaunches.Load()) >= f.config.MaxConcurrentProvisions {
		return
	}

	if err := f.provisionWarmInstance(ctx); err != nil {
		f.logger.Warn("scale-up provision failed", "error", err)
	}
}

// provisionWarmInstance launches a new instance in Provisioning state.
// The control loop's phase 6 promotes it to Ready after health check passes.
func (f *FleetSandboxControl) provisionWarmInstance(ctx context.Context) error {
	defer f.updateMetricsSnapshot()

	f.mu.RLock()
	total := len(f.instances)
	f.mu.RUnlock()
	if total >= f.config.MaxInstances {
		return ErrNoCapacity
	}

	for {
		current := f.pendingLaunches.Load()
		if int(current) >= f.config.MaxConcurrentProvisions {
			return ErrNoCapacity
		}
		if f.pendingLaunches.CompareAndSwap(current, current+1) {
			break
		}
	}
	defer f.pendingLaunches.Add(-1)

	start := f.clock.Now()
	info, err := f.provisioner.LaunchInstance(ctx, f.config.InstanceConfig)
	if err != nil {
		f.metrics.observeProvisionDuration(f.clock.Now().Sub(start))
		f.metrics.provisionsTotal.WithLabelValues("error").Inc()
		return fmt.Errorf("fleet: warm provision failed: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", info.PrivateIP, f.config.SandboxHostPort)
	client, err := f.clientFactory(addr)
	if err != nil {
		f.metrics.observeProvisionDuration(f.clock.Now().Sub(start))
		f.metrics.provisionsTotal.WithLabelValues("error").Inc()
		return fmt.Errorf("fleet: client creation failed for %s: %w", info.InstanceID, err)
	}

	mi := &ManagedInstance{
		InstanceID:       info.InstanceID,
		State:            InstanceProvisioning,
		Client:           client,
		MaxSessions:      f.config.MaxSessionsPerInstance,
		IP:               info.PrivateIP,
		ProvisionStarted: start,
	}

	f.mu.Lock()
	f.instances[info.InstanceID] = mi
	f.mu.Unlock()
	f.metrics.provisionsTotal.WithLabelValues("success").Inc()

	f.logger.Info("warm pool provision started",
		"instance_id", info.InstanceID, "ip", info.PrivateIP)
	return nil
}

// ---------- Phase 4: Scale-Down ----------

func (f *FleetSandboxControl) phaseScaleDown() {
	f.mu.RLock()
	total := len(f.instances)
	var candidates []*ManagedInstance
	idleCount := 0

	for _, mi := range f.instances {
		mi.mu.RLock()
		if mi.State == InstanceReady && mi.SessionCount == 0 {
			idleCount++
			if !mi.IdleSince.IsZero() &&
				f.clock.Now().Sub(mi.IdleSince) > f.config.IdleCooldown {
				candidates = append(candidates, mi)
			}
		}
		mi.mu.RUnlock()
	}
	f.mu.RUnlock()

	for _, mi := range candidates {
		// Check guards: don't go below MinInstances or WarmPoolTarget.
		if total <= f.config.MinInstances {
			break
		}
		if idleCount <= f.config.WarmPoolTarget {
			break
		}

		mi.mu.Lock()
		// Re-check state under lock.
		if mi.State == InstanceReady && mi.SessionCount == 0 {
			if err := mi.TransitionTo(InstanceDraining); err == nil {
				mi.DrainReason = DrainScaleDown
				mi.DrainStarted = f.clock.Now()
				total--
				idleCount--
				f.logger.Info("scale-down: draining idle instance",
					"instance_id", mi.InstanceID)
			}
		}
		mi.mu.Unlock()
	}
}

// ---------- Phase 5: Drain Completion + 5b: Stuck Terminating ----------

func (f *FleetSandboxControl) phaseDrainCompletion(ctx context.Context) {
	f.mu.RLock()
	targets := make([]*ManagedInstance, 0)
	for _, mi := range f.instances {
		targets = append(targets, mi)
	}
	f.mu.RUnlock()

	var toRemove []string

	for _, mi := range targets {
		mi.mu.RLock()
		state := mi.State
		sessions := mi.SessionCount
		drainStarted := mi.DrainStarted
		drainReason := mi.DrainReason
		instanceID := mi.InstanceID
		mi.mu.RUnlock()

		if state == InstanceDraining {
			// Check drain timeout.
			if !drainStarted.IsZero() && f.clock.Now().Sub(drainStarted) > f.config.DrainTimeout {
				f.logger.Warn("drain timeout exceeded, force-terminating",
					"instance_id", instanceID, "sessions", sessions)
				f.metrics.terminationsTotal.WithLabelValues(terminationReasonLabel(drainReason, true)).Inc()
				if err := f.provisioner.TerminateInstance(ctx, instanceID); err == nil {
					if !drainStarted.IsZero() {
						f.metrics.observeDrainDuration(f.clock.Now().Sub(drainStarted))
					}
					mi.mu.Lock()
					mi.State = InstanceTerminating
					mi.mu.Unlock()
					toRemove = append(toRemove, instanceID)
				}
				continue
			}

			// Terminate drained instances with 0 sessions.
			if sessions == 0 {
				f.metrics.terminationsTotal.WithLabelValues(terminationReasonLabel(drainReason, false)).Inc()
				err := f.provisioner.TerminateInstance(ctx, instanceID)
				mi.mu.Lock()
				if err == nil {
					if !drainStarted.IsZero() {
						f.metrics.observeDrainDuration(f.clock.Now().Sub(drainStarted))
					}
					mi.State = InstanceTerminating
					toRemove = append(toRemove, instanceID)
				} else {
					mi.TerminateRetries++
					if mi.TerminateRetries >= f.config.MaxTerminateRetries {
						f.logger.Error("ALERT: max terminate retries exceeded",
							"instance_id", instanceID,
							"retries", mi.TerminateRetries)
					}
				}
				mi.mu.Unlock()
			}
		}

		// Phase 5b: Clean up stuck Terminating instances.
		if state == InstanceTerminating {
			if err := f.provisioner.TerminateInstance(ctx, instanceID); err == nil {
				toRemove = append(toRemove, instanceID)
			}
		}
	}

	if len(toRemove) > 0 {
		f.mu.Lock()
		for _, id := range toRemove {
			delete(f.instances, id)
		}
		f.mu.Unlock()
	}
}

// ---------- Phase 6: Provisioning Completion ----------

func (f *FleetSandboxControl) phaseProvisioningCompletion(ctx context.Context) {
	f.mu.RLock()
	targets := make([]*ManagedInstance, 0)
	for _, mi := range f.instances {
		targets = append(targets, mi)
	}
	f.mu.RUnlock()

	var toRemove []string

	for _, mi := range targets {
		mi.mu.RLock()
		state := mi.State
		client := mi.Client
		provStarted := mi.ProvisionStarted
		instanceID := mi.InstanceID
		mi.mu.RUnlock()

		if state != InstanceProvisioning {
			continue
		}

		// Check provision timeout.
		if !provStarted.IsZero() && f.clock.Now().Sub(provStarted) > f.config.ProvisionTimeout {
			f.logger.Warn("provision timeout, terminating",
				"instance_id", instanceID)
			f.metrics.provisionsTotal.WithLabelValues("timeout").Inc()
			if err := f.provisioner.TerminateInstance(ctx, instanceID); err == nil {
				mi.mu.Lock()
				mi.State = InstanceTerminating
				mi.mu.Unlock()
				toRemove = append(toRemove, instanceID)
			}
			continue
		}

		if client == nil {
			continue
		}

		// Poll health check.
		_, err := client.HealthCheck(ctx)
		if err == nil {
			mi.mu.Lock()
			if err := mi.TransitionTo(InstanceReady); err == nil {
				mi.IdleSince = f.clock.Now()
				if !provStarted.IsZero() {
					f.metrics.observeProvisionDuration(f.clock.Now().Sub(provStarted))
				}
				f.logger.Info("instance provisioned and ready",
					"instance_id", instanceID)
			}
			mi.mu.Unlock()
		}
	}

	if len(toRemove) > 0 {
		f.mu.Lock()
		for _, id := range toRemove {
			delete(f.instances, id)
		}
		f.mu.Unlock()
	}
}

// ---------- Crash Recovery ----------

// RecoverInstances rediscovers managed instances via cloud API tags and
// re-adds them to the fleet. Call before StartControlLoop after a restart.
func (f *FleetSandboxControl) RecoverInstances(ctx context.Context) error {
	defer f.updateMetricsSnapshot()

	filter := instance.InstanceFilter{
		Tags: f.config.InstanceConfig.Tags,
		States: []instance.CloudInstanceState{
			instance.CloudInstanceRunning,
			instance.CloudInstancePending,
		},
	}

	infos, err := f.provisioner.ListInstances(ctx, filter)
	if err != nil {
		return fmt.Errorf("fleet: crash recovery list failed: %w", err)
	}

	for _, info := range infos {
		f.mu.RLock()
		_, exists := f.instances[info.InstanceID]
		f.mu.RUnlock()
		if exists {
			continue
		}

		addr := fmt.Sprintf("%s:%d", info.PrivateIP, f.config.SandboxHostPort)
		client, err := f.clientFactory(addr)
		if err != nil {
			f.logger.Warn("crash recovery: failed to create client",
				"instance_id", info.InstanceID, "error", err)
			continue
		}

		mi := &ManagedInstance{
			InstanceID:       info.InstanceID,
			State:            InstanceProvisioning,
			Client:           client,
			MaxSessions:      f.config.MaxSessionsPerInstance,
			IP:               info.PrivateIP,
			ProvisionStarted: f.clock.Now(),
		}

		f.mu.Lock()
		f.instances[info.InstanceID] = mi
		f.mu.Unlock()

		f.logger.Info("crash recovery: rediscovered instance",
			"instance_id", info.InstanceID, "ip", info.PrivateIP)
	}

	return nil
}
