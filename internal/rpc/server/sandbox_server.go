package server

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"h2-agent-runtime/internal/rpc"
	"h2-agent-runtime/internal/rpc/api"
	"h2-agent-runtime/internal/rpc/codec"
	"h2-agent-runtime/internal/sandbox"
)

type SandboxServer struct {
	host *sandbox.SandboxHostService

	mu          sync.Mutex
	idempotency map[string]idempotencyEntry
	ttl         time.Duration
	sweepEvery  time.Duration
	stopSweep   chan struct{}
	sweepClosed atomic.Bool
}

type idempotencyEntry struct {
	response *api.ExecuteToolResponse
	err      error
	expires  time.Time
}

func NewSandboxServer(host *sandbox.SandboxHostService) *SandboxServer {
	s := &SandboxServer{
		host:        host,
		idempotency: make(map[string]idempotencyEntry),
		ttl:         5 * time.Minute,
		sweepEvery:  time.Minute,
		stopSweep:   make(chan struct{}),
	}
	go s.sweepLoop()
	return s
}

func (s *SandboxServer) CreateSession(ctx context.Context, req *api.CreateSessionRequest) (*api.CreateSessionResponse, error) {
	info, err := s.host.CreateSession(ctx, codec.ToCreateSessionRequest(req))
	if err != nil {
		return nil, rpc.MapError(err)
	}
	return &api.CreateSessionResponse{Session: codec.FromSessionInfo(info)}, nil
}

func (s *SandboxServer) GetSession(ctx context.Context, req *api.GetSessionRequest) (*api.GetSessionResponse, error) {
	info, err := s.host.GetSession(ctx, req.SessionID)
	if err != nil {
		return nil, rpc.MapError(err)
	}
	return &api.GetSessionResponse{Session: codec.FromSessionInfo(info)}, nil
}

func (s *SandboxServer) PauseSession(ctx context.Context, req *api.PauseSessionRequest) (*api.PauseSessionResponse, error) {
	if err := s.host.PauseSession(ctx, req.SessionID); err != nil {
		return nil, rpc.MapError(err)
	}
	return &api.PauseSessionResponse{}, nil
}

func (s *SandboxServer) ResumeSession(ctx context.Context, req *api.ResumeSessionRequest) (*api.ResumeSessionResponse, error) {
	if err := s.host.ResumeSession(ctx, req.SessionID); err != nil {
		return nil, rpc.MapError(err)
	}
	return &api.ResumeSessionResponse{}, nil
}

func (s *SandboxServer) DestroySession(ctx context.Context, req *api.DestroySessionRequest) (*api.DestroySessionResponse, error) {
	if err := s.host.DestroySession(ctx, req.SessionID); err != nil {
		return nil, rpc.MapError(err)
	}
	return &api.DestroySessionResponse{}, nil
}

func (s *SandboxServer) ExecuteTool(ctx context.Context, req *api.ExecuteToolRequest) (*api.ExecuteToolResponse, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	if req.ToolCallID == "" {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "tool_call_id is required", nil)
	}
	key := req.SessionID + ":" + req.ToolCallID
	if cached, ok := s.getCached(key); ok {
		return cached.response, cached.err
	}

	resp, err := s.host.ExecuteTool(ctx, codec.ToExecuteToolRequest(req))
	if err != nil {
		rpcErr := rpc.MapError(err)
		s.setCached(key, nil, rpcErr)
		return nil, rpcErr
	}
	apiResp := codec.FromExecuteToolResponse(resp, req)
	s.setCached(key, apiResp, nil)
	return apiResp, nil
}

func (s *SandboxServer) ExecuteToolStream(ctx context.Context, req *api.ExecuteToolRequest) (api.ExecuteToolStreamReceiver, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	if req.ToolCallID == "" {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "tool_call_id is required", nil)
	}
	key := req.SessionID + ":" + req.ToolCallID
	if cached, ok := s.getCached(key); ok {
		if cached.err != nil {
			return nil, cached.err
		}
		return api.NewExecuteToolStream(&api.ExecuteToolStreamMessage{Response: cached.response}), nil
	}

	ch := make(chan *api.ExecuteToolStreamMessage, 32)
	errCh := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(ch)
		defer close(errCh)
		defer close(done)
		hostReq := codec.ToExecuteToolRequest(req)
		hostReq.OnProgress = func(content string, isError bool) {
			if content == "" {
				return
			}
			select {
			case <-ctx.Done():
				return
			case ch <- &api.ExecuteToolStreamMessage{Progress: &api.ToolProgress{Content: content, IsError: isError}}:
			}
		}

		resp, err := s.host.ExecuteTool(ctx, hostReq)
		if err != nil {
			rpcErr := rpc.MapError(err)
			s.setCached(key, nil, rpcErr)
			errCh <- rpcErr
			return
		}
		apiResp := codec.FromExecuteToolResponse(resp, req)
		s.setCached(key, apiResp, nil)
		ch <- &api.ExecuteToolStreamMessage{Response: apiResp}
	}()

	return api.NewExecuteToolStreamChannelWithErr(ch, errCh, func() error {
		select {
		case <-done:
		case <-ctx.Done():
		}
		return nil
	}), nil
}

func (s *SandboxServer) TurnComplete(ctx context.Context, req *api.TurnCompleteRequest) (*api.TurnCompleteResponse, error) {
	result, err := s.host.TurnComplete(ctx, req.SessionID)
	if err != nil {
		return nil, rpc.MapError(err)
	}
	return &api.TurnCompleteResponse{SnapshotID: result.SnapshotID, TurnNumber: result.TurnNumber, SpaceUsed: result.SpaceUsed}, nil
}

func (s *SandboxServer) CreateSnapshot(ctx context.Context, req *api.CreateSnapshotRequest) (*api.CreateSnapshotResponse, error) {
	result, err := s.host.CreateSnapshot(ctx, req.SessionID, req.Name)
	if err != nil {
		return nil, rpc.MapError(err)
	}
	return codec.FromSnapshotResult(result), nil
}

func (s *SandboxServer) RollbackSession(ctx context.Context, req *api.RollbackSessionRequest) (*api.RollbackSessionResponse, error) {
	if err := s.host.RollbackSession(ctx, req.SessionID, req.SnapshotID); err != nil {
		return nil, rpc.MapError(err)
	}
	return &api.RollbackSessionResponse{}, nil
}

func (s *SandboxServer) ListSnapshots(ctx context.Context, req *api.ListSnapshotsRequest) (*api.ListSnapshotsResponse, error) {
	items, err := s.host.ListSnapshots(ctx, req.SessionID)
	if err != nil {
		return nil, rpc.MapError(err)
	}
	return &api.ListSnapshotsResponse{Snapshots: codec.FromSnapshots(items)}, nil
}

func (s *SandboxServer) HealthCheck(ctx context.Context, _ *api.HealthCheckRequest) (*api.HealthCheckResponse, error) {
	health, err := s.host.HealthCheck(ctx)
	if err != nil {
		return nil, rpc.MapError(err)
	}
	return &api.HealthCheckResponse{
		Status:       health.Status,
		PoolState:    string(health.PoolState),
		SessionCount: health.SessionCount,
		ActiveTools:  health.ActiveTools,
		Uptime:       health.Uptime,
		Errors:       append([]string(nil), health.Errors...),
	}, nil
}

func (s *SandboxServer) getCached(key string) (idempotencyEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.idempotency[key]
	if !ok {
		return idempotencyEntry{}, false
	}
	if time.Now().After(entry.expires) {
		delete(s.idempotency, key)
		return idempotencyEntry{}, false
	}
	return entry, true
}

func (s *SandboxServer) setCached(key string, resp *api.ExecuteToolResponse, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idempotency[key] = idempotencyEntry{response: resp, err: err, expires: time.Now().Add(s.ttl)}
}

func (s *SandboxServer) sweepLoop() {
	ticker := time.NewTicker(s.sweepEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.evictExpired()
		case <-s.stopSweep:
			return
		}
	}
}

func (s *SandboxServer) evictExpired() {
	now := time.Now()
	s.mu.Lock()
	for key, entry := range s.idempotency {
		if now.After(entry.expires) {
			delete(s.idempotency, key)
		}
	}
	s.mu.Unlock()
}

func (s *SandboxServer) Close() error {
	if s.sweepClosed.CompareAndSwap(false, true) {
		close(s.stopSweep)
	}
	return nil
}

var _ api.SandboxService = (*SandboxServer)(nil)

func (s *SandboxServer) String() string {
	return fmt.Sprintf("SandboxServer(ttl=%s)", s.ttl)
}

var _ interface{ Close() error } = (*SandboxServer)(nil)
