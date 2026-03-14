package mode3

import (
	"context"
	"testing"

	mh "h2-agent-runtime/e2etests/mode3/harness"
	"h2-agent-runtime/e2etests/testutil"
	"h2-agent-runtime/internal/rpc/api"
	"h2-agent-runtime/internal/tools"
)

// S3: Rollback correctness (state restoration).
func TestMode3_S3RollbackCorrectness(t *testing.T) {
	env := mh.NewRemoteEnv(t, []testutil.ScriptEntry{{Text: "noop"}}, map[string]string{"state.txt": "v1"})

	_, err := env.Client.ExecuteTool(context.Background(), env.SessionID, tools.ToolRequest{
		ToolCallID: "w1",
		ToolName:   "write_file",
		Params:     map[string]any{"path": "state.txt", "content": "v2"},
	}, nil)
	if err != nil {
		t.Fatalf("write v2: %v", err)
	}
	v2Snap, err := env.Service.CreateSnapshot(context.Background(), &api.CreateSnapshotRequest{SessionID: env.SessionID, Name: "snap-v2"})
	if err != nil {
		t.Fatalf("snapshot v2: %v", err)
	}

	_, err = env.Client.ExecuteTool(context.Background(), env.SessionID, tools.ToolRequest{
		ToolCallID: "w2",
		ToolName:   "write_file",
		Params:     map[string]any{"path": "state.txt", "content": "v3"},
	}, nil)
	if err != nil {
		t.Fatalf("write v3: %v", err)
	}

	_, err = env.Service.RollbackSession(context.Background(), &api.RollbackSessionRequest{SessionID: env.SessionID, SnapshotID: v2Snap.SnapshotID})
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}

	mh.SnapshotAssertion{}.AssertFileContent(t, env.Service, env.SessionID, "state.txt", "v2")
}
