package codec

import (
	"h2-agent-runtime/internal/rpc/api"
	"h2-agent-runtime/internal/sandbox"
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
		resources = &gvisor.ResourceSpec{CPUs: float64(req.Resources.CPUs), MemoryMB: req.Resources.MemMB}
	}
	return sandbox.ExecuteToolRequest{
		SessionID:  req.SessionID,
		ToolName:   req.ToolName,
		ToolCallID: req.ToolCallID,
		Params:     req.Params,
		Resources:  resources,
	}
}

func FromExecuteToolResponse(resp *sandbox.ExecuteToolResponse, req *api.ExecuteToolRequest) *api.ExecuteToolResponse {
	if resp == nil {
		return nil
	}
	return &api.ExecuteToolResponse{
		SessionID:  req.SessionID,
		ToolCallID: req.ToolCallID,
		ToolName:   req.ToolName,
		Content:    resp.Content,
		SnapshotID: resp.SnapshotID,
		ExitCode:   resp.ExitCode,
		Tier:       resp.Tier,
		Duration:   resp.Duration,
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
		apiReq.Resources = &api.ResourceSpec{CPUs: req.Resources.CPUs, MemMB: req.Resources.MemMB}
	}
	return apiReq
}
