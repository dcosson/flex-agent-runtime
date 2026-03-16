package harness

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"flex-agent-runtime/internal/agent"
	"flex-agent-runtime/internal/ai"
	"flex-agent-runtime/internal/rpc"
	"flex-agent-runtime/internal/rpc/api"
	"flex-agent-runtime/internal/tools"
	"flex-agent-runtime/tests/integration/testutil"
)

func harnessTier() string {
	tier := strings.ToLower(strings.TrimSpace(os.Getenv("MODE3_HARNESS_TIER")))
	if tier == "" {
		return "pr-standard"
	}
	switch tier {
	case "pr-fast", "pr-standard", "nightly", "weekly":
		return tier
	default:
		return "pr-standard"
	}
}

func tierAllowed(cur string, allowed ...string) bool {
	for _, a := range allowed {
		if cur == a {
			return true
		}
	}
	return false
}

func requireTier(t *testing.T, allowed ...string) {
	t.Helper()
	if !tierAllowed(harnessTier(), allowed...) {
		t.Skipf("skipping for MODE3_HARNESS_TIER=%q; allowed=%v", harnessTier(), allowed)
	}
}

// P1. Lifecycle State Invariant
func TestP1_LifecycleStateInvariant(t *testing.T) {
	svc, sessionID := newTestMemoryService(t)
	ctx := context.Background()

	for i := 0; i < 25; i++ {
		switch i % 4 {
		case 0:
			_, _ = svc.PauseSession(ctx, &api.PauseSessionRequest{SessionID: sessionID})
		case 1:
			_, _ = svc.ResumeSession(ctx, &api.ResumeSessionRequest{SessionID: sessionID})
		case 2:
			_, _ = svc.PauseSession(ctx, &api.PauseSessionRequest{SessionID: sessionID})
			_, _ = svc.ResumeSession(ctx, &api.ResumeSessionRequest{SessionID: sessionID})
		case 3:
			_, _ = svc.GetSession(ctx, &api.GetSessionRequest{SessionID: sessionID})
		}
		state, err := svc.GetSession(ctx, &api.GetSessionRequest{SessionID: sessionID})
		if err != nil {
			t.Fatalf("get session: %v", err)
		}
		if state.Session.State != memoryStateActive && state.Session.State != memoryStatePaused {
			t.Fatalf("illegal state reached: %s", state.Session.State)
		}
	}
}

// P2. Snapshot Monotonicity
func TestP2_SnapshotMonotonicity(t *testing.T) {
	svc, sessionID := newTestMemoryService(t)
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		_, err := svc.ExecuteTool(ctx, &api.ExecuteToolRequest{
			SessionID:  sessionID,
			ToolCallID: fmt.Sprintf("tc-%d", i),
			ToolName:   "bash",
			Params:     map[string]any{"cmd": fmt.Sprintf("echo v%d > state.txt", i)},
		})
		if err != nil {
			t.Fatalf("execute tier2: %v", err)
		}
	}
	snaps, err := svc.ListSnapshots(ctx, &api.ListSnapshotsRequest{SessionID: sessionID})
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	SnapshotAssertion{}.AssertMonotonicIDs(t, snaps.Snapshots)
}

// P3. Rollback Consistency
func TestP3_RollbackConsistency(t *testing.T) {
	svc, sessionID := newTestMemoryService(t)
	ctx := context.Background()

	_, _ = svc.ExecuteTool(ctx, &api.ExecuteToolRequest{SessionID: sessionID, ToolCallID: "w1", ToolName: "write_file", Params: map[string]any{"path": "state.txt", "content": "alpha"}})
	s1, err := svc.CreateSnapshot(ctx, &api.CreateSnapshotRequest{SessionID: sessionID, Name: "s1"})
	if err != nil {
		t.Fatalf("create snapshot s1: %v", err)
	}
	hashAtS1 := hashFiles(t, svc, sessionID, []string{"state.txt"})

	_, _ = svc.ExecuteTool(ctx, &api.ExecuteToolRequest{SessionID: sessionID, ToolCallID: "w2", ToolName: "write_file", Params: map[string]any{"path": "state.txt", "content": "beta"}})
	if _, err := svc.RollbackSession(ctx, &api.RollbackSessionRequest{SessionID: sessionID, SnapshotID: s1.SnapshotID}); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	hashAfterRollback := hashFiles(t, svc, sessionID, []string{"state.txt"})
	if hashAfterRollback != hashAtS1 {
		t.Fatalf("rollback hash mismatch: got=%s want=%s", hashAfterRollback, hashAtS1)
	}
}

// P4. Remote Dispatch Integrity
func TestP4_RemoteDispatchIntegrity(t *testing.T) {
	env := NewRemoteEnv(t, []testutil.ScriptEntry{
		{ToolCalls: []testutil.ToolCallSpec{{ID: "d1", Name: "read_file", Arguments: map[string]any{"path": "state.txt"}}}, StopReason: ai.StopReasonToolUse},
		{ToolCalls: []testutil.ToolCallSpec{{ID: "d2", Name: "write_file", Arguments: map[string]any{"path": "state.txt", "content": "updated"}}}, StopReason: ai.StopReasonToolUse},
		{Text: "done", StopReason: ai.StopReasonStop},
	}, map[string]string{"state.txt": "base"})

	env.PromptAndWait(t, "read then write state file", 5*time.Second)

	routes := env.Service.Routes()
	started := eventToolCallIDs(env.Events(), agent.EventToolStarted)
	if len(routes) != len(started) {
		t.Fatalf("dispatch mismatch routes=%d started=%d", len(routes), len(started))
	}
	for i, r := range routes {
		if r.ToolCallID != started[i] {
			t.Fatalf("tool_call_id mismatch at %d: route=%s event=%s", i, r.ToolCallID, started[i])
		}
	}
}

// P5. Event/RPC Correlation
func TestP5_EventRPCCorrelation(t *testing.T) {
	env := NewRemoteEnv(t, []testutil.ScriptEntry{
		{ToolCalls: []testutil.ToolCallSpec{{ID: "c1", Name: "read_file", Arguments: map[string]any{"path": "state.txt"}}, {ID: "c2", Name: "bash", Arguments: map[string]any{"cmd": "echo hi > state.txt"}}}},
		{Text: "done"},
	}, map[string]string{"state.txt": "base"})
	env.PromptAndWait(t, "run two tools", 5*time.Second)

	routes := env.Service.Routes()
	started := eventToolCallIDs(env.Events(), agent.EventToolStarted)
	completed := eventToolCallIDs(env.Events(), agent.EventToolCompleted)
	if len(routes) != len(started) || len(routes) != len(completed) {
		t.Fatalf("route/event cardinality mismatch routes=%d started=%d completed=%d", len(routes), len(started), len(completed))
	}
	for i := range routes {
		if routes[i].ToolCallID != started[i] || routes[i].ToolCallID != completed[i] {
			t.Fatalf("correlation mismatch idx=%d route=%s start=%s done=%s", i, routes[i].ToolCallID, started[i], completed[i])
		}
	}
}

// F1. RPC transient failure during tool call
func TestF1_RPCTransientFailureDuringToolCall(t *testing.T) {
	base, sessionID := newTestMemoryService(t)
	chaos := &chaosSandboxService{base: base, failStreamOnce: rpc.NewRPCError(rpc.CodeUnavailable, "transient unavailable", nil)}

	rpcClient := apiToolClient{svc: chaos}
	_, err := rpcClient.ExecuteTool(context.Background(), sessionID, tools.ToolRequest{ToolCallID: "f1-a", ToolName: "read_file", Params: map[string]any{"path": "state.txt"}}, nil)
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeUnavailable {
		t.Fatalf("expected unavailable on first attempt, got %v", err)
	}
	_, err = rpcClient.ExecuteTool(context.Background(), sessionID, tools.ToolRequest{ToolCallID: "f1-b", ToolName: "read_file", Params: map[string]any{"path": "state.txt"}}, nil)
	if err != nil {
		t.Fatalf("second attempt should recover: %v", err)
	}
}

// F2. Host restart during active session
func TestF2_HostRestartDuringActiveSession(t *testing.T) {
	svc1 := NewMemorySandboxService()
	baseSnapshot := "memory/base@initial"
	svc1.SeedBaseSnapshot(baseSnapshot, map[string]string{"state.txt": "base"})
	resp, err := svc1.CreateSession(context.Background(), &api.CreateSessionRequest{SessionID: "restart-1", BaseSnapshot: baseSnapshot})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, _ = svc1.ExecuteTool(context.Background(), &api.ExecuteToolRequest{SessionID: resp.Session.ID, ToolCallID: "w1", ToolName: "write_file", Params: map[string]any{"path": "state.txt", "content": "changed"}})

	// Simulated restart: new host boots from same base snapshot and can create fresh recovery session.
	svc2 := NewMemorySandboxService()
	svc2.SeedBaseSnapshot(baseSnapshot, map[string]string{"state.txt": "base"})
	recovered, err := svc2.CreateSession(context.Background(), &api.CreateSessionRequest{SessionID: "restart-2", BaseSnapshot: baseSnapshot})
	if err != nil {
		t.Fatalf("create recovered session: %v", err)
	}
	got, _ := svc2.ReadSessionFile(recovered.Session.ID, "state.txt")
	if got != "base" {
		t.Fatalf("expected recovered session at clean base, got %q", got)
	}
}

// F3. Rollback race with concurrent tool request
func TestF3_RollbackRaceWithConcurrentToolRequest(t *testing.T) {
	svc, sessionID := newTestMemoryService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := svc.CreateSnapshot(ctx, &api.CreateSnapshotRequest{SessionID: sessionID, Name: "race-0"}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	errCh := make(chan error, 2)
	go func() {
		_, err := svc.ExecuteTool(ctx, &api.ExecuteToolRequest{SessionID: sessionID, ToolCallID: "race-tool", ToolName: "bash", Params: map[string]any{"cmd": "sleep 50"}})
		errCh <- err
	}()
	go func() {
		time.Sleep(5 * time.Millisecond)
		_, err := svc.RollbackSession(ctx, &api.RollbackSessionRequest{SessionID: sessionID, SnapshotID: "race-0"})
		errCh <- err
	}()

	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			if err != nil {
				var rpcErr *rpc.RPCError
				if !errors.As(err, &rpcErr) {
					t.Fatalf("unexpected non-rpc error: %v", err)
				}
			}
		case <-ctx.Done():
			t.Fatal("race test timed out")
		}
	}
}

// F4. Pause/resume race conditions
func TestF4_PauseResumeRaceConditions(t *testing.T) {
	svc, sessionID := newTestMemoryService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errCh := make(chan error, 64)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_, err := svc.PauseSession(ctx, &api.PauseSessionRequest{SessionID: sessionID})
				if err != nil {
					var rpcErr *rpc.RPCError
					if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeFailedPrecondition {
						errCh <- err
					}
				}
			} else {
				_, err := svc.ResumeSession(ctx, &api.ResumeSessionRequest{SessionID: sessionID})
				if err != nil {
					var rpcErr *rpc.RPCError
					if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeFailedPrecondition {
						errCh <- err
					}
				}
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("unexpected pause/resume race error: %v", err)
	}
}

// F5. Event stream interruption
func TestF5_EventStreamInterruption(t *testing.T) {
	env := NewRemoteEnv(t, []testutil.ScriptEntry{
		{ToolCalls: []testutil.ToolCallSpec{{ID: "f5-1", Name: "read_file", Arguments: map[string]any{"path": "state.txt"}}}},
		{Text: "done"},
	}, map[string]string{"state.txt": "base"})

	recv1 := env.SubscribeRemoteEvents(t)
	_ = recv1.Close()
	recv2 := env.SubscribeRemoteEvents(t)
	env.PromptAndWait(t, "trigger events after reconnect", 5*time.Second)
	_ = recv2.Close()

	events := env.Events()
	if len(events) == 0 {
		t.Fatal("expected events after stream resubscribe")
	}
}

// F6. Connection reset during ExecuteTool response transfer
func TestF6_ConnectionResetDuringResponseTransfer(t *testing.T) {
	requireTier(t, "nightly", "weekly")
	// Note: this currently uses application-level chaos injection (failing stream)
	// to validate typed error handling. A toxiproxy-backed network fault lane can
	// be layered in when a real remote host lane is available.
	base, sessionID := newTestMemoryService(t)
	chaos := &chaosSandboxService{base: base, streamErrAfterProgress: rpc.NewRPCError(rpc.CodeUnavailable, "connection reset by peer", nil)}
	client := apiToolClient{svc: chaos}
	_, err := client.ExecuteTool(context.Background(), sessionID, tools.ToolRequest{ToolCallID: "f6", ToolName: "bash", Params: map[string]any{"cmd": "echo hi"}}, nil)
	if err == nil {
		t.Fatal("expected connection-reset style error")
	}
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeUnavailable {
		t.Fatalf("expected unavailable typed error, got %v", err)
	}
}

// F7. TLS certificate rotation/expiry during active session
func TestF7_TLSCertificateRotationExpiry(t *testing.T) {
	requireTier(t, "nightly", "weekly")
	// Note: this currently injects TLS-style failures at the RPC boundary rather
	// than via live certificate rotation behind a TCP proxy.
	base, sessionID := newTestMemoryService(t)
	chaos := &chaosSandboxService{base: base, failStreamOnce: rpc.NewRPCError(rpc.CodeUnavailable, "tls: bad certificate", nil)}
	client := apiToolClient{svc: chaos}
	_, err := client.ExecuteTool(context.Background(), sessionID, tools.ToolRequest{ToolCallID: "f7a", ToolName: "read_file", Params: map[string]any{"path": "state.txt"}}, nil)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "tls") {
		t.Fatalf("expected tls failure, got %v", err)
	}
	_, err = client.ExecuteTool(context.Background(), sessionID, tools.ToolRequest{ToolCallID: "f7b", ToolName: "read_file", Params: map[string]any{"path": "state.txt"}}, nil)
	if err != nil {
		t.Fatalf("expected post-rotation recovery: %v", err)
	}
}

// F8. Network partition between request send and response receive
func TestF8_NetworkPartitionBetweenSendAndReceive(t *testing.T) {
	requireTier(t, "nightly", "weekly")
	// Note: this currently validates timeout/propagation semantics with
	// application-level stream interruption; network-layer partition injection
	// (toxiproxy) is a follow-up for real-host lanes.
	base, sessionID := newTestMemoryService(t)
	chaos := &chaosSandboxService{base: base, streamErrAfterProgress: rpc.NewRPCError(rpc.CodeDeadlineExceeded, "network partition timeout", context.DeadlineExceeded)}
	client := apiToolClient{svc: chaos}
	_, err := client.ExecuteTool(context.Background(), sessionID, tools.ToolRequest{ToolCallID: "f8", ToolName: "bash", Params: map[string]any{"cmd": "echo hi"}}, nil)
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) || (rpcErr.Code != rpc.CodeDeadlineExceeded && rpcErr.Code != rpc.CodeUnavailable) {
		t.Fatalf("expected typed timeout/unavailable error, got %v", err)
	}
}

// O1. Mode 1 vs Mode 3 semantic parity oracle
func TestO1_Mode1VsMode3SemanticParityOracle(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "state.txt"), []byte("base"), 0o644); err != nil {
		t.Fatalf("seed local file: %v", err)
	}
	local := tools.NewLocalBackend(root)
	localSeq := []tools.ToolRequest{
		{ToolCallID: "o1-l1", ToolName: "read_file", Params: map[string]any{"path": "state.txt"}},
		{ToolCallID: "o1-l2", ToolName: "write_file", Params: map[string]any{"path": "state.txt", "content": "changed"}},
		{ToolCallID: "o1-l3", ToolName: "read_file", Params: map[string]any{"path": "state.txt"}},
	}
	localOut := make([]string, 0, len(localSeq))
	localExit := make([]*int, 0, len(localSeq))
	for _, req := range localSeq {
		resp, err := local.ExecuteTool(context.Background(), req, nil)
		if err != nil {
			t.Fatalf("local execute %s: %v", req.ToolName, err)
		}
		localOut = append(localOut, firstText(resp.Content))
		localExit = append(localExit, cloneExitCode(resp.ExitCode))
	}

	svc, sessionID := newTestMemoryService(t)
	remoteClient := apiToolClient{svc: svc}
	remoteSeq := []tools.ToolRequest{
		{ToolCallID: "o1-r1", ToolName: "read_file", Params: map[string]any{"path": "state.txt"}},
		{ToolCallID: "o1-r2", ToolName: "write_file", Params: map[string]any{"path": "state.txt", "content": "changed"}},
		{ToolCallID: "o1-r3", ToolName: "read_file", Params: map[string]any{"path": "state.txt"}},
	}
	remoteOut := make([]string, 0, len(remoteSeq))
	remoteExit := make([]*int, 0, len(remoteSeq))
	for _, req := range remoteSeq {
		resp, err := remoteClient.ExecuteTool(context.Background(), sessionID, req, nil)
		if err != nil {
			t.Fatalf("remote execute %s: %v", req.ToolName, err)
		}
		remoteOut = append(remoteOut, firstText(resp.Content))
		remoteExit = append(remoteExit, cloneExitCode(resp.ExitCode))
	}

	if !strings.Contains(localOut[1], "wrote") || !strings.Contains(remoteOut[1], "wrote") {
		t.Fatalf("write semantic mismatch local=%q remote=%q", localOut[1], remoteOut[1])
	}
	if !equalExitCodes(localExit, remoteExit) {
		t.Fatalf("exit code mismatch local=%v remote=%v", localExit, remoteExit)
	}
	localBytes, err := os.ReadFile(filepath.Join(root, "state.txt"))
	if err != nil {
		t.Fatalf("read local final file: %v", err)
	}
	remoteText, ok := svc.ReadSessionFile(sessionID, "state.txt")
	if !ok {
		t.Fatal("read remote final file: missing state.txt")
	}
	if string(localBytes) != remoteText {
		t.Fatalf("byte-for-byte final file mismatch local=%q remote=%q", string(localBytes), remoteText)
	}
}

// O2. Snapshot oracle
func TestO2_SnapshotOracle(t *testing.T) {
	svc, sessionID := newTestMemoryService(t)
	ctx := context.Background()
	expected := []string{"snap-a", "snap-b", "snap-c"}
	for i, name := range expected {
		_, _ = svc.ExecuteTool(ctx, &api.ExecuteToolRequest{SessionID: sessionID, ToolCallID: fmt.Sprintf("o2-%d", i), ToolName: "write_file", Params: map[string]any{"path": "state.txt", "content": name}})
		if _, err := svc.CreateSnapshot(ctx, &api.CreateSnapshotRequest{SessionID: sessionID, Name: name}); err != nil {
			t.Fatalf("create snapshot %s: %v", name, err)
		}
	}
	snaps, err := svc.ListSnapshots(ctx, &api.ListSnapshotsRequest{SessionID: sessionID})
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	got := make([]string, 0, len(snaps.Snapshots))
	for _, s := range snaps.Snapshots {
		if strings.HasPrefix(s.Name, "snap-") {
			got = append(got, s.Name)
		}
	}
	if strings.Join(got, ",") != strings.Join(expected, ",") {
		t.Fatalf("snapshot oracle mismatch got=%v want=%v", got, expected)
	}
}

// O3. Lifecycle trace replay
func TestO3_LifecycleTraceReplay(t *testing.T) {
	op := func(svc *MemorySandboxService, sid string) []string {
		ctx := context.Background()
		trace := []string{}
		record := func(label string, err error) {
			if err != nil {
				trace = append(trace, label+":err")
				return
			}
			state, gerr := svc.GetSession(ctx, &api.GetSessionRequest{SessionID: sid})
			if gerr != nil {
				trace = append(trace, label+":not_found")
				return
			}
			trace = append(trace, label+":"+state.Session.State)
		}
		_, err := svc.PauseSession(ctx, &api.PauseSessionRequest{SessionID: sid})
		record("pause", err)
		_, err = svc.ResumeSession(ctx, &api.ResumeSessionRequest{SessionID: sid})
		record("resume", err)
		_, err = svc.DestroySession(ctx, &api.DestroySessionRequest{SessionID: sid})
		record("destroy", err)
		_, err = svc.ResumeSession(ctx, &api.ResumeSessionRequest{SessionID: sid})
		record("resume_after_destroy", err)
		return trace
	}

	s1, sid1 := newTestMemoryService(t)
	s2, sid2 := newTestMemoryService(t)
	t1 := op(s1, sid1)
	t2 := op(s2, sid2)
	if strings.Join(t1, "|") != strings.Join(t2, "|") {
		t.Fatalf("lifecycle replay diverged t1=%v t2=%v", t1, t2)
	}
}

// S1. Distributed timeline simulation
func TestS1_DistributedTimelineSimulation(t *testing.T) {
	env := NewRemoteEnv(t, []testutil.ScriptEntry{
		{ToolCalls: []testutil.ToolCallSpec{{ID: "s1-1", Name: "read_file", Arguments: map[string]any{"path": "state.txt"}}}},
		{Text: "done"},
	}, map[string]string{"state.txt": "base"})
	env.PromptAndWait(t, "run timeline", 5*time.Second)
	AssertEventSequence(t, env.Events(),
		agent.EventSessionStarted,
		agent.EventToolStarted,
		agent.EventToolCompleted,
		agent.EventTurnCompleted,
	)
}

// S2. Rollback branching simulation
func TestS2_RollbackBranchingSimulation(t *testing.T) {
	svc, sid := newTestMemoryService(t)
	ctx := context.Background()
	_, _ = svc.ExecuteTool(ctx, &api.ExecuteToolRequest{SessionID: sid, ToolCallID: "s2-1", ToolName: "write_file", Params: map[string]any{"path": "state.txt", "content": "branch-a"}})
	baseSnap, _ := svc.CreateSnapshot(ctx, &api.CreateSnapshotRequest{SessionID: sid, Name: "branch-base"})
	_, _ = svc.ExecuteTool(ctx, &api.ExecuteToolRequest{SessionID: sid, ToolCallID: "s2-2", ToolName: "write_file", Params: map[string]any{"path": "state.txt", "content": "branch-b"}})
	_, _ = svc.RollbackSession(ctx, &api.RollbackSessionRequest{SessionID: sid, SnapshotID: baseSnap.SnapshotID})
	_, _ = svc.ExecuteTool(ctx, &api.ExecuteToolRequest{SessionID: sid, ToolCallID: "s2-3", ToolName: "write_file", Params: map[string]any{"path": "state.txt", "content": "branch-c"}})
	got, _ := svc.ReadSessionFile(sid, "state.txt")
	if got != "branch-c" {
		t.Fatalf("branching simulation mismatch: got=%q", got)
	}
}

// S3. Session recovery simulation
func TestS3_SessionRecoverySimulation(t *testing.T) {
	svc := NewMemorySandboxService()
	baseSnapshot := "memory/base@initial"
	svc.SeedBaseSnapshot(baseSnapshot, map[string]string{"state.txt": "base"})
	old, _ := svc.CreateSession(context.Background(), &api.CreateSessionRequest{SessionID: "s3-old", BaseSnapshot: baseSnapshot})
	_, _ = svc.ExecuteTool(context.Background(), &api.ExecuteToolRequest{SessionID: old.Session.ID, ToolCallID: "s3-1", ToolName: "write_file", Params: map[string]any{"path": "state.txt", "content": "old-work"}})
	_, _ = svc.DestroySession(context.Background(), &api.DestroySessionRequest{SessionID: old.Session.ID})
	fresh, err := svc.CreateSession(context.Background(), &api.CreateSessionRequest{SessionID: "s3-new", BaseSnapshot: baseSnapshot})
	if err != nil {
		t.Fatalf("create recovered session: %v", err)
	}
	got, _ := svc.ReadSessionFile(fresh.Session.ID, "state.txt")
	if got != "base" {
		t.Fatalf("recovery simulation expected clean base, got %q", got)
	}
}

// ST1. Multi-session distributed stress
func TestST1_MultiSessionDistributedStress(t *testing.T) {
	requireTier(t, "nightly", "weekly")
	svc := NewMemorySandboxService()
	baseSnapshot := "memory/base@initial"
	svc.SeedBaseSnapshot(baseSnapshot, map[string]string{"state.txt": "base"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const sessions = 24
	var wg sync.WaitGroup
	errCh := make(chan error, sessions)
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sid := fmt.Sprintf("st1-%d", i)
			if _, err := svc.CreateSession(ctx, &api.CreateSessionRequest{SessionID: sid, BaseSnapshot: baseSnapshot}); err != nil {
				errCh <- err
				return
			}
			for j := 0; j < 6; j++ {
				_, err := svc.ExecuteTool(ctx, &api.ExecuteToolRequest{SessionID: sid, ToolCallID: fmt.Sprintf("%s-%d", sid, j), ToolName: "read_file", Params: map[string]any{"path": "state.txt"}})
				if err != nil {
					errCh <- err
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("stress error: %v", err)
	}
}

// ST2. Long-running mode3 soak
func TestST2_LongRunningMode3Soak(t *testing.T) {
	requireTier(t, "weekly")
	if os.Getenv("MODE3_ENABLE_SOAK") != "1" {
		t.Skip("set MODE3_ENABLE_SOAK=1 to run soak lane")
	}
	d := 30 * time.Minute
	if v := strings.TrimSpace(os.Getenv("MODE3_SOAK_DURATION")); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil && parsed > 0 {
			d = parsed
		}
	}
	if testing.Short() && d > 2*time.Minute {
		t.Skipf("short mode: configured duration %s too long", d)
	}
	end := time.Now().Add(d)
	svc, sid := newTestMemoryService(t)
	i := 0
	for time.Now().Before(end) {
		_, err := svc.ExecuteTool(context.Background(), &api.ExecuteToolRequest{SessionID: sid, ToolCallID: fmt.Sprintf("soak-%d", i), ToolName: "read_file", Params: map[string]any{"path": "state.txt"}})
		if err != nil {
			t.Fatalf("soak failure at iter=%d: %v", i, err)
		}
		i++
	}
}

// ST3. Burst recovery stress
func TestST3_BurstRecoveryStress(t *testing.T) {
	requireTier(t, "nightly", "weekly")
	base, sid := newTestMemoryService(t)
	chaos := &chaosSandboxService{base: base, burstEvery: 4, burstCode: rpc.CodeUnavailable}
	client := apiToolClient{svc: chaos}
	for i := 0; i < 24; i++ {
		_, err := client.ExecuteTool(context.Background(), sid, tools.ToolRequest{ToolCallID: fmt.Sprintf("burst-%d", i), ToolName: "read_file", Params: map[string]any{"path": "state.txt"}}, nil)
		if err != nil {
			var rpcErr *rpc.RPCError
			if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeUnavailable {
				t.Fatalf("unexpected burst error at %d: %v", i, err)
			}
		}
	}
}

// SEC1. Session isolation
func TestSEC1_SessionIsolation(t *testing.T) {
	svc := NewMemorySandboxService()
	base := "memory/base@initial"
	svc.SeedBaseSnapshot(base, map[string]string{"state.txt": "seed"})
	_, _ = svc.CreateSession(context.Background(), &api.CreateSessionRequest{SessionID: "sec-a", BaseSnapshot: base})
	_, _ = svc.CreateSession(context.Background(), &api.CreateSessionRequest{SessionID: "sec-b", BaseSnapshot: base})
	_, _ = svc.ExecuteTool(context.Background(), &api.ExecuteToolRequest{SessionID: "sec-a", ToolCallID: "a1", ToolName: "write_file", Params: map[string]any{"path": "state.txt", "content": "from-a"}})
	gotA, _ := svc.ReadSessionFile("sec-a", "state.txt")
	gotB, _ := svc.ReadSessionFile("sec-b", "state.txt")
	if gotA == gotB {
		t.Fatalf("session isolation violated: a=%q b=%q", gotA, gotB)
	}
}

// SEC2. Snapshot authorization checks
func TestSEC2_SnapshotAuthorizationChecks(t *testing.T) {
	svc := NewMemorySandboxService()
	base := "memory/base@initial"
	svc.SeedBaseSnapshot(base, map[string]string{"state.txt": "seed"})
	_, _ = svc.CreateSession(context.Background(), &api.CreateSessionRequest{SessionID: "owner", BaseSnapshot: base})
	_, _ = svc.CreateSnapshot(context.Background(), &api.CreateSnapshotRequest{SessionID: "owner", Name: "owner-snap"})
	_, err := svc.RollbackSession(context.Background(), &api.RollbackSessionRequest{SessionID: "intruder", SnapshotID: "owner-snap"})
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeNotFound {
		t.Fatalf("expected not_found for unauthorized context, got %v", err)
	}
}

// SEC3. Transport hardening (malformed/oversized payloads)
func TestSEC3_TransportHardening(t *testing.T) {
	svc, sid := newTestMemoryService(t)
	if _, err := svc.ExecuteTool(context.Background(), nil); err == nil {
		t.Fatal("expected invalid argument for nil request")
	}
	large := strings.Repeat("x", 2*1024*1024)
	resp, err := svc.ExecuteTool(context.Background(), &api.ExecuteToolRequest{
		SessionID:  sid,
		ToolCallID: "sec3-large",
		ToolName:   "write_file",
		Params:     map[string]any{"path": "big.txt", "content": large},
	})
	if err != nil {
		t.Fatalf("oversized payload should be handled deterministically: %v", err)
	}
	if resp == nil {
		t.Fatal("expected response for oversized payload write")
	}
}

// SEC4. Sensitive metadata handling
func TestSEC4_SensitiveMetadataRedaction(t *testing.T) {
	input := "token=abcd1234 password=hunter2 api_key=xyz Authorization: Bearer abcdef"
	redacted := redactSensitive(input)
	for _, secret := range []string{"abcd1234", "hunter2", "xyz", "abcdef"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("secret %q was not redacted: %q", secret, redacted)
		}
	}
	for _, marker := range []string{"token=[REDACTED]", "password=[REDACTED]", "api_key=[REDACTED]", "Authorization: Bearer [REDACTED]"} {
		if !strings.Contains(redacted, marker) {
			t.Fatalf("missing marker %q in redacted output %q", marker, redacted)
		}
	}
}

var redactionPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	{re: regexp.MustCompile(`(?i)(token\s*=\s*)([^\s]+)`), repl: `${1}[REDACTED]`},
	{re: regexp.MustCompile(`(?i)(password\s*=\s*)([^\s]+)`), repl: `${1}[REDACTED]`},
	{re: regexp.MustCompile(`(?i)(api[_-]?key\s*=\s*)([^\s]+)`), repl: `${1}[REDACTED]`},
	{re: regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+)([^\s]+)`), repl: `${1}[REDACTED]`},
}

func redactSensitive(s string) string {
	out := s
	for _, p := range redactionPatterns {
		out = p.re.ReplaceAllString(out, p.repl)
	}
	return out
}

type apiToolClient struct {
	svc api.SandboxService
}

func (c apiToolClient) ExecuteTool(ctx context.Context, sessionID string, req tools.ToolRequest, onProgress func(tools.ToolProgress)) (*tools.ToolResponse, error) {
	stream, err := c.svc.ExecuteToolStream(ctx, &api.ExecuteToolRequest{
		SessionID:  sessionID,
		ToolCallID: req.ToolCallID,
		ToolName:   req.ToolName,
		Params:     req.Params,
	})
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var final *api.ExecuteToolResponse
	for {
		msg, recvErr := stream.Recv()
		if recvErr != nil {
			if recvErr == io.EOF {
				break
			}
			return nil, recvErr
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
		return nil, rpc.NewRPCError(rpc.CodeInternal, "missing final response", nil)
	}
	return &tools.ToolResponse{Content: []ai.ContentBlock{&ai.TextContent{Text: final.Content}}, SnapshotID: final.SnapshotID, ExitCode: final.ExitCode}, nil
}

type chaosSandboxService struct {
	base                   api.SandboxService
	mu                     sync.Mutex
	failStreamOnce         error
	streamErrAfterProgress error
	burstEvery             int
	calls                  int
	burstCode              rpc.Code
}

func (c *chaosSandboxService) CreateSession(ctx context.Context, req *api.CreateSessionRequest) (*api.CreateSessionResponse, error) {
	return c.base.CreateSession(ctx, req)
}
func (c *chaosSandboxService) GetSession(ctx context.Context, req *api.GetSessionRequest) (*api.GetSessionResponse, error) {
	return c.base.GetSession(ctx, req)
}
func (c *chaosSandboxService) PauseSession(ctx context.Context, req *api.PauseSessionRequest) (*api.PauseSessionResponse, error) {
	return c.base.PauseSession(ctx, req)
}
func (c *chaosSandboxService) ResumeSession(ctx context.Context, req *api.ResumeSessionRequest) (*api.ResumeSessionResponse, error) {
	return c.base.ResumeSession(ctx, req)
}
func (c *chaosSandboxService) DestroySession(ctx context.Context, req *api.DestroySessionRequest) (*api.DestroySessionResponse, error) {
	return c.base.DestroySession(ctx, req)
}
func (c *chaosSandboxService) ExecuteTool(ctx context.Context, req *api.ExecuteToolRequest) (*api.ExecuteToolResponse, error) {
	return c.base.ExecuteTool(ctx, req)
}
func (c *chaosSandboxService) ExecuteToolStream(ctx context.Context, req *api.ExecuteToolRequest) (api.ExecuteToolStreamReceiver, error) {
	c.mu.Lock()
	c.calls++
	calls := c.calls
	if c.failStreamOnce != nil {
		err := c.failStreamOnce
		c.failStreamOnce = nil
		c.mu.Unlock()
		return nil, err
	}
	if c.burstEvery > 0 && calls%c.burstEvery == 0 {
		code := c.burstCode
		if code == "" {
			code = rpc.CodeUnavailable
		}
		c.mu.Unlock()
		return nil, rpc.NewRPCError(code, "burst outage", nil)
	}
	errAfterProgress := c.streamErrAfterProgress
	c.mu.Unlock()

	stream, err := c.base.ExecuteToolStream(ctx, req)
	if err != nil {
		return nil, err
	}
	if errAfterProgress == nil {
		return stream, nil
	}
	return &failingStream{base: stream, failAfterProgress: true, err: errAfterProgress}, nil
}
func (c *chaosSandboxService) TurnComplete(ctx context.Context, req *api.TurnCompleteRequest) (*api.TurnCompleteResponse, error) {
	return c.base.TurnComplete(ctx, req)
}
func (c *chaosSandboxService) CreateSnapshot(ctx context.Context, req *api.CreateSnapshotRequest) (*api.CreateSnapshotResponse, error) {
	return c.base.CreateSnapshot(ctx, req)
}
func (c *chaosSandboxService) RollbackSession(ctx context.Context, req *api.RollbackSessionRequest) (*api.RollbackSessionResponse, error) {
	return c.base.RollbackSession(ctx, req)
}
func (c *chaosSandboxService) ListSnapshots(ctx context.Context, req *api.ListSnapshotsRequest) (*api.ListSnapshotsResponse, error) {
	return c.base.ListSnapshots(ctx, req)
}
func (c *chaosSandboxService) HealthCheck(ctx context.Context, req *api.HealthCheckRequest) (*api.HealthCheckResponse, error) {
	return c.base.HealthCheck(ctx, req)
}

type failingStream struct {
	base              api.ExecuteToolStreamReceiver
	failAfterProgress bool
	err               error
	seenProgress      bool
}

func (s *failingStream) Recv() (*api.ExecuteToolStreamMessage, error) {
	msg, err := s.base.Recv()
	if err != nil {
		return nil, err
	}
	if msg != nil && msg.Progress != nil {
		s.seenProgress = true
	}
	if s.failAfterProgress && s.seenProgress {
		return nil, s.err
	}
	return msg, nil
}

func (s *failingStream) Close() error { return s.base.Close() }

func eventToolCallIDs(events []agent.AgentEvent, typ agent.AgentEventType) []string {
	ids := make([]string, 0)
	for _, evt := range events {
		if evt.Type == typ && evt.ToolCallID != "" {
			ids = append(ids, evt.ToolCallID)
		}
	}
	return ids
}

func hashFiles(t *testing.T, svc *MemorySandboxService, sessionID string, files []string) string {
	t.Helper()
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	h := sha256.New()
	for _, name := range sorted {
		content, ok := svc.ReadSessionFile(sessionID, name)
		if !ok {
			continue
		}
		_, _ = io.WriteString(h, name)
		_, _ = io.WriteString(h, "\x00")
		_, _ = io.WriteString(h, content)
		_, _ = io.WriteString(h, "\x00")
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func firstText(blocks []ai.ContentBlock) string {
	for _, b := range blocks {
		if tc, ok := b.(*ai.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

func cloneExitCode(in *int) *int {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

func equalExitCodes(a, b []*int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] == nil && b[i] == nil {
			continue
		}
		if a[i] == nil || b[i] == nil {
			return false
		}
		if *a[i] != *b[i] {
			return false
		}
	}
	return true
}
