# Code Review: aiag-rmv.9 (R1, reviewer-sea)

- Bead: aiag-rmv.9
- Commit range: b9d9e8e (single commit)
- Plan doc: docs/plans/18-agent-loop-rpc.md (§14), docs/plans/18-agent-loop-rpc-test-harness.md
- Parent review: docs/reviews/aiag-rmv.4-r1-review-reviewer-sea.md
- Reviewer: reviewer-sea
- Review commit: b9d9e8e15d48fe0e046178dba2f46ad89d0b05d9

## P1 Disposition from aiag-rmv.4 R1

| Finding | Status | Notes |
|---------|--------|-------|
| P1-1: FK2 sessions not destroyed between waves | **Fixed** | DestroySession added per fork after drain (fork_test.go:114-117) |
| P1-2: FK2 goroutine leak threshold too generous | **Fixed** | Tightened to baseG+20 (fork_test.go:134) |
| P1-3: EF1 uses 120 events, spec says 500 | **Fixed** | Changed to `const messageCount = 500` (agent_harness_test.go:17) |
| P1-4: Missing EF2 terminal event guarantees | **Fixed** | Added TestEF2_TerminalEventGuarantees with repeated EOF checks under timeout (agent_harness_test.go:54-87) |
| P1-5: Missing CR2 conversion chain | **Fixed** | Added TestCR2_RecordToConversationEntryConversion + new AgentMessageToConversationEntries codec (termmux_codec.go, resume_test.go:55-168) |

All P1 findings addressed. Additionally addressed P2/P3 items:
- P2-3 (Close order): Fixed — service drained before transport (rpc_stack.go:73-82)
- P3-1 (EF3 event count): Bumped to 200 (agent_harness_test.go)
- P3-2 (test_stack.go alias): Removed (deleted file)
- Property tests now clean up sessions via DestroySession after each iteration

## New Code Findings

### P2-1 - recvWithTimeout leaks goroutine on timeout

**Location:** `internal/rpc/rpctest/agent_harness_test.go:187-202`

**Problem**
`recvWithTimeout` spawns a goroutine that calls `recv.Recv()`. If the timeout fires, the goroutine is abandoned — it blocks on `done <- result{...}` (buffered channel size 1, but if timeout already consumed the select, the goroutine blocks forever). In practice this is fine for tests since the process exits, but under `-race` with many iterations it could accumulate.

**Suggested fix**
This is a minor test-only concern. The channel is buffered at 1, so the goroutine will complete and the send will succeed even after timeout — the value is just never read. On closer inspection this is actually safe as-is. No change needed.

---

### P2-2 - SubscribeEvents initial state event is a behavioral change

**Location:** `internal/agent/service.go:327-335`

**Problem**
The new initial `EventStateChange` push in `SubscribeEvents` is a runtime behavioral change, not just a test change. It fixes a real ConnectRPC deadlock (well documented in the comment). The EF4 test correctly drains this initial event before testing the destroy-EOF path (agent_harness_test.go:142-149). However, any existing callers of `SubscribeEvents` that don't expect this initial event could see unexpected behavior.

**Suggested fix**
Verify that all existing SubscribeEvents consumers (RPC server event handler, any orchestrator code) handle or ignore this initial state event correctly. The fix itself is correct and well-motivated — just flagging for awareness.

---

### P3-1 - termmux_codec.go conversationTimestamp falls back to Unix epoch

**Location:** `internal/agent/api/termmux_codec.go:131-136`

**Problem**
When both `CreatedAt` and `Message.GetTimestamp()` are zero/missing, `conversationTimestamp` returns `time.Unix(0, 0).UTC()` (January 1, 1970). This is a reasonable sentinel but could be confusing in logs. Minor — the zero-value behavior is documented by the test at termmux_codec_test.go:102-119 which verifies the fallback to message timestamp.

**Suggested fix**
No change needed. The behavior is tested and the sentinel is standard Go.

---

### P3-2 - CR2 test is thorough and well-structured

**Location:** `tests/integration/scenarios/agent_rpc_resume_test.go:55-168`

**Problem** (positive observation)
The CR2 test covers the full chain: Record -> AgentMessage -> ConversationEntries -> WriteSessionLog -> ParseSessionLog, verifying thinking blocks, tool_use with args, tool_result with content, and user text. It also validates the round-trip through Claude Code's session log format. This is exactly what the harness doc specified and goes beyond by also testing the serialization round-trip.

---

## Summary

4 findings: 0 P0, 0 P1, 2 P2, 2 P3

**Verdict**: Approved

All P1 findings from the parent review (aiag-rmv.4 R1) have been addressed cleanly. The new AgentMessageToConversationEntries codec is well-implemented with proper test coverage. The SubscribeEvents initial-event fix solves a real deadlock and is properly handled in tests. The bonus improvements (property test session cleanup, EF3 event count bump, test_stack.go removal, Close() order fix) are all welcome.
