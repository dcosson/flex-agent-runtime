package native

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"h2-agent-runtime/internal/ai"
	"h2-agent-runtime/internal/rpc/api"
	"h2-agent-runtime/internal/sandbox/environment"
	"h2-agent-runtime/internal/tools"
)

// mockSandboxService implements api.SandboxService for unit testing.
type mockSandboxService struct {
	mu sync.Mutex

	// Session state tracking
	sessions map[string]*api.Session

	// ExecuteToolStream behavior
	streamMessages []*api.ExecuteToolStreamMessage
	streamErr      error
	streamRecvErr  error

	// Snapshot tracking
	snapshots map[string]*api.CreateSnapshotResponse

	// Error injection
	createErr   error
	pauseErr    error
	resumeErr   error
	destroyErr  error
	getErr      error
	snapshotErr error
	rollbackErr error

	// Call tracking
	createCalls   int
	pauseCalls    int
	resumeCalls   int
	destroyCalls  int
	getCalls      int
	executeCalls  int
	snapshotCalls int
	rollbackCalls int
	lastCreateReq *api.CreateSessionRequest
	lastExecReq   *api.ExecuteToolRequest
	serverCaps    api.Capabilities
}

func newMockService() *mockSandboxService {
	return &mockSandboxService{
		sessions:   make(map[string]*api.Session),
		snapshots:  make(map[string]*api.CreateSnapshotResponse),
		serverCaps: api.Capabilities{Snapshots: true, Rollback: true, Pause: true, TierRouting: true, StreamingProgress: true},
	}
}

func (m *mockSandboxService) CreateSession(_ context.Context, req *api.CreateSessionRequest) (*api.CreateSessionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createCalls++
	m.lastCreateReq = req
	if m.createErr != nil {
		return nil, m.createErr
	}
	sess := &api.Session{
		ID:    req.SessionID,
		State: "active",
	}
	m.sessions[req.SessionID] = sess
	return &api.CreateSessionResponse{
		Session:            sess,
		ServerCapabilities: m.serverCaps,
	}, nil
}

func (m *mockSandboxService) GetSession(_ context.Context, req *api.GetSessionRequest) (*api.GetSessionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getCalls++
	if m.getErr != nil {
		return nil, m.getErr
	}
	sess, ok := m.sessions[req.SessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", req.SessionID)
	}
	return &api.GetSessionResponse{Session: sess}, nil
}

func (m *mockSandboxService) PauseSession(_ context.Context, req *api.PauseSessionRequest) (*api.PauseSessionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pauseCalls++
	if m.pauseErr != nil {
		return nil, m.pauseErr
	}
	if sess, ok := m.sessions[req.SessionID]; ok {
		sess.State = "paused"
	}
	return &api.PauseSessionResponse{}, nil
}

func (m *mockSandboxService) ResumeSession(_ context.Context, req *api.ResumeSessionRequest) (*api.ResumeSessionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resumeCalls++
	if m.resumeErr != nil {
		return nil, m.resumeErr
	}
	if sess, ok := m.sessions[req.SessionID]; ok {
		sess.State = "active"
	}
	return &api.ResumeSessionResponse{}, nil
}

func (m *mockSandboxService) DestroySession(_ context.Context, req *api.DestroySessionRequest) (*api.DestroySessionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.destroyCalls++
	if m.destroyErr != nil {
		return nil, m.destroyErr
	}
	delete(m.sessions, req.SessionID)
	return &api.DestroySessionResponse{}, nil
}

func (m *mockSandboxService) ExecuteTool(_ context.Context, _ *api.ExecuteToolRequest) (*api.ExecuteToolResponse, error) {
	return nil, fmt.Errorf("ExecuteTool not implemented; use ExecuteToolStream")
}

func (m *mockSandboxService) ExecuteToolStream(_ context.Context, req *api.ExecuteToolRequest) (api.ExecuteToolStreamReceiver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.executeCalls++
	m.lastExecReq = req
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	return &mockStreamReceiver{
		messages: m.streamMessages,
		recvErr:  m.streamRecvErr,
	}, nil
}

func (m *mockSandboxService) TurnComplete(_ context.Context, _ *api.TurnCompleteRequest) (*api.TurnCompleteResponse, error) {
	return &api.TurnCompleteResponse{}, nil
}

func (m *mockSandboxService) CreateSnapshot(_ context.Context, req *api.CreateSnapshotRequest) (*api.CreateSnapshotResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshotCalls++
	if m.snapshotErr != nil {
		return nil, m.snapshotErr
	}
	resp := &api.CreateSnapshotResponse{
		SnapshotID: fmt.Sprintf("snap-%s-%s", req.SessionID, req.Name),
		TurnNumber: 1,
		SpaceUsed:  4096,
	}
	m.snapshots[resp.SnapshotID] = resp
	return resp, nil
}

func (m *mockSandboxService) RollbackSession(_ context.Context, _ *api.RollbackSessionRequest) (*api.RollbackSessionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rollbackCalls++
	if m.rollbackErr != nil {
		return nil, m.rollbackErr
	}
	return &api.RollbackSessionResponse{}, nil
}

func (m *mockSandboxService) ListSnapshots(_ context.Context, _ *api.ListSnapshotsRequest) (*api.ListSnapshotsResponse, error) {
	return &api.ListSnapshotsResponse{}, nil
}

func (m *mockSandboxService) HealthCheck(_ context.Context, _ *api.HealthCheckRequest) (*api.HealthCheckResponse, error) {
	return &api.HealthCheckResponse{Status: "healthy"}, nil
}

// mockStreamReceiver implements api.ExecuteToolStreamReceiver.
type mockStreamReceiver struct {
	messages []*api.ExecuteToolStreamMessage
	idx      int
	recvErr  error
	closed   bool
}

func (r *mockStreamReceiver) Recv() (*api.ExecuteToolStreamMessage, error) {
	if r.recvErr != nil && r.idx >= len(r.messages) {
		return nil, r.recvErr
	}
	if r.idx >= len(r.messages) {
		return nil, fmt.Errorf("stream exhausted without final response")
	}
	msg := r.messages[r.idx]
	r.idx++
	return msg, nil
}

func (r *mockStreamReceiver) Close() error {
	r.closed = true
	return nil
}

// =====================================================================
// Lifecycle Tests
// =====================================================================

func TestCreate_Success(t *testing.T) {
	svc := newMockService()
	env := NewNativeSandboxEnvironment(svc, DefaultConfig(), slog.Default())

	err := env.Create(context.Background(), environment.SessionConfig{
		SessionID: "sess-1",
		BaseImage: "pool/bases/repo@v1",
		Labels:    map[string]string{"owner": "test"},
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if env.sessionID != "sess-1" {
		t.Fatalf("sessionID = %q, want %q", env.sessionID, "sess-1")
	}
	if svc.createCalls != 1 {
		t.Fatalf("createCalls = %d, want 1", svc.createCalls)
	}
	if svc.lastCreateReq.BaseSnapshot != "pool/bases/repo@v1" {
		t.Fatalf("BaseSnapshot = %q, want %q", svc.lastCreateReq.BaseSnapshot, "pool/bases/repo@v1")
	}
	if svc.lastCreateReq.Labels["owner"] != "test" {
		t.Fatalf("Labels not propagated")
	}
}

func TestCreate_EmptySessionID(t *testing.T) {
	svc := newMockService()
	env := NewNativeSandboxEnvironment(svc, DefaultConfig(), slog.Default())
	err := env.Create(context.Background(), environment.SessionConfig{})
	if err == nil {
		t.Fatal("Create() with empty SessionID should fail")
	}
	if svc.createCalls != 0 {
		t.Fatalf("RPC should not be called on validation failure")
	}
}

func TestCreate_RPCError(t *testing.T) {
	svc := newMockService()
	svc.createErr = fmt.Errorf("connection refused")
	env := NewNativeSandboxEnvironment(svc, DefaultConfig(), slog.Default())

	err := env.Create(context.Background(), environment.SessionConfig{SessionID: "sess-1"})
	if err == nil {
		t.Fatal("expected error from RPC failure")
	}
	if !strings.Contains(err.Error(), "native sandbox: create") {
		t.Fatalf("unexpected error format: %v", err)
	}
}

func TestPause_Success(t *testing.T) {
	svc := newMockService()
	env := createTestEnv(t, svc, "sess-1")

	if err := env.Pause(context.Background()); err != nil {
		t.Fatalf("Pause() error: %v", err)
	}
	if svc.pauseCalls != 1 {
		t.Fatalf("pauseCalls = %d, want 1", svc.pauseCalls)
	}
}

func TestPause_RPCError(t *testing.T) {
	svc := newMockService()
	env := createTestEnv(t, svc, "sess-1")
	svc.pauseErr = fmt.Errorf("session busy")

	err := env.Pause(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "native sandbox: pause") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResume_Success(t *testing.T) {
	svc := newMockService()
	env := createTestEnv(t, svc, "sess-1")
	_ = env.Pause(context.Background())

	if err := env.Resume(context.Background()); err != nil {
		t.Fatalf("Resume() error: %v", err)
	}
	if svc.resumeCalls != 1 {
		t.Fatalf("resumeCalls = %d, want 1", svc.resumeCalls)
	}
}

func TestResume_RPCError(t *testing.T) {
	svc := newMockService()
	env := createTestEnv(t, svc, "sess-1")
	svc.resumeErr = fmt.Errorf("not paused")

	err := env.Resume(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "native sandbox: resume") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDestroy_Success(t *testing.T) {
	svc := newMockService()
	env := createTestEnv(t, svc, "sess-1")

	if err := env.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error: %v", err)
	}
	if svc.destroyCalls != 1 {
		t.Fatalf("destroyCalls = %d, want 1", svc.destroyCalls)
	}
	if !env.destroyed.Load() {
		t.Fatal("destroyed flag should be set")
	}
}

func TestDestroy_DoubleDestroy_Idempotent(t *testing.T) {
	svc := newMockService()
	env := createTestEnv(t, svc, "sess-1")

	if err := env.Destroy(context.Background()); err != nil {
		t.Fatalf("first Destroy() error: %v", err)
	}
	if svc.destroyCalls != 1 {
		t.Fatalf("destroyCalls after first = %d, want 1", svc.destroyCalls)
	}

	// Second destroy should be a no-op — no RPC call.
	if err := env.Destroy(context.Background()); err != nil {
		t.Fatalf("second Destroy() error: %v", err)
	}
	if svc.destroyCalls != 1 {
		t.Fatalf("destroyCalls after second = %d, want 1 (no-op)", svc.destroyCalls)
	}
}

func TestDestroy_RPCError(t *testing.T) {
	svc := newMockService()
	env := createTestEnv(t, svc, "sess-1")
	svc.destroyErr = fmt.Errorf("already destroying")

	err := env.Destroy(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if env.destroyed.Load() {
		t.Fatal("destroyed flag should not be set on error")
	}
}

// =====================================================================
// State Tests
// =====================================================================

func TestState_BeforeCreate(t *testing.T) {
	svc := newMockService()
	env := NewNativeSandboxEnvironment(svc, DefaultConfig(), slog.Default())

	if got := env.State(); got != environment.StateCreating {
		t.Fatalf("State() before Create = %q, want %q", got, environment.StateCreating)
	}
}

func TestState_Active(t *testing.T) {
	svc := newMockService()
	env := createTestEnv(t, svc, "sess-1")

	if got := env.State(); got != environment.StateActive {
		t.Fatalf("State() = %q, want %q", got, environment.StateActive)
	}
}

func TestState_Paused(t *testing.T) {
	svc := newMockService()
	env := createTestEnv(t, svc, "sess-1")
	_ = env.Pause(context.Background())

	if got := env.State(); got != environment.StatePaused {
		t.Fatalf("State() = %q, want %q", got, environment.StatePaused)
	}
}

func TestState_AfterDestroy(t *testing.T) {
	svc := newMockService()
	env := createTestEnv(t, svc, "sess-1")
	_ = env.Destroy(context.Background())

	if got := env.State(); got != environment.StateDestroyed {
		t.Fatalf("State() after Destroy = %q, want %q", got, environment.StateDestroyed)
	}
}

func TestState_RPCError_ReturnsFailed(t *testing.T) {
	svc := newMockService()
	env := createTestEnv(t, svc, "sess-1")
	svc.getErr = fmt.Errorf("connection lost")

	if got := env.State(); got != environment.StateFailed {
		t.Fatalf("State() on RPC error = %q, want %q", got, environment.StateFailed)
	}
}

// =====================================================================
// Capabilities Tests
// =====================================================================

func TestCapabilities(t *testing.T) {
	env := NewNativeSandboxEnvironment(newMockService(), DefaultConfig(), slog.Default())
	caps := env.Capabilities()

	if !caps.Snapshots {
		t.Error("Snapshots should be true")
	}
	if !caps.Rollback {
		t.Error("Rollback should be true")
	}
	if !caps.Pause {
		t.Error("Pause should be true")
	}
	if !caps.TierRouting {
		t.Error("TierRouting should be true")
	}
	if !caps.StreamingProgress {
		t.Error("StreamingProgress should be true")
	}
	if got, want := caps.Snapshots, true; got != want {
		t.Fatalf("Snapshots = %v, want %v", got, want)
	}
	if got, want := caps.Rollback, true; got != want {
		t.Fatalf("Rollback = %v, want %v", got, want)
	}
	if got, want := caps.Pause, true; got != want {
		t.Fatalf("Pause = %v, want %v", got, want)
	}
	if got, want := caps.TierRouting, true; got != want {
		t.Fatalf("TierRouting = %v, want %v", got, want)
	}
}

func TestCapabilities_FromConfig(t *testing.T) {
	env := NewNativeSandboxEnvironment(newMockService(), NativeSandboxConfig{
		StorageBackend:   StorageBackendLocalDisk,
		ContainerRuntime: ContainerRuntimeNone,
	}, slog.Default())
	caps := env.Capabilities()
	if caps.Snapshots {
		t.Fatal("Snapshots should be false for local-disk")
	}
	if caps.Rollback {
		t.Fatal("Rollback should be false for local-disk")
	}
	if caps.TierRouting {
		t.Fatal("TierRouting should be false for runtime=none")
	}
	if !caps.Pause {
		t.Fatal("Pause should remain true")
	}
}

func TestCreate_CapabilityNegotiationMismatchSnapshots(t *testing.T) {
	svc := newMockService()
	svc.serverCaps.Snapshots = false
	svc.serverCaps.Rollback = false
	env := NewNativeSandboxEnvironment(svc, NativeSandboxConfig{
		StorageBackend:   StorageBackendZFS,
		ContainerRuntime: ContainerRuntimeGVisor,
	}, slog.Default())
	err := env.Create(context.Background(), environment.SessionConfig{SessionID: "sess-1"})
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if !strings.Contains(err.Error(), "configured zfs backend") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreate_CapabilityNegotiationMismatchTierRouting(t *testing.T) {
	svc := newMockService()
	svc.serverCaps.TierRouting = false
	env := NewNativeSandboxEnvironment(svc, NativeSandboxConfig{
		StorageBackend:   StorageBackendZFS,
		ContainerRuntime: ContainerRuntimeGVisor,
	}, slog.Default())
	err := env.Create(context.Background(), environment.SessionConfig{SessionID: "sess-1"})
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if !strings.Contains(err.Error(), "configured gvisor runtime") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =====================================================================
// ExecuteTool Tests
// =====================================================================

func TestExecuteTool_ResponseOnly(t *testing.T) {
	svc := newMockService()
	svc.streamMessages = []*api.ExecuteToolStreamMessage{
		{Response: &api.ExecuteToolResponse{
			SessionID:  "sess-1",
			ToolCallID: "tc-1",
			ToolName:   "read_file",
			Content:    "file contents here",
			ExitCode:   intPtr(0),
		}},
	}
	env := createTestEnv(t, svc, "sess-1")

	resp, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
		ToolCallID: "tc-1",
		ToolName:   "read_file",
		Params:     map[string]any{"path": "test.txt"},
	}, nil)
	if err != nil {
		t.Fatalf("ExecuteTool() error: %v", err)
	}
	if len(resp.Content) == 0 {
		t.Fatal("empty response content")
	}
	tc, ok := resp.Content[0].(*ai.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", resp.Content[0])
	}
	if tc.Text != "file contents here" {
		t.Fatalf("content = %q, want %q", tc.Text, "file contents here")
	}
	if resp.ExitCode == nil || *resp.ExitCode != 0 {
		t.Fatalf("exit code mismatch: %v", resp.ExitCode)
	}
}

func TestExecuteTool_WithContentBlocks(t *testing.T) {
	svc := newMockService()
	svc.streamMessages = []*api.ExecuteToolStreamMessage{
		{Response: &api.ExecuteToolResponse{
			SessionID:  "sess-1",
			ToolCallID: "tc-1",
			ToolName:   "read_file",
			ContentBlocks: []api.ContentBlock{
				{Type: "text", Text: "block 1"},
				{Type: "text", Text: "block 2"},
			},
			SnapshotID: "snap-123",
		}},
	}
	env := createTestEnv(t, svc, "sess-1")

	resp, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
		ToolCallID: "tc-1",
		ToolName:   "read_file",
	}, nil)
	if err != nil {
		t.Fatalf("ExecuteTool() error: %v", err)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(resp.Content))
	}
	if resp.SnapshotID != "snap-123" {
		t.Fatalf("SnapshotID = %q, want %q", resp.SnapshotID, "snap-123")
	}
}

func TestExecuteTool_WithProgressCallbacks(t *testing.T) {
	svc := newMockService()
	svc.streamMessages = []*api.ExecuteToolStreamMessage{
		{Progress: &api.ToolProgress{Content: "line 1", IsError: false}},
		{Progress: &api.ToolProgress{Content: "line 2", IsError: true}},
		{Response: &api.ExecuteToolResponse{
			SessionID:  "sess-1",
			ToolCallID: "tc-1",
			ToolName:   "bash",
			Content:    "done",
			ExitCode:   intPtr(0),
		}},
	}
	env := createTestEnv(t, svc, "sess-1")

	var progress []environment.ToolProgress
	resp, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
		ToolCallID: "tc-1",
		ToolName:   "bash",
		Params:     map[string]any{"cmd": "echo hello"},
	}, func(p environment.ToolProgress) {
		progress = append(progress, p)
	})
	if err != nil {
		t.Fatalf("ExecuteTool() error: %v", err)
	}
	if len(progress) != 2 {
		t.Fatalf("expected 2 progress callbacks, got %d", len(progress))
	}
	if progress[0].Content != "line 1" || progress[0].IsError {
		t.Fatalf("progress[0] = %+v, unexpected", progress[0])
	}
	if progress[1].Content != "line 2" || !progress[1].IsError {
		t.Fatalf("progress[1] = %+v, unexpected", progress[1])
	}
	if firstText(resp) != "done" {
		t.Fatalf("response = %q, want %q", firstText(resp), "done")
	}
}

func TestExecuteTool_NilProgressCallback(t *testing.T) {
	svc := newMockService()
	svc.streamMessages = []*api.ExecuteToolStreamMessage{
		{Progress: &api.ToolProgress{Content: "ignored"}},
		{Response: &api.ExecuteToolResponse{
			Content: "result",
		}},
	}
	env := createTestEnv(t, svc, "sess-1")

	// nil onProgress should not panic
	resp, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
		ToolName: "read_file",
	}, nil)
	if err != nil {
		t.Fatalf("ExecuteTool() error: %v", err)
	}
	if firstText(resp) != "result" {
		t.Fatalf("response = %q, want %q", firstText(resp), "result")
	}
}

func TestExecuteTool_StreamOpenError(t *testing.T) {
	svc := newMockService()
	svc.streamErr = fmt.Errorf("stream unavailable")
	env := createTestEnv(t, svc, "sess-1")

	_, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
		ToolName: "bash",
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "native sandbox: execute tool bash") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteTool_StreamRecvError(t *testing.T) {
	svc := newMockService()
	svc.streamMessages = nil
	svc.streamRecvErr = fmt.Errorf("connection reset")
	env := createTestEnv(t, svc, "sess-1")

	_, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
		ToolName: "read_file",
	}, nil)
	if err == nil {
		t.Fatal("expected error from stream recv")
	}
	if !strings.Contains(err.Error(), "native sandbox: stream recv") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteTool_ResourceSpecPropagation(t *testing.T) {
	svc := newMockService()
	svc.streamMessages = []*api.ExecuteToolStreamMessage{
		{Response: &api.ExecuteToolResponse{Content: "ok"}},
	}
	env := createTestEnv(t, svc, "sess-1")

	_, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
		ToolCallID: "tc-1",
		ToolName:   "bash",
		Params:     map[string]any{"cmd": "heavy"},
		Resources:  &tools.ResourceSpec{CPUs: 4, MemMB: 2048},
	}, nil)
	if err != nil {
		t.Fatalf("ExecuteTool() error: %v", err)
	}
	if svc.lastExecReq.Resources == nil {
		t.Fatal("Resources not propagated to RPC")
	}
	if svc.lastExecReq.Resources.CPUs != 4 {
		t.Fatalf("CPUs = %v, want 4", svc.lastExecReq.Resources.CPUs)
	}
	if svc.lastExecReq.Resources.MemMB != 2048 {
		t.Fatalf("MemMB = %d, want 2048", svc.lastExecReq.Resources.MemMB)
	}
}

func TestExecuteTool_FieldMapping(t *testing.T) {
	svc := newMockService()
	svc.streamMessages = []*api.ExecuteToolStreamMessage{
		{Response: &api.ExecuteToolResponse{Content: "ok"}},
	}
	env := createTestEnv(t, svc, "sess-1")

	_, err := env.ExecuteTool(context.Background(), environment.ToolRequest{
		ToolCallID: "tc-42",
		ToolName:   "write_file",
		Params:     map[string]any{"path": "x.txt", "content": "hello"},
	}, nil)
	if err != nil {
		t.Fatalf("ExecuteTool() error: %v", err)
	}
	if svc.lastExecReq.SessionID != "sess-1" {
		t.Fatalf("SessionID = %q, want %q", svc.lastExecReq.SessionID, "sess-1")
	}
	if svc.lastExecReq.ToolCallID != "tc-42" {
		t.Fatalf("ToolCallID = %q, want %q", svc.lastExecReq.ToolCallID, "tc-42")
	}
	if svc.lastExecReq.ToolName != "write_file" {
		t.Fatalf("ToolName = %q, want %q", svc.lastExecReq.ToolName, "write_file")
	}
}

// =====================================================================
// Snapshot / Rollback Tests
// =====================================================================

func TestCreateSnapshot_Success(t *testing.T) {
	svc := newMockService()
	env := createTestEnv(t, svc, "sess-1")

	snap, err := env.CreateSnapshot(context.Background(), "checkpoint-1")
	if err != nil {
		t.Fatalf("CreateSnapshot() error: %v", err)
	}
	if snap.ID != "snap-sess-1-checkpoint-1" {
		t.Fatalf("ID = %q, unexpected", snap.ID)
	}
	if snap.Name != "checkpoint-1" {
		t.Fatalf("Name = %q, want %q", snap.Name, "checkpoint-1")
	}
	if snap.SpaceUsed != 4096 {
		t.Fatalf("SpaceUsed = %d, want 4096", snap.SpaceUsed)
	}
	if svc.snapshotCalls != 1 {
		t.Fatalf("snapshotCalls = %d, want 1", svc.snapshotCalls)
	}
}

func TestCreateSnapshot_RPCError(t *testing.T) {
	svc := newMockService()
	svc.snapshotErr = fmt.Errorf("quota exceeded")
	env := createTestEnv(t, svc, "sess-1")

	_, err := env.CreateSnapshot(context.Background(), "snap-1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "native sandbox: create snapshot") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateSnapshot_NonZFSCapability(t *testing.T) {
	svc := newMockService()
	env := NewNativeSandboxEnvironment(svc, NativeSandboxConfig{
		StorageBackend:   StorageBackendLocalDisk,
		ContainerRuntime: ContainerRuntimeNone,
	}, slog.Default())
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "sess-1"}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if _, err := env.CreateSnapshot(context.Background(), "snap"); !errors.Is(err, environment.ErrCapabilityNotSupported) {
		t.Fatalf("CreateSnapshot error = %v, want ErrCapabilityNotSupported", err)
	}
}

func TestRollback_Success(t *testing.T) {
	svc := newMockService()
	env := createTestEnv(t, svc, "sess-1")

	err := env.Rollback(context.Background(), "snap-123")
	if err != nil {
		t.Fatalf("Rollback() error: %v", err)
	}
	if svc.rollbackCalls != 1 {
		t.Fatalf("rollbackCalls = %d, want 1", svc.rollbackCalls)
	}
}

func TestRollback_RPCError(t *testing.T) {
	svc := newMockService()
	svc.rollbackErr = fmt.Errorf("snapshot not found")
	env := createTestEnv(t, svc, "sess-1")

	err := env.Rollback(context.Background(), "snap-invalid")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "native sandbox: rollback") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRollback_NonZFSCapability(t *testing.T) {
	svc := newMockService()
	env := NewNativeSandboxEnvironment(svc, NativeSandboxConfig{
		StorageBackend:   StorageBackendLocalDisk,
		ContainerRuntime: ContainerRuntimeNone,
	}, slog.Default())
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: "sess-1"}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if err := env.Rollback(context.Background(), "snap"); !errors.Is(err, environment.ErrCapabilityNotSupported) {
		t.Fatalf("Rollback error = %v, want ErrCapabilityNotSupported", err)
	}
}

// =====================================================================
// Full Lifecycle Tests
// =====================================================================

func TestFullLifecycle(t *testing.T) {
	svc := newMockService()
	svc.streamMessages = []*api.ExecuteToolStreamMessage{
		{Response: &api.ExecuteToolResponse{Content: "tool result"}},
	}
	env := NewNativeSandboxEnvironment(svc, DefaultConfig(), slog.Default())
	ctx := context.Background()

	// Create
	if err := env.Create(ctx, environment.SessionConfig{SessionID: "lifecycle-1", BaseImage: "base@v1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := env.State(); got != environment.StateActive {
		t.Fatalf("State after Create = %q, want active", got)
	}

	// Execute
	resp, err := env.ExecuteTool(ctx, environment.ToolRequest{ToolName: "read_file"}, nil)
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if firstText(resp) != "tool result" {
		t.Fatalf("tool result = %q", firstText(resp))
	}

	// Snapshot
	snap, err := env.CreateSnapshot(ctx, "mid-point")
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	// Rollback
	if err := env.Rollback(ctx, snap.ID); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	// Pause / Resume
	if err := env.Pause(ctx); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if got := env.State(); got != environment.StatePaused {
		t.Fatalf("State after Pause = %q, want paused", got)
	}
	if err := env.Resume(ctx); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got := env.State(); got != environment.StateActive {
		t.Fatalf("State after Resume = %q, want active", got)
	}

	// Destroy
	if err := env.Destroy(ctx); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if got := env.State(); got != environment.StateDestroyed {
		t.Fatalf("State after Destroy = %q, want destroyed", got)
	}
}

// =====================================================================
// Interface Compliance
// =====================================================================

func TestInterfaceCompliance(t *testing.T) {
	var _ environment.ExecutionEnvironment = (*NativeSandboxEnvironment)(nil)
}

// =====================================================================
// Error Wrapping Tests
// =====================================================================

func TestErrorWrapping_PreservesOriginal(t *testing.T) {
	origErr := fmt.Errorf("original error")
	svc := newMockService()
	svc.createErr = origErr
	env := NewNativeSandboxEnvironment(svc, DefaultConfig(), slog.Default())

	err := env.Create(context.Background(), environment.SessionConfig{SessionID: "sess-1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, origErr) {
		t.Fatalf("error wrapping lost original: %v", err)
	}
}

// =====================================================================
// Helpers
// =====================================================================

func createTestEnv(t *testing.T, svc *mockSandboxService, sessionID string) *NativeSandboxEnvironment {
	t.Helper()
	env := NewNativeSandboxEnvironment(svc, DefaultConfig(), slog.Default())
	if err := env.Create(context.Background(), environment.SessionConfig{SessionID: sessionID}); err != nil {
		t.Fatalf("setup Create: %v", err)
	}
	return env
}

func firstText(resp *environment.ToolResponse) string {
	if resp == nil || len(resp.Content) == 0 {
		return ""
	}
	if tc, ok := resp.Content[0].(*ai.TextContent); ok {
		return tc.Text
	}
	return ""
}

func intPtr(v int) *int { return &v }
