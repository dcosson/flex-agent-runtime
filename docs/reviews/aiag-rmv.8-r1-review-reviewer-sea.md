# Code Review: aiag-rmv.8 (R1, reviewer-sea)

- Bead: aiag-rmv.8
- Commit range: 99b8a4d (single commit)
- Plan doc: docs/plans/18-agent-loop-rpc.md §14.2
- Reviewer: reviewer-sea
- Review commit: 99b8a4de055d6dcc571ec56d3c2f910cc746c9a0

## Findings

### P1-1 - reservePort has TOCTOU race with LaunchProcess

**Location:** `tests/integration/agent_rpc/e2e_test.go:474-482`

**Problem**
`reservePort` binds a TCP listener, reads the port, then closes the listener before returning. Between the close and LaunchProcess binding the helper process to that port, another process could grab it. This is the classic ephemeral port TOCTOU race. On a busy CI machine with parallel tests, this can cause flaky test failures.

**Suggested fix**
Pass port 0 to the helper process and have it report its actual listening port back (e.g., write the port to a temp file or stdout). Alternatively, accept the TOCTOU risk with a comment explaining it's unlikely in practice — this is a common testing pattern and most CI runners don't have enough parallel activity to trigger it. Given this is an E2E test that only runs once, this is borderline P1/P2.

---

### P2-1 - Helper process passes nil EventPublisher to NewAgentLoopService

**Location:** `tests/integration/agent_rpc/e2e_test.go:443`

**Problem**
`agent.NewAgentLoopService(nil, ...)` passes nil as the EventPublisher. If any code path in AgentLoopService calls `publisher.Publish()` without a nil check, this will panic. The mock driver script includes tool events, which may trigger event publishing. If AgentLoopService guards against nil publisher, this is fine — but it's fragile and not documented.

**Suggested fix**
Either pass a no-op EventPublisher (e.g., `&agenttest.MockEventPublisher{}`) or verify that AgentLoopService safely handles nil publisher. The safest option is to pass a mock since it costs nothing and eliminates the risk.

---

### P2-2 - assertEventSequence uses subsequence matching, not exact match

**Location:** `tests/integration/agent_rpc/e2e_test.go:307-318`

**Problem**
`assertEventSequence` checks that the wanted event types appear as a subsequence of the actual events (in order, but with arbitrary events between them). This is intentionally loose — it won't catch if extra unexpected events are injected. For the E2E test this is reasonable since the exact event count varies with mock driver configuration. However, it means the assertion wouldn't catch regressions that add spurious events.

**Suggested fix**
No change strictly needed — the loose matching is appropriate for E2E where intermediate events (deltas, state changes) are expected. Consider adding a comment explaining the design choice: "subsequence match allows intermediate events (deltas, state changes) between the key lifecycle events."

---

### P2-3 - verifyCrossAgentRoundTrip doesn't verify content preservation

**Location:** `tests/integration/agent_rpc/e2e_test.go:369-422`

**Problem**
The cross-agent round-trip test (lines 369-422) converts records to entries, writes to session log, parses back, and asserts `len(parsed) > 0`. It doesn't verify that the parsed entries contain the original content ("Fix main.go", "I will inspect main.go", tool call tc-1, tool result). This is weaker than CR2 in aiag-rmv.9 which does full content verification.

**Suggested fix**
Add spot-check assertions on the parsed entries: verify user text content preserved, tool call name/ID preserved, tool result content preserved. The round-trip test in CR2 already covers this thoroughly, so this is lower priority for the E2E suite, but it would strengthen the E2E test independently.

---

### P3-1 - TestHelperArgParsing includes an unrelated json.Marshal call

**Location:** `tests/integration/agent_rpc/e2e_test.go:492-496`

**Problem**
Lines 492-496 marshal a map to JSON and check it's non-empty. This has no relation to helper argument parsing and appears to be dead test code or leftover from development.

**Suggested fix**
Remove the json.Marshal lines from TestHelperArgParsing.

---

### P3-2 - httpServer.Handler wraps server.Handler() redundantly

**Location:** `tests/integration/agent_rpc/e2e_test.go:457-459`

**Problem**
The helper creates a `http.NewServeMux()`, registers `"/"` with `server.Handler()`, then uses the mux as the http.Server handler. Since `transport.Server.Handler()` already returns a mux-compatible handler, the intermediate mux is unnecessary — you can pass `server.Handler()` directly as the http.Server handler.

**Suggested fix**
Simplify to `httpServer := &http.Server{Addr: listenAddr, Handler: server.Handler()}`. Minor cleanup.

---

### P3-3 - E2E test covers all bead deliverables — good

**Location:** `tests/integration/agent_rpc/e2e_test.go` (overall)

**Problem** (positive observation)
The test covers all deliverables listed in the bead: sandbox creation + tool execution, LaunchProcess agent helper, remote agent session lifecycle, session forking from conversation log, cross-agent resume conversion, connection drop recovery (kill + relaunch + resume), and graceful shutdown via SIGTERM. The helper process pattern (re-exec test binary with env flag) is a well-established Go testing idiom.

---

### P3-4 - buildResumeLog duplicated from scenarios package

**Location:** `tests/integration/agent_rpc/e2e_test.go:340-367`

**Problem**
`buildResumeLog` and `mustRecord` are identical to the implementations in `tests/integration/scenarios/agent_rpc_fork_test.go`. This is acceptable since they're in different test packages, but if more test files need these helpers, consider extracting to a shared testutil package.

**Suggested fix**
No change needed now. If a third consumer appears, extract to `tests/integration/testutil/`.

---

## Summary

7 findings: 0 P0, 1 P1, 3 P2, 3 P3

**Verdict**: Approved with revisions

The E2E test is comprehensive, covering the full orchestrator → agent → sandbox flow including all scenarios specified in the bead. The helper process pattern is clean and well-structured. The single P1 (port TOCTOU) is a common testing limitation and borderline P1/P2 — acceptable to defer with a comment. The P2 items (nil publisher, loose event assertion, weak round-trip verification) would strengthen the test but are not blocking.
