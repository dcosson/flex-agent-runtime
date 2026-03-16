package client

import (
	"context"
	"errors"
	"io"
	"testing"

	"flex-agent-runtime/internal/ai"
	"flex-agent-runtime/internal/rpc"
	"flex-agent-runtime/internal/rpc/api"
	"flex-agent-runtime/internal/tools"
)

type fakeStream struct {
	msgs   []*api.ExecuteToolStreamMessage
	closed bool
}

func (s *fakeStream) Recv() (*api.ExecuteToolStreamMessage, error) {
	if len(s.msgs) == 0 {
		return nil, io.EOF
	}
	m := s.msgs[0]
	s.msgs = s.msgs[1:]
	return m, nil
}

func (s *fakeStream) Close() error {
	s.closed = true
	return nil
}

type fakeSandboxService struct {
	stream api.ExecuteToolStreamReceiver
	err    error
}

func (f *fakeSandboxService) CreateSession(context.Context, *api.CreateSessionRequest) (*api.CreateSessionResponse, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeSandboxService) GetSession(context.Context, *api.GetSessionRequest) (*api.GetSessionResponse, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeSandboxService) PauseSession(context.Context, *api.PauseSessionRequest) (*api.PauseSessionResponse, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeSandboxService) ResumeSession(context.Context, *api.ResumeSessionRequest) (*api.ResumeSessionResponse, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeSandboxService) DestroySession(context.Context, *api.DestroySessionRequest) (*api.DestroySessionResponse, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeSandboxService) ExecuteTool(context.Context, *api.ExecuteToolRequest) (*api.ExecuteToolResponse, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeSandboxService) ExecuteToolStream(context.Context, *api.ExecuteToolRequest) (api.ExecuteToolStreamReceiver, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.stream, nil
}
func (f *fakeSandboxService) TurnComplete(context.Context, *api.TurnCompleteRequest) (*api.TurnCompleteResponse, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeSandboxService) CreateSnapshot(context.Context, *api.CreateSnapshotRequest) (*api.CreateSnapshotResponse, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeSandboxService) RollbackSession(context.Context, *api.RollbackSessionRequest) (*api.RollbackSessionResponse, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeSandboxService) ListSnapshots(context.Context, *api.ListSnapshotsRequest) (*api.ListSnapshotsResponse, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeSandboxService) HealthCheck(context.Context, *api.HealthCheckRequest) (*api.HealthCheckResponse, error) {
	return nil, errors.New("not implemented")
}

func TestSandboxClientExecuteToolStream(t *testing.T) {
	exitCode := 0
	service := &fakeSandboxService{stream: &fakeStream{msgs: []*api.ExecuteToolStreamMessage{
		{Progress: &api.ToolProgress{Content: "partial", IsError: false}},
		{Response: &api.ExecuteToolResponse{
			ToolCallID:    "tc1",
			Content:       "final",
			ContentBlocks: []api.ContentBlock{{Type: "text", Text: "final"}},
			SnapshotID:    "snap-1",
			ExitCode:      &exitCode,
		}},
	}}}
	client := NewSandboxClient(service)

	progressCalled := false
	resp, err := client.ExecuteTool(context.Background(), "sess-1", tools.ToolRequest{
		ToolCallID: "tc1",
		ToolName:   "bash",
		Params:     map[string]any{"cmd": "echo hi"},
	}, func(p tools.ToolProgress) {
		if p.Content == "partial" {
			progressCalled = true
		}
	})
	if err != nil {
		t.Fatalf("ExecuteTool error = %v", err)
	}
	if !progressCalled {
		t.Fatalf("expected progress callback")
	}
	if resp.SnapshotID != "snap-1" || resp.ExitCode == nil || *resp.ExitCode != 0 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("expected one content block, got %d", len(resp.Content))
	}
	text, ok := resp.Content[0].(*ai.TextContent)
	if !ok || text.Text != "final" {
		t.Fatalf("expected text content block, got %#v", resp.Content[0])
	}
}

func TestSandboxClientToolCallIDMismatch(t *testing.T) {
	service := &fakeSandboxService{stream: &fakeStream{msgs: []*api.ExecuteToolStreamMessage{
		{Response: &api.ExecuteToolResponse{ToolCallID: "other", Content: "final"}},
	}}}
	client := NewSandboxClient(service)
	_, err := client.ExecuteTool(context.Background(), "sess-1", tools.ToolRequest{ToolCallID: "tc1", ToolName: "bash"}, nil)
	if err == nil {
		t.Fatalf("expected mismatch error")
	}
}

func TestSandboxClientWrapsRPCErrorWithContext(t *testing.T) {
	service := &fakeSandboxService{err: rpc.NewRPCError(rpc.CodeUnavailable, "host down", nil)}
	client := NewSandboxClient(service)
	_, err := client.ExecuteTool(context.Background(), "sess-1", tools.ToolRequest{ToolCallID: "tc1", ToolName: "bash"}, nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected rpc error, got %T", err)
	}
	if rpcErr.Details["session_id"] != "sess-1" || rpcErr.Details["tool_name"] != "bash" {
		t.Fatalf("missing details in wrapped error: %+v", rpcErr.Details)
	}
}
