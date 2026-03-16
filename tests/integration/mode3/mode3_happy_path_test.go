package mode3

import (
	"strings"
	"testing"
	"time"

	mh "flex-agent-runtime/tests/integration/mode3/harness"
	"flex-agent-runtime/tests/integration/testutil"
)

// S1: Happy-path remote workflow (RPC dispatch, multi-turn).
func TestMode3_S1HappyPathRemoteWorkflow(t *testing.T) {
	env := mh.NewRemoteEnv(t, []testutil.ScriptEntry{
		{ToolCalls: []testutil.ToolCallSpec{{ID: "tc-1", Name: "read_file", Arguments: map[string]any{"path": "notes.txt"}}}},
		{ToolCalls: []testutil.ToolCallSpec{{ID: "tc-2", Name: "write_file", Arguments: map[string]any{"path": "notes.txt", "content": "updated remotely"}}}},
		{Text: "done"},
	}, map[string]string{"notes.txt": "initial"})

	env.PromptAndWait(t, "read and update the notes file", 5*time.Second)

	got, ok := env.Service.ReadSessionFile(env.SessionID, "notes.txt")
	if !ok {
		t.Fatal("expected notes.txt to exist")
	}
	if !strings.Contains(got, "updated remotely") {
		t.Fatalf("unexpected file content: %q", got)
	}

	routes := env.Service.Routes()
	if len(routes) < 2 {
		t.Fatalf("expected >=2 routed tool calls, got %d", len(routes))
	}
	if env.Service.ExecuteCalls() != int64(len(routes)) {
		t.Fatalf("execute call mismatch: calls=%d routes=%d", env.Service.ExecuteCalls(), len(routes))
	}
	if env.Provider.Calls() < 2 {
		t.Fatalf("expected multi-turn provider calls, got %d", env.Provider.Calls())
	}
}
