package fleet

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
)

// Compile-time interface checks.
var (
	_ control.SandboxControl = (*FleetSandboxControl)(nil)
	_ io.Closer              = (*FleetSandboxControl)(nil)
)

// FleetSandboxControl implements SandboxControl by managing a fleet of
// sandbox-host instances. It provisions instances via InstanceProvisioner,
// monitors health, maintains a warm pool, and routes sandbox operations
// to per-instance NodeSandboxControl clients.
//
// Lock ordering discipline (MUST be followed throughout):
//  1. FleetSandboxControl.mu  (fleet-level lock)
//  2. ManagedInstance.mu       (per-instance lock)
//
// The fleet lock is ALWAYS acquired before any instance lock. No code path
// may acquire FleetSandboxControl.mu while holding any ManagedInstance.mu.
// This prevents deadlocks between the control loop and concurrent callers.
type FleetSandboxControl struct {
	provisioner   instance.InstanceProvisioner
	config        FleetConfig
	router        *CapacityRouter
	clientFactory SandboxClientFactory
	logger        *slog.Logger

	// instances maps instance IDs to their managed state. Guarded by mu.
	instances map[string]*ManagedInstance
	// mu guards the instances map. See lock ordering discipline above.
	mu sync.RWMutex

	// closed indicates whether Close has been called.
	closed atomic.Bool

	// cancelLoop cancels the fleet control loop goroutine.
	cancelLoop context.CancelFunc
	// loopDone is closed when the control loop exits.
	loopDone chan struct{}

	// pendingLaunches tracks the current number of in-flight provisions.
	pendingLaunches atomic.Int32
}

// NewFleetSandboxControl creates a new fleet manager. The control loop
// (health checks, scaling) is started in bead aiag-1xd.4 via StartControlLoop.
func NewFleetSandboxControl(
	provisioner instance.InstanceProvisioner,
	clientFactory SandboxClientFactory,
	cfg FleetConfig,
	opts ...FleetOption,
) (*FleetSandboxControl, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	o := &fleetOptions{logger: slog.Default()}
	for _, opt := range opts {
		opt(o)
	}
	if o.leaveInstancesOnClose {
		cfg.LeaveInstancesOnClose = true
	}

	f := &FleetSandboxControl{
		provisioner:   provisioner,
		config:        cfg,
		router:        NewCapacityRouter(cfg),
		clientFactory: clientFactory,
		logger:        o.logger,
		instances:     make(map[string]*ManagedInstance),
		loopDone:      make(chan struct{}),
		cancelLoop:    func() {},
	}
	// No control loop yet; Close() shouldn't block on loopDone.
	close(f.loopDone)

	return f, nil
}

// ---------- SandboxControl: CreateSandbox ----------

// CreateSandbox picks the best instance via capacity-aware routing, atomically
// claims a session slot, creates a sandbox on it, and returns a fleet-prefixed
// SandboxID. If no capacity exists, attempts to provision a new instance.
func (f *FleetSandboxControl) CreateSandbox(ctx context.Context, req control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
	if f.closed.Load() {
		return nil, ErrFleetClosed
	}

	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		mi, err := f.selectAndClaim()
		if err == ErrNoCapacity {
			if provErr := f.provisionInstance(ctx); provErr != nil {
				return nil, ErrNoCapacity
			}
			continue
		}
		if err != nil {
			// Claim race (instance drained or filled between select and claim).
			continue
		}

		resp, rpcErr := mi.Client.CreateSandbox(ctx, req)
		if rpcErr != nil {
			// Roll back the claimed slot.
			mi.mu.Lock()
			_ = mi.ReleaseSession()
			mi.mu.Unlock()
			return nil, rpcErr
		}

		fleetID, encErr := encodeSandboxID(mi.InstanceID, resp.SandboxID)
		if encErr != nil {
			mi.mu.Lock()
			_ = mi.ReleaseSession()
			mi.mu.Unlock()
			return nil, fmt.Errorf("fleet: encode sandbox ID: %w", encErr)
		}

		return &control.CreateSandboxResponse{
			SandboxID:    fleetID,
			Address:      fmt.Sprintf("%s:%d", mi.IP, f.config.SandboxHostPort),
			Capabilities: f.Capabilities(),
		}, nil
	}

	return nil, ErrNoCapacity
}

// selectAndClaim finds the best instance and atomically claims a session slot.
// It takes consistent snapshots of instance fields under their read locks for
// routing, then claims on the real instance under its write lock.
func (f *FleetSandboxControl) selectAndClaim() (*ManagedInstance, error) {
	f.mu.RLock()
	var snapshots []*ManagedInstance
	real := make(map[string]*ManagedInstance, len(f.instances))
	for _, mi := range f.instances {
		mi.mu.RLock()
		snap := &ManagedInstance{
			InstanceID:   mi.InstanceID,
			State:        mi.State,
			SessionCount: mi.SessionCount,
			MaxSessions:  mi.MaxSessions,
			IdleSince:    mi.IdleSince,
		}
		mi.mu.RUnlock()
		snapshots = append(snapshots, snap)
		real[mi.InstanceID] = mi
	}
	f.mu.RUnlock()

	selected, err := f.router.SelectInstance(snapshots)
	if err != nil {
		return nil, err
	}

	mi := real[selected.InstanceID]
	mi.mu.Lock()
	err = mi.ClaimSession()
	mi.mu.Unlock()
	if err != nil {
		return nil, err
	}

	return mi, nil
}

// provisionInstance launches a new instance and adds it to the fleet.
func (f *FleetSandboxControl) provisionInstance(ctx context.Context) error {
	f.mu.RLock()
	total := len(f.instances)
	f.mu.RUnlock()
	if total >= f.config.MaxInstances {
		return ErrNoCapacity
	}

	// Limit concurrent provisions.
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

	info, err := f.provisioner.LaunchInstance(ctx, f.config.InstanceConfig)
	if err != nil {
		return fmt.Errorf("fleet: provision failed: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", info.PrivateIP, f.config.SandboxHostPort)
	client, err := f.clientFactory(addr)
	if err != nil {
		return fmt.Errorf("fleet: client creation failed for %s: %w", info.InstanceID, err)
	}

	mi := &ManagedInstance{
		InstanceID:  info.InstanceID,
		State:       InstanceReady,
		Client:      client,
		MaxSessions: f.config.MaxSessionsPerInstance,
		IP:          info.PrivateIP,
		IdleSince:   time.Now(),
	}

	f.mu.Lock()
	f.instances[info.InstanceID] = mi
	f.mu.Unlock()

	f.logger.Info("provisioned new instance",
		"instance_id", info.InstanceID,
		"ip", info.PrivateIP)

	return nil
}

// ---------- SandboxControl: DestroySandbox ----------

// DestroySandbox parses the fleet SandboxID, decrements the session count
// eagerly, then delegates to the per-instance client. On RPC failure the
// count is rolled back; the reconciliation loop corrects any remaining drift.
func (f *FleetSandboxControl) DestroySandbox(ctx context.Context, sandboxID string) error {
	instanceID, sessionID, err := parseSandboxID(sandboxID)
	if err != nil {
		return err
	}

	mi, err := f.getInstance(instanceID)
	if err != nil {
		return err
	}

	// Decrement before RPC (inverse of claim-slot).
	mi.mu.Lock()
	releaseErr := mi.ReleaseSession()
	mi.mu.Unlock()

	rpcErr := mi.Client.DestroySandbox(ctx, sessionID)
	if rpcErr != nil {
		// Rollback: re-increment if we successfully released.
		if releaseErr == nil {
			mi.mu.Lock()
			if claimErr := mi.ClaimSession(); claimErr != nil {
				f.logger.Warn("failed to rollback session release after destroy failure",
					"instance_id", instanceID, "error", claimErr)
			}
			mi.mu.Unlock()
		}
		return rpcErr
	}

	return nil
}

// ---------- SandboxControl: Delegation methods ----------

// LaunchProcess delegates to the per-instance client after rewriting SandboxID
// to the session-local ID.
func (f *FleetSandboxControl) LaunchProcess(ctx context.Context, req control.LaunchProcessRequest) (*control.LaunchProcessResponse, error) {
	instanceID, sessionID, err := parseSandboxID(req.SandboxID)
	if err != nil {
		return nil, err
	}
	mi, err := f.getInstance(instanceID)
	if err != nil {
		return nil, err
	}
	delegateReq := control.LaunchProcessRequest{
		SandboxID:  sessionID,
		Binary:     req.Binary,
		Args:       req.Args,
		Env:        req.Env,
		ExposePort: req.ExposePort,
	}
	return mi.Client.LaunchProcess(ctx, delegateReq)
}

// KillProcess delegates to the per-instance client after rewriting SandboxID.
func (f *FleetSandboxControl) KillProcess(ctx context.Context, req control.KillProcessRequest) error {
	instanceID, sessionID, err := parseSandboxID(req.SandboxID)
	if err != nil {
		return err
	}
	mi, err := f.getInstance(instanceID)
	if err != nil {
		return err
	}
	delegateReq := control.KillProcessRequest{
		SandboxID: sessionID,
		ProcessID: req.ProcessID,
		Signal:    req.Signal,
	}
	return mi.Client.KillProcess(ctx, delegateReq)
}

// GetProcessStatus delegates to the per-instance client after rewriting SandboxID.
func (f *FleetSandboxControl) GetProcessStatus(ctx context.Context, req control.GetProcessStatusRequest) (*control.GetProcessStatusResponse, error) {
	instanceID, sessionID, err := parseSandboxID(req.SandboxID)
	if err != nil {
		return nil, err
	}
	mi, err := f.getInstance(instanceID)
	if err != nil {
		return nil, err
	}
	delegateReq := control.GetProcessStatusRequest{
		SandboxID: sessionID,
		ProcessID: req.ProcessID,
	}
	return mi.Client.GetProcessStatus(ctx, delegateReq)
}

// PauseSandbox delegates to the per-instance client after parsing the SandboxID.
func (f *FleetSandboxControl) PauseSandbox(ctx context.Context, sandboxID string) error {
	instanceID, sessionID, err := parseSandboxID(sandboxID)
	if err != nil {
		return err
	}
	mi, err := f.getInstance(instanceID)
	if err != nil {
		return err
	}
	return mi.Client.PauseSandbox(ctx, sessionID)
}

// ResumeSandbox delegates to the per-instance client after parsing the SandboxID.
func (f *FleetSandboxControl) ResumeSandbox(ctx context.Context, sandboxID string) error {
	instanceID, sessionID, err := parseSandboxID(sandboxID)
	if err != nil {
		return err
	}
	mi, err := f.getInstance(instanceID)
	if err != nil {
		return err
	}
	return mi.Client.ResumeSandbox(ctx, sessionID)
}

// ---------- SandboxControl: Capabilities ----------

// Capabilities returns the fleet's capability set. ConcurrentSandboxes is the
// theoretical ceiling (MaxInstances * MaxSessionsPerInstance); actual available
// capacity depends on fleet state.
func (f *FleetSandboxControl) Capabilities() control.SandboxCapabilities {
	return control.SandboxCapabilities{
		Snapshots:           true,
		Rollback:            true,
		Pause:               true,
		LaunchProcess:       true,
		DeepPause:           false,
		ConcurrentSandboxes: f.config.MaxInstances * f.config.MaxSessionsPerInstance,
	}
}

// ---------- Close (io.Closer) ----------

// Close initiates graceful shutdown: cancels the control loop, drains and
// terminates instances (unless LeaveInstancesOnClose is set).
func (f *FleetSandboxControl) Close() error {
	if !f.closed.CompareAndSwap(false, true) {
		return nil
	}

	f.cancelLoop()
	<-f.loopDone

	if f.config.LeaveInstancesOnClose {
		return nil
	}

	// Collect instances under lock, then terminate without lock.
	f.mu.RLock()
	instances := make([]*ManagedInstance, 0, len(f.instances))
	for _, mi := range f.instances {
		instances = append(instances, mi)
	}
	f.mu.RUnlock()

	ctx := context.Background()
	for _, mi := range instances {
		mi.mu.Lock()
		if mi.State != InstanceTerminating {
			mi.State = InstanceTerminating
		}
		mi.mu.Unlock()

		if err := f.provisioner.TerminateInstance(ctx, mi.InstanceID); err != nil {
			f.logger.Warn("failed to terminate instance during close",
				"instance_id", mi.InstanceID, "error", err)
		}
	}

	f.mu.Lock()
	f.instances = make(map[string]*ManagedInstance)
	f.mu.Unlock()

	return nil
}

// ---------- FleetStatus ----------

// FleetStatus contains a point-in-time snapshot of fleet state.
type FleetStatus struct {
	TotalInstances    int
	InstancesByState  map[InstanceState]int
	TotalSessions     int64
	PendingProvisions int32
	Closed            bool
}

// FleetStatus returns a snapshot of the fleet's current state.
func (f *FleetSandboxControl) FleetStatus() FleetStatus {
	f.mu.RLock()
	defer f.mu.RUnlock()

	status := FleetStatus{
		TotalInstances:    len(f.instances),
		InstancesByState:  make(map[InstanceState]int),
		PendingProvisions: f.pendingLaunches.Load(),
		Closed:            f.closed.Load(),
	}
	for _, mi := range f.instances {
		status.InstancesByState[mi.State]++
		status.TotalSessions += mi.SessionCount
	}
	return status
}

// ---------- Helpers ----------

// getInstance looks up a managed instance by ID. Returns ErrInstanceNotFound
// if the instance is not in the fleet.
func (f *FleetSandboxControl) getInstance(instanceID string) (*ManagedInstance, error) {
	f.mu.RLock()
	mi, ok := f.instances[instanceID]
	f.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrInstanceNotFound, instanceID)
	}
	return mi, nil
}
