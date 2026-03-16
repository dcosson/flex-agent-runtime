package environment_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"flex-agent-runtime/internal/rpc/api"
	"flex-agent-runtime/internal/sandbox/environment"
	"flex-agent-runtime/internal/sandbox/environment/local"
	"flex-agent-runtime/internal/sandbox/environment/native"
)

type envFactory func(t *testing.T) environment.ExecutionEnvironment

func runEnvironmentComplianceSuite(t *testing.T, mkEnv envFactory, config environment.SessionConfig, readReq environment.ToolRequest, writeReq environment.ToolRequest) {
	t.Helper()
	ctx := context.Background()

	t.Run("Capabilities", func(t *testing.T) {
		caps := mkEnv(t).Capabilities()
		_ = caps.Snapshots
		_ = caps.Pause
	})

	t.Run("FullLifecycle", func(t *testing.T) {
		env := mkEnv(t)
		if err := env.Create(ctx, config); err != nil {
			t.Fatalf("Create() error: %v", err)
		}
		if got := env.State(); got != environment.StateActive {
			t.Fatalf("State() after Create = %q, want %q", got, environment.StateActive)
		}
		resp, err := env.ExecuteTool(ctx, readReq, nil)
		if err != nil {
			t.Fatalf("ExecuteTool() error: %v", err)
		}
		if resp == nil {
			t.Fatal("ExecuteTool() returned nil response")
		}
		if err := env.Destroy(ctx); err != nil {
			t.Fatalf("Destroy() error: %v", err)
		}
		if got := env.State(); got == environment.StateCreating || got == environment.StateActive || got == environment.StatePaused {
			t.Fatalf("State() after Destroy = %q, want terminal state", got)
		}
	})

	t.Run("PauseResume", func(t *testing.T) {
		env := mkEnv(t)
		if err := env.Create(ctx, config); err != nil {
			t.Fatalf("Create() error: %v", err)
		}
		if !env.Capabilities().Pause {
			if err := env.Pause(ctx); !errors.Is(err, environment.ErrCapabilityNotSupported) {
				t.Fatalf("Pause() error = %v, want ErrCapabilityNotSupported", err)
			}
			if err := env.Resume(ctx); !errors.Is(err, environment.ErrCapabilityNotSupported) {
				t.Fatalf("Resume() error = %v, want ErrCapabilityNotSupported", err)
			}
			return
		}
		if err := env.Pause(ctx); err != nil {
			t.Fatalf("Pause() error: %v", err)
		}
		if got := env.State(); got != environment.StatePaused && got != environment.StateActive {
			t.Fatalf("State() after Pause = %q, want paused/active", got)
		}
		if err := env.Resume(ctx); err != nil {
			t.Fatalf("Resume() error: %v", err)
		}
		if got := env.State(); got != environment.StateActive {
			t.Fatalf("State() after Resume = %q, want %q", got, environment.StateActive)
		}
	})

	t.Run("SnapshotRollback", func(t *testing.T) {
		env := mkEnv(t)
		if err := env.Create(ctx, config); err != nil {
			t.Fatalf("Create() error: %v", err)
		}
		if !env.Capabilities().Rollback {
			if err := env.Rollback(ctx, "any"); !errors.Is(err, environment.ErrCapabilityNotSupported) {
				t.Fatalf("Rollback() error = %v, want ErrCapabilityNotSupported", err)
			}
			return
		}
		if _, err := env.ExecuteTool(ctx, writeReq, nil); err != nil {
			t.Fatalf("ExecuteTool(write) error: %v", err)
		}
		snap, err := env.CreateSnapshot(ctx, "pre-change")
		if err != nil {
			t.Fatalf("CreateSnapshot() error: %v", err)
		}
		if snap == nil || snap.ID == "" {
			t.Fatal("CreateSnapshot() returned empty snapshot")
		}
		if err := env.Rollback(ctx, snap.ID); err != nil {
			t.Fatalf("Rollback() error: %v", err)
		}
	})

	t.Run("DestroyedEnvironmentErrors", func(t *testing.T) {
		env := mkEnv(t)
		if err := env.Create(ctx, config); err != nil {
			t.Fatalf("Create() error: %v", err)
		}
		if err := env.Destroy(ctx); err != nil {
			t.Fatalf("Destroy() error: %v", err)
		}
		_, err := env.ExecuteTool(ctx, readReq, nil)
		if err == nil {
			t.Fatal("ExecuteTool() after destroy succeeded, want error")
		}
	})
}

func TestLocalEnvironmentComplianceSuite(t *testing.T) {
	factory := func(t *testing.T) environment.ExecutionEnvironment {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "input.txt"), []byte("hello\n"), 0o644); err != nil {
			t.Fatalf("seed file: %v", err)
		}
		return local.NewLocalEnvironment(root, slog.Default())
	}
	config := environment.SessionConfig{SessionID: "local-compliance"}
	readReq := environment.ToolRequest{
		ToolName: "read_file",
		Params:   map[string]any{"path": "input.txt"},
	}
	writeReq := environment.ToolRequest{
		ToolName: "write_file",
		Params:   map[string]any{"path": "written.txt", "content": "hi"},
	}

	runEnvironmentComplianceSuite(t, factory, config, readReq, writeReq)
}

// complianceMockService is a minimal api.SandboxService mock for compliance testing.
type complianceMockService struct {
	mu sync.Mutex

	sessions map[string]string // id -> state

	streamErr          error
	streamRecvErr      error
	streamProgressOnly bool

	lastCreateReq *api.CreateSessionRequest
	lastExecReq   *api.ExecuteToolRequest
}

func newComplianceMockService() *complianceMockService {
	return &complianceMockService{sessions: make(map[string]string)}
}

func (m *complianceMockService) CreateSession(_ context.Context, req *api.CreateSessionRequest) (*api.CreateSessionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastCreateReq = req
	m.sessions[req.SessionID] = "active"
	return &api.CreateSessionResponse{
		Session: &api.Session{ID: req.SessionID, State: "active"},
		ServerCapabilities: api.Capabilities{
			Snapshots:         true,
			Rollback:          true,
			Pause:             true,
			TierRouting:       true,
			StreamingProgress: true,
		},
	}, nil
}

func (m *complianceMockService) GetSession(_ context.Context, req *api.GetSessionRequest) (*api.GetSessionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.sessions[req.SessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", req.SessionID)
	}
	return &api.GetSessionResponse{Session: &api.Session{ID: req.SessionID, State: state}}, nil
}

func (m *complianceMockService) PauseSession(_ context.Context, req *api.PauseSessionRequest) (*api.PauseSessionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[req.SessionID] = "paused"
	return &api.PauseSessionResponse{}, nil
}

func (m *complianceMockService) ResumeSession(_ context.Context, req *api.ResumeSessionRequest) (*api.ResumeSessionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[req.SessionID] = "active"
	return &api.ResumeSessionResponse{}, nil
}

func (m *complianceMockService) DestroySession(_ context.Context, req *api.DestroySessionRequest) (*api.DestroySessionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, req.SessionID)
	return &api.DestroySessionResponse{}, nil
}

func (m *complianceMockService) ExecuteTool(_ context.Context, req *api.ExecuteToolRequest) (*api.ExecuteToolResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastExecReq = req
	return &api.ExecuteToolResponse{
		SessionID:  req.SessionID,
		ToolCallID: req.ToolCallID,
		ToolName:   req.ToolName,
		Content:    "mock result",
	}, nil
}

func (m *complianceMockService) ExecuteToolStream(_ context.Context, req *api.ExecuteToolRequest) (api.ExecuteToolStreamReceiver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	m.lastExecReq = req
	if _, ok := m.sessions[req.SessionID]; !ok {
		return nil, fmt.Errorf("session not found: %s", req.SessionID)
	}
	return &complianceMockStream{response: &api.ExecuteToolResponse{
		SessionID:  req.SessionID,
		ToolCallID: req.ToolCallID,
		ToolName:   req.ToolName,
		Content:    "mock result",
	}, recvErr: m.streamRecvErr, progressOnly: m.streamProgressOnly}, nil
}

func (m *complianceMockService) TurnComplete(_ context.Context, _ *api.TurnCompleteRequest) (*api.TurnCompleteResponse, error) {
	return &api.TurnCompleteResponse{}, nil
}

func (m *complianceMockService) CreateSnapshot(_ context.Context, req *api.CreateSnapshotRequest) (*api.CreateSnapshotResponse, error) {
	return &api.CreateSnapshotResponse{
		SnapshotID: fmt.Sprintf("snap-%s-%s", req.SessionID, req.Name),
		SpaceUsed:  4096,
	}, nil
}

func (m *complianceMockService) RollbackSession(_ context.Context, _ *api.RollbackSessionRequest) (*api.RollbackSessionResponse, error) {
	return &api.RollbackSessionResponse{}, nil
}

func (m *complianceMockService) ListSnapshots(_ context.Context, _ *api.ListSnapshotsRequest) (*api.ListSnapshotsResponse, error) {
	return &api.ListSnapshotsResponse{}, nil
}

func (m *complianceMockService) HealthCheck(_ context.Context, _ *api.HealthCheckRequest) (*api.HealthCheckResponse, error) {
	return &api.HealthCheckResponse{Status: "healthy"}, nil
}

// complianceMockStream implements api.ExecuteToolStreamReceiver for compliance testing.
type complianceMockStream struct {
	response     *api.ExecuteToolResponse
	sent         bool
	recvErr      error
	progressOnly bool
}

func (s *complianceMockStream) Recv() (*api.ExecuteToolStreamMessage, error) {
	if s.sent {
		if s.recvErr != nil {
			return nil, s.recvErr
		}
		return nil, fmt.Errorf("stream exhausted")
	}
	s.sent = true
	if s.progressOnly {
		return &api.ExecuteToolStreamMessage{Progress: &api.ToolProgress{Content: "partial", IsError: false}}, nil
	}
	return &api.ExecuteToolStreamMessage{Response: s.response}, nil
}

func (s *complianceMockStream) Close() error { return nil }

func TestNativeEnvironmentComplianceSuite(t *testing.T) {
	factory := func(t *testing.T) environment.ExecutionEnvironment {
		return native.NewNativeSandboxEnvironment(newComplianceMockService(), native.DefaultConfig(), slog.Default())
	}
	config := environment.SessionConfig{SessionID: "native-compliance"}
	readReq := environment.ToolRequest{
		ToolName: "read_file",
		Params:   map[string]any{"path": "input.txt"},
	}
	writeReq := environment.ToolRequest{
		ToolName: "write_file",
		Params:   map[string]any{"path": "written.txt", "content": "hi"},
	}

	runEnvironmentComplianceSuite(t, factory, config, readReq, writeReq)
}
