package codec

import (
	"reflect"
	"testing"
	"time"

	"flex-agent-runtime/internal/ai"
	"flex-agent-runtime/internal/rpc/api"
	"flex-agent-runtime/internal/sandbox"
	"flex-agent-runtime/internal/sandbox/zfs"
	"flex-agent-runtime/internal/tools"
)

func TestToCreateSessionRequest(t *testing.T) {
	if got := ToCreateSessionRequest(nil); got.BaseSnapshot != "" || got.SessionID != "" || got.Quota != 0 || got.Labels != nil {
		t.Fatalf("nil request should map to zero-ish value, got %+v", got)
	}

	labels := map[string]string{"k": "v"}
	got := ToCreateSessionRequest(&api.CreateSessionRequest{BaseSnapshot: "base@1", SessionID: "s1", Quota: 42, Labels: labels})
	if got.BaseSnapshot != "base@1" || got.SessionID != "s1" || got.Quota != 42 || !reflect.DeepEqual(got.Labels, labels) {
		t.Fatalf("unexpected mapping: %+v", got)
	}
}

func TestFromSessionInfo(t *testing.T) {
	if FromSessionInfo(nil) != nil {
		t.Fatalf("nil should map to nil")
	}
	created := time.Unix(10, 0)
	in := &sandbox.SessionInfo{ID: "s1", State: sandbox.SessionActive, Mountpoint: "/m", TurnCount: 2, SnapCount: 3, Created: created, Labels: map[string]string{"a": "b"}, SpaceUsed: 99}
	out := FromSessionInfo(in)
	if out.ID != in.ID || out.State != string(in.State) || out.Mountpoint != in.Mountpoint || out.TurnCount != in.TurnCount || out.SnapCount != in.SnapCount || !out.Created.Equal(created) || out.SpaceUsed != in.SpaceUsed {
		t.Fatalf("bad session mapping: %+v", out)
	}
}

func TestToExecuteToolRequest(t *testing.T) {
	if got := ToExecuteToolRequest(nil); got.SessionID != "" || got.ToolCallID != "" || got.ToolName != "" || got.Params != nil || got.Resources != nil {
		t.Fatalf("nil request should map to zero-ish value, got %+v", got)
	}
	got := ToExecuteToolRequest(&api.ExecuteToolRequest{SessionID: "s1", ToolCallID: "tc1", ToolName: "bash", Params: map[string]any{"cmd": "echo hi"}, Resources: &api.ResourceSpec{CPUs: 0.5, MemMB: 128}})
	if got.SessionID != "s1" || got.ToolCallID != "tc1" || got.ToolName != "bash" || got.Resources == nil || got.Resources.CPUs != 0.5 || got.Resources.MemoryMB != 128 {
		t.Fatalf("bad execute request mapping: %+v", got)
	}
}

func TestFromExecuteToolResponse(t *testing.T) {
	exitCode := 0
	in := &sandbox.ExecuteToolResponse{Content: "done", ContentBlocks: []ai.ContentBlock{&ai.TextContent{Text: "done"}}, ExitCode: &exitCode, SnapshotID: "snap1", Tier: 2, Duration: 20 * time.Millisecond}
	req := &api.ExecuteToolRequest{SessionID: "s1", ToolCallID: "tc1", ToolName: "bash"}
	out := FromExecuteToolResponse(in, req)
	if out.SessionID != "s1" || out.ToolCallID != "tc1" || out.ToolName != "bash" || out.Content != "done" || out.SnapshotID != "snap1" || out.Tier != 2 || out.ExitCode == nil || *out.ExitCode != 0 {
		t.Fatalf("bad response mapping: %+v", out)
	}
	if len(out.ContentBlocks) != 1 || out.ContentBlocks[0].Type != "text" || out.ContentBlocks[0].Text != "done" {
		t.Fatalf("content blocks not preserved: %+v", out.ContentBlocks)
	}
}

func TestFromSnapshots(t *testing.T) {
	if out := FromSnapshots(nil); len(out) != 0 {
		t.Fatalf("nil snapshots should map to empty slice")
	}
	ts := time.Unix(123, 0)
	out := FromSnapshots([]zfs.SnapshotInfo{{Name: "turn-1", Dataset: "tank/s/s1", Used: 1, Refer: 2, Creation: ts, Holds: 1}})
	if len(out) != 1 || out[0].Name != "turn-1" || out[0].Dataset != "tank/s/s1" || !out[0].CreatedAt.Equal(ts) || out[0].Holds != 1 {
		t.Fatalf("bad snapshot mapping: %+v", out)
	}
}

func TestToToolRequest(t *testing.T) {
	out := ToToolRequest("s1", tools.ToolRequest{ToolCallID: "tc1", ToolName: "bash", Params: map[string]any{"cmd": "echo"}})
	if out.SessionID != "s1" || out.ToolCallID != "tc1" || out.Resources != nil {
		t.Fatalf("bad tool request mapping: %+v", out)
	}
	out = ToToolRequest("s1", tools.ToolRequest{ToolCallID: "tc2", ToolName: "bash", Resources: &tools.ResourceSpec{CPUs: 1, MemMB: 64}})
	if out.Resources == nil || out.Resources.CPUs != 1 || out.Resources.MemMB != 64 {
		t.Fatalf("resources not mapped: %+v", out)
	}
}

func TestContentBlockRoundTrip(t *testing.T) {
	in := []ai.ContentBlock{
		&ai.TextContent{Text: "txt", TextSignature: "sig"},
		&ai.ThinkingContent{Thinking: "hmm", ThinkingSignature: "tsig", Redacted: true},
		&ai.ImageContent{Data: "b64", MimeType: "image/png"},
		&ai.ToolCall{ID: "id1", Name: "tool", Arguments: map[string]any{"k": "v"}, ThoughtSignature: "psig"},
	}
	dto := ToAPIContentBlocks(in)
	if len(dto) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(dto))
	}
	out := FromAPIContentBlocks(dto)
	if len(out) != 4 {
		t.Fatalf("expected 4 blocks on decode, got %d", len(out))
	}
}
