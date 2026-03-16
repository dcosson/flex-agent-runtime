package mode3

import (
	"context"
	"testing"

	"flex-agent-runtime/internal/rpc/api"
	"flex-agent-runtime/internal/tools"
	mh "flex-agent-runtime/tests/integration/mode3/harness"
	"flex-agent-runtime/tests/integration/testutil"
)

// S2: Tier routing with snapshot metadata (Tier 1+2 mix).
func TestMode3_S2TierRoutingWithSnapshotMetadata(t *testing.T) {
	env := mh.NewRemoteEnv(t, []testutil.ScriptEntry{{Text: "noop"}}, map[string]string{"app.txt": "hello"})

	_, err := env.Client.ExecuteTool(context.Background(), env.SessionID, tools.ToolRequest{
		ToolCallID: "tier1-read",
		ToolName:   "read_file",
		Params:     map[string]any{"path": "app.txt"},
	}, nil)
	if err != nil {
		t.Fatalf("tier1 execute: %v", err)
	}

	resp, err := env.Client.ExecuteTool(context.Background(), env.SessionID, tools.ToolRequest{
		ToolCallID: "tier2-bash",
		ToolName:   "bash",
		Params:     map[string]any{"cmd": "echo updated > app.txt"},
	}, nil)
	if err != nil {
		t.Fatalf("tier2 execute: %v", err)
	}
	if resp.SnapshotID == "" {
		t.Fatal("expected non-empty snapshot id for tier2 execution")
	}

	routes := env.Service.Routes()
	if len(routes) < 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
	if routes[0].Tier != 1 || routes[1].Tier != 2 {
		t.Fatalf("expected tier sequence [1,2], got [%d,%d]", routes[0].Tier, routes[1].Tier)
	}

	snaps, err := env.Service.ListSnapshots(context.Background(), &api.ListSnapshotsRequest{SessionID: env.SessionID})
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	mh.SnapshotAssertion{}.AssertMonotonicIDs(t, snaps.Snapshots)
}
