package fleet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
	"github.com/prometheus/client_golang/prometheus"
)

// ---------- Mock test doubles ----------

// mockNodeClient implements FleetNodeClient for testing.
type mockNodeClient struct {
	createSandboxFn    func(ctx context.Context, req control.CreateSandboxRequest) (*control.CreateSandboxResponse, error)
	destroySandboxFn   func(ctx context.Context, sandboxID string) error
	launchProcessFn    func(ctx context.Context, req control.LaunchProcessRequest) (*control.LaunchProcessResponse, error)
	killProcessFn      func(ctx context.Context, req control.KillProcessRequest) error
	getProcessStatusFn func(ctx context.Context, req control.GetProcessStatusRequest) (*control.GetProcessStatusResponse, error)
	pauseSandboxFn     func(ctx context.Context, sandboxID string) error
	resumeSandboxFn    func(ctx context.Context, sandboxID string) error
	capabilitiesFn     func() control.SandboxCapabilities
	healthCheckFn      func(ctx context.Context) (*HealthCheckResult, error)

	mu              sync.Mutex
	createCalls     []control.CreateSandboxRequest
	destroyCalls    []string
	launchCalls     []control.LaunchProcessRequest
	killCalls       []control.KillProcessRequest
	statusCalls     []control.GetProcessStatusRequest
	pauseCalls      []string
	resumeCalls     []string
	createCallCount atomic.Int32
}

func (m *mockNodeClient) CreateSandbox(ctx context.Context, req control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
	m.createCallCount.Add(1)
	m.mu.Lock()
	m.createCalls = append(m.createCalls, req)
	m.mu.Unlock()
	if m.createSandboxFn != nil {
		return m.createSandboxFn(ctx, req)
	}
	return &control.CreateSandboxResponse{
		SandboxID: "sess-default",
		Address:   "/pool/sessions/sess-default",
	}, nil
}

func (m *mockNodeClient) DestroySandbox(ctx context.Context, sandboxID string) error {
	m.mu.Lock()
	m.destroyCalls = append(m.destroyCalls, sandboxID)
	m.mu.Unlock()
	if m.destroySandboxFn != nil {
		return m.destroySandboxFn(ctx, sandboxID)
	}
	return nil
}

func (m *mockNodeClient) LaunchProcess(ctx context.Context, req control.LaunchProcessRequest) (*control.LaunchProcessResponse, error) {
	m.mu.Lock()
	m.launchCalls = append(m.launchCalls, req)
	m.mu.Unlock()
	if m.launchProcessFn != nil {
		return m.launchProcessFn(ctx, req)
	}
	return &control.LaunchProcessResponse{
		ProcessID: "proc-1",
		Address:   "10.0.1.5:8080",
		Status:    control.ProcessRunning,
	}, nil
}

func (m *mockNodeClient) KillProcess(ctx context.Context, req control.KillProcessRequest) error {
	m.mu.Lock()
	m.killCalls = append(m.killCalls, req)
	m.mu.Unlock()
	if m.killProcessFn != nil {
		return m.killProcessFn(ctx, req)
	}
	return nil
}

func (m *mockNodeClient) GetProcessStatus(ctx context.Context, req control.GetProcessStatusRequest) (*control.GetProcessStatusResponse, error) {
	m.mu.Lock()
	m.statusCalls = append(m.statusCalls, req)
	m.mu.Unlock()
	if m.getProcessStatusFn != nil {
		return m.getProcessStatusFn(ctx, req)
	}
	return &control.GetProcessStatusResponse{
		Status: control.ProcessRunning,
	}, nil
}

func (m *mockNodeClient) PauseSandbox(ctx context.Context, sandboxID string) error {
	m.mu.Lock()
	m.pauseCalls = append(m.pauseCalls, sandboxID)
	m.mu.Unlock()
	if m.pauseSandboxFn != nil {
		return m.pauseSandboxFn(ctx, sandboxID)
	}
	return nil
}

func (m *mockNodeClient) ResumeSandbox(ctx context.Context, sandboxID string) error {
	m.mu.Lock()
	m.resumeCalls = append(m.resumeCalls, sandboxID)
	m.mu.Unlock()
	if m.resumeSandboxFn != nil {
		return m.resumeSandboxFn(ctx, sandboxID)
	}
	return nil
}

func (m *mockNodeClient) Capabilities() control.SandboxCapabilities {
	if m.capabilitiesFn != nil {
		return m.capabilitiesFn()
	}
	return control.SandboxCapabilities{}
}

func (m *mockNodeClient) HealthCheck(ctx context.Context) (*HealthCheckResult, error) {
	if m.healthCheckFn != nil {
		return m.healthCheckFn(ctx)
	}
	return &HealthCheckResult{Status: "healthy"}, nil
}

// mockProvisioner implements instance.InstanceProvisioner for testing.
type mockProvisioner struct {
	launchFn    func(ctx context.Context, cfg instance.InstanceConfig) (*instance.InstanceInfo, error)
	terminateFn func(ctx context.Context, id string) error
	listFn      func(ctx context.Context, filter instance.InstanceFilter) ([]instance.InstanceInfo, error)

	mu             sync.Mutex
	launchCalls    []instance.InstanceConfig
	terminateCalls []string
	nextInstanceID int
}

func (m *mockProvisioner) LaunchInstance(ctx context.Context, cfg instance.InstanceConfig) (*instance.InstanceInfo, error) {
	m.mu.Lock()
	m.launchCalls = append(m.launchCalls, cfg)
	id := m.nextInstanceID
	m.nextInstanceID++
	m.mu.Unlock()
	if m.launchFn != nil {
		return m.launchFn(ctx, cfg)
	}
	return &instance.InstanceInfo{
		InstanceID: fmt.Sprintf("i-new-%d", id),
		PrivateIP:  fmt.Sprintf("10.0.0.%d", 100+id),
		State:      instance.CloudInstanceRunning,
		LaunchTime: time.Now(),
	}, nil
}

func (m *mockProvisioner) TerminateInstance(ctx context.Context, id string) error {
	m.mu.Lock()
	m.terminateCalls = append(m.terminateCalls, id)
	m.mu.Unlock()
	if m.terminateFn != nil {
		return m.terminateFn(ctx, id)
	}
	return nil
}

func (m *mockProvisioner) StopInstance(ctx context.Context, id string) error { return nil }
func (m *mockProvisioner) StartInstance(ctx context.Context, id string) (*instance.InstanceInfo, error) {
	return &instance.InstanceInfo{InstanceID: id}, nil
}
func (m *mockProvisioner) DescribeInstance(ctx context.Context, id string) (*instance.InstanceStatus, error) {
	return &instance.InstanceStatus{InstanceID: id, State: instance.CloudInstanceRunning}, nil
}
func (m *mockProvisioner) ListInstances(ctx context.Context, filter instance.InstanceFilter) ([]instance.InstanceInfo, error) {
	if m.listFn != nil {
		return m.listFn(ctx, filter)
	}
	return nil, nil
}

// ---------- Test helpers ----------

func newTestFleet(t *testing.T) (*FleetSandboxControl, *mockProvisioner) {
	t.Helper()
	mp := &mockProvisioner{}
	cfg := DefaultFleetConfig()
	cfg.MaxSessionsPerInstance = 10

	f, err := NewFleetSandboxControl(mp, func(addr string) (FleetNodeClient, error) {
		return &mockNodeClient{}, nil
	}, cfg, WithMetricsRegisterer(prometheus.NewRegistry()))
	if err != nil {
		t.Fatal(err)
	}
	return f, mp
}

func addInstance(f *FleetSandboxControl, id, ip string, state InstanceState, sessions int64, client FleetNodeClient) *ManagedInstance {
	mi := &ManagedInstance{
		InstanceID:   id,
		State:        state,
		SessionCount: sessions,
		MaxSessions:  f.config.MaxSessionsPerInstance,
		IP:           ip,
		Client:       client,
		IdleSince:    time.Now(),
	}
	if sessions > 0 {
		mi.IdleSince = time.Time{}
	}
	f.mu.Lock()
	f.instances[id] = mi
	f.mu.Unlock()
	return mi
}

// ---------- CreateSandbox tests ----------

func TestCreateSandbox_RoutesToLeastLoaded(t *testing.T) {
	f, _ := newTestFleet(t)

	mc1 := &mockNodeClient{}
	mc1.createSandboxFn = func(_ context.Context, _ control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
		return &control.CreateSandboxResponse{SandboxID: "sess-from-1", Address: "/pool/sess-from-1"}, nil
	}
	mc2 := &mockNodeClient{}
	mc2.createSandboxFn = func(_ context.Context, _ control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
		return &control.CreateSandboxResponse{SandboxID: "sess-from-2", Address: "/pool/sess-from-2"}, nil
	}

	addInstance(f, "i-loaded", "10.0.1.1", InstanceActive, 8, mc1)
	addInstance(f, "i-light", "10.0.1.2", InstanceActive, 2, mc2)

	resp, err := f.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should route to i-light (more headroom).
	if !strings.Contains(resp.SandboxID, "i-light") {
		t.Errorf("expected routing to i-light, got SandboxID=%s", resp.SandboxID)
	}
}

func TestCreateSandbox_ReturnsFleetPrefixedSandboxID(t *testing.T) {
	f, _ := newTestFleet(t)

	mc := &mockNodeClient{}
	mc.createSandboxFn = func(_ context.Context, _ control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
		return &control.CreateSandboxResponse{SandboxID: "sess-abc", Address: "/pool/sess-abc"}, nil
	}
	addInstance(f, "i-test", "10.0.1.5", InstanceReady, 0, mc)

	resp, err := f.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.SandboxID != "fleet:i-test:sess-abc" {
		t.Errorf("SandboxID = %q, want %q", resp.SandboxID, "fleet:i-test:sess-abc")
	}
}

func TestCreateSandbox_OverridesAddressWithRoutableHostPort(t *testing.T) {
	f, _ := newTestFleet(t)

	mc := &mockNodeClient{}
	mc.createSandboxFn = func(_ context.Context, _ control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
		return &control.CreateSandboxResponse{SandboxID: "sess-1", Address: "/pool/sessions/sess-1"}, nil
	}
	addInstance(f, "i-test", "10.0.1.5", InstanceReady, 0, mc)

	resp, err := f.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "10.0.1.5:9100"
	if resp.Address != want {
		t.Errorf("Address = %q, want %q (should override ZFS mountpoint with host:port)", resp.Address, want)
	}
}

func TestCreateSandbox_NoCapacityTriggersProvision(t *testing.T) {
	f, _ := newTestFleet(t)

	mc := &mockNodeClient{}
	mc.createSandboxFn = func(_ context.Context, _ control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
		return &control.CreateSandboxResponse{SandboxID: "sess-1", Address: "/pool/sess-1"}, nil
	}
	// All existing instances are full.
	addInstance(f, "i-full", "10.0.1.1", InstanceActive, 10, mc)

	// Client factory returns a mock for newly provisioned instances.
	f.clientFactory = func(addr string) (FleetNodeClient, error) {
		newMC := &mockNodeClient{}
		newMC.createSandboxFn = func(_ context.Context, _ control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
			return &control.CreateSandboxResponse{SandboxID: "sess-new", Address: "/pool/sess-new"}, nil
		}
		return newMC, nil
	}

	resp, err := f.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(resp.SandboxID, "i-new-") {
		t.Errorf("expected routing to provisioned instance, got SandboxID=%s", resp.SandboxID)
	}
}

func TestCreateSandbox_AtMaxInstancesReturnsError(t *testing.T) {
	f, _ := newTestFleet(t)
	f.config.MaxInstances = 1

	mc := &mockNodeClient{}
	addInstance(f, "i-full", "10.0.1.1", InstanceActive, 10, mc)

	_, err := f.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
	if !errors.Is(err, ErrNoCapacity) {
		t.Errorf("expected ErrNoCapacity, got: %v", err)
	}
}

func TestCreateSandbox_ClaimSlotRollsBackOnRPCFailure(t *testing.T) {
	f, _ := newTestFleet(t)

	rpcErr := errors.New("rpc: connection refused")
	mc := &mockNodeClient{}
	mc.createSandboxFn = func(_ context.Context, _ control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
		return nil, rpcErr
	}
	mi := addInstance(f, "i-test", "10.0.1.5", InstanceReady, 0, mc)

	_, err := f.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
	if err == nil {
		t.Fatal("expected error from RPC failure")
	}

	// Session count should be rolled back to 0.
	mi.mu.RLock()
	count := mi.SessionCount
	state := mi.State
	mi.mu.RUnlock()

	if count != 0 {
		t.Errorf("SessionCount = %d after rollback, want 0", count)
	}
	if state != InstanceReady {
		t.Errorf("State = %s after rollback, want Ready", state)
	}
}

func TestCreateSandbox_ConcurrentCallsSerialized(t *testing.T) {
	f, _ := newTestFleet(t)

	var callCount atomic.Int32
	mc := &mockNodeClient{}
	mc.createSandboxFn = func(_ context.Context, _ control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
		n := callCount.Add(1)
		return &control.CreateSandboxResponse{
			SandboxID: fmt.Sprintf("sess-%d", n),
			Address:   "/pool/sess",
		}, nil
	}
	addInstance(f, "i-test", "10.0.1.5", InstanceReady, 0, mc)

	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = f.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: unexpected error: %v", i, err)
		}
	}

	// All 5 should have succeeded, session count should be 5.
	f.mu.RLock()
	mi := f.instances["i-test"]
	f.mu.RUnlock()
	mi.mu.RLock()
	count := mi.SessionCount
	mi.mu.RUnlock()
	if count != 5 {
		t.Errorf("SessionCount = %d, want 5", count)
	}
}

func TestCreateSandbox_ClosedReturnsError(t *testing.T) {
	f, _ := newTestFleet(t)
	f.closed.Store(true)

	_, err := f.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
	if !errors.Is(err, ErrFleetClosed) {
		t.Errorf("expected ErrFleetClosed, got: %v", err)
	}
}

// ---------- DestroySandbox tests ----------

func TestDestroySandbox_DelegatesAndDecrements(t *testing.T) {
	f, _ := newTestFleet(t)

	mc := &mockNodeClient{}
	mi := addInstance(f, "i-test", "10.0.1.5", InstanceActive, 3, mc)

	err := f.DestroySandbox(context.Background(), "fleet:i-test:sess-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have delegated with session-local ID.
	mc.mu.Lock()
	if len(mc.destroyCalls) != 1 || mc.destroyCalls[0] != "sess-abc" {
		t.Errorf("destroy delegated with %v, want [sess-abc]", mc.destroyCalls)
	}
	mc.mu.Unlock()

	// Session count should be decremented.
	mi.mu.RLock()
	if mi.SessionCount != 2 {
		t.Errorf("SessionCount = %d, want 2", mi.SessionCount)
	}
	mi.mu.RUnlock()
}

func TestDestroySandbox_RPCFailureRollsBackCount(t *testing.T) {
	f, _ := newTestFleet(t)

	mc := &mockNodeClient{}
	mc.destroySandboxFn = func(_ context.Context, _ string) error {
		return errors.New("rpc: timeout")
	}
	mi := addInstance(f, "i-test", "10.0.1.5", InstanceActive, 3, mc)

	err := f.DestroySandbox(context.Background(), "fleet:i-test:sess-abc")
	if err == nil {
		t.Fatal("expected error from RPC failure")
	}

	// Session count should be restored.
	mi.mu.RLock()
	if mi.SessionCount != 3 {
		t.Errorf("SessionCount = %d after rollback, want 3", mi.SessionCount)
	}
	mi.mu.RUnlock()
}

func TestDestroySandbox_InvalidSandboxID(t *testing.T) {
	f, _ := newTestFleet(t)

	err := f.DestroySandbox(context.Background(), "invalid-id")
	if err == nil {
		t.Fatal("expected error for invalid SandboxID")
	}
}

// ---------- Delegation method tests ----------

func TestLaunchProcess_RewritesSandboxID(t *testing.T) {
	f, _ := newTestFleet(t)

	mc := &mockNodeClient{}
	addInstance(f, "i-test", "10.0.1.5", InstanceActive, 1, mc)

	req := control.LaunchProcessRequest{
		SandboxID:  "fleet:i-test:sess-abc",
		Binary:     "/usr/bin/python",
		Args:       []string{"app.py"},
		Env:        map[string]string{"PORT": "8080"},
		ExposePort: 8080,
	}

	_, err := f.LaunchProcess(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mc.mu.Lock()
	if len(mc.launchCalls) != 1 {
		t.Fatalf("expected 1 launch call, got %d", len(mc.launchCalls))
	}
	delegated := mc.launchCalls[0]
	mc.mu.Unlock()

	if delegated.SandboxID != "sess-abc" {
		t.Errorf("delegated SandboxID = %q, want %q", delegated.SandboxID, "sess-abc")
	}
	if delegated.Binary != "/usr/bin/python" {
		t.Errorf("delegated Binary = %q, want /usr/bin/python", delegated.Binary)
	}
	if delegated.ExposePort != 8080 {
		t.Errorf("delegated ExposePort = %d, want 8080", delegated.ExposePort)
	}
}

func TestKillProcess_RewritesSandboxID(t *testing.T) {
	f, _ := newTestFleet(t)

	mc := &mockNodeClient{}
	addInstance(f, "i-test", "10.0.1.5", InstanceActive, 1, mc)

	err := f.KillProcess(context.Background(), control.KillProcessRequest{
		SandboxID: "fleet:i-test:sess-abc",
		ProcessID: "proc-1",
		Signal:    15,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mc.mu.Lock()
	if len(mc.killCalls) != 1 {
		t.Fatalf("expected 1 kill call, got %d", len(mc.killCalls))
	}
	if mc.killCalls[0].SandboxID != "sess-abc" {
		t.Errorf("delegated SandboxID = %q, want %q", mc.killCalls[0].SandboxID, "sess-abc")
	}
	if mc.killCalls[0].ProcessID != "proc-1" {
		t.Errorf("delegated ProcessID = %q, want %q", mc.killCalls[0].ProcessID, "proc-1")
	}
	mc.mu.Unlock()
}

func TestGetProcessStatus_RewritesSandboxID(t *testing.T) {
	f, _ := newTestFleet(t)

	mc := &mockNodeClient{}
	addInstance(f, "i-test", "10.0.1.5", InstanceActive, 1, mc)

	resp, err := f.GetProcessStatus(context.Background(), control.GetProcessStatusRequest{
		SandboxID: "fleet:i-test:sess-abc",
		ProcessID: "proc-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != control.ProcessRunning {
		t.Errorf("Status = %q, want %q", resp.Status, control.ProcessRunning)
	}

	mc.mu.Lock()
	if len(mc.statusCalls) != 1 || mc.statusCalls[0].SandboxID != "sess-abc" {
		t.Errorf("delegated SandboxID = %q, want %q", mc.statusCalls[0].SandboxID, "sess-abc")
	}
	mc.mu.Unlock()
}

func TestPauseSandbox_DelegatesCorrectly(t *testing.T) {
	f, _ := newTestFleet(t)

	mc := &mockNodeClient{}
	addInstance(f, "i-test", "10.0.1.5", InstanceActive, 1, mc)

	err := f.PauseSandbox(context.Background(), "fleet:i-test:sess-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mc.mu.Lock()
	if len(mc.pauseCalls) != 1 || mc.pauseCalls[0] != "sess-abc" {
		t.Errorf("pause delegated with %v, want [sess-abc]", mc.pauseCalls)
	}
	mc.mu.Unlock()
}

func TestResumeSandbox_DelegatesCorrectly(t *testing.T) {
	f, _ := newTestFleet(t)

	mc := &mockNodeClient{}
	addInstance(f, "i-test", "10.0.1.5", InstanceActive, 1, mc)

	err := f.ResumeSandbox(context.Background(), "fleet:i-test:sess-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mc.mu.Lock()
	if len(mc.resumeCalls) != 1 || mc.resumeCalls[0] != "sess-abc" {
		t.Errorf("resume delegated with %v, want [sess-abc]", mc.resumeCalls)
	}
	mc.mu.Unlock()
}

func TestDelegation_InstanceNotFound(t *testing.T) {
	f, _ := newTestFleet(t)

	_, err := f.LaunchProcess(context.Background(), control.LaunchProcessRequest{
		SandboxID: "fleet:i-nonexistent:sess-abc",
	})
	if !errors.Is(err, ErrInstanceNotFound) {
		t.Errorf("expected ErrInstanceNotFound, got: %v", err)
	}
}

// ---------- Capabilities tests ----------

func TestCapabilities_ConcurrentSandboxes(t *testing.T) {
	f, _ := newTestFleet(t)

	caps := f.Capabilities()
	want := f.config.MaxInstances * f.config.MaxSessionsPerInstance
	if caps.ConcurrentSandboxes != want {
		t.Errorf("ConcurrentSandboxes = %d, want %d", caps.ConcurrentSandboxes, want)
	}
	if !caps.Snapshots {
		t.Error("Snapshots should be true")
	}
	if !caps.Pause {
		t.Error("Pause should be true")
	}
	if !caps.LaunchProcess {
		t.Error("LaunchProcess should be true")
	}
}

// ---------- Close tests ----------

func TestClose_TerminatesInstances(t *testing.T) {
	f, mp := newTestFleet(t)

	mc := &mockNodeClient{}
	addInstance(f, "i-1", "10.0.1.1", InstanceReady, 0, mc)
	addInstance(f, "i-2", "10.0.1.2", InstanceActive, 3, mc)

	err := f.Close()
	if err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	mp.mu.Lock()
	terminatedIDs := make(map[string]bool)
	for _, id := range mp.terminateCalls {
		terminatedIDs[id] = true
	}
	mp.mu.Unlock()

	if !terminatedIDs["i-1"] || !terminatedIDs["i-2"] {
		t.Errorf("expected both instances terminated, got: %v", mp.terminateCalls)
	}

	// Instances map should be cleared.
	status := f.FleetStatus()
	if status.TotalInstances != 0 {
		t.Errorf("TotalInstances = %d after Close, want 0", status.TotalInstances)
	}
}

func TestClose_LeaveInstancesOnClose(t *testing.T) {
	mp := &mockProvisioner{}
	cfg := DefaultFleetConfig()
	cfg.LeaveInstancesOnClose = true

	f, err := NewFleetSandboxControl(mp, func(addr string) (FleetNodeClient, error) {
		return &mockNodeClient{}, nil
	}, cfg, WithMetricsRegisterer(prometheus.NewRegistry()))
	if err != nil {
		t.Fatal(err)
	}

	mc := &mockNodeClient{}
	addInstance(f, "i-1", "10.0.1.1", InstanceReady, 0, mc)

	err = f.Close()
	if err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// No instances should be terminated.
	mp.mu.Lock()
	if len(mp.terminateCalls) != 0 {
		t.Errorf("expected 0 terminate calls with LeaveInstancesOnClose, got %d", len(mp.terminateCalls))
	}
	mp.mu.Unlock()
}

func TestClose_RejectsNewCreateSandbox(t *testing.T) {
	f, _ := newTestFleet(t)
	mc := &mockNodeClient{}
	addInstance(f, "i-test", "10.0.1.5", InstanceReady, 0, mc)

	if err := f.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	_, err := f.CreateSandbox(context.Background(), control.CreateSandboxRequest{})
	if !errors.Is(err, ErrFleetClosed) {
		t.Errorf("expected ErrFleetClosed after Close, got: %v", err)
	}
}

func TestClose_Idempotent(t *testing.T) {
	f, _ := newTestFleet(t)

	if err := f.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("second Close() error: %v", err)
	}
}

// ---------- FleetStatus tests ----------

func TestFleetStatus_ReflectsState(t *testing.T) {
	f, _ := newTestFleet(t)
	mc := &mockNodeClient{}

	addInstance(f, "i-ready", "10.0.1.1", InstanceReady, 0, mc)
	addInstance(f, "i-active1", "10.0.1.2", InstanceActive, 3, mc)
	addInstance(f, "i-active2", "10.0.1.3", InstanceActive, 5, mc)
	addInstance(f, "i-drain", "10.0.1.4", InstanceDraining, 1, mc)

	status := f.FleetStatus()
	if status.TotalInstances != 4 {
		t.Errorf("TotalInstances = %d, want 4", status.TotalInstances)
	}
	if status.TotalSessions != 9 {
		t.Errorf("TotalSessions = %d, want 9", status.TotalSessions)
	}
	if status.InstancesByState[InstanceReady] != 1 {
		t.Errorf("Ready instances = %d, want 1", status.InstancesByState[InstanceReady])
	}
	if status.InstancesByState[InstanceActive] != 2 {
		t.Errorf("Active instances = %d, want 2", status.InstancesByState[InstanceActive])
	}
	if status.InstancesByState[InstanceDraining] != 1 {
		t.Errorf("Draining instances = %d, want 1", status.InstancesByState[InstanceDraining])
	}
	if status.WarmPoolSize != 1 {
		t.Errorf("WarmPoolSize = %d, want 1 (one Ready instance with 0 sessions)", status.WarmPoolSize)
	}
	if !status.Healthy {
		t.Error("Healthy should be true (warm=1 >= MinInstances=1, healthy instances exist)")
	}
	if status.Closed {
		t.Error("Closed should be false before Close()")
	}
}

func TestFleetStatus_UnhealthyWhenNoWarmPool(t *testing.T) {
	f, _ := newTestFleet(t)
	mc := &mockNodeClient{}

	// All instances are active with sessions — no warm pool.
	addInstance(f, "i-active1", "10.0.1.1", InstanceActive, 3, mc)
	addInstance(f, "i-active2", "10.0.1.2", InstanceActive, 5, mc)

	status := f.FleetStatus()
	if status.WarmPoolSize != 0 {
		t.Errorf("WarmPoolSize = %d, want 0", status.WarmPoolSize)
	}
	if status.Healthy {
		t.Error("Healthy should be false (warm=0 < MinInstances=1)")
	}
}

func TestFleetStatus_WarmPoolCountsOnlyIdleReady(t *testing.T) {
	f, _ := newTestFleet(t)
	mc := &mockNodeClient{}

	// Ready with sessions is NOT warm.
	addInstance(f, "i-ready-busy", "10.0.1.1", InstanceReady, 2, mc)
	// Ready with 0 sessions IS warm.
	addInstance(f, "i-ready-idle", "10.0.1.2", InstanceReady, 0, mc)
	// Draining with 0 sessions is NOT warm.
	addInstance(f, "i-drain-idle", "10.0.1.3", InstanceDraining, 0, mc)

	status := f.FleetStatus()
	if status.WarmPoolSize != 1 {
		t.Errorf("WarmPoolSize = %d, want 1", status.WarmPoolSize)
	}
}

// ---------- Constructor tests ----------

func TestNewFleetSandboxControl_InvalidConfig(t *testing.T) {
	cfg := FleetConfig{} // zero value fails validation
	_, err := NewFleetSandboxControl(&mockProvisioner{}, func(string) (FleetNodeClient, error) {
		return &mockNodeClient{}, nil
	}, cfg, WithMetricsRegisterer(prometheus.NewRegistry()))
	if err == nil {
		t.Fatal("expected error for invalid config")
	}
}

func TestNewFleetSandboxControl_WithLeaveInstancesOption(t *testing.T) {
	cfg := DefaultFleetConfig()
	mp := &mockProvisioner{}

	f, err := NewFleetSandboxControl(mp, func(string) (FleetNodeClient, error) {
		return &mockNodeClient{}, nil
	}, cfg, WithLeaveInstancesOnClose(true), WithMetricsRegisterer(prometheus.NewRegistry()))
	if err != nil {
		t.Fatal(err)
	}
	if !f.config.LeaveInstancesOnClose {
		t.Error("LeaveInstancesOnClose should be true from option")
	}
}

func TestTerminationReasonLabel_UnknownFallback(t *testing.T) {
	got := terminationReasonLabel(DrainReason("unexpected"), false)
	if got != "unknown" {
		t.Fatalf("terminationReasonLabel fallback = %q, want unknown", got)
	}
}
