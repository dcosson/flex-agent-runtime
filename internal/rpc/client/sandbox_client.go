package client

import (
	"context"
	"fmt"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/rpc"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/codec"
	"github.com/dcosson/flex-agent-runtime/internal/tools"
)

type SandboxClient struct {
	service api.SandboxService
}

func NewSandboxClient(service api.SandboxService) *SandboxClient {
	return &SandboxClient{service: service}
}

// ExecuteTool dispatches a tool call via RPC ExecuteToolStream.
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
		Content:    decodeResponseContent(final),
		SnapshotID: final.SnapshotID,
		ExitCode:   final.ExitCode,
	}, nil
}

func wrapClientError(err error, sessionID, toolName string) error {
	return rpc.WrapRPCError(err, sessionID, toolName)
}

func decodeResponseContent(resp *api.ExecuteToolResponse) []ai.ContentBlock {
	if resp == nil {
		return nil
	}
	if len(resp.ContentBlocks) > 0 {
		return codec.FromAPIContentBlocks(resp.ContentBlocks)
	}
	return []ai.ContentBlock{&ai.TextContent{Text: resp.Content}}
}
