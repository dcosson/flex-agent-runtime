# Code Review: aiag-rmv.4 (R1, reviewer-sea)

- Bead: aiag-rmv.4
- Commit range: 7d75617 (single commit)
- Plan doc: docs/plans/18-agent-loop-rpc.md (§14), docs/plans/18-agent-loop-rpc-test-harness.md
- Reviewer: reviewer-sea
- Review commit: 7d75617c1ecefe08600bd160aa22adb95a9f21ae

## Findings

### P1-1 - FK2 sessions not destroyed between waves

**Location:** `tests/integration/scenarios/agent_rpc_fork_test.go:87-125`

**Problem**
Each wave creates 10 sessions via ResumeSession + SendMessage but never calls DestroySession. After 5 waves, 50 sessions remain live. The harness doc (§7, FK2) specifies "create/message/destroy cycles without leaks" — the destroy part is missing. This means the goroutine leak check at line 131 is testing accumulated live sessions rather than leaked goroutines from completed cycles.

**Suggested fix**
After draining each fork's event receiver, call `stack.Client.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{SessionID: id})`. This ensures the leak check measures actual leaks, not expected live state.

---

### P1-2 - FK2 goroutine leak threshold too generous

**Location:** `tests/integration/scenarios/agent_rpc_fork_test.go:131`

**Problem**
Threshold of `baseG+80` for 50 total fork operations allows >1 leaked goroutine per operation. With proper destroy cycles (P1-1), the delta should be much smaller. A threshold this loose would mask real goroutine leaks.

**Suggested fix**
After fixing P1-1, tighten to `baseG+20` to account for httptest server goroutines and GC settling lag. If that fails, investigate the real leak source.

---

### P1-3 - EF1 uses 120 events, harness doc specifies 500

**Location:** `internal/rpc/rpctest/agent_harness_test.go:15`

**Problem**
The EF1 spec in the test harness doc (§9) states: "All 500 emitted events received by EventReceiver". The implementation uses `const messageCount = 120`. While functionally correct, this doesn't match the documented spec and reduces the stress surface for drop detection at higher volumes.

**Suggested fix**
Change `const messageCount = 120` to `const messageCount = 500`.

---

### P1-4 - Missing EF2 (terminal event guarantees)

**Location:** `internal/rpc/rpctest/agent_harness_test.go` (missing test)

**Problem**
The harness doc (§9, EF2) specifies a dedicated test for terminal event guarantees: "EventReceiver terminates with io.EOF or non-EOF error, never hangs. Subsequent Recv() calls also return io.EOF." This invariant is not explicitly tested. EF1/EF3/EF4 exercise EOF incidentally via `CollectAllEvents` but none verify that a *second* Recv() after EOF also returns EOF (vs hanging or panicking).

**Suggested fix**
Add `TestEF2_TerminalEventGuarantees` that: (a) runs a turn to completion, (b) verifies first Recv() after TurnCompleted returns io.EOF, (c) verifies a second Recv() also returns io.EOF, (d) verifies this completes within a timeout (no hang).

---

### P1-5 - Missing CR2 (Record -> ConversationEntry conversion)

**Location:** `tests/integration/scenarios/agent_rpc_resume_test.go` (missing test)

**Problem**
The bead deliverables list CR1-CR3 from the harness doc (§8). CR1 and CR3 are implemented, but CR2 is missing. CR2 validates the full conversion chain: `AgentMessageRecord -> AgentMessage -> ConversationEntry`, specifically that tool_use, tool_result, thinking, and text blocks all convert correctly to the Claude Code session log format. This is the critical cross-agent interop verification.

**Suggested fix**
Add `TestCR2_RecordToConversationEntryConversion` that builds records containing tool_use, tool_result, thinking, and text blocks, converts through the full chain via `RecordToAgentMessage` then `AgentMessageToConversationEntries`, and verifies each block type is represented correctly in the output entries.

---

### P2-1 - P3 property test has narrow mutation strategy

**Location:** `internal/agent/service_property_test.go:23-24`

**Problem**
The P3 property test only mutates the first record's role to "unknown" as its malformation strategy. The harness doc (§3, P3) and plan §3.4 specify validation for: schema version mismatch (tested separately in CR3), malformed JSON, null content, non-contiguous turns, and unknown roles. Only the role mutation is covered here, leaving the property test's malformation surface thin.

**Suggested fix**
Use `rapid.OneOf` to randomly select among mutation strategies: invalid role, turn number regression (decreasing turn), empty role string, null/missing content blocks. This significantly strengthens the property test without adding much code.

---

### P2-2 - GenerateConversationLog may produce invalid logs for P6

**Location:** `internal/agent/agenttest/generators.go:18-21`

**Problem**
`GenerateConversationLog` picks roles uniformly at random, producing sequences like three consecutive "user" records. This is correct for P3 (testing validation warnings), but P6 (`TestP6_CrossAgentResumeRoundTripProperty`) uses the same generator to create logs intended to be valid for resume + SendMessage round-trips. Consecutive same-role records trigger warnings (per plan §3.4 rule 6), which is acceptable, but if the validation behavior is ever tightened from warning to error this would cause confusing P6 failures.

**Suggested fix**
Add a `GenerateValidConversationLog` variant that alternates user/assistant roles (matching real conversation structure) for use by P6 and other tests that expect clean resume behavior.

---

### P2-3 - Close() order: transport before service

**Location:** `internal/agent/agenttest/rpc_stack.go:72-81`

**Problem**
`AgentTestStack.Close()` closes the httptest transport server before closing the AgentLoopService. In-flight RPC requests will receive connection-reset errors rather than graceful service-level drain errors. While this is test infrastructure, it establishes a teardown pattern that diverges from the plan's specified Close() behavior (§4): drain active sessions first, then tear down transport.

**Suggested fix**
Reverse the order: close `s.Service` first (graceful drain), then `s.transport`. This also makes test failure modes more informative — a stuck drain shows as a service timeout, not a mysterious connection reset.

---

### P2-4 - BenchmarkB3 only covers read path

**Location:** `internal/rpc/rpctest/agent_harness_test.go:114-139`

**Problem**
BenchmarkB3 only benchmarks `GetSession` (a read-only unary call). The harness doc (§14, B3) specifies measuring "RPC overhead vs in-process" generically. Mutation paths (CreateSession, DestroySession) involve codec serialization of larger payloads and different server-side lock patterns, which may have materially different overhead profiles.

**Suggested fix**
Add a `b.Run("rpc-create-destroy", ...)` sub-benchmark that measures a CreateSession+DestroySession round-trip via RPC vs in-process.

---

### P3-1 - EF3 uses 25 events, harness doc suggests 200+

**Location:** `internal/rpc/rpctest/agent_harness_test.go:54`

**Problem**
EF3 generates 25 delta events for ordering verification. The harness doc (§9, EF3) says "200+ events". Functionally the test works fine at 25 since the ordering logic is count-independent.

**Suggested fix**
Bump to 200 to match spec. Low priority.

---

### P3-2 - test_stack.go is a single-line compatibility alias

**Location:** `internal/agent/agenttest/test_stack.go:1-12`

**Problem**
`NewTestStack` is a one-line wrapper that delegates to `NewAgentTestStack`. If nothing outside this commit references `NewTestStack`, this is dead code. If something does reference it, callers should be updated to use `NewAgentTestStack` directly.

**Suggested fix**
Check for usages of `NewTestStack`. If none, remove the file. If used, update callers and remove the alias.

---

### P3-3 - harness_test.go P3 fix is clean

**Location:** `internal/agent/harness_test.go:243-254`

**Problem** (positive observation, no fix needed)
The change from counting all attempted follow-ups to counting only accepted follow-ups is correct. `FollowUp` can be rejected when the queue is full or the agent has transitioned, so the expected turn count must be based on accepted operations. Good fix.

---

## Summary

12 findings: 0 P0, 5 P1, 4 P2, 3 P3

**Verdict**: Approved with revisions

The infrastructure (rpc_stack, helpers, generators, mock driver extensions) is well-structured and the test patterns are solid. The P1 items are mostly coverage gaps against the harness doc spec (missing EF2, CR2, FK2 destroy cycles, EF1 event count). These should be addressed before final sign-off to maintain spec compliance. The bead was closed before this review — suggest reopening to address P1 findings or creating a follow-up bead.
