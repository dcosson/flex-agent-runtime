package native

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"h2-agent-runtime/internal/ai"
	"h2-agent-runtime/internal/rpc/api"
	"h2-agent-runtime/internal/rpc/codec"
	"h2-agent-runtime/internal/sandbox/environment"
)

// NativeSandboxEnvironment adapts the ConnectRPC SandboxClient to the
// ExecutionEnvironment interface. Wraps api.SandboxService (the RPC client
// interface) and translates between environment-level types and RPC-level types.
//
// Per §3.4 concurrency contract: NativeSandboxEnvironment delegates all state
// management to the server-side SandboxHostService via RPC, so it does not
// need internal locking. The sessionID is set during Create() and read-only
// after that. A destroyed atomic.Bool tracks local Destroy completion to avoid
// querying a deleted server-side session.
type NativeSandboxEnvironment struct {
	service   api.SandboxService
	logger    *slog.Logger
	config    NativeSandboxConfig
	sessionID string // set after Create(), read-only thereafter
	destroyed atomic.Bool
}

func NewNativeSandboxEnvironment(service api.SandboxService, logger *slog.Logger, cfg ...NativeSandboxConfig) *NativeSandboxEnvironment {
	config := DefaultConfig()
	if len(cfg) > 0 {
		config = cfg[0]
	}
	return &NativeSandboxEnvironment{service: service, logger: logger, config: config}
}

func (e *NativeSandboxEnvironment) Create(ctx context.Context, config environment.SessionConfig) error {
	if config.SessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	rpcResp, err := e.service.CreateSession(ctx, &api.CreateSessionRequest{
		BaseSnapshot: config.BaseImage,
		SessionID:    config.SessionID,
		Labels:       config.Labels,
	})
	if err != nil {
		return fmt.Errorf("native sandbox: create: %w", err)
	}
	if e.config.StorageBackend == StorageBackendZFS && !rpcResp.ServerCapabilities.Snapshots {
		return fmt.Errorf("native sandbox: capability mismatch: configured zfs backend but server has snapshots=false")
	}
	if e.config.ContainerRuntime == ContainerRuntimeGVisor && !rpcResp.ServerCapabilities.TierRouting {
		return fmt.Errorf("native sandbox: capability mismatch: configured gvisor runtime but server has tier_routing=false")
	}
	e.sessionID = rpcResp.Session.ID
	return nil
}

func (e *NativeSandboxEnvironment) Pause(ctx context.Context) error {
	_, err := e.service.PauseSession(ctx, &api.PauseSessionRequest{SessionID: e.sessionID})
	if err != nil {
		return fmt.Errorf("native sandbox: pause: %w", err)
	}
	return nil
}

func (e *NativeSandboxEnvironment) Resume(ctx context.Context) error {
	_, err := e.service.ResumeSession(ctx, &api.ResumeSessionRequest{SessionID: e.sessionID})
	if err != nil {
		return fmt.Errorf("native sandbox: resume: %w", err)
	}
	return nil
}

func (e *NativeSandboxEnvironment) Destroy(ctx context.Context) error {
	if e.destroyed.Load() {
		return nil
	}
	_, err := e.service.DestroySession(ctx, &api.DestroySessionRequest{SessionID: e.sessionID})
	if err != nil {
		return fmt.Errorf("native sandbox: destroy: %w", err)
	}
	e.destroyed.Store(true)
	return nil
}

func (e *NativeSandboxEnvironment) ExecuteTool(ctx context.Context, req environment.ToolRequest, onProgress func(environment.ToolProgress)) (*environment.ToolResponse, error) {
	rpcReq := &api.ExecuteToolRequest{
		SessionID:  e.sessionID,
		ToolCallID: req.ToolCallID,
		ToolName:   req.ToolName,
		Params:     req.Params,
	}
	if req.Resources != nil {
		rpcReq.Resources = &api.ResourceSpec{
			CPUs:  float64(req.Resources.CPUs),
			MemMB: req.Resources.MemMB,
		}
	}

	stream, err := e.service.ExecuteToolStream(ctx, rpcReq)
	if err != nil {
		return nil, fmt.Errorf("native sandbox: execute tool %s: %w", req.ToolName, err)
	}
	defer stream.Close()

	var final *api.ExecuteToolResponse
	for {
		msg, recvErr := stream.Recv()
		if recvErr != nil {
			return nil, fmt.Errorf("native sandbox: stream recv: %w", recvErr)
		}
		if msg == nil {
			continue
		}
		if msg.Progress != nil && onProgress != nil {
			onProgress(environment.ToolProgress{Content: msg.Progress.Content, IsError: msg.Progress.IsError})
		}
		if msg.Response != nil {
			final = msg.Response
			break
		}
	}
	if final == nil {
		return nil, fmt.Errorf("native sandbox: missing final response for tool %s", req.ToolName)
	}

	return &environment.ToolResponse{
		Content:    decodeResponseContent(final),
		SnapshotID: final.SnapshotID,
		ExitCode:   final.ExitCode,
	}, nil
}

// stateQueryTimeout bounds the RPC call in State() to prevent indefinite
// blocking on network issues. State() uses context.Background() because the
// interface signature has no context parameter (plan §3.4); this timeout
// ensures we fail fast rather than hanging the agent loop.
const stateQueryTimeout = 5 * time.Second

// State queries the server for the current session state. After Destroy(),
// returns StateDestroyed without querying (the session is deleted server-side).
func (e *NativeSandboxEnvironment) State() environment.SessionState {
	if e.destroyed.Load() {
		return environment.StateDestroyed
	}
	if e.sessionID == "" {
		return environment.StateCreating
	}
	ctx, cancel := context.WithTimeout(context.Background(), stateQueryTimeout)
	defer cancel()
	resp, err := e.service.GetSession(ctx, &api.GetSessionRequest{SessionID: e.sessionID})
	if err != nil {
		e.logger.Warn("native sandbox: state query failed", "session_id", e.sessionID, "error", err)
		return environment.StateFailed
	}
	return environment.SessionState(resp.Session.State)
}

func (e *NativeSandboxEnvironment) Capabilities() environment.Capabilities {
	return environment.Capabilities{
		Snapshots:         e.config.StorageBackend == StorageBackendZFS,
		Rollback:          e.config.StorageBackend == StorageBackendZFS,
		Pause:             true,
		TierRouting:       e.config.ContainerRuntime == ContainerRuntimeGVisor,
		StreamingProgress: true,
	}
}

func (e *NativeSandboxEnvironment) CreateSnapshot(ctx context.Context, name string) (*environment.SnapshotInfo, error) {
	if e.config.StorageBackend != StorageBackendZFS {
		return nil, environment.ErrCapabilityNotSupported
	}
	resp, err := e.service.CreateSnapshot(ctx, &api.CreateSnapshotRequest{
		SessionID: e.sessionID,
		Name:      name,
	})
	if err != nil {
		return nil, fmt.Errorf("native sandbox: create snapshot: %w", err)
	}
	return &environment.SnapshotInfo{
		ID:        resp.SnapshotID,
		Name:      name,
		CreatedAt: time.Now(),
		SpaceUsed: resp.SpaceUsed,
	}, nil
}

func (e *NativeSandboxEnvironment) Rollback(ctx context.Context, snapshotID string) error {
	if e.config.StorageBackend != StorageBackendZFS {
		return environment.ErrCapabilityNotSupported
	}
	_, err := e.service.RollbackSession(ctx, &api.RollbackSessionRequest{
		SessionID:  e.sessionID,
		SnapshotID: snapshotID,
	})
	if err != nil {
		return fmt.Errorf("native sandbox: rollback: %w", err)
	}
	return nil
}

// decodeResponseContent converts API-level content blocks to ai.ContentBlock.
func decodeResponseContent(resp *api.ExecuteToolResponse) []ai.ContentBlock {
	if resp == nil {
		return nil
	}
	if len(resp.ContentBlocks) > 0 {
		return codec.FromAPIContentBlocks(resp.ContentBlocks)
	}
	return []ai.ContentBlock{&ai.TextContent{Text: resp.Content}}
}
