package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"h2-agent-runtime/internal/sandbox/gvisor"
	"h2-agent-runtime/internal/sandbox/zfs"
)

type SandboxHostService struct {
	config     ServiceConfig
	zfs        zfs.ZFSManager
	gvisor     gvisor.GVisorManager
	sessions   sync.Map // map[string]*Session
	sessionsMu sync.Mutex
	logger     *slog.Logger
	started    time.Time
	metrics    *serviceMetrics
}

type serviceMetrics struct {
	sessionsCreated   atomic.Int64
	sessionsPaused    atomic.Int64
	sessionsResumed   atomic.Int64
	sessionsDestroyed atomic.Int64
	toolExecutions    atomic.Int64
	snapshotsTaken    atomic.Int64
	rollbacks         atomic.Int64
}

type CreateSessionRequest struct {
	BaseSnapshot string
	SessionID    string
	Quota        int64
	Labels       map[string]string
}

type SessionInfo struct {
	ID         string
	State      SessionState
	Mountpoint string
	TurnCount  int
	SnapCount  int
	Created    time.Time
	Labels     map[string]string
	SpaceUsed  int64
}

type ExecuteToolRequest struct {
	SessionID  string
	ToolName   string
	ToolCallID string
	Params     map[string]any
	Resources  *gvisor.ResourceSpec
}

type ExecuteToolResponse struct {
	Content    string
	ExitCode   *int
	SnapshotID string
	Duration   time.Duration
	Tier       int
}

type SnapshotResult struct {
	SnapshotID string
	TurnNumber int
	SpaceUsed  int64
}

type HealthStatus struct {
	Status       string
	PoolState    zfs.PoolState
	PoolSpace    zfs.PoolSpace
	SessionCount int
	ActiveTools  int
	Uptime       time.Duration
	Errors       []string
}

func NewSandboxHostService(cfg ServiceConfig, z zfs.ZFSManager, g gvisor.GVisorManager, logger *slog.Logger) *SandboxHostService {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.SnapshotPrefix == "" {
		cfg = mergeDefaultConfig(cfg)
	}
	return &SandboxHostService{config: cfg, zfs: z, gvisor: g, logger: logger, started: time.Now(), metrics: &serviceMetrics{}}
}

func mergeDefaultConfig(cfg ServiceConfig) ServiceConfig {
	d := DefaultServiceConfig()
	if cfg.SnapshotPrefix == "" {
		cfg.SnapshotPrefix = d.SnapshotPrefix
	}
	if cfg.ToolTimeout == 0 {
		cfg.ToolTimeout = d.ToolTimeout
	}
	if cfg.PoolSpaceWarnThreshold == 0 {
		cfg.PoolSpaceWarnThreshold = d.PoolSpaceWarnThreshold
	}
	if cfg.PoolSpaceCritThreshold == 0 {
		cfg.PoolSpaceCritThreshold = d.PoolSpaceCritThreshold
	}
	if cfg.HealthCheckInterval == 0 {
		cfg.HealthCheckInterval = d.HealthCheckInterval
	}
	if cfg.PauseDrainTimeout == 0 {
		cfg.PauseDrainTimeout = d.PauseDrainTimeout
	}
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = d.ShutdownTimeout
	}
	return cfg
}

func (svc *SandboxHostService) sessionCount() int {
	count := 0
	svc.sessions.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

func (svc *SandboxHostService) CreateSession(ctx context.Context, req CreateSessionRequest) (*SessionInfo, error) {
	if req.BaseSnapshot == "" {
		return nil, fmt.Errorf("base snapshot is required")
	}
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = generateSessionID()
	}

	svc.sessionsMu.Lock()
	if svc.config.MaxSessions > 0 && svc.sessionCount() >= svc.config.MaxSessions {
		svc.sessionsMu.Unlock()
		return nil, ErrMaxSessionsReached
	}
	if _, loaded := svc.sessions.Load(sessionID); loaded {
		svc.sessionsMu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrSessionExists, sessionID)
	}
	svc.sessions.Store(sessionID, &Session{id: sessionID, state: SessionCreating})
	svc.sessionsMu.Unlock()

	dataset := svc.config.SessionsDataset + "/" + sessionID
	if err := svc.zfs.CloneFromSnapshot(ctx, req.BaseSnapshot, dataset); err != nil {
		svc.sessions.Delete(sessionID)
		return nil, fmt.Errorf("clone base snapshot: %w", err)
	}

	quota := req.Quota
	if quota == 0 {
		quota = svc.config.DefaultSessionQuota
	}
	if quota > 0 {
		if err := svc.zfs.SetProperty(ctx, dataset, "quota", fmt.Sprintf("%d", quota)); err != nil {
			_ = svc.zfs.DestroyDataset(ctx, dataset, zfs.DestroyOptions{Recursive: true, Force: true})
			svc.sessions.Delete(sessionID)
			return nil, fmt.Errorf("set quota: %w", err)
		}
	}
	mountpoint, err := svc.zfs.GetMountpoint(ctx, dataset)
	if err != nil {
		_ = svc.zfs.DestroyDataset(ctx, dataset, zfs.DestroyOptions{Recursive: true, Force: true})
		svc.sessions.Delete(sessionID)
		return nil, fmt.Errorf("get mountpoint: %w", err)
	}

	sess := &Session{
		id:         sessionID,
		state:      SessionActive,
		dataset:    dataset,
		mountpoint: mountpoint,
		created:    time.Now(),
		labels:     copyLabels(req.Labels),
	}
	svc.sessions.Store(sessionID, sess)
	svc.metrics.sessionsCreated.Add(1)
	info := sess.Info()
	return &info, nil
}

func (s *Session) Info() SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return SessionInfo{
		ID:         s.id,
		State:      s.state,
		Mountpoint: s.mountpoint,
		TurnCount:  s.turnCount,
		SnapCount:  len(s.snapshots),
		Created:    s.created,
		Labels:     copyLabels(s.labels),
	}
}

func copyLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (svc *SandboxHostService) getSession(sessionID string) (*Session, error) {
	v, ok := svc.sessions.Load(sessionID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}
	s, ok := v.(*Session)
	if !ok {
		return nil, fmt.Errorf("invalid session type")
	}
	return s, nil
}

func (svc *SandboxHostService) getActiveSession(sessionID string) (*Session, error) {
	s, err := svc.getSession(sessionID)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch s.state {
	case SessionActive:
		return s, nil
	case SessionPaused:
		return nil, ErrSessionPaused
	case SessionDestroying:
		return nil, ErrSessionDestroying
	default:
		return nil, fmt.Errorf("%w: %s", ErrInvalidState, s.state)
	}
}

func (svc *SandboxHostService) PauseSession(ctx context.Context, sessionID string) error {
	sess, err := svc.getActiveSession(sessionID)
	if err != nil {
		return err
	}
	timeout := svc.config.PauseDrainTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for sess.activeTools.Load() > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("%w: timed out waiting for %d in-flight tools", ErrToolsInFlight, sess.activeTools.Load())
		case <-time.After(20 * time.Millisecond):
		}
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.state != SessionActive {
		return fmt.Errorf("%w: session is %s, not active", ErrInvalidState, sess.state)
	}
	sess.state = SessionPaused
	svc.metrics.sessionsPaused.Add(1)
	return nil
}

func (svc *SandboxHostService) ResumeSession(_ context.Context, sessionID string) error {
	sess, err := svc.getSession(sessionID)
	if err != nil {
		return err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.state != SessionPaused {
		return fmt.Errorf("%w: session is %s, not paused", ErrInvalidState, sess.state)
	}
	sess.state = SessionActive
	svc.metrics.sessionsResumed.Add(1)
	return nil
}

func (svc *SandboxHostService) DestroySession(ctx context.Context, sessionID string) error {
	sess, err := svc.getSession(sessionID)
	if err != nil {
		return err
	}
	sess.mu.Lock()
	sess.state = SessionDestroying
	sess.mu.Unlock()

	timeout := svc.config.ShutdownTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for sess.activeTools.Load() > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			goto destroy
		case <-time.After(20 * time.Millisecond):
		}
	}

destroy:
	_ = svc.zfs.DestroyDataset(ctx, sess.dataset, zfs.DestroyOptions{Recursive: true, Force: true})
	svc.sessions.Delete(sessionID)
	svc.metrics.sessionsDestroyed.Add(1)
	return nil
}

func (svc *SandboxHostService) GetSession(_ context.Context, sessionID string) (*SessionInfo, error) {
	s, err := svc.getSession(sessionID)
	if err != nil {
		return nil, err
	}
	info := s.Info()
	return &info, nil
}

func (svc *SandboxHostService) ListSessions(_ context.Context) ([]SessionInfo, error) {
	list := make([]SessionInfo, 0)
	svc.sessions.Range(func(_, v any) bool {
		if s, ok := v.(*Session); ok {
			list = append(list, s.Info())
		}
		return true
	})
	return list, nil
}

func (svc *SandboxHostService) HealthCheck(ctx context.Context) (*HealthStatus, error) {
	ps, err := svc.zfs.PoolSpace(ctx, svc.config.PoolName)
	if err != nil {
		return &HealthStatus{
			Status:       "unhealthy",
			SessionCount: svc.sessionCount(),
			ActiveTools:  svc.totalActiveTools(),
			Uptime:       time.Since(svc.started),
			Errors:       []string{err.Error()},
		}, nil
	}
	st, err := svc.zfs.PoolStatus(ctx, svc.config.PoolName)
	if err != nil {
		return &HealthStatus{
			Status:       "unhealthy",
			PoolSpace:    *ps,
			SessionCount: svc.sessionCount(),
			ActiveTools:  svc.totalActiveTools(),
			Uptime:       time.Since(svc.started),
			Errors:       []string{err.Error()},
		}, nil
	}
	status := "healthy"
	if ps.Capacity >= svc.config.PoolSpaceCritThreshold || st.State == zfs.PoolFaulted {
		status = "unhealthy"
	} else if ps.Capacity >= svc.config.PoolSpaceWarnThreshold || st.State == zfs.PoolDegraded {
		status = "degraded"
	}
	return &HealthStatus{
		Status:       status,
		PoolState:    st.State,
		PoolSpace:    *ps,
		SessionCount: svc.sessionCount(),
		ActiveTools:  svc.totalActiveTools(),
		Uptime:       time.Since(svc.started),
	}, nil
}

func (svc *SandboxHostService) Start(ctx context.Context) {
	interval := svc.config.HealthCheckInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				health, err := svc.HealthCheck(ctx)
				if err != nil {
					svc.logger.WarnContext(ctx, "health check failed", "error", err)
					continue
				}
				if health.Status != "healthy" {
					svc.logger.WarnContext(ctx, "sandbox health degraded",
						"status", health.Status,
						"pool_state", health.PoolState,
						"capacity", health.PoolSpace.Capacity,
						"errors", health.Errors,
					)
				}
			}
		}
	}()
}

func (svc *SandboxHostService) totalActiveTools() int {
	total := 0
	svc.sessions.Range(func(_, v any) bool {
		if s, ok := v.(*Session); ok {
			total += int(s.activeTools.Load())
		}
		return true
	})
	return total
}

func (svc *SandboxHostService) Shutdown(ctx context.Context) error {
	list, _ := svc.ListSessions(ctx)
	for _, s := range list {
		_ = svc.DestroySession(ctx, s.ID)
	}
	if svc.gvisor != nil {
		return svc.gvisor.Close()
	}
	return nil
}
