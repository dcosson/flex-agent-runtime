//go:build integration

package fleet

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
	"github.com/prometheus/client_golang/prometheus"
)

type integrationProvisioner struct {
	mu             sync.Mutex
	nextID         int
	running        map[string]instance.InstanceInfo
	terminateCalls []string
}

func newIntegrationProvisioner() *integrationProvisioner {
	return &integrationProvisioner{
		running: make(map[string]instance.InstanceInfo),
	}
}

func (p *integrationProvisioner) LaunchInstance(context.Context, instance.InstanceConfig) (*instance.InstanceInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	id := fmt.Sprintf("i-int-%d", p.nextID)
	ip := fmt.Sprintf("10.200.0.%d", p.nextID+10)
	p.nextID++

	info := instance.InstanceInfo{
		InstanceID: id,
		PrivateIP:  ip,
		State:      instance.CloudInstanceRunning,
		LaunchTime: time.Now(),
	}
	p.running[id] = info
	return &info, nil
}

func (p *integrationProvisioner) TerminateInstance(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.terminateCalls = append(p.terminateCalls, id)
	delete(p.running, id)
	return nil
}

func (p *integrationProvisioner) StopInstance(context.Context, string) error { return nil }
func (p *integrationProvisioner) StartInstance(context.Context, string) (*instance.InstanceInfo, error) {
	return nil, nil
}
func (p *integrationProvisioner) DescribeInstance(context.Context, string) (*instance.InstanceStatus, error) {
	return nil, nil
}

func (p *integrationProvisioner) ListInstances(context.Context, instance.InstanceFilter) ([]instance.InstanceInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	infos := make([]instance.InstanceInfo, 0, len(p.running))
	for _, info := range p.running {
		infos = append(infos, info)
	}
	return infos, nil
}

func (p *integrationProvisioner) addRunning(info instance.InstanceInfo) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running[info.InstanceID] = info
}

func (p *integrationProvisioner) terminatedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.terminateCalls)
}

type integrationNodeRuntime struct {
	mu            sync.Mutex
	sessionCount  int
	unhealthy     bool
	nextSessionID int
}

func (r *integrationNodeRuntime) makeClient() FleetNodeClient {
	return &mockNodeClient{
		createSandboxFn: func(context.Context, control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			id := fmt.Sprintf("sess-int-%d", r.nextSessionID)
			r.nextSessionID++
			r.sessionCount++
			return &control.CreateSandboxResponse{SandboxID: id}, nil
		},
		destroySandboxFn: func(context.Context, string) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.sessionCount > 0 {
				r.sessionCount--
			}
			return nil
		},
		healthCheckFn: func(context.Context) (*HealthCheckResult, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.unhealthy {
				return nil, fmt.Errorf("unhealthy")
			}
			return &HealthCheckResult{
				Status:       "healthy",
				SessionCount: r.sessionCount,
			}, nil
		},
	}
}

type integrationHarness struct {
	t       *testing.T
	fleet   *FleetSandboxControl
	prov    *integrationProvisioner
	clock   *fakeClock
	runtime map[string]*integrationNodeRuntime
}

func newIntegrationHarness(t *testing.T, cfg FleetConfig) *integrationHarness {
	t.Helper()

	prov := newIntegrationProvisioner()
	rtByAddr := make(map[string]*integrationNodeRuntime)

	f, err := NewFleetSandboxControl(prov, func(addr string) (FleetNodeClient, error) {
		rt := &integrationNodeRuntime{}
		rtByAddr[addr] = rt
		return rt.makeClient(), nil
	}, cfg, WithMetricsRegisterer(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("NewFleetSandboxControl() error: %v", err)
	}

	clk := newFakeClock(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	f.clock = clk

	return &integrationHarness{
		t:       t,
		fleet:   f,
		prov:    prov,
		clock:   clk,
		runtime: rtByAddr,
	}
}

func TestIntegrationFleet_FullLifecycle(t *testing.T) {
	cfg := DefaultFleetConfig()
	cfg.MinInstances = 0
	cfg.WarmPoolTarget = 0
	cfg.MaxInstances = 3
	cfg.IdleCooldown = 1 * time.Second

	h := newIntegrationHarness(t, cfg)
	ctx := context.Background()

	resp, err := h.fleet.CreateSandbox(ctx, control.CreateSandboxRequest{})
	if err != nil {
		t.Fatalf("CreateSandbox() error: %v", err)
	}
	if _, _, err := parseSandboxID(resp.SandboxID); err != nil {
		t.Fatalf("CreateSandbox() returned invalid fleet sandbox ID: %v", err)
	}

	if err := h.fleet.DestroySandbox(ctx, resp.SandboxID); err != nil {
		t.Fatalf("DestroySandbox() error: %v", err)
	}

	h.fleet.mu.RLock()
	for _, mi := range h.fleet.instances {
		mi.mu.Lock()
		mi.IdleSince = h.clock.Now().Add(-cfg.IdleCooldown - time.Second)
		mi.mu.Unlock()
	}
	h.fleet.mu.RUnlock()
	h.fleet.runIteration(ctx)

	if got := h.fleet.FleetStatus().TotalInstances; got != 0 {
		t.Fatalf("total instances after scale-down = %d, want 0", got)
	}
	if got := h.prov.terminatedCount(); got != 1 {
		t.Fatalf("terminate calls = %d, want 1", got)
	}
}

func TestIntegrationFleet_WarmPoolProvisioning(t *testing.T) {
	cfg := DefaultFleetConfig()
	cfg.MinInstances = 0
	cfg.WarmPoolTarget = 2
	cfg.MaxInstances = 4

	h := newIntegrationHarness(t, cfg)
	ctx := context.Background()

	h.fleet.runIteration(ctx)
	h.fleet.runIteration(ctx)

	status := h.fleet.FleetStatus()
	if status.TotalInstances != 2 {
		t.Fatalf("total instances = %d, want 2", status.TotalInstances)
	}
	if status.InstancesByState[InstanceReady] != 2 {
		t.Fatalf("ready instances = %d, want 2", status.InstancesByState[InstanceReady])
	}
}

func TestIntegrationFleet_UnhealthyDrainLifecycle(t *testing.T) {
	cfg := DefaultFleetConfig()
	cfg.MinInstances = 0
	cfg.WarmPoolTarget = 1
	cfg.MaxInstances = 2
	cfg.UnhealthyThreshold = 3

	h := newIntegrationHarness(t, cfg)
	ctx := context.Background()

	h.fleet.runIteration(ctx)
	h.fleet.runIteration(ctx)

	// Mark the single node unhealthy and let the control loop drain it.
	h.fleet.mu.RLock()
	var target *ManagedInstance
	for _, mi := range h.fleet.instances {
		target = mi
		break
	}
	h.fleet.mu.RUnlock()
	if target == nil {
		t.Fatal("expected one managed instance")
	}

	addr := fmt.Sprintf("%s:%d", target.IP, cfg.SandboxHostPort)
	rt := h.runtime[addr]
	if rt == nil {
		t.Fatalf("missing runtime for %s", addr)
	}
	rt.mu.Lock()
	rt.unhealthy = true
	rt.mu.Unlock()

	for i := 0; i < cfg.UnhealthyThreshold; i++ {
		h.fleet.runIteration(ctx)
	}

	target.mu.RLock()
	state := target.State
	target.mu.RUnlock()
	if state != InstanceDraining && state != InstanceTerminating {
		t.Fatalf("state = %s, want Draining/Terminating", state)
	}

	h.fleet.runIteration(ctx)
	if got := h.prov.terminatedCount(); got == 0 {
		t.Fatal("expected terminate call for unhealthy drained instance")
	}
}

func TestIntegrationFleet_CrashRecoveryReAdopt(t *testing.T) {
	cfg := DefaultFleetConfig()
	cfg.MinInstances = 0
	cfg.WarmPoolTarget = 0

	h := newIntegrationHarness(t, cfg)
	ctx := context.Background()

	h.prov.addRunning(instance.InstanceInfo{
		InstanceID: "i-recover-1",
		PrivateIP:  "10.201.0.11",
		State:      instance.CloudInstanceRunning,
	})
	h.prov.addRunning(instance.InstanceInfo{
		InstanceID: "i-recover-2",
		PrivateIP:  "10.201.0.12",
		State:      instance.CloudInstanceRunning,
	})

	if err := h.fleet.RecoverInstances(ctx); err != nil {
		t.Fatalf("RecoverInstances() error: %v", err)
	}
	h.fleet.runIteration(ctx)

	status := h.fleet.FleetStatus()
	if status.TotalInstances != 2 {
		t.Fatalf("total instances after recovery = %d, want 2", status.TotalInstances)
	}
	if status.InstancesByState[InstanceReady] != 2 {
		t.Fatalf("ready instances after recovery = %d, want 2", status.InstancesByState[InstanceReady])
	}
}
