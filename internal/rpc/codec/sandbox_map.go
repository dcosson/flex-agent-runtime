package codec

import (
	"fmt"

	"h2-agent-runtime/internal/ai"
	"h2-agent-runtime/internal/rpc/api"
	"h2-agent-runtime/internal/sandbox"
	"h2-agent-runtime/internal/sandbox/environment"
	"h2-agent-runtime/internal/sandbox/gvisor"
	"h2-agent-runtime/internal/sandbox/zfs"
	"h2-agent-runtime/internal/tools"
)

func ToCreateSessionRequest(req *api.CreateSessionRequest) sandbox.CreateSessionRequest {
	if req == nil {
		return sandbox.CreateSessionRequest{}
	}
	return sandbox.CreateSessionRequest{
		BaseSnapshot: req.BaseSnapshot,
		SessionID:    req.SessionID,
		Quota:        req.Quota,
		Labels:       req.Labels,
	}
}

func FromEnvironmentCapabilities(c environment.Capabilities) api.Capabilities {
	return api.Capabilities{
		Snapshots:         c.Snapshots,
		Rollback:          c.Rollback,
		Pause:             c.Pause,
		StreamingProgress: c.StreamingProgress,
		TierRouting:       c.TierRouting,
	}
}

func FromSessionInfo(s *sandbox.SessionInfo) *api.Session {
	if s == nil {
		return nil
	}
	return &api.Session{
		ID:         s.ID,
		State:      string(s.State),
		Mountpoint: s.Mountpoint,
		TurnCount:  s.TurnCount,
		SnapCount:  s.SnapCount,
		Created:    s.Created,
		Labels:     s.Labels,
		SpaceUsed:  s.SpaceUsed,
	}
}

func ToExecuteToolRequest(req *api.ExecuteToolRequest) sandbox.ExecuteToolRequest {
	if req == nil {
		return sandbox.ExecuteToolRequest{}
	}
	var resources *gvisor.ResourceSpec
	if req.Resources != nil {
		resources = &gvisor.ResourceSpec{CPUs: req.Resources.CPUs, MemoryMB: req.Resources.MemMB}
	}
	return sandbox.ExecuteToolRequest{
		SessionID:  req.SessionID,
		ToolName:   req.ToolName,
		ToolCallID: req.ToolCallID,
		Params:     req.Params,
		Resources:  resources,
		OnProgress: nil,
	}
}

func FromExecuteToolResponse(resp *sandbox.ExecuteToolResponse, req *api.ExecuteToolRequest) *api.ExecuteToolResponse {
	if resp == nil {
		return nil
	}
	return &api.ExecuteToolResponse{
		SessionID:     req.SessionID,
		ToolCallID:    req.ToolCallID,
		ToolName:      req.ToolName,
		Content:       resp.Content,
		ContentBlocks: ToAPIContentBlocks(resp.ContentBlocks),
		SnapshotID:    resp.SnapshotID,
		ExitCode:      resp.ExitCode,
		Tier:          resp.Tier,
		Duration:      resp.Duration,
	}
}

func FromSnapshotResult(result *sandbox.SnapshotResult) *api.CreateSnapshotResponse {
	if result == nil {
		return nil
	}
	return &api.CreateSnapshotResponse{
		SnapshotID: result.SnapshotID,
		TurnNumber: result.TurnNumber,
		SpaceUsed:  result.SpaceUsed,
	}
}

func FromSnapshots(items []zfs.SnapshotInfo) []api.Snapshot {
	out := make([]api.Snapshot, 0, len(items))
	for _, s := range items {
		out = append(out, api.Snapshot{
			Name:      s.Name,
			Dataset:   s.Dataset,
			Used:      s.Used,
			Refer:     s.Refer,
			CreatedAt: s.Creation,
			Holds:     s.Holds,
		})
	}
	return out
}

func ToToolRequest(sessionID string, req tools.ToolRequest) *api.ExecuteToolRequest {
	apiReq := &api.ExecuteToolRequest{
		SessionID:  sessionID,
		ToolCallID: req.ToolCallID,
		ToolName:   req.ToolName,
		Params:     req.Params,
	}
	if req.Resources != nil {
		apiReq.Resources = &api.ResourceSpec{CPUs: float64(req.Resources.CPUs), MemMB: req.Resources.MemMB}
	}
	return apiReq
}

func ToAPIContentBlocks(blocks []ai.ContentBlock) []api.ContentBlock {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]api.ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		switch b := block.(type) {
		case *ai.TextContent:
			out = append(out, api.ContentBlock{Type: "text", Text: b.Text, TextSignature: b.TextSignature})
		case *ai.ThinkingContent:
			out = append(out, api.ContentBlock{
				Type:              "thinking",
				Thinking:          b.Thinking,
				ThinkingSignature: b.ThinkingSignature,
				Redacted:          b.Redacted,
			})
		case *ai.ImageContent:
			out = append(out, api.ContentBlock{Type: "image", ImageData: b.Data, ImageMimeType: b.MimeType})
		case *ai.ToolCall:
			out = append(out, api.ContentBlock{
				Type:              "tool_call",
				ToolCallID:        b.ID,
				ToolCallName:      b.Name,
				ToolCallArguments: b.Arguments,
				ThoughtSignature:  b.ThoughtSignature,
			})
		default:
			out = append(out, api.ContentBlock{Type: "text", Text: fmt.Sprintf("%T", b)})
		}
	}
	return out
}

func FromAPIContentBlocks(blocks []api.ContentBlock) []ai.ContentBlock {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]ai.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, &ai.TextContent{Text: b.Text, TextSignature: b.TextSignature})
		case "thinking":
			out = append(out, &ai.ThinkingContent{
				Thinking:          b.Thinking,
				ThinkingSignature: b.ThinkingSignature,
				Redacted:          b.Redacted,
			})
		case "image":
			out = append(out, &ai.ImageContent{Data: b.ImageData, MimeType: b.ImageMimeType})
		case "tool_call":
			out = append(out, &ai.ToolCall{
				ID:               b.ToolCallID,
				Name:             b.ToolCallName,
				Arguments:        b.ToolCallArguments,
				ThoughtSignature: b.ThoughtSignature,
			})
		default:
			out = append(out, &ai.TextContent{Text: b.Text})
		}
	}
	return out
}
