package mode3

import (
	"context"
	"errors"
	"testing"

	"github.com/anthropics/flex-agent-runtime/internal/rpc"
	"github.com/anthropics/flex-agent-runtime/internal/tools"
	mh "github.com/anthropics/flex-agent-runtime/tests/integration/mode3/harness"
	"github.com/anthropics/flex-agent-runtime/tests/integration/testutil"
)

// S4: Pause/resume continuity (identity + workspace).
func TestMode3_S4PauseResumeContinuity(t *testing.T) {
	env := mh.NewRemoteEnv(t, []testutil.ScriptEntry{{Text: "noop"}}, map[string]string{"resume.txt": "before"})

	if err := env.Lifecycle.Pause(context.Background(), env.SessionID); err != nil {
		t.Fatalf("pause: %v", err)
	}

	_, err := env.Client.ExecuteTool(context.Background(), env.SessionID, tools.ToolRequest{
		ToolCallID: "paused-read",
		ToolName:   "read_file",
		Params:     map[string]any{"path": "resume.txt"},
	}, nil)
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeFailedPrecondition {
		t.Fatalf("expected failed_precondition while paused, got %v", err)
	}

	if err := env.Lifecycle.Resume(context.Background(), env.SessionID); err != nil {
		t.Fatalf("resume: %v", err)
	}
	_, err = env.Client.ExecuteTool(context.Background(), env.SessionID, tools.ToolRequest{
		ToolCallID: "resumed-write",
		ToolName:   "write_file",
		Params:     map[string]any{"path": "resume.txt", "content": "after"},
	}, nil)
	if err != nil {
		t.Fatalf("write after resume: %v", err)
	}

	sess, err := env.Lifecycle.Get(context.Background(), env.SessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.ID != env.SessionID {
		t.Fatalf("session identity changed: got=%s want=%s", sess.ID, env.SessionID)
	}
	mh.SnapshotAssertion{}.AssertFileContent(t, env.Service, env.SessionID, "resume.txt", "after")
}
