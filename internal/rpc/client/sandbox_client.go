package client

import (
	"context"
	"fmt"

	"h2-agent-runtime/internal/ai"
	"h2-agent-runtime/internal/rpc"
	"h2-agent-runtime/internal/rpc/api"
	"h2-agent-runtime/internal/rpc/codec"
	"h2-agent-runtime/internal/tools"
)

type SandboxClient struct {
	service api.SandboxService
}

func NewSandboxClient(service api.SandboxService) *SandboxClient {
	return &SandboxClient{service: service}
}

// ExecuteTool implements tools.SandboxToolClient via RPC ExecuteToolStream.
func (c *SandboxClient) ExecuteTool(ctx context.Context, sessionID string, req tools.ToolRequest, onProgress func(tools.ToolProgress)) (*tools.ToolResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("sandbox rpc client not configured")
	}
	rpcReq := codec.ToToolRequest(sessionID, req)
	if rpcReq.ToolCallID == "" {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "tool_call_id is required", nil)
	}
	stream, err := c.service.ExecuteToolStream(ctx, rpcReq)
	if err != nil {
		return nil, wrapClientError(err, sessionID, req.ToolName)
	}
	defer stream.Close()

	var final *api.ExecuteToolResponse
	for {
		msg, recvErr := stream.Recv()
		if recvErr != nil {
			if final != nil {
				break
			}
			return nil, wrapClientError(recvErr, sessionID, req.ToolName)
		}
		if msg == nil {
			continue
		}
		if msg.Progress != nil && onProgress != nil {
			onProgress(tools.ToolProgress{Content: msg.Progress.Content, IsError: msg.Progress.IsError})
		}
		if msg.Response != nil {
			final = msg.Response
			break
		}
	}
	if final == nil {
		return nil, wrapClientError(fmt.Errorf("missing final response"), sessionID, req.ToolName)
	}
	if final.ToolCallID != req.ToolCallID {
		return nil, rpc.NewRPCError(
			rpc.CodeInternal,
			fmt.Sprintf("tool_call_id mismatch: sent %q, received %q", req.ToolCallID, final.ToolCallID),
			nil,
		)
	}
	return &tools.ToolResponse{
		Content:    []ai.ContentBlock{&ai.TextContent{Text: final.Content}},
		SnapshotID: final.SnapshotID,
		ExitCode:   final.ExitCode,
	}, nil
}

func wrapClientError(err error, sessionID, toolName string) error {
	return rpc.NewRPCError(rpc.CodeInternal, err.Error(), err)
}

var _ tools.SandboxToolClient = (*SandboxClient)(nil)
