package harness

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"flex-agent-runtime/internal/rpc"
	"flex-agent-runtime/internal/rpc/api"
	"flex-agent-runtime/internal/sandbox"
	"flex-agent-runtime/internal/tools"
)

const (
	memoryStateCreated    = "created"
	memoryStateActive     = "active"
	memoryStatePaused     = "paused"
	memoryStateDestroying = "destroying"
	memoryStateDestroyed  = "destroyed"
	memoryStateFailed     = "failed"
)

type ToolRoute struct {
	SessionID  string
	ToolCallID string
	ToolName   string
	Tier       int
	SnapshotID string
}

type MemorySandboxService struct {
	mu            sync.RWMutex
	sessions      map[string]*memorySession
	baseSnapshots map[string]map[string][]byte
	nextSession   int64
	nextSnapshot  int64
	routes        []ToolRoute
	executeCalls  atomic.Int64
	started       time.Time
}

type memorySession struct {
	id         string
	state      string
	created    time.Time
	labels     map[string]string
	mountpoint string

	mu        sync.RWMutex
	files     map[string][]byte
	snapshots []memorySnapshot
	turnCount int
	snapCount int
}

type memorySnapshot struct {
	id      string
	name    string
	created time.Time
	files   map[string][]byte
}

func NewMemorySandboxService() *MemorySandboxService {
	return &MemorySandboxService{
		sessions:      make(map[string]*memorySession),
		baseSnapshots: make(map[string]map[string][]byte),
		started:       time.Now(),
	}
}

func (m *MemorySandboxService) SeedBaseSnapshot(name string, files map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	converted := make(map[string][]byte, len(files))
	for k, v := range files {
		converted[normalizePath(k)] = []byte(v)
	}
	m.baseSnapshots[name] = converted
}

func (m *MemorySandboxService) Routes() []ToolRoute {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ToolRoute, len(m.routes))
	copy(out, m.routes)
	return out
}

func (m *MemorySandboxService) ExecuteCalls() int64 {
	return m.executeCalls.Load()
}

func (m *MemorySandboxService) ReadSessionFile(sessionID, filePath string) (string, bool) {
	sess, err := m.getSession(sessionID)
	if err != nil {
		return "", false
	}
	sess.mu.RLock()
	defer sess.mu.RUnlock()
	data, ok := sess.files[normalizePath(filePath)]
	if !ok {
		return "", false
	}
	return string(data), true
}

func (m *MemorySandboxService) CreateSession(_ context.Context, req *api.CreateSessionRequest) (*api.CreateSessionResponse, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	if req.BaseSnapshot == "" {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "base_snapshot is required", nil)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	base, ok := m.baseSnapshots[req.BaseSnapshot]
	if !ok {
		return nil, rpc.NewRPCError(rpc.CodeNotFound, "base snapshot not found", sandbox.ErrSessionNotFound)
	}

	sessionID := req.SessionID
	if sessionID == "" {
		n := atomic.AddInt64(&m.nextSession, 1)
		sessionID = fmt.Sprintf("mem-%d", n)
	}
	if _, exists := m.sessions[sessionID]; exists {
		return nil, rpc.NewRPCError(rpc.CodeAlreadyExists, "session already exists", sandbox.ErrSessionExists)
	}

	sess := &memorySession{
		id:         sessionID,
		state:      memoryStateCreated,
		created:    time.Now(),
		labels:     cloneLabels(req.Labels),
		mountpoint: filepath.ToSlash(path.Join("/memory", sessionID)),
		files:      cloneFileMap(base),
	}
	sess.state = memoryStateActive
	m.sessions[sessionID] = sess

	return &api.CreateSessionResponse{
		Session: &api.Session{
			ID:         sessionID,
			State:      sess.state,
			Mountpoint: sess.mountpoint,
			TurnCount:  sess.turnCount,
			SnapCount:  sess.snapCount,
			Created:    sess.created,
			Labels:     cloneLabels(sess.labels),
		},
		ServerCapabilities: api.Capabilities{
			Snapshots:         true,
			Rollback:          true,
			Pause:             true,
			TierRouting:       true,
			StreamingProgress: true,
		},
	}, nil
}

func (m *MemorySandboxService) GetSession(_ context.Context, req *api.GetSessionRequest) (*api.GetSessionResponse, error) {
	if req == nil || req.SessionID == "" {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "session_id is required", nil)
	}
	sess, err := m.getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	sess.mu.RLock()
	defer sess.mu.RUnlock()
	return &api.GetSessionResponse{Session: &api.Session{
		ID:         sess.id,
		State:      sess.state,
		Mountpoint: sess.mountpoint,
		TurnCount:  sess.turnCount,
		SnapCount:  sess.snapCount,
		Created:    sess.created,
		Labels:     cloneLabels(sess.labels),
	}}, nil
}

func (m *MemorySandboxService) PauseSession(_ context.Context, req *api.PauseSessionRequest) (*api.PauseSessionResponse, error) {
	if req == nil || req.SessionID == "" {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "session_id is required", nil)
	}
	sess, err := m.getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.state != memoryStateActive {
		return nil, rpc.NewRPCError(rpc.CodeFailedPrecondition, fmt.Sprintf("invalid state: %s", sess.state), sandbox.ErrInvalidState)
	}
	sess.state = memoryStatePaused
	return &api.PauseSessionResponse{}, nil
}

func (m *MemorySandboxService) ResumeSession(_ context.Context, req *api.ResumeSessionRequest) (*api.ResumeSessionResponse, error) {
	if req == nil || req.SessionID == "" {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "session_id is required", nil)
	}
	sess, err := m.getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.state != memoryStatePaused {
		return nil, rpc.NewRPCError(rpc.CodeFailedPrecondition, fmt.Sprintf("invalid state: %s", sess.state), sandbox.ErrInvalidState)
	}
	sess.state = memoryStateActive
	return &api.ResumeSessionResponse{}, nil
}

func (m *MemorySandboxService) DestroySession(_ context.Context, req *api.DestroySessionRequest) (*api.DestroySessionResponse, error) {
	if req == nil || req.SessionID == "" {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "session_id is required", nil)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[req.SessionID]
	if !ok {
		return nil, rpc.NewRPCError(rpc.CodeNotFound, "session not found", sandbox.ErrSessionNotFound)
	}
	sess.mu.Lock()
	// Preserve the real host FSM shape; this transition is intentionally brief
	// in the in-memory fake before the session is removed from the registry.
	sess.state = memoryStateDestroying
	sess.state = memoryStateDestroyed
	sess.mu.Unlock()
	delete(m.sessions, req.SessionID)
	return &api.DestroySessionResponse{}, nil
}

func (m *MemorySandboxService) ExecuteTool(ctx context.Context, req *api.ExecuteToolRequest) (*api.ExecuteToolResponse, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	return m.executeTool(ctx, req, nil)
}

func (m *MemorySandboxService) ExecuteToolStream(ctx context.Context, req *api.ExecuteToolRequest) (api.ExecuteToolStreamReceiver, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	var progress []*api.ToolProgress
	resp, err := m.executeTool(ctx, req, func(chunk string, isError bool) {
		if chunk == "" {
			return
		}
		progress = append(progress, &api.ToolProgress{Content: chunk, IsError: isError})
	})
	if err != nil {
		return nil, err
	}
	messages := make([]*api.ExecuteToolStreamMessage, 0, len(progress)+1)
	for _, p := range progress {
		messages = append(messages, &api.ExecuteToolStreamMessage{Progress: p})
	}
	messages = append(messages, &api.ExecuteToolStreamMessage{Response: resp})
	return api.NewExecuteToolStream(messages...), nil
}

func (m *MemorySandboxService) TurnComplete(_ context.Context, req *api.TurnCompleteRequest) (*api.TurnCompleteResponse, error) {
	if req == nil || req.SessionID == "" {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "session_id is required", nil)
	}
	sess, err := m.getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.state != memoryStateActive {
		return nil, rpc.NewRPCError(rpc.CodeFailedPrecondition, fmt.Sprintf("invalid state: %s", sess.state), sandbox.ErrInvalidState)
	}
	sess.turnCount++
	snapID := fmt.Sprintf("turn-%04d", sess.turnCount)
	sess.snapshots = append(sess.snapshots, memorySnapshot{
		id:      snapID,
		name:    snapID,
		created: time.Now(),
		files:   cloneFileMap(sess.files),
	})
	sess.snapCount = len(sess.snapshots)
	return &api.TurnCompleteResponse{SnapshotID: snapID, TurnNumber: sess.turnCount}, nil
}

func (m *MemorySandboxService) CreateSnapshot(_ context.Context, req *api.CreateSnapshotRequest) (*api.CreateSnapshotResponse, error) {
	if req == nil || req.SessionID == "" {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "session_id is required", nil)
	}
	sess, err := m.getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.state != memoryStateActive && sess.state != memoryStatePaused {
		return nil, rpc.NewRPCError(rpc.CodeFailedPrecondition, fmt.Sprintf("invalid state: %s", sess.state), sandbox.ErrInvalidState)
	}
	snapID := req.Name
	if snapID == "" {
		n := atomic.AddInt64(&m.nextSnapshot, 1)
		snapID = fmt.Sprintf("snap-%04d", n)
	}
	sess.snapshots = append(sess.snapshots, memorySnapshot{
		id:      snapID,
		name:    snapID,
		created: time.Now(),
		files:   cloneFileMap(sess.files),
	})
	sess.snapCount = len(sess.snapshots)
	return &api.CreateSnapshotResponse{SnapshotID: snapID, TurnNumber: sess.turnCount}, nil
}

func (m *MemorySandboxService) RollbackSession(_ context.Context, req *api.RollbackSessionRequest) (*api.RollbackSessionResponse, error) {
	if req == nil || req.SessionID == "" || req.SnapshotID == "" {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "session_id and snapshot_id are required", nil)
	}
	sess, err := m.getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.state != memoryStateActive {
		return nil, rpc.NewRPCError(rpc.CodeFailedPrecondition, fmt.Sprintf("invalid state: %s", sess.state), sandbox.ErrInvalidState)
	}
	target := -1
	for i, snap := range sess.snapshots {
		if snap.id == req.SnapshotID {
			target = i
			break
		}
	}
	if target < 0 {
		return nil, rpc.NewRPCError(rpc.CodeNotFound, "snapshot not found", sandbox.ErrSessionNotFound)
	}
	sess.files = cloneFileMap(sess.snapshots[target].files)
	sess.snapshots = append([]memorySnapshot(nil), sess.snapshots[:target+1]...)
	sess.snapCount = len(sess.snapshots)
	return &api.RollbackSessionResponse{}, nil
}

func (m *MemorySandboxService) ListSnapshots(_ context.Context, req *api.ListSnapshotsRequest) (*api.ListSnapshotsResponse, error) {
	if req == nil || req.SessionID == "" {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "session_id is required", nil)
	}
	sess, err := m.getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	sess.mu.RLock()
	defer sess.mu.RUnlock()
	out := make([]api.Snapshot, 0, len(sess.snapshots))
	for _, snap := range sess.snapshots {
		out = append(out, api.Snapshot{
			Name:      snap.name,
			Dataset:   sess.id,
			CreatedAt: snap.created,
		})
	}
	return &api.ListSnapshotsResponse{Snapshots: out}, nil
}

func (m *MemorySandboxService) HealthCheck(_ context.Context, _ *api.HealthCheckRequest) (*api.HealthCheckResponse, error) {
	m.mu.RLock()
	sessionCount := len(m.sessions)
	m.mu.RUnlock()
	return &api.HealthCheckResponse{
		Status:       "healthy",
		PoolState:    "ONLINE",
		SessionCount: sessionCount,
		ActiveTools:  0,
		Uptime:       time.Since(m.started),
	}, nil
}

func (m *MemorySandboxService) executeTool(ctx context.Context, req *api.ExecuteToolRequest, progress func(chunk string, isError bool)) (*api.ExecuteToolResponse, error) {
	if req.SessionID == "" || req.ToolName == "" || req.ToolCallID == "" {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "session_id, tool_name, tool_call_id are required", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, rpc.MapError(err)
	}
	sess, err := m.getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	sess.mu.Lock()
	if sess.state != memoryStateActive {
		sess.mu.Unlock()
		return nil, rpc.NewRPCError(rpc.CodeFailedPrecondition, fmt.Sprintf("invalid state: %s", sess.state), sandbox.ErrInvalidState)
	}

	tier := int(tools.ClassifyTool(req.ToolName))
	if progress != nil && tier == int(tools.Tier2) {
		progress("dispatching remote tier2 execution", false)
	}

	content, exitCode, execErr := executeInMemoryTool(req.ToolName, req.Params, sess.files)
	if execErr != nil {
		sess.mu.Unlock()
		return nil, rpc.NewRPCError(rpc.CodeInternal, execErr.Error(), execErr)
	}

	snapshotID := ""
	if tier == int(tools.Tier2) {
		n := atomic.AddInt64(&m.nextSnapshot, 1)
		snapshotID = fmt.Sprintf("tool-%04d", n)
		sess.snapshots = append(sess.snapshots, memorySnapshot{
			id:      snapshotID,
			name:    snapshotID,
			created: time.Now(),
			files:   cloneFileMap(sess.files),
		})
		sess.snapCount = len(sess.snapshots)
	}
	sess.mu.Unlock()

	m.executeCalls.Add(1)
	m.mu.Lock()
	m.routes = append(m.routes, ToolRoute{
		SessionID:  req.SessionID,
		ToolCallID: req.ToolCallID,
		ToolName:   req.ToolName,
		Tier:       tier,
		SnapshotID: snapshotID,
	})
	m.mu.Unlock()

	resp := &api.ExecuteToolResponse{
		SessionID:  req.SessionID,
		ToolCallID: req.ToolCallID,
		ToolName:   req.ToolName,
		Content:    content,
		Tier:       tier,
		SnapshotID: snapshotID,
	}
	if exitCode != nil {
		resp.ExitCode = exitCode
	}
	return resp, nil
}

func (m *MemorySandboxService) getSession(sessionID string) (*memorySession, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, rpc.NewRPCError(rpc.CodeNotFound, "session not found", sandbox.ErrSessionNotFound)
	}
	return sess, nil
}

func executeInMemoryTool(toolName string, params map[string]any, files map[string][]byte) (string, *int, error) {
	switch toolName {
	case "read_file":
		p, _ := params["path"].(string)
		if p == "" {
			return "missing required parameter: path", nil, nil
		}
		data, ok := files[normalizePath(p)]
		if !ok {
			return fmt.Sprintf("file not found: %s", p), nil, nil
		}
		return string(data), nil, nil
	case "write_file":
		p, _ := params["path"].(string)
		if p == "" {
			return "missing required parameter: path", nil, nil
		}
		content, _ := params["content"].(string)
		files[normalizePath(p)] = []byte(content)
		return fmt.Sprintf("wrote %d bytes to %s", len(content), p), nil, nil
	case "edit_file":
		p, _ := params["path"].(string)
		oldStr, _ := params["old_string"].(string)
		newStr, _ := params["new_string"].(string)
		if p == "" || oldStr == "" {
			return "missing required parameters", nil, nil
		}
		key := normalizePath(p)
		cur, ok := files[key]
		if !ok {
			return fmt.Sprintf("file not found: %s", p), nil, nil
		}
		next := strings.Replace(string(cur), oldStr, newStr, 1)
		files[key] = []byte(next)
		return fmt.Sprintf("edited %s", p), nil, nil
	case "grep":
		pattern, _ := params["pattern"].(string)
		if pattern == "" {
			return "missing required parameter: pattern", nil, nil
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return "invalid regex pattern", nil, nil
		}
		var hits []string
		for p, data := range files {
			for i, line := range strings.Split(string(data), "\n") {
				if re.MatchString(line) {
					hits = append(hits, fmt.Sprintf("%s:%d: %s", p, i+1, line))
				}
			}
		}
		sort.Strings(hits)
		if len(hits) == 0 {
			return "no matches found", nil, nil
		}
		return strings.Join(hits, "\n"), nil, nil
	case "glob":
		pattern, _ := params["pattern"].(string)
		if pattern == "" {
			return "missing required parameter: pattern", nil, nil
		}
		var matches []string
		for p := range files {
			ok, _ := path.Match(pattern, p)
			if ok {
				matches = append(matches, p)
			}
		}
		sort.Strings(matches)
		if len(matches) == 0 {
			return "no files matched", nil, nil
		}
		return strings.Join(matches, "\n"), nil, nil
	case "bash":
		cmd, _ := params["cmd"].(string)
		return executeFakeBash(cmd, files)
	default:
		return fmt.Sprintf("unsupported tool in memory sandbox: %s", toolName), nil, nil
	}
}

var echoRedirectRe = regexp.MustCompile(`^echo\s+['\"]?(.*?)['\"]?\s*>\s*(\S+)$`)

func executeFakeBash(cmd string, files map[string][]byte) (string, *int, error) {
	trimmed := strings.TrimSpace(cmd)
	exitCode := 0
	if trimmed == "" {
		errCode := 1
		return "missing required parameter: cmd", &errCode, nil
	}
	if m := echoRedirectRe.FindStringSubmatch(trimmed); len(m) == 3 {
		files[normalizePath(m[2])] = []byte(m[1] + "\n")
		return m[1], &exitCode, nil
	}
	if strings.HasPrefix(trimmed, "cat ") {
		target := strings.TrimSpace(strings.TrimPrefix(trimmed, "cat "))
		data, ok := files[normalizePath(target)]
		if !ok {
			errCode := 1
			return fmt.Sprintf("cat: %s: No such file", target), &errCode, nil
		}
		return string(data), &exitCode, nil
	}
	if trimmed == "pwd" {
		return "/workspace", &exitCode, nil
	}
	if strings.HasPrefix(trimmed, "sleep ") {
		parts := strings.Fields(trimmed)
		if len(parts) == 2 {
			if n, err := strconv.Atoi(parts[1]); err == nil && n > 0 {
				time.Sleep(time.Duration(n) * time.Millisecond)
			}
		}
		return "", &exitCode, nil
	}
	return fmt.Sprintf("executed: %s", trimmed), &exitCode, nil
}

func normalizePath(p string) string {
	p = filepath.ToSlash(strings.TrimSpace(p))
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	return path.Clean(p)
}

func cloneFileMap(in map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(in))
	for k, v := range in {
		cp := make([]byte, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

func cloneLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

var _ api.SandboxService = (*MemorySandboxService)(nil)
