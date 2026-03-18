package client

import (
	"context"
	"errors"
	"fmt"
	"io"

	"connectrpc.com/connect"
	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc"
	rpcapi "github.com/dcosson/flex-agent-runtime/internal/rpc/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/codec"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/transport"
)

// AgentServiceClient implements agentapi.AgentService over ConnectRPC.
type AgentServiceClient struct {
	service *transport.AgentClient
}

func NewAgentServiceClient(httpClient connect.HTTPClient, baseURL string, cfg transport.ClientConfig) *AgentServiceClient {
	return &AgentServiceClient{service: transport.NewAgentClient(httpClient, baseURL, cfg)}
}

func (c *AgentServiceClient) CreateSession(ctx context.Context, req *agentapi.CreateAgentSessionRequest) (*agentapi.CreateAgentSessionResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("agent rpc client not configured")
	}
	resp, err := c.service.CreateSession.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *AgentServiceClient) GetSession(ctx context.Context, req *agentapi.GetAgentSessionRequest) (*agentapi.GetAgentSessionResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("agent rpc client not configured")
	}
	resp, err := c.service.GetSession.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *AgentServiceClient) ListSessions(ctx context.Context, req *agentapi.ListAgentSessionsRequest) (*agentapi.ListAgentSessionsResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("agent rpc client not configured")
	}
	resp, err := c.service.ListSessions.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *AgentServiceClient) SendMessage(ctx context.Context, req *agentapi.SendMessageRequest) (agentapi.EventReceiver, error) {
	if c.service == nil {
		return nil, fmt.Errorf("agent rpc client not configured")
	}
	stream, err := c.service.SendMessage.CallServerStream(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return codec.UnwrapEventReceiver(&connectAgentEventReceiver{stream: stream}), nil
}

func (c *AgentServiceClient) Continue(ctx context.Context, req *agentapi.ContinueRequest) (agentapi.EventReceiver, error) {
	if c.service == nil {
		return nil, fmt.Errorf("agent rpc client not configured")
	}
	stream, err := c.service.Continue.CallServerStream(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return codec.UnwrapEventReceiver(&connectAgentEventReceiver{stream: stream}), nil
}

func (c *AgentServiceClient) Steer(ctx context.Context, req *agentapi.SteerRequest) (*agentapi.SteerResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("agent rpc client not configured")
	}
	resp, err := c.service.Steer.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *AgentServiceClient) FollowUp(ctx context.Context, req *agentapi.FollowUpRequest) (*agentapi.FollowUpResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("agent rpc client not configured")
	}
	resp, err := c.service.FollowUp.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *AgentServiceClient) Abort(ctx context.Context, req *agentapi.AbortRequest) (*agentapi.AbortResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("agent rpc client not configured")
	}
	resp, err := c.service.Abort.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *AgentServiceClient) SubscribeEvents(ctx context.Context, req *agentapi.SubscribeEventsRequest) (agentapi.EventReceiver, error) {
	if c.service == nil {
		return nil, fmt.Errorf("agent rpc client not configured")
	}
	stream, err := c.service.SubscribeEvents.CallServerStream(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return codec.UnwrapEventReceiver(&connectAgentEventReceiver{stream: stream}), nil
}

func (c *AgentServiceClient) ResumeSession(ctx context.Context, req *agentapi.ResumeSessionRequest) (*agentapi.ResumeSessionResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("agent rpc client not configured")
	}
	resp, err := c.service.ResumeSession.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *AgentServiceClient) DestroySession(ctx context.Context, req *agentapi.DestroyAgentSessionRequest) (*agentapi.DestroyAgentSessionResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("agent rpc client not configured")
	}
	resp, err := c.service.DestroySession.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *AgentServiceClient) Close() error { return nil }

var _ agentapi.AgentService = (*AgentServiceClient)(nil)

type connectAgentEventReceiver struct {
	stream *connect.ServerStreamForClient[rpcapi.AgentEventEnvelope]
}

func (r *connectAgentEventReceiver) Recv() (*rpcapi.AgentEventEnvelope, error) {
	if r.stream == nil {
		return nil, io.EOF
	}
	if !r.stream.Receive() {
		if err := r.stream.Err(); err != nil {
			return nil, fromConnectError(err)
		}
		return nil, io.EOF
	}
	return r.stream.Msg(), nil
}

func (r *connectAgentEventReceiver) Close() error {
	if r.stream == nil {
		return nil
	}
	if err := r.stream.Close(); err != nil {
		return fromConnectError(err)
	}
	return nil
}

func fromConnectError(err error) error {
	if err == nil {
		return nil
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return rpc.NewRPCError(rpc.CodeInternal, err.Error(), err)
	}
	return rpc.NewRPCError(connectCodeToRPCCode(connectErr.Code()), connectErr.Message(), err)
}

func connectCodeToRPCCode(code connect.Code) rpc.Code {
	switch code {
	case connect.CodeInvalidArgument:
		return rpc.CodeInvalidArgument
	case connect.CodeNotFound:
		return rpc.CodeNotFound
	case connect.CodeAlreadyExists:
		return rpc.CodeAlreadyExists
	case connect.CodeFailedPrecondition:
		return rpc.CodeFailedPrecondition
	case connect.CodePermissionDenied:
		return rpc.CodePermissionDenied
	case connect.CodeResourceExhausted:
		return rpc.CodeResourceExhausted
	case connect.CodeUnavailable:
		return rpc.CodeUnavailable
	case connect.CodeDeadlineExceeded:
		return rpc.CodeDeadlineExceeded
	case connect.CodeCanceled:
		return rpc.CodeCanceled
	default:
		return rpc.CodeInternal
	}
}
