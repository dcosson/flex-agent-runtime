package native

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/rpc/api"
	"github.com/anthropics/flex-agent-runtime/internal/sandbox/control"
)

// mockSandboxService implements api.SandboxService for testing.
type mockSandboxService struct {
	mu       sync.Mutex
	sessions map[string]*api.Session
	paused   map[string]bool

	launchProcessID string
	launchAddress   string
	processStatus   api.ProcessStatus
	processExitCode *int
	processKilled   bool

	// Error injection per method.
	errors map[string]error
}

func newMockSandboxService() *mockSandboxService {
	return &mockSandboxService{
		sessions:      make(map[string]*api.Session),
		paused:        make(map[string]bool),
		processStatus: api.ProcessStatusRunning,
		errors:        make(map[string]error),
	}
}

func (m *mockSandboxService) setLaunchProcessResult(processID, address string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.launchProcessID = processID
	m.launchAddress = address
}

func (m *mockSandboxService) setError(method string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors[method] = err
}

func (m *mockSandboxService) checkError(method string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.errors[method]
}

func (m *mockSandboxService) CreateSession(_ context.Context, req *api.CreateSessionRequest) (*api.CreateSessionResponse, error) {
	if err := m.checkError("CreateSession"); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := fmt.Sprintf("sess-%d", len(m.sessions)+1)
	sess := &api.Session{
		ID:         id,
		State:      "active",
		Mountpoint: "/pool/sessions/" + id,
		Created:    time.Now(),
		Labels:     req.Labels,
	}
	m.sessions[id] = sess
	return &api.CreateSessionResponse{
		Session: sess,
		ServerCapabilities: api.Capabilities{
			Snapshots:         true,
			Rollback:          true,
			Pause:             true,
			StreamingProgress: true,
			TierRouting:       true,
		},
	}, nil
}

func (m *mockSandboxService) GetSession(_ context.Context, req *api.GetSessionRequest) (*api.GetSessionResponse, error) {
	if err := m.checkError("GetSession"); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[req.SessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", req.SessionID)
	}
	return &api.GetSessionResponse{Session: sess}, nil
}

func (m *mockSandboxService) PauseSession(_ context.Context, req *api.PauseSessionRequest) (*api.PauseSessionResponse, error) {
	if err := m.checkError("PauseSession"); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[req.SessionID]; !ok {
		return nil, fmt.Errorf("session not found: %s", req.SessionID)
	}
	m.paused[req.SessionID] = true
	return &api.PauseSessionResponse{}, nil
}

func (m *mockSandboxService) ResumeSession(_ context.Context, req *api.ResumeSessionRequest) (*api.ResumeSessionResponse, error) {
	if err := m.checkError("ResumeSession"); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[req.SessionID]; !ok {
		return nil, fmt.Errorf("session not found: %s", req.SessionID)
	}
	delete(m.paused, req.SessionID)
	return &api.ResumeSessionResponse{}, nil
}

func (m *mockSandboxService) DestroySession(_ context.Context, req *api.DestroySessionRequest) (*api.DestroySessionResponse, error) {
	if err := m.checkError("DestroySession"); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[req.SessionID]; !ok {
		return nil, fmt.Errorf("session not found: %s", req.SessionID)
	}
	delete(m.sessions, req.SessionID)
	delete(m.paused, req.SessionID)
	return &api.DestroySessionResponse{}, nil
}

func (m *mockSandboxService) LaunchProcess(_ context.Context, _ *api.LaunchProcessRequest) (*api.LaunchProcessResponse, error) {
	if err := m.checkError("LaunchProcess"); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	pid := m.launchProcessID
	if pid == "" {
		pid = "proc-default"
	}
	addr := m.launchAddress
	if addr == "" {
		addr = "localhost:9100"
	}
	m.processKilled = false
	return &api.LaunchProcessResponse{
		ProcessID: pid,
		Address:   addr,
		Status:    api.ProcessStatusRunning,
	}, nil
}

func (m *mockSandboxService) KillProcess(_ context.Context, _ *api.KillProcessRequest) (*api.KillProcessResponse, error) {
	if err := m.checkError("KillProcess"); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processKilled = true
	exitCode := 137
	m.processExitCode = &exitCode
	m.processStatus = api.ProcessStatusExited
	return &api.KillProcessResponse{}, nil
}

func (m *mockSandboxService) GetProcessStatus(_ context.Context, _ *api.GetProcessStatusRequest) (*api.GetProcessStatusResponse, error) {
	if err := m.checkError("GetProcessStatus"); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return &api.GetProcessStatusResponse{
		Status:   m.processStatus,
		ExitCode: m.processExitCode,
	}, nil
}

func (m *mockSandboxService) ExecuteTool(_ context.Context, _ *api.ExecuteToolRequest) (*api.ExecuteToolResponse, error) {
	return &api.ExecuteToolResponse{}, nil
}

func (m *mockSandboxService) ExecuteToolStream(_ context.Context, _ *api.ExecuteToolRequest) (api.ExecuteToolStreamReceiver, error) {
	return nil, fmt.Errorf("not implemented in mock")
}

func (m *mockSandboxService) TurnComplete(_ context.Context, _ *api.TurnCompleteRequest) (*api.TurnCompleteResponse, error) {
	return &api.TurnCompleteResponse{}, nil
}

func (m *mockSandboxService) CreateSnapshot(_ context.Context, _ *api.CreateSnapshotRequest) (*api.CreateSnapshotResponse, error) {
	return &api.CreateSnapshotResponse{}, nil
}

func (m *mockSandboxService) RollbackSession(_ context.Context, _ *api.RollbackSessionRequest) (*api.RollbackSessionResponse, error) {
	return &api.RollbackSessionResponse{}, nil
}

func (m *mockSandboxService) ListSnapshots(_ context.Context, _ *api.ListSnapshotsRequest) (*api.ListSnapshotsResponse, error) {
	return &api.ListSnapshotsResponse{}, nil
}

func (m *mockSandboxService) HealthCheck(_ context.Context, _ *api.HealthCheckRequest) (*api.HealthCheckResponse, error) {
	return &api.HealthCheckResponse{Status: "ok"}, nil
}

// --- Tests ---

// SC1: NativeSandboxControl CreateSandbox/DestroySandbox
func TestSC1_NativeSandboxControl_CreateDestroy(t *testing.T) {
	mock := newMockSandboxService()
	ctrl := NewNativeSandboxControl(mock)
	ctx := context.Background()

	resp, err := ctrl.CreateSandbox(ctx, control.CreateSandboxRequest{
		Template:  "tank/bases/repo@v1",
		Labels:    map[string]string{"env": "test"},
		Resources: control.ResourceSpec{CPUs: 2.0, MemMB: 4096},
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	if resp.SandboxID == "" {
		t.Fatal("expected non-empty SandboxID")
	}
	if resp.Address == "" {
		t.Fatal("expected non-empty Address")
	}

	caps := ctrl.Capabilities()
	if !caps.Snapshots {
		t.Error("expected Snapshots capability")
	}
	if !caps.Rollback {
		t.Error("expected Rollback capability")
	}
	if !caps.Pause {
		t.Error("expected Pause capability")
	}
	if !caps.LaunchProcess {
		t.Error("expected LaunchProcess capability")
	}

	if err := ctrl.DestroySandbox(ctx, resp.SandboxID); err != nil {
		t.Fatalf("DestroySandbox: %v", err)
	}

	// Destroying again should fail (session gone).
	if err := ctrl.DestroySandbox(ctx, resp.SandboxID); err == nil {
		t.Fatal("expected error destroying non-existent sandbox")
	}
}

// SC2: NativeSandboxControl Pause/Resume
func TestSC2_NativeSandboxControl_PauseResume(t *testing.T) {
	mock := newMockSandboxService()
	ctrl := NewNativeSandboxControl(mock)
	ctx := context.Background()

	resp, err := ctrl.CreateSandbox(ctx, control.CreateSandboxRequest{
		Template: "tank/bases/repo@v1",
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	if err := ctrl.PauseSandbox(ctx, resp.SandboxID); err != nil {
		t.Fatalf("PauseSandbox: %v", err)
	}
	if err := ctrl.ResumeSandbox(ctx, resp.SandboxID); err != nil {
		t.Fatalf("ResumeSandbox: %v", err)
	}

	if err := ctrl.DestroySandbox(ctx, resp.SandboxID); err != nil {
		t.Fatalf("DestroySandbox: %v", err)
	}
}

// SC3: NativeSandboxControl LaunchProcess/KillProcess/GetProcessStatus
func TestSC3_NativeSandboxControl_LaunchProcess(t *testing.T) {
	mock := newMockSandboxService()
	mock.setLaunchProcessResult("proc-1", "sandbox-host:9100")
	ctrl := NewNativeSandboxControl(mock)
	ctx := context.Background()

	sandboxResp, err := ctrl.CreateSandbox(ctx, control.CreateSandboxRequest{
		Template: "tank/bases/repo@v1",
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	launchResp, err := ctrl.LaunchProcess(ctx, control.LaunchProcessRequest{
		SandboxID:  sandboxResp.SandboxID,
		Binary:     "flexagent",
		Args:       []string{"serve", "agent", "--addr", ":8080"},
		Env:        map[string]string{"ANTHROPIC_API_KEY": "sk-test"},
		ExposePort: 8080,
	})
	if err != nil {
		t.Fatalf("LaunchProcess: %v", err)
	}
	if launchResp.ProcessID != "proc-1" {
		t.Errorf("ProcessID = %q, want proc-1", launchResp.ProcessID)
	}
	if launchResp.Address != "sandbox-host:9100" {
		t.Errorf("Address = %q, want sandbox-host:9100", launchResp.Address)
	}
	if launchResp.Status != control.ProcessRunning {
		t.Errorf("Status = %q, want running", launchResp.Status)
	}

	// GetProcessStatus
	status, err := ctrl.GetProcessStatus(ctx, control.GetProcessStatusRequest{
		SandboxID: sandboxResp.SandboxID,
		ProcessID: "proc-1",
	})
	if err != nil {
		t.Fatalf("GetProcessStatus: %v", err)
	}
	if status.Status != control.ProcessRunning {
		t.Errorf("Status = %q, want running", status.Status)
	}
	if status.ExitCode != nil {
		t.Errorf("ExitCode should be nil for running process, got %d", *status.ExitCode)
	}

	// KillProcess
	if err := ctrl.KillProcess(ctx, control.KillProcessRequest{
		SandboxID: sandboxResp.SandboxID,
		ProcessID: "proc-1",
	}); err != nil {
		t.Fatalf("KillProcess: %v", err)
	}

	// After kill, status should be exited.
	status, err = ctrl.GetProcessStatus(ctx, control.GetProcessStatusRequest{
		SandboxID: sandboxResp.SandboxID,
		ProcessID: "proc-1",
	})
	if err != nil {
		t.Fatalf("GetProcessStatus after kill: %v", err)
	}
	if status.Status != control.ProcessExited {
		t.Errorf("Status = %q, want exited", status.Status)
	}
	if status.ExitCode == nil || *status.ExitCode != 137 {
		t.Errorf("ExitCode = %v, want 137", status.ExitCode)
	}
}

// SC4: CloudSandboxControl placeholder compliance (tested in cloud package).
// Here we verify that NativeSandboxControl satisfies the interface.
func TestNativeSandboxControl_InterfaceCompliance(t *testing.T) {
	mock := newMockSandboxService()
	var _ control.SandboxControl = NewNativeSandboxControl(mock)
}

// Additional tests beyond SC1-SC4.

func TestNativeSandboxControl_CreateSandboxFieldMapping(t *testing.T) {
	mock := newMockSandboxService()
	ctrl := NewNativeSandboxControl(mock, WithDefaultQuota(10*1024*1024*1024))
	ctx := context.Background()

	resp, err := ctrl.CreateSandbox(ctx, control.CreateSandboxRequest{
		Template:  "tank/bases/ubuntu@latest",
		Labels:    map[string]string{"project": "test", "env": "ci"},
		Resources: control.ResourceSpec{CPUs: 4.0, MemMB: 8192},
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	// Verify session was created in mock.
	mock.mu.Lock()
	sess := mock.sessions[resp.SandboxID]
	mock.mu.Unlock()

	if sess == nil {
		t.Fatal("session not found in mock")
	}
	if sess.Labels["project"] != "test" {
		t.Errorf("label project = %q, want test", sess.Labels["project"])
	}
	if sess.Labels["env"] != "ci" {
		t.Errorf("label env = %q, want ci", sess.Labels["env"])
	}
}

func TestNativeSandboxControl_ErrorPropagation(t *testing.T) {
	mock := newMockSandboxService()
	ctrl := NewNativeSandboxControl(mock)
	ctx := context.Background()

	testErr := errors.New("injected error")

	tests := []struct {
		name   string
		method string
		call   func() error
	}{
		{"CreateSandbox", "CreateSession", func() error {
			_, err := ctrl.CreateSandbox(ctx, control.CreateSandboxRequest{Template: "t"})
			return err
		}},
		{"LaunchProcess", "LaunchProcess", func() error {
			_, err := ctrl.LaunchProcess(ctx, control.LaunchProcessRequest{SandboxID: "s"})
			return err
		}},
		{"KillProcess", "KillProcess", func() error {
			return ctrl.KillProcess(ctx, control.KillProcessRequest{SandboxID: "s", ProcessID: "p"})
		}},
		{"GetProcessStatus", "GetProcessStatus", func() error {
			_, err := ctrl.GetProcessStatus(ctx, control.GetProcessStatusRequest{SandboxID: "s", ProcessID: "p"})
			return err
		}},
		{"PauseSandbox", "PauseSession", func() error {
			return ctrl.PauseSandbox(ctx, "nonexistent")
		}},
		{"ResumeSandbox", "ResumeSession", func() error {
			return ctrl.ResumeSandbox(ctx, "nonexistent")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock.setError(tt.method, testErr)
			defer mock.setError(tt.method, nil)

			err := tt.call()
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestNativeSandboxControl_KillProcessDefaultSignal(t *testing.T) {
	mock := newMockSandboxService()
	ctrl := NewNativeSandboxControl(mock)
	ctx := context.Background()

	// Signal=0 means default (SIGTERM). Verify it passes through.
	err := ctrl.KillProcess(ctx, control.KillProcessRequest{
		SandboxID: "sess-1",
		ProcessID: "proc-1",
		Signal:    0,
	})
	if err != nil {
		t.Fatalf("KillProcess: %v", err)
	}
}

func TestNativeSandboxControl_PauseNonexistent(t *testing.T) {
	mock := newMockSandboxService()
	ctrl := NewNativeSandboxControl(mock)
	ctx := context.Background()

	if err := ctrl.PauseSandbox(ctx, "nonexistent"); err == nil {
		t.Fatal("expected error pausing nonexistent sandbox")
	}
}

func TestNativeSandboxControl_AddressToURL(t *testing.T) {
	url := control.AddressToURL("sandbox-host:9100")
	if url != "http://sandbox-host:9100" {
		t.Errorf("AddressToURL = %q, want http://sandbox-host:9100", url)
	}
}

func TestNativeSandboxControl_ProcessStatusMapping(t *testing.T) {
	// Verify ProcessStatus constants match between control and api packages.
	if control.ProcessStatus(api.ProcessStatusStarting) != control.ProcessStarting {
		t.Error("ProcessStarting mismatch")
	}
	if control.ProcessStatus(api.ProcessStatusRunning) != control.ProcessRunning {
		t.Error("ProcessRunning mismatch")
	}
	if control.ProcessStatus(api.ProcessStatusExited) != control.ProcessExited {
		t.Error("ProcessExited mismatch")
	}
}
