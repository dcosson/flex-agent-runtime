package mode3

import (
	"context"
	"errors"
	"testing"

	"flex-agent-runtime/internal/ai"
	"flex-agent-runtime/internal/rpc"
	"flex-agent-runtime/internal/tools"
	mh "flex-agent-runtime/tests/integration/mode3/harness"
	"flex-agent-runtime/tests/integration/testutil"
)

// S5: Session recovery/destroy semantics.
func TestMode3_S5SessionRecoveryDestroySemantics(t *testing.T) {
	env := mh.NewRemoteEnv(t, []testutil.ScriptEntry{{Text: "noop"}}, map[string]string{"recover.txt": "seed"})

	_, err := env.Client.ExecuteTool(context.Background(), env.SessionID, tools.ToolRequest{
		ToolCallID: "mutate",
		ToolName:   "write_file",
		Params:     map[string]any{"path": "recover.txt", "content": "changed"},
	}, nil)
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}

	if err := env.Lifecycle.Destroy(context.Background(), env.SessionID); err != nil {
		t.Fatalf("destroy: %v", err)
	}

	_, err = env.Client.ExecuteTool(context.Background(), env.SessionID, tools.ToolRequest{
		ToolCallID: "after-destroy",
		ToolName:   "read_file",
		Params:     map[string]any{"path": "recover.txt"},
	}, nil)
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeNotFound {
		t.Fatalf("expected not_found after destroy, got %v", err)
	}

	newSession := env.SessionID + "-new"
	if _, err := env.Lifecycle.Create(context.Background(), env.BaseSnapshot, newSession); err != nil {
		t.Fatalf("recreate session: %v", err)
	}
	resp, err := env.Client.ExecuteTool(context.Background(), newSession, tools.ToolRequest{
		ToolCallID: "read-clean",
		ToolName:   "read_file",
		Params:     map[string]any{"path": "recover.txt"},
	}, nil)
	if err != nil {
		t.Fatalf("read clean session: %v", err)
	}
	if len(resp.Content) == 0 {
		t.Fatal("missing read response content")
	}
	text, ok := resp.Content[0].(*ai.TextContent)
	if !ok {
		t.Fatalf("unexpected content block type: %T", resp.Content[0])
	}
	if text.Text != "seed" {
		t.Fatalf("expected clean base content, got %q", text.Text)
	}
}
