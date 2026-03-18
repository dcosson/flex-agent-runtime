package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/fleet"
)

func (o *Orchestrator) CreateSession(ctx context.Context, req *agentapi.CreateAgentSessionRequest) (*agentapi.CreateAgentSessionResponse, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	if err := o.beginOperation(); err != nil {
		return nil, err
	}
	defer o.endOperation()

	placement, err := placementFromMetadata(req.SessionConfig.Metadata)
	if err != nil {
		return nil, err
	}
	if err := o.validatePlacement(placement); err != nil {
		return nil, err
	}

	clientSessionID := generateSessionID()
	entry := &sessionEntry{
		sessionID: clientSessionID,
		placement: placement,
		state:     sessionCreating,
		createdAt: time.Now(),
	}

	createReq := cloneCreateSessionRequest(req)
	createReq.SessionConfig.SessionID = clientSessionID

	createCtx, cancel := o.createTimeoutContext()
	defer cancel()

	switch placement {
	case PlacementAgentDirect:
		return o.createAgentDirectSession(createCtx, entry, &createReq)
	case PlacementAgentSandbox:
		return o.createAgentSandboxSession(createCtx, entry, &createReq)
	case PlacementToolsSandbox:
		return o.createToolsSandboxSession(createCtx, entry, &createReq)
	default:
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, fmt.Sprintf("unsupported placement %q", placement), nil)
	}
}

func (o *Orchestrator) createAgentDirectSession(ctx context.Context, entry *sessionEntry, req *agentapi.CreateAgentSessionRequest) (*agentapi.CreateAgentSessionResponse, error) {
	return o.createRemoteAgentSession(ctx, entry, req, o.config.DirectControl)
}

func (o *Orchestrator) createAgentSandboxSession(ctx context.Context, entry *sessionEntry, req *agentapi.CreateAgentSessionRequest) (*agentapi.CreateAgentSessionResponse, error) {
	return o.createRemoteAgentSession(ctx, entry, req, o.config.NodeControl)
}

// createRemoteAgentSession implements the shared creation flow for agent-direct
// and agent-sandbox modes. Both follow the same pattern: create sandbox, launch
// agent process, connect agent service, create backend session.
func (o *Orchestrator) createRemoteAgentSession(ctx context.Context, entry *sessionEntry, req *agentapi.CreateAgentSessionRequest, sc control.SandboxControl) (*agentapi.CreateAgentSessionResponse, error) {
	sandboxResp, err := sc.CreateSandbox(ctx, control.CreateSandboxRequest{
		Labels: metadataStringLabels(req.SessionConfig.Metadata),
	})
	if err != nil {
		return nil, err
	}

	launchResp, err := sc.LaunchProcess(ctx, control.LaunchProcessRequest{
		SandboxID:  sandboxResp.SandboxID,
		Binary:     o.config.AgentBinary,
		Args:       o.agentLaunchArgs(),
		Env:        cloneStringMap(o.config.AgentEnv),
		ExposePort: o.config.AgentPort,
	})
	if err != nil {
		cleanupErr := sc.DestroySandbox(ctx, sandboxResp.SandboxID)
		return nil, joinErrors(err, cleanupErr)
	}

	agentService, err := o.config.AgentServiceFactory(launchResp.Address)
	if err != nil {
		cleanupErr := joinErrors(
			sc.KillProcess(ctx, control.KillProcessRequest{SandboxID: sandboxResp.SandboxID, ProcessID: launchResp.ProcessID}),
			sc.DestroySandbox(ctx, sandboxResp.SandboxID),
		)
		return nil, joinErrors(err, cleanupErr)
	}

	createResp, err := agentService.CreateSession(ctx, req)
	if err != nil {
		_, destroyErr := agentService.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{SessionID: req.SessionConfig.SessionID})
		cleanupErr := joinErrors(
			destroyErr,
			agentService.Close(),
			sc.KillProcess(ctx, control.KillProcessRequest{SandboxID: sandboxResp.SandboxID, ProcessID: launchResp.ProcessID}),
			sc.DestroySandbox(ctx, sandboxResp.SandboxID),
		)
		return nil, joinErrors(err, cleanupErr)
	}

	remoteSessionID := createResp.SessionID
	if strings.TrimSpace(remoteSessionID) == "" {
		remoteSessionID = req.SessionConfig.SessionID
	}

	entry.remoteSessionID = remoteSessionID
	entry.sandboxID = sandboxResp.SandboxID
	entry.processID = launchResp.ProcessID
	entry.sandboxControl = sc
	entry.sandboxHostAddr = sandboxResp.Address
	entry.agentService = agentService
	entry.state = sessionActive
	entry.lastHealth = time.Now()

	if err := o.addSession(entry); err != nil {
		_, destroyErr := agentService.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{SessionID: remoteSessionID})
		cleanupErr := joinErrors(
			destroyErr,
			agentService.Close(),
			sc.KillProcess(ctx, control.KillProcessRequest{SandboxID: sandboxResp.SandboxID, ProcessID: launchResp.ProcessID}),
			sc.DestroySandbox(ctx, sandboxResp.SandboxID),
		)
		return nil, joinErrors(err, cleanupErr)
	}

	resp := *createResp
	resp.SessionID = entry.sessionID
	return &resp, nil
}

func (o *Orchestrator) createToolsSandboxSession(ctx context.Context, entry *sessionEntry, req *agentapi.CreateAgentSessionRequest) (*agentapi.CreateAgentSessionResponse, error) {
	sc := o.config.NodeControl
	sandboxResp, err := sc.CreateSandbox(ctx, control.CreateSandboxRequest{
		Labels: metadataStringLabels(req.SessionConfig.Metadata),
	})
	if err != nil {
		return nil, err
	}

	hostAddr := o.resolveToolsSandboxHostAddr(sandboxResp)
	toolSessionID := extractHostLocalSessionID(sandboxResp.SandboxID)

	toolsReq := cloneCreateSessionRequest(req)
	toolsReq.SessionConfig.ToolEnvironment = agentapi.ToolEnvironmentConfig{
		Type:             agentapi.ToolEnvSandbox,
		SandboxHostAddr:  hostAddr,
		SandboxSessionID: toolSessionID,
	}

	createResp, err := o.config.AgentLoopService.CreateSession(ctx, &toolsReq)
	if err != nil {
		// Compensation: destroy sandbox we just created.
		cleanupErr := sc.DestroySandbox(ctx, sandboxResp.SandboxID)
		return nil, joinErrors(err, cleanupErr)
	}

	remoteSessionID := createResp.SessionID
	if strings.TrimSpace(remoteSessionID) == "" {
		remoteSessionID = req.SessionConfig.SessionID
	}

	entry.remoteSessionID = remoteSessionID
	entry.sandboxID = sandboxResp.SandboxID
	entry.toolSessionID = toolSessionID
	entry.sandboxControl = sc
	entry.sandboxHostAddr = hostAddr
	entry.agentService = o.config.AgentLoopService
	entry.state = sessionActive
	entry.lastHealth = time.Now()

	if err := o.addSession(entry); err != nil {
		_, destroySessionErr := o.config.AgentLoopService.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{SessionID: remoteSessionID})
		cleanupErr := joinErrors(
			destroySessionErr,
			sc.DestroySandbox(ctx, sandboxResp.SandboxID),
		)
		return nil, joinErrors(err, cleanupErr)
	}

	resp := *createResp
	resp.SessionID = entry.sessionID
	return &resp, nil
}

// resolveToolsSandboxHostAddr determines the RPC address of the sandbox-host
// for tool dispatch. Node mode returns a mountpoint in Address (useless for
// RPC), so we use the configured --sandbox-host-addr. Fleet mode returns
// host:port directly.
func (o *Orchestrator) resolveToolsSandboxHostAddr(sandbox *control.CreateSandboxResponse) string {
	if isFleetSandboxID(sandbox.SandboxID) {
		return sandbox.Address
	}
	// Node mode: use configured sandbox-host address.
	if len(o.config.SandboxHostAddrs) > 0 {
		return o.config.SandboxHostAddrs[0]
	}
	return sandbox.Address
}

// extractHostLocalSessionID extracts the host-local session ID from a sandbox
// ID. For Fleet IDs ("fleet:{instanceID}:{sessionID}"), returns the sessionID
// portion. For Node IDs (no prefix), returns the ID unchanged.
func extractHostLocalSessionID(sandboxID string) string {
	if !isFleetSandboxID(sandboxID) {
		return sandboxID
	}
	_, sessionID, err := fleet.ParseSandboxID(sandboxID)
	if err != nil {
		return sandboxID
	}
	return sessionID
}

// isFleetSandboxID returns true if the sandbox ID has a fleet prefix.
func isFleetSandboxID(sandboxID string) bool {
	return strings.HasPrefix(sandboxID, fleet.SandboxIDPrefix)
}

// PauseSession pauses a session's sandbox. For agent-direct (lossy pause),
// the agent process is aborted first since EC2 stop kills all processes.
func (o *Orchestrator) PauseSession(ctx context.Context, sessionID string) error {
	entry, err := o.getSessionEntry(sessionID)
	if err != nil {
		return err
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.state != sessionActive {
		return rpc.NewRPCError(rpc.CodeFailedPrecondition, fmt.Sprintf("session %q is %s, not active", sessionID, entry.state), nil)
	}
	if entry.sandboxControl == nil {
		return rpc.NewRPCError(rpc.CodeInternal, "session has no sandbox control", nil)
	}

	// For lossy-pause providers (agent-direct), abort the active turn first.
	caps := entry.sandboxControl.Capabilities()
	if caps.Pause && entry.placement == PlacementAgentDirect {
		_, _ = entry.agentService.Abort(ctx, &agentapi.AbortRequest{
			SessionID: entry.remoteSessionID,
			Reason:    "pause: lossy sandbox pause requires agent stop",
		})
	}

	if err := entry.sandboxControl.PauseSandbox(ctx, entry.sandboxID); err != nil {
		return err
	}
	entry.state = sessionPaused
	return nil
}

// ResumeSessionSandbox resumes a paused session. For agent-direct (lossy),
// re-launches the agent process and reconnects the agent service.
func (o *Orchestrator) ResumeSessionSandbox(ctx context.Context, sessionID string) error {
	entry, err := o.getSessionEntry(sessionID)
	if err != nil {
		return err
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.state != sessionPaused {
		return rpc.NewRPCError(rpc.CodeFailedPrecondition, fmt.Sprintf("session %q is %s, not paused", sessionID, entry.state), nil)
	}
	if entry.sandboxControl == nil {
		return rpc.NewRPCError(rpc.CodeInternal, "session has no sandbox control", nil)
	}

	if err := entry.sandboxControl.ResumeSandbox(ctx, entry.sandboxID); err != nil {
		return err
	}

	// For agent-direct (lossy pause), re-launch agent and reconnect.
	if entry.placement == PlacementAgentDirect {
		launchResp, err := entry.sandboxControl.LaunchProcess(ctx, control.LaunchProcessRequest{
			SandboxID:  entry.sandboxID,
			Binary:     o.config.AgentBinary,
			Args:       o.agentLaunchArgs(),
			Env:        cloneStringMap(o.config.AgentEnv),
			ExposePort: o.config.AgentPort,
		})
		if err != nil {
			return fmt.Errorf("re-launch agent after resume: %w", err)
		}

		agentService, err := o.config.AgentServiceFactory(launchResp.Address)
		if err != nil {
			// Compensate: kill the orphaned process we just launched.
			_ = entry.sandboxControl.KillProcess(ctx, control.KillProcessRequest{
				SandboxID: entry.sandboxID,
				ProcessID: launchResp.ProcessID,
			})
			return fmt.Errorf("reconnect agent after resume: %w", err)
		}
		if entry.agentService != nil {
			_ = entry.agentService.Close()
		}
		entry.processID = launchResp.ProcessID
		entry.agentService = agentService
	}

	entry.state = sessionActive
	entry.lastHealth = time.Now()
	return nil
}

func (o *Orchestrator) GetSession(ctx context.Context, req *agentapi.GetAgentSessionRequest) (*agentapi.GetAgentSessionResponse, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	entry, err := o.getSessionEntry(req.SessionID)
	if err != nil {
		return nil, err
	}
	backendReq := *req
	backendReq.SessionID = entry.remoteSessionID
	resp, err := entry.agentService.GetSession(ctx, &backendReq)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	out := *resp
	out.SessionID = entry.sessionID
	return &out, nil
}

func (o *Orchestrator) ListSessions(_ context.Context, _ *agentapi.ListAgentSessionsRequest) (*agentapi.ListAgentSessionsResponse, error) {
	entries := o.listSessionEntries()
	resp := &agentapi.ListAgentSessionsResponse{Sessions: make([]agentapi.AgentSessionSummary, 0, len(entries))}
	for _, entry := range entries {
		resp.Sessions = append(resp.Sessions, agentapi.AgentSessionSummary{
			SessionID: entry.sessionID,
			State:     string(entry.state),
			CreatedAt: entry.createdAt,
		})
	}
	return resp, nil
}

func (o *Orchestrator) SendMessage(ctx context.Context, req *agentapi.SendMessageRequest) (agentapi.EventReceiver, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	entry, err := o.getSessionEntry(req.SessionID)
	if err != nil {
		return nil, err
	}
	backendReq := *req
	backendReq.SessionID = entry.remoteSessionID
	recv, err := entry.agentService.SendMessage(ctx, &backendReq)
	if err != nil {
		return nil, err
	}
	return newSessionIDRewritingReceiver(recv, entry.sessionID), nil
}

func (o *Orchestrator) Continue(ctx context.Context, req *agentapi.ContinueRequest) (agentapi.EventReceiver, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	entry, err := o.getSessionEntry(req.SessionID)
	if err != nil {
		return nil, err
	}
	backendReq := *req
	backendReq.SessionID = entry.remoteSessionID
	recv, err := entry.agentService.Continue(ctx, &backendReq)
	if err != nil {
		return nil, err
	}
	return newSessionIDRewritingReceiver(recv, entry.sessionID), nil
}

func (o *Orchestrator) Steer(ctx context.Context, req *agentapi.SteerRequest) (*agentapi.SteerResponse, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	entry, err := o.getSessionEntry(req.SessionID)
	if err != nil {
		return nil, err
	}
	backendReq := *req
	backendReq.SessionID = entry.remoteSessionID
	return entry.agentService.Steer(ctx, &backendReq)
}

func (o *Orchestrator) FollowUp(ctx context.Context, req *agentapi.FollowUpRequest) (*agentapi.FollowUpResponse, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	entry, err := o.getSessionEntry(req.SessionID)
	if err != nil {
		return nil, err
	}
	backendReq := *req
	backendReq.SessionID = entry.remoteSessionID
	return entry.agentService.FollowUp(ctx, &backendReq)
}

func (o *Orchestrator) Abort(ctx context.Context, req *agentapi.AbortRequest) (*agentapi.AbortResponse, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	entry, err := o.getSessionEntry(req.SessionID)
	if err != nil {
		return nil, err
	}
	backendReq := *req
	backendReq.SessionID = entry.remoteSessionID
	return entry.agentService.Abort(ctx, &backendReq)
}

func (o *Orchestrator) SubscribeEvents(ctx context.Context, req *agentapi.SubscribeEventsRequest) (agentapi.EventReceiver, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	entry, err := o.getSessionEntry(req.SessionID)
	if err != nil {
		return nil, err
	}
	backendReq := *req
	backendReq.SessionID = entry.remoteSessionID
	recv, err := entry.agentService.SubscribeEvents(ctx, &backendReq)
	if err != nil {
		return nil, err
	}
	return newSessionIDRewritingReceiver(recv, entry.sessionID), nil
}

func (o *Orchestrator) ResumeSession(ctx context.Context, req *agentapi.ResumeSessionRequest) (*agentapi.ResumeSessionResponse, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	if err := o.beginOperation(); err != nil {
		return nil, err
	}
	defer o.endOperation()
	if strings.TrimSpace(req.SessionConfig.SessionID) == "" {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "resume session requires session_config.session_id", nil)
	}
	entry, err := o.getSessionEntry(req.SessionConfig.SessionID)
	if err != nil {
		return nil, err
	}
	backendReq := cloneResumeSessionRequest(req)
	backendReq.SessionConfig.SessionID = entry.remoteSessionID
	resp, err := entry.agentService.ResumeSession(ctx, &backendReq)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	out := *resp
	out.SessionID = entry.sessionID
	return &out, nil
}

func (o *Orchestrator) DestroySession(ctx context.Context, req *agentapi.DestroyAgentSessionRequest) (*agentapi.DestroyAgentSessionResponse, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	if err := o.beginOperation(); err != nil {
		return nil, err
	}
	defer o.endOperation()

	entry, err := o.getSessionEntry(req.SessionID)
	if err != nil {
		return nil, err
	}
	return o.destroySessionEntry(ctx, entry)
}

func (o *Orchestrator) destroySessionEntry(_ context.Context, entry *sessionEntry) (*agentapi.DestroyAgentSessionResponse, error) {
	entry.mu.Lock()
	defer entry.mu.Unlock()

	stepTimeout := o.config.ShutdownTimeout
	if stepTimeout <= 0 {
		stepTimeout = defaultShutdownTimeout
	}

	var errs []error

	remoteSessionID := entry.remoteSessionID
	if strings.TrimSpace(remoteSessionID) == "" {
		remoteSessionID = entry.sessionID
	}

	if entry.agentService != nil {
		stepCtx, cancel := context.WithTimeout(context.Background(), stepTimeout)
		_, err := entry.agentService.DestroySession(stepCtx, &agentapi.DestroyAgentSessionRequest{SessionID: remoteSessionID})
		cancel()
		if err != nil && !isIgnorableTeardownErr(err) {
			errs = append(errs, err)
		}
	}

	if entry.processID != "" && entry.sandboxControl != nil {
		stepCtx, cancel := context.WithTimeout(context.Background(), stepTimeout)
		err := entry.sandboxControl.KillProcess(stepCtx, control.KillProcessRequest{SandboxID: entry.sandboxID, ProcessID: entry.processID})
		cancel()
		if err != nil && !isIgnorableTeardownErr(err) {
			errs = append(errs, err)
		}
	}

	if entry.sandboxID != "" && entry.sandboxControl != nil {
		stepCtx, cancel := context.WithTimeout(context.Background(), stepTimeout)
		err := entry.sandboxControl.DestroySandbox(stepCtx, entry.sandboxID)
		cancel()
		if err != nil && !isIgnorableTeardownErr(err) {
			errs = append(errs, err)
		}
	}

	if entry.agentService != nil {
		if err := entry.agentService.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	entry.state = sessionDestroyed
	o.removeSessionEntry(entry.sessionID)

	return &agentapi.DestroyAgentSessionResponse{}, errors.Join(errs...)
}

func (o *Orchestrator) agentLaunchArgs() []string {
	if len(o.config.AgentArgs) > 0 {
		return append([]string(nil), o.config.AgentArgs...)
	}
	return []string{"serve", "agent", "--listen", fmt.Sprintf(":%d", o.config.AgentPort)}
}

func isIgnorableTeardownErr(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	var rpcErr *rpc.RPCError
	if errors.As(err, &rpcErr) {
		switch rpcErr.Code {
		case rpc.CodeNotFound, rpc.CodeFailedPrecondition:
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "not found") || strings.Contains(msg, "already") {
		return true
	}
	return false
}

type sessionIDRewritingReceiver struct {
	inner           agentapi.EventReceiver
	clientSessionID string
}

func newSessionIDRewritingReceiver(inner agentapi.EventReceiver, clientSessionID string) agentapi.EventReceiver {
	return &sessionIDRewritingReceiver{inner: inner, clientSessionID: clientSessionID}
}

func (r *sessionIDRewritingReceiver) Recv() (*agentapi.AgentEvent, error) {
	evt, err := r.inner.Recv()
	if err != nil {
		return nil, err
	}
	if evt == nil {
		return nil, nil
	}
	out := *evt
	out.SessionID = r.clientSessionID
	return &out, nil
}

func (r *sessionIDRewritingReceiver) Close() error {
	if r.inner == nil {
		return nil
	}
	return r.inner.Close()
}

func cloneCreateSessionRequest(req *agentapi.CreateAgentSessionRequest) agentapi.CreateAgentSessionRequest {
	out := *req
	out.SessionConfig = cloneSessionConfig(req.SessionConfig)
	return out
}

func cloneResumeSessionRequest(req *agentapi.ResumeSessionRequest) agentapi.ResumeSessionRequest {
	out := *req
	out.SessionConfig = cloneSessionConfig(req.SessionConfig)
	out.ConversationLog = append([]agentapi.AgentMessageRecord(nil), req.ConversationLog...)
	return out
}

func cloneSessionConfig(cfg agentapi.SessionConfig) agentapi.SessionConfig {
	out := cfg
	out.Tools = append([]string(nil), cfg.Tools...)
	if len(cfg.Metadata) > 0 {
		out.Metadata = make(map[string]any, len(cfg.Metadata))
		for k, v := range cfg.Metadata {
			out.Metadata[k] = v
		}
	} else {
		out.Metadata = nil
	}
	out.ToolEnvironment = cfg.ToolEnvironment
	return out
}
