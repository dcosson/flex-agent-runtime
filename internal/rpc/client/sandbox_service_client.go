package client

import (
	"context"
	"fmt"
	"io"

	"connectrpc.com/connect"
	"github.com/anthropics/flex-agent-runtime/internal/rpc/api"
	"github.com/anthropics/flex-agent-runtime/internal/rpc/transport"
)

// SandboxServiceClient implements api.SandboxService over ConnectRPC.
type SandboxServiceClient struct {
	service *transport.SandboxClient
}

func NewSandboxServiceClient(httpClient connect.HTTPClient, baseURL string, cfg transport.ClientConfig) *SandboxServiceClient {
	return &SandboxServiceClient{service: transport.NewSandboxClient(httpClient, baseURL, cfg)}
}

func (c *SandboxServiceClient) CreateSession(ctx context.Context, req *api.CreateSessionRequest) (*api.CreateSessionResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("sandbox rpc client not configured")
	}
	resp, err := c.service.CreateSession.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *SandboxServiceClient) GetSession(ctx context.Context, req *api.GetSessionRequest) (*api.GetSessionResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("sandbox rpc client not configured")
	}
	resp, err := c.service.GetSession.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *SandboxServiceClient) PauseSession(ctx context.Context, req *api.PauseSessionRequest) (*api.PauseSessionResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("sandbox rpc client not configured")
	}
	resp, err := c.service.PauseSession.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *SandboxServiceClient) ResumeSession(ctx context.Context, req *api.ResumeSessionRequest) (*api.ResumeSessionResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("sandbox rpc client not configured")
	}
	resp, err := c.service.ResumeSession.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *SandboxServiceClient) DestroySession(ctx context.Context, req *api.DestroySessionRequest) (*api.DestroySessionResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("sandbox rpc client not configured")
	}
	resp, err := c.service.DestroySession.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *SandboxServiceClient) LaunchProcess(ctx context.Context, req *api.LaunchProcessRequest) (*api.LaunchProcessResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("sandbox rpc client not configured")
	}
	resp, err := c.service.LaunchProcess.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *SandboxServiceClient) KillProcess(ctx context.Context, req *api.KillProcessRequest) (*api.KillProcessResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("sandbox rpc client not configured")
	}
	resp, err := c.service.KillProcess.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *SandboxServiceClient) GetProcessStatus(ctx context.Context, req *api.GetProcessStatusRequest) (*api.GetProcessStatusResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("sandbox rpc client not configured")
	}
	resp, err := c.service.GetProcessStatus.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *SandboxServiceClient) ExecuteTool(ctx context.Context, req *api.ExecuteToolRequest) (*api.ExecuteToolResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("sandbox rpc client not configured")
	}
	resp, err := c.service.ExecuteTool.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *SandboxServiceClient) ExecuteToolStream(ctx context.Context, req *api.ExecuteToolRequest) (api.ExecuteToolStreamReceiver, error) {
	if c.service == nil {
		return nil, fmt.Errorf("sandbox rpc client not configured")
	}
	stream, err := c.service.ExecuteStream.CallServerStream(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return &connectExecuteToolStreamReceiver{stream: stream}, nil
}

func (c *SandboxServiceClient) TurnComplete(ctx context.Context, req *api.TurnCompleteRequest) (*api.TurnCompleteResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("sandbox rpc client not configured")
	}
	resp, err := c.service.TurnComplete.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *SandboxServiceClient) CreateSnapshot(ctx context.Context, req *api.CreateSnapshotRequest) (*api.CreateSnapshotResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("sandbox rpc client not configured")
	}
	resp, err := c.service.CreateSnapshot.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *SandboxServiceClient) RollbackSession(ctx context.Context, req *api.RollbackSessionRequest) (*api.RollbackSessionResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("sandbox rpc client not configured")
	}
	resp, err := c.service.Rollback.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *SandboxServiceClient) ListSnapshots(ctx context.Context, req *api.ListSnapshotsRequest) (*api.ListSnapshotsResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("sandbox rpc client not configured")
	}
	resp, err := c.service.ListSnapshots.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

func (c *SandboxServiceClient) HealthCheck(ctx context.Context, req *api.HealthCheckRequest) (*api.HealthCheckResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("sandbox rpc client not configured")
	}
	resp, err := c.service.HealthCheck.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fromConnectError(err)
	}
	return resp.Msg, nil
}

var _ api.SandboxService = (*SandboxServiceClient)(nil)

type connectExecuteToolStreamReceiver struct {
	stream *connect.ServerStreamForClient[api.ExecuteToolStreamMessage]
}

func (r *connectExecuteToolStreamReceiver) Recv() (*api.ExecuteToolStreamMessage, error) {
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

func (r *connectExecuteToolStreamReceiver) Close() error {
	if r.stream == nil {
		return nil
	}
	if err := r.stream.Close(); err != nil {
		return fromConnectError(err)
	}
	return nil
}
