package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	agentapi "github.com/anthropics/flex-agent-runtime/internal/agent/api"
	"github.com/anthropics/flex-agent-runtime/internal/ai"
)

const (
	defaultCloseDrainTimeout = 30 * time.Second
	turnStreamBufferSize     = 256
	sessionStreamBufferSize  = 512
)

var generatedSessionIDCounter atomic.Uint64

// EventPublisher publishes session events to an external sink.
type EventPublisher interface {
	Publish(sessionID string, event AgentEvent)
}

type ServiceOption func(*AgentLoopService)

// ToolCatalogFactory builds the runtime tool catalog for a session.
type ToolCatalogFactory func(cfg agentapi.SessionConfig) ([]AgentTool, error)

// WithMaxSessions sets the maximum concurrent session count.
// Zero means unlimited.
func WithMaxSessions(n int) ServiceOption {
	return func(s *AgentLoopService) {
		if n < 0 {
			n = 0
		}
		s.maxSess = n
	}
}

// WithCloseDrainTimeout sets the close drain timeout.
func WithCloseDrainTimeout(d time.Duration) ServiceOption {
	return func(s *AgentLoopService) {
		if d > 0 {
			s.closeDrainTimeout = d
		}
	}
}

// WithToolCatalogFactory configures how session tools are built from
// SessionConfig. If nil, the default noop catalog is used.
func WithToolCatalogFactory(factory ToolCatalogFactory) ServiceOption {
	return func(s *AgentLoopService) {
		if factory != nil {
			s.toolFactory = factory
		}
	}
}

// AgentLoopService is the in-process implementation of agentapi.AgentService.
type AgentLoopService struct {
	mu        sync.Mutex
	sessions  map[string]*managedSession
	publisher EventPublisher
	maxSess   int
	closed    bool

	driverFactory func(name string, cfg DriverConfig) (AgentDriver, error)
	toolFactory   ToolCatalogFactory

	closeDrainTimeout time.Duration

	wg sync.WaitGroup
}

type managedSession struct {
	agent     *Agent
	config    agentapi.SessionConfig
	ctx       context.Context
	cancel    context.CancelFunc
	createdAt time.Time

	mu        sync.Mutex
	started   bool
	destroyed bool
	streams   map[*serviceEventReceiver]struct{}
	turnDone  func()

	unsubscribe func()
}

// Compile-time interface assertion.
var _ agentapi.AgentService = (*AgentLoopService)(nil)

func NewAgentLoopService(publisher EventPublisher, opts ...ServiceOption) *AgentLoopService {
	s := &AgentLoopService{
		sessions:          make(map[string]*managedSession),
		publisher:         publisher,
		driverFactory:     NewDriver,
		toolFactory:       defaultToolCatalogFactory,
		closeDrainTimeout: defaultCloseDrainTimeout,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// SetDriverFactory overrides the driver factory. Primarily used in tests.
func (s *AgentLoopService) SetDriverFactory(factory func(name string, cfg DriverConfig) (AgentDriver, error)) {
	if factory == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.driverFactory = factory
}

func (s *AgentLoopService) CreateSession(ctx context.Context, req *agentapi.CreateAgentSessionRequest) (*agentapi.CreateAgentSessionResponse, error) {
	_ = ctx
	if req == nil {
		return nil, newServiceError(CodeInvalidArgument, "nil request", nil)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, newServiceError(CodeUnavailable, "service is closing", nil)
	}
	if s.maxSess > 0 && len(s.sessions) >= s.maxSess {
		return nil, newServiceError(CodeResourceExhausted, "max sessions reached", nil)
	}

	cfg := req.SessionConfig
	sessionID := cfg.SessionID
	if sessionID == "" {
		sessionID = generateSessionID()
		cfg.SessionID = sessionID
	}
	if _, exists := s.sessions[sessionID]; exists {
		return nil, newServiceError(CodeAlreadyExists, fmt.Sprintf("session %q already exists", sessionID), nil)
	}

	ms, err := s.newManagedSessionLocked(cfg, nil)
	if err != nil {
		return nil, err
	}
	s.sessions[sessionID] = ms

	return &agentapi.CreateAgentSessionResponse{
		SessionID: sessionID,
		State:     string(ms.agent.State()),
	}, nil
}

func (s *AgentLoopService) GetSession(ctx context.Context, req *agentapi.GetAgentSessionRequest) (*agentapi.GetAgentSessionResponse, error) {
	_ = ctx
	if req == nil || req.SessionID == "" {
		return nil, newServiceError(CodeInvalidArgument, "session_id is required", nil)
	}
	ms, err := s.getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	sess := ms.agent.Session()
	if sess == nil {
		return nil, newServiceError(CodeInternal, "session state unavailable", nil)
	}
	return &agentapi.GetAgentSessionResponse{
		SessionID:       req.SessionID,
		State:           string(ms.agent.State()),
		Metrics:         toAPISessionMetrics(sess.Metrics),
		ConversationLen: ms.agent.ConversationLen(),
	}, nil
}

func (s *AgentLoopService) ListSessions(ctx context.Context, req *agentapi.ListAgentSessionsRequest) (*agentapi.ListAgentSessionsResponse, error) {
	_ = ctx
	filter := map[string]string(nil)
	if req != nil {
		filter = req.Labels
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	resp := &agentapi.ListAgentSessionsResponse{}
	for id, ms := range s.sessions {
		if !matchesLabelFilter(ms.config, filter) {
			continue
		}
		if ms.isDestroyed() {
			continue
		}
		resp.Sessions = append(resp.Sessions, agentapi.AgentSessionSummary{
			SessionID: id,
			State:     string(ms.agent.State()),
			CreatedAt: ms.createdAt,
		})
	}
	return resp, nil
}

func (s *AgentLoopService) SendMessage(ctx context.Context, req *agentapi.SendMessageRequest) (agentapi.EventReceiver, error) {
	_ = ctx
	if req == nil {
		return nil, newServiceError(CodeInvalidArgument, "nil request", nil)
	}
	if req.SessionID == "" {
		return nil, newServiceError(CodeInvalidArgument, "session_id is required", nil)
	}

	ms, err := s.getSession(req.SessionID)
	if err != nil {
		return nil, err
	}

	receiver, doneTurn := s.newTurnScopedReceiver(ms)
	callErr := s.safeCallStartOrPrompt(ms, req.Message)
	if callErr != nil {
		doneTurn()
		receiver.Close()
		return nil, mapServiceError(callErr)
	}

	ms.mu.Lock()
	ms.started = true
	ms.mu.Unlock()

	return receiver, nil
}

func (s *AgentLoopService) Continue(ctx context.Context, req *agentapi.ContinueRequest) (agentapi.EventReceiver, error) {
	_ = ctx
	if req == nil {
		return nil, newServiceError(CodeInvalidArgument, "nil request", nil)
	}
	if req.SessionID == "" {
		return nil, newServiceError(CodeInvalidArgument, "session_id is required", nil)
	}

	ms, err := s.getSession(req.SessionID)
	if err != nil {
		return nil, err
	}

	receiver, doneTurn := s.newTurnScopedReceiver(ms)
	callErr := s.safeCall(func() error { return ms.agent.Continue(ms.ctx) })
	if callErr != nil {
		doneTurn()
		receiver.Close()
		return nil, mapServiceError(callErr)
	}

	ms.mu.Lock()
	ms.started = true
	ms.mu.Unlock()

	return receiver, nil
}

func (s *AgentLoopService) Steer(ctx context.Context, req *agentapi.SteerRequest) (*agentapi.SteerResponse, error) {
	_ = ctx
	if req == nil {
		return nil, newServiceError(CodeInvalidArgument, "nil request", nil)
	}
	if req.SessionID == "" {
		return nil, newServiceError(CodeInvalidArgument, "session_id is required", nil)
	}
	ms, err := s.getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	if ms.agent.State() == StateIdle {
		return nil, newServiceError(CodeFailedPrecondition, "cannot steer while idle", nil)
	}
	if err := ms.agent.Steer(req.Message); err != nil {
		return nil, mapServiceError(err)
	}
	return &agentapi.SteerResponse{}, nil
}

func (s *AgentLoopService) FollowUp(ctx context.Context, req *agentapi.FollowUpRequest) (*agentapi.FollowUpResponse, error) {
	_ = ctx
	if req == nil {
		return nil, newServiceError(CodeInvalidArgument, "nil request", nil)
	}
	if req.SessionID == "" {
		return nil, newServiceError(CodeInvalidArgument, "session_id is required", nil)
	}
	ms, err := s.getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	if err := ms.agent.FollowUp(req.Message); err != nil {
		return nil, mapServiceError(err)
	}
	return &agentapi.FollowUpResponse{}, nil
}

func (s *AgentLoopService) Abort(ctx context.Context, req *agentapi.AbortRequest) (*agentapi.AbortResponse, error) {
	_ = ctx
	if req == nil {
		return nil, newServiceError(CodeInvalidArgument, "nil request", nil)
	}
	if req.SessionID == "" {
		return nil, newServiceError(CodeInvalidArgument, "session_id is required", nil)
	}
	ms, err := s.getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	if err := ms.agent.Abort(req.Reason); err != nil {
		return nil, mapServiceError(err)
	}
	return &agentapi.AbortResponse{}, nil
}

func (s *AgentLoopService) SubscribeEvents(ctx context.Context, req *agentapi.SubscribeEventsRequest) (agentapi.EventReceiver, error) {
	_ = ctx
	if req == nil {
		return nil, newServiceError(CodeInvalidArgument, "nil request", nil)
	}
	if req.SessionID == "" {
		return nil, newServiceError(CodeInvalidArgument, "session_id is required", nil)
	}

	ms, err := s.getSession(req.SessionID)
	if err != nil {
		return nil, err
	}

	receiver := newServiceEventReceiver(sessionStreamBufferSize)

	// Push an initial state event so that RPC streaming handlers can flush
	// HTTP response headers immediately instead of blocking until the first
	// real event arrives.  Without this, ConnectRPC server-stream callers
	// deadlock: CallServerStream waits for response headers while the handler
	// waits on recv.Recv() — neither side makes progress.
	receiver.push(normalizeSessionEvent(req.SessionID, AgentEvent{
		Type:  EventStateChange,
		State: ms.agent.State(),
		At:    time.Now(),
	}))

	unsub := ms.agent.Subscribe(func(evt AgentEvent) {
		event := normalizeSessionEvent(req.SessionID, evt)
		if !receiver.push(event) {
			return
		}
	})
	receiver.setCloseHook(func() { unsub() })
	ms.addStream(receiver)
	receiver.setCloseHook(func() { ms.removeStream(receiver) })
	return receiver, nil
}

func (s *AgentLoopService) ResumeSession(ctx context.Context, req *agentapi.ResumeSessionRequest) (*agentapi.ResumeSessionResponse, error) {
	_ = ctx
	if req == nil {
		return nil, newServiceError(CodeInvalidArgument, "nil request", nil)
	}
	if req.SchemaVersion != agentapi.CurrentConversationSchemaVersion {
		return nil, newServiceError(
			CodeInvalidArgument,
			fmt.Sprintf("schema version %d unsupported, expected %d", req.SchemaVersion, agentapi.CurrentConversationSchemaVersion),
			nil,
		)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, newServiceError(CodeUnavailable, "service is closing", nil)
	}
	if s.maxSess > 0 && len(s.sessions) >= s.maxSess {
		return nil, newServiceError(CodeResourceExhausted, "max sessions reached", nil)
	}

	cfg := req.SessionConfig
	sessionID := cfg.SessionID
	if sessionID == "" {
		sessionID = generateSessionID()
		cfg.SessionID = sessionID
	}
	if _, exists := s.sessions[sessionID]; exists {
		return nil, newServiceError(CodeAlreadyExists, fmt.Sprintf("session %q already exists", sessionID), nil)
	}

	conversation, warnings, err := validateAndDecodeResumeLog(req.ConversationLog, cfg.Tools)
	if err != nil {
		return nil, err
	}

	ms, err := s.newManagedSessionLocked(cfg, conversation)
	if err != nil {
		return nil, err
	}
	ms.started = true
	s.sessions[sessionID] = ms

	return &agentapi.ResumeSessionResponse{
		SessionID:       sessionID,
		State:           string(ms.agent.State()),
		ConversationLen: ms.agent.ConversationLen(),
		Warnings:        warnings,
	}, nil
}

func (s *AgentLoopService) DestroySession(ctx context.Context, req *agentapi.DestroyAgentSessionRequest) (*agentapi.DestroyAgentSessionResponse, error) {
	if req == nil {
		return nil, newServiceError(CodeInvalidArgument, "nil request", nil)
	}
	if req.SessionID == "" {
		return nil, newServiceError(CodeInvalidArgument, "session_id is required", nil)
	}

	s.mu.Lock()
	ms, ok := s.sessions[req.SessionID]
	if !ok {
		s.mu.Unlock()
		return nil, newServiceError(CodeNotFound, fmt.Sprintf("session %q not found", req.SessionID), nil)
	}
	ms.markDestroyed()
	s.mu.Unlock()

	if ms.unsubscribe != nil {
		ms.unsubscribe()
	}
	stopErr := ms.agent.Stop(ctx)
	ms.cancel()
	ms.completeActiveTurn()
	ms.closeAllStreams()

	s.mu.Lock()
	delete(s.sessions, req.SessionID)
	s.mu.Unlock()

	if stopErr != nil {
		return nil, mapServiceError(stopErr)
	}
	return &agentapi.DestroyAgentSessionResponse{}, nil
}

func (s *AgentLoopService) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true

	sessions := make(map[string]*managedSession, len(s.sessions))
	for id, ms := range s.sessions {
		sessions[id] = ms
	}
	s.mu.Unlock()

	for _, ms := range sessions {
		ms.markDestroyed()
		if ms.unsubscribe != nil {
			ms.unsubscribe()
		}
		_ = ms.agent.Stop(context.Background())
		ms.cancel()
		ms.completeActiveTurn()
		ms.closeAllStreams()
	}

	s.mu.Lock()
	for id := range sessions {
		delete(s.sessions, id)
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.wg.Wait()
	}()

	select {
	case <-done:
		return nil
	case <-time.After(s.closeDrainTimeout):
		for _, ms := range sessions {
			ms.cancel()
			ms.completeActiveTurn()
		}
		select {
		case <-done:
			return nil
		case <-time.After(5 * time.Second):
			return newServiceError(CodeDeadlineExceeded, "close drain timeout exceeded", nil)
		}
	}
}

func (s *AgentLoopService) newManagedSessionLocked(cfg agentapi.SessionConfig, conversation []AgentMessage) (*managedSession, error) {
	model, err := ai.GetModel(cfg.Provider, cfg.Model)
	if err != nil {
		return nil, newServiceError(CodeInvalidArgument, err.Error(), err)
	}
	toolCatalog, err := s.toolFactory(cfg)
	if err != nil {
		var svcErr *ServiceError
		if errors.As(err, &svcErr) {
			return nil, svcErr
		}
		return nil, newServiceError(CodeInvalidArgument, err.Error(), err)
	}

	driverCfg := DriverConfig{
		Model:        model,
		SystemPrompt: cfg.SystemPrompt,
		Tools:        toolCatalog,
		Metadata: map[string]any{
			"session_id": cfg.SessionID,
			"provider":   cfg.Provider,
		},
	}
	driver, err := s.driverFactory(cfg.Driver, driverCfg)
	if err != nil {
		return nil, newServiceError(CodeInvalidArgument, err.Error(), err)
	}

	a := New(driver)
	baseSession := &Session{
		ID:              cfg.SessionID,
		DriverSessionID: cfg.SessionID,
		ConversationLog: append([]AgentMessage(nil), conversation...),
		StateHistory:    []AgentState{StateIdle},
	}
	a.SetSession(baseSession)

	msCtx, msCancel := context.WithCancel(context.Background())
	ms := &managedSession{
		agent:       a,
		config:      cfg,
		ctx:         msCtx,
		cancel:      msCancel,
		createdAt:   time.Now(),
		streams:     make(map[*serviceEventReceiver]struct{}),
		destroyed:   false,
		unsubscribe: nil,
	}

	if s.publisher != nil {
		ms.unsubscribe = a.Subscribe(func(evt AgentEvent) {
			s.publisher.Publish(cfg.SessionID, normalizeSessionEvent(cfg.SessionID, evt))
		})
	}
	return ms, nil
}

func (s *AgentLoopService) getSession(sessionID string) (*managedSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ms, ok := s.sessions[sessionID]
	if !ok || ms.isDestroyed() {
		return nil, newServiceError(CodeNotFound, fmt.Sprintf("session %q not found", sessionID), nil)
	}
	return ms, nil
}

func (s *AgentLoopService) safeCallStartOrPrompt(ms *managedSession, message string) error {
	ms.mu.Lock()
	started := ms.started
	ms.mu.Unlock()

	if !started {
		return s.safeCall(func() error {
			return ms.agent.Start(ms.ctx, ms.agent.Session(), message)
		})
	}
	return s.safeCall(func() error {
		return ms.agent.Prompt(ms.ctx, message)
	})
}

func (s *AgentLoopService) safeCall(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("agent driver panic: %v", r)
		}
	}()
	return fn()
}

func (s *AgentLoopService) newTurnScopedReceiver(ms *managedSession) (*serviceEventReceiver, func()) {
	receiver := newServiceEventReceiver(turnStreamBufferSize)
	s.wg.Add(1)

	var once sync.Once
	doneTurn := func() {
		once.Do(func() {
			s.wg.Done()
			ms.clearTurnDone()
		})
	}
	ms.setTurnDone(doneTurn)

	unsubTracker := ms.agent.Subscribe(func(evt AgentEvent) {
		event := normalizeSessionEvent(ms.config.SessionID, evt)
		if isTerminalTurnEvent(event) {
			doneTurn()
		}
	})

	unsubTurn := ms.agent.Subscribe(func(evt AgentEvent) {
		event := normalizeSessionEvent(ms.config.SessionID, evt)
		if !receiver.push(event) {
			return
		}
		if isTerminalTurnEvent(event) {
			receiver.closeWithError(io.EOF)
			doneTurn()
		}
	})

	receiver.setCloseHook(func() {
		unsubTurn()
		unsubTracker()
	})
	ms.addStream(receiver)
	receiver.setCloseHook(func() { ms.removeStream(receiver) })
	return receiver, doneTurn
}

func validateAndDecodeResumeLog(records []agentapi.AgentMessageRecord, tools []string) ([]AgentMessage, []string, error) {
	if len(records) == 0 {
		return nil, nil, nil
	}

	warnings := make([]string, 0)
	out := make([]AgentMessage, 0, len(records))
	toolSet := make(map[string]struct{}, len(tools))
	for _, name := range tools {
		toolSet[name] = struct{}{}
	}

	prevTurn := records[0].Turn
	prevRole := ""

	for i, rec := range records {
		switch rec.Role {
		case agentapi.AgentMessageRoleUser, agentapi.AgentMessageRoleAssistant, agentapi.AgentMessageRoleToolResult:
		default:
			return nil, nil, newServiceError(CodeInvalidArgument, fmt.Sprintf("unknown role %q at index %d", rec.Role, i), nil)
		}

		msg, err := agentapi.RecordToAgentMessage(rec)
		if err != nil {
			return nil, nil, newServiceError(CodeInvalidArgument, fmt.Sprintf("invalid record at index %d: %v", i, err), err)
		}

		if i > 0 {
			if rec.Turn < prevTurn {
				return nil, nil, newServiceError(CodeInvalidArgument, fmt.Sprintf("turn number decreased at index %d: %d < %d", i, rec.Turn, prevTurn), nil)
			}
			if rec.Turn > prevTurn+1 {
				warnings = append(warnings, fmt.Sprintf("non-contiguous turn numbers at index %d: %d -> %d", i-1, prevTurn, rec.Turn))
			}
			if prevRole == rec.Role {
				warnings = append(warnings, fmt.Sprintf("consecutive records with same role %q at index %d", rec.Role, i))
			}
		}

		if toolResult, ok := msg.Message.(*ai.ToolResultMessage); ok {
			if _, exists := toolSet[toolResult.ToolName]; !exists {
				warnings = append(warnings, fmt.Sprintf("tool_result references unknown tool %q at index %d", toolResult.ToolName, i))
			}
		}

		out = append(out, fromAPIMessage(msg))
		prevTurn = rec.Turn
		prevRole = rec.Role
	}
	return out, warnings, nil
}

func validateToolEnvironment(cfg agentapi.ToolEnvironmentConfig) error {
	switch cfg.Type {
	case "", agentapi.ToolEnvLocal:
		return nil
	case agentapi.ToolEnvSandbox:
		return newServiceError(CodeFailedPrecondition, "sandbox tool environment is not configured in AgentLoopService", nil)
	default:
		return newServiceError(CodeInvalidArgument, fmt.Sprintf("unknown tool environment type %q", cfg.Type), nil)
	}
}

func defaultToolCatalogFactory(cfg agentapi.SessionConfig) ([]AgentTool, error) {
	if err := validateToolEnvironment(cfg.ToolEnvironment); err != nil {
		return nil, err
	}
	return buildNoopTools(cfg.Tools), nil
}

func buildNoopTools(names []string) []AgentTool {
	if len(names) == 0 {
		return nil
	}
	out := make([]AgentTool, 0, len(names))
	for _, rawName := range names {
		name := rawName
		out = append(out, AgentTool{
			Tool: ai.Tool{
				Name:        name,
				Description: name,
				Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
			},
			Label: name,
			Execute: func(ctx context.Context, toolCallID string, params map[string]any, onUpdate func(AgentToolResult)) (AgentToolResult, error) {
				_ = ctx
				_ = toolCallID
				_ = params
				_ = onUpdate
				return AgentToolResult{
					IsError: true,
					Content: []ai.ContentBlock{&ai.TextContent{Text: "tool execution not configured"}},
				}, fmt.Errorf("tool %q execution not configured", name)
			},
		})
	}
	return out
}

func normalizeSessionEvent(sessionID string, evt AgentEvent) AgentEvent {
	normalized := evt.clone()
	if normalized.SessionID == "" {
		normalized.SessionID = sessionID
	}
	return normalized
}

func isTerminalTurnEvent(evt AgentEvent) bool {
	switch evt.Type {
	case EventTurnCompleted, EventDriverError, EventProviderError, EventAborted:
		return true
	default:
		return false
	}
}

func toAPIEvent(evt AgentEvent) agentapi.AgentEvent {
	var msg *agentapi.AgentMessage
	if evt.Message != nil {
		msgCopy := agentapi.AgentMessage{
			Turn:      evt.Message.Turn,
			Message:   evt.Message.Message,
			CreatedAt: evt.Message.CreatedAt,
		}
		msg = &msgCopy
	}

	var toolResult *agentapi.AgentToolResult
	if evt.ToolResult != nil {
		toolResult = &agentapi.AgentToolResult{
			Content:    append([]ai.ContentBlock(nil), evt.ToolResult.Content...),
			SnapshotID: evt.ToolResult.SnapshotID,
			ExitCode:   evt.ToolResult.ExitCode,
			IsError:    evt.ToolResult.IsError,
			Metadata:   cloneMetadata(evt.ToolResult.Metadata),
		}
	}

	return agentapi.AgentEvent{
		Type:            agentapi.AgentEventType(evt.Type),
		SessionID:       evt.SessionID,
		DriverSessionID: evt.DriverSessionID,
		State:           agentapi.AgentState(evt.State),
		Turn:            evt.Turn,
		Message:         msg,
		Assistant:       evt.Assistant,
		ToolName:        evt.ToolName,
		ToolCallID:      evt.ToolCallID,
		ToolResult:      toolResult,
		Delta:           evt.Delta,
		ControlMessage:  evt.ControlMessage,
		ErrorMessage:    evt.ErrorMessage,
		At:              evt.At,
		Metadata:        cloneMetadata(evt.Metadata),
	}
}

func cloneMetadata(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func mapServiceError(err error) error {
	if err == nil {
		return nil
	}
	var svcErr *ServiceError
	if errors.As(err, &svcErr) {
		return svcErr
	}
	switch {
	case errors.Is(err, ErrBusy):
		return newServiceError(CodeFailedPrecondition, err.Error(), err)
	case errors.Is(err, ErrInvalidState):
		return newServiceError(CodeFailedPrecondition, err.Error(), err)
	case errors.Is(err, ErrStopped):
		return newServiceError(CodeFailedPrecondition, err.Error(), err)
	case errors.Is(err, context.Canceled):
		return newServiceError(CodeCanceled, err.Error(), err)
	case errors.Is(err, context.DeadlineExceeded):
		return newServiceError(CodeDeadlineExceeded, err.Error(), err)
	default:
		return newServiceError(CodeInternal, err.Error(), err)
	}
}

func matchesLabelFilter(cfg agentapi.SessionConfig, labels map[string]string) bool {
	if len(labels) == 0 {
		return true
	}
	if len(cfg.Metadata) == 0 {
		return false
	}
	for key, expected := range labels {
		raw, ok := cfg.Metadata[key]
		if !ok {
			return false
		}
		got, ok := raw.(string)
		if !ok || got != expected {
			return false
		}
	}
	return true
}

func generateSessionID() string {
	n := generatedSessionIDCounter.Add(1)
	return fmt.Sprintf("agent-%08x", n)
}

type serviceEventReceiver struct {
	ch chan *agentapi.AgentEvent

	mu          sync.Mutex
	closed      bool
	terminalErr error
	closeHooks  []func()

	done chan struct{}
	once sync.Once
}

func newServiceEventReceiver(buffer int) *serviceEventReceiver {
	if buffer <= 0 {
		buffer = 1
	}
	return &serviceEventReceiver{
		ch:   make(chan *agentapi.AgentEvent, buffer),
		done: make(chan struct{}),
	}
}

func (r *serviceEventReceiver) push(evt AgentEvent) bool {
	apiEvt := toAPIEvent(evt)
	r.mu.Lock()
	closed := r.closed
	ch := r.ch
	done := r.done
	r.mu.Unlock()
	if closed {
		return false
	}
	select {
	case ch <- &apiEvt:
		return true
	case <-done:
		return false
	}
}

func (r *serviceEventReceiver) Recv() (*agentapi.AgentEvent, error) {
	r.mu.Lock()
	if r.closed && len(r.ch) == 0 {
		err := r.terminalErr
		r.mu.Unlock()
		return nil, err
	}
	ch := r.ch
	r.mu.Unlock()

	evt, ok := <-ch
	if ok {
		return evt, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminalErr == nil {
		r.terminalErr = io.EOF
	}
	r.closed = true
	return nil, r.terminalErr
}

func toAPISessionMetrics(m SessionMetrics) agentapi.SessionMetrics {
	return agentapi.SessionMetrics{
		TurnsStarted:      m.TurnsStarted,
		TurnsCompleted:    m.TurnsCompleted,
		MessagesAppended:  m.MessagesAppended,
		ToolCallsStarted:  m.ToolCallsStarted,
		ToolCallsFinished: m.ToolCallsFinished,
		Errors:            m.Errors,
	}
}

func fromAPIMessage(m agentapi.AgentMessage) AgentMessage {
	return AgentMessage{
		Turn:      m.Turn,
		Message:   m.Message,
		CreatedAt: m.CreatedAt,
	}
}

func (r *serviceEventReceiver) Close() error {
	r.closeWithError(io.EOF)
	return nil
}

func (r *serviceEventReceiver) setCloseHook(hook func()) {
	if hook == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		hook()
		return
	}
	r.closeHooks = append(r.closeHooks, hook)
}

func (r *serviceEventReceiver) closeWithError(err error) {
	r.once.Do(func() {
		if err == nil {
			err = io.EOF
		}

		r.mu.Lock()
		r.closed = true
		r.terminalErr = err
		hooks := append([]func(){}, r.closeHooks...)
		r.closeHooks = nil
		close(r.done)
		close(r.ch)
		r.mu.Unlock()

		for _, hook := range hooks {
			if hook != nil {
				hook()
			}
		}
	})
}

func (ms *managedSession) isDestroyed() bool {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return ms.destroyed
}

func (ms *managedSession) markDestroyed() {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.destroyed = true
}

func (ms *managedSession) setTurnDone(fn func()) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.turnDone = fn
}

func (ms *managedSession) clearTurnDone() {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.turnDone = nil
}

func (ms *managedSession) completeActiveTurn() {
	ms.mu.Lock()
	done := ms.turnDone
	ms.turnDone = nil
	ms.mu.Unlock()
	if done != nil {
		done()
	}
}

func (ms *managedSession) addStream(receiver *serviceEventReceiver) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.streams[receiver] = struct{}{}
}

func (ms *managedSession) removeStream(receiver *serviceEventReceiver) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	delete(ms.streams, receiver)
}

func (ms *managedSession) closeAllStreams() {
	ms.mu.Lock()
	streams := make([]*serviceEventReceiver, 0, len(ms.streams))
	for receiver := range ms.streams {
		streams = append(streams, receiver)
	}
	ms.streams = make(map[*serviceEventReceiver]struct{})
	ms.mu.Unlock()

	for _, receiver := range streams {
		_ = receiver.Close()
	}
}
