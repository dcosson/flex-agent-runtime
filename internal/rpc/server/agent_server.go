package server

import (
	"context"

	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/codec"
)

// AgentRPCServer adapts agentapi.AgentService for the RPC transport layer.
type AgentRPCServer struct {
	service agentapi.AgentService
}

func NewAgentRPCServer(service agentapi.AgentService) *AgentRPCServer {
	return &AgentRPCServer{service: service}
}

func (s *AgentRPCServer) CreateSession(ctx context.Context, req *agentapi.CreateAgentSessionRequest) (*agentapi.CreateAgentSessionResponse, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	service, err := s.requireService()
	if err != nil {
		return nil, err
	}
	resp, callErr := service.CreateSession(ctx, req)
	if callErr != nil {
		return nil, rpc.MapError(callErr)
	}
	if resp == nil {
		return nil, nil
	}
	out := *resp
	return &out, nil
}

func (s *AgentRPCServer) GetSession(ctx context.Context, req *agentapi.GetAgentSessionRequest) (*agentapi.GetAgentSessionResponse, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	service, err := s.requireService()
	if err != nil {
		return nil, err
	}
	resp, callErr := service.GetSession(ctx, req)
	if callErr != nil {
		return nil, rpc.MapError(callErr)
	}
	if resp == nil {
		return nil, nil
	}
	out := *resp
	out.Metrics = codec.CopySessionMetrics(resp.Metrics)
	return &out, nil
}

func (s *AgentRPCServer) ListSessions(ctx context.Context, req *agentapi.ListAgentSessionsRequest) (*agentapi.ListAgentSessionsResponse, error) {
	service, err := s.requireService()
	if err != nil {
		return nil, err
	}
	resp, callErr := service.ListSessions(ctx, req)
	if callErr != nil {
		return nil, rpc.MapError(callErr)
	}
	if resp == nil {
		return nil, nil
	}
	out := *resp
	out.Sessions = append([]agentapi.AgentSessionSummary(nil), resp.Sessions...)
	return &out, nil
}

func (s *AgentRPCServer) SendMessage(ctx context.Context, req *agentapi.SendMessageRequest) (api.AgentEventReceiver, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	service, err := s.requireService()
	if err != nil {
		return nil, err
	}
	recv, callErr := service.SendMessage(ctx, req)
	if callErr != nil {
		return nil, rpc.MapError(callErr)
	}
	return codec.WrapEventReceiver(req.SessionID, recv), nil
}

func (s *AgentRPCServer) Continue(ctx context.Context, req *agentapi.ContinueRequest) (api.AgentEventReceiver, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	service, err := s.requireService()
	if err != nil {
		return nil, err
	}
	recv, callErr := service.Continue(ctx, req)
	if callErr != nil {
		return nil, rpc.MapError(callErr)
	}
	return codec.WrapEventReceiver(req.SessionID, recv), nil
}

func (s *AgentRPCServer) Steer(ctx context.Context, req *agentapi.SteerRequest) (*agentapi.SteerResponse, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	service, err := s.requireService()
	if err != nil {
		return nil, err
	}
	resp, callErr := service.Steer(ctx, req)
	if callErr != nil {
		return nil, rpc.MapError(callErr)
	}
	if resp == nil {
		return &agentapi.SteerResponse{}, nil
	}
	out := *resp
	return &out, nil
}

func (s *AgentRPCServer) FollowUp(ctx context.Context, req *agentapi.FollowUpRequest) (*agentapi.FollowUpResponse, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	service, err := s.requireService()
	if err != nil {
		return nil, err
	}
	resp, callErr := service.FollowUp(ctx, req)
	if callErr != nil {
		return nil, rpc.MapError(callErr)
	}
	if resp == nil {
		return &agentapi.FollowUpResponse{}, nil
	}
	out := *resp
	return &out, nil
}

func (s *AgentRPCServer) Abort(ctx context.Context, req *agentapi.AbortRequest) (*agentapi.AbortResponse, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	service, err := s.requireService()
	if err != nil {
		return nil, err
	}
	resp, callErr := service.Abort(ctx, req)
	if callErr != nil {
		return nil, rpc.MapError(callErr)
	}
	if resp == nil {
		return &agentapi.AbortResponse{}, nil
	}
	out := *resp
	return &out, nil
}

func (s *AgentRPCServer) SubscribeEvents(ctx context.Context, req *agentapi.SubscribeEventsRequest) (api.AgentEventReceiver, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	service, err := s.requireService()
	if err != nil {
		return nil, err
	}
	recv, callErr := service.SubscribeEvents(ctx, req)
	if callErr != nil {
		return nil, rpc.MapError(callErr)
	}
	return codec.WrapEventReceiver(req.SessionID, recv), nil
}

func (s *AgentRPCServer) ResumeSession(ctx context.Context, req *agentapi.ResumeSessionRequest) (*agentapi.ResumeSessionResponse, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	service, err := s.requireService()
	if err != nil {
		return nil, err
	}
	resp, callErr := service.ResumeSession(ctx, req)
	if callErr != nil {
		return nil, rpc.MapError(callErr)
	}
	if resp == nil {
		return nil, nil
	}
	out := *resp
	out.Warnings = append([]string(nil), resp.Warnings...)
	return &out, nil
}

func (s *AgentRPCServer) DestroySession(ctx context.Context, req *agentapi.DestroyAgentSessionRequest) (*agentapi.DestroyAgentSessionResponse, error) {
	if req == nil {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "nil request", nil)
	}
	service, err := s.requireService()
	if err != nil {
		return nil, err
	}
	resp, callErr := service.DestroySession(ctx, req)
	if callErr != nil {
		return nil, rpc.MapError(callErr)
	}
	if resp == nil {
		return &agentapi.DestroyAgentSessionResponse{}, nil
	}
	out := *resp
	return &out, nil
}

func (s *AgentRPCServer) Close() error {
	if s.service == nil {
		return nil
	}
	return s.service.Close()
}

func (s *AgentRPCServer) requireService() (agentapi.AgentService, error) {
	if s.service == nil {
		return nil, rpc.NewRPCError(rpc.CodeUnavailable, "agent service not configured", nil)
	}
	return s.service, nil
}
