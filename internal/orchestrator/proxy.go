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
	case PlacementAgentSandbox, PlacementToolsSandbox:
		return nil, rpc.NewRPCError(rpc.CodeUnavailable, fmt.Sprintf("placement %q is not implemented in this bead", placement), nil)
	default:
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, fmt.Sprintf("unsupported placement %q", placement), nil)
	}
}

func (o *Orchestrator) createAgentDirectSession(ctx context.Context, entry *sessionEntry, req *agentapi.CreateAgentSessionRequest) (*agentapi.CreateAgentSessionResponse, error) {
	sc := o.config.DirectControl
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
		cleanupErr := joinErrors(
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
