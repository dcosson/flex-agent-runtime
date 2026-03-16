# Code Review: aiag-orl.1 (R1, reviewer-sea)

- Bead: aiag-orl.1
- Commit range: d6b22d7..b15c4c4
- Plan doc: docs/plans/14-mode3-e2e.md
- Reviewer: reviewer-sea
- Review commit: b15c4c4

## Findings

### P1 - Lock ordering deadlock between DestroySession and executeTool

**Location:** `tests/integration/mode3/harness/memory_sandbox_service.go:217-233,380-429`

**Problem**
`DestroySession` acquires service lock (`m.mu.Lock()` line 221) then session lock (`sess.mu.Lock()` line 228). `executeTool` acquires session lock (`sess.mu.Lock()` line 391) then service lock (`m.mu.Lock()` line 421, for routes append). This is an ABBA deadlock: if two goroutines hit these methods concurrently on the same session, each holds one lock and waits for the other.

The plan §7.3 says "Sessions... support concurrent access." The Mode 3 test harness (aiag-orl.2) will include stress tests (ST suites) that exercise concurrent operations on this fake — this deadlock will surface there.

**Suggested fix**
Move the routes append in `executeTool` outside the `sess.mu.Lock()` scope. The route data (`ToolRoute`) is computed from values already captured, so it doesn't need the session lock. Something like:

```go
// ... compute result under sess.mu.Lock() ...
sess.mu.Unlock()

m.executeCalls.Add(1)
m.mu.Lock()
m.routes = append(m.routes, route)
m.mu.Unlock()

return resp, nil
```

---

### P2 - No unit tests for MemorySandboxService

**Location:** `tests/integration/mode3/harness/`

**Problem**
The bead description lists "Unit tests for MemorySandboxService, all harness components" as a deliverable. The harness package has no test files (`[no test files]` in test output). The MemorySandboxService is 602 lines with state machine logic, rollback with snapshot truncation, tool execution routing, and concurrent access patterns. It's exercised indirectly through the 6 E2E scenarios, but direct unit tests would cover edge cases (invalid state transitions, double-destroy, concurrent create+destroy, rollback to nonexistent snapshot, etc.) more thoroughly.

**Suggested fix**
Add `memory_sandbox_service_test.go` with targeted unit tests for: (1) state machine invalid transitions (e.g., pause from paused, resume from active), (2) rollback truncates post-rollback snapshots, (3) concurrent session operations, (4) tool execution edge cases. This may also be covered in the aiag-orl.2 test harness bead — if so, just note that and defer.

---

### P3 - DestroySession "destroying" state is unobservable

**Location:** `tests/integration/mode3/harness/memory_sandbox_service.go:228-229`

**Problem**
`sess.state = memoryStateDestroying` is immediately followed by `sess.state = memoryStateDestroyed` with no observable side effects between them. The "destroying" state exists in the plan's FSM but is never observable in the fake. This is fine for a test fake but slightly misleading — a reader might expect cleanup to happen between states.

**Suggested fix**
Either remove the `memoryStateDestroying` assignment (since it's immediately overwritten) or add a brief comment noting the two-step is a placeholder matching the real implementation's FSM.

---

### P3 - S6 event wait uses polling loop

**Location:** `tests/integration/mode3/mode3_event_stream_test.go:50-65`

**Problem**
The test polls for `EventSessionEnded` with `time.Sleep(10ms)` in a loop. This works but is a minor smell — a channel-based wait (similar to the idle wait in `PromptAndWait`) would be cleaner and avoid unnecessary CPU churn.

**Suggested fix**
Low priority. The polling is bounded (2s deadline) and the sleep interval is small. Could be cleaned up if desired but functional as-is.

---

## Summary

4 findings: 0 P0, 1 P1, 1 P2, 2 P3

**Verdict**: Approved with revisions

The implementation is well-structured and covers all 6 plan scenarios (S1-S6). The MemorySandboxService is comprehensive — full state machine, tier routing via `ClassifyTool`, snapshot deep-copy with rollback, in-memory tool execution (read/write/edit/grep/glob/bash), and concurrent-safe design. The `RemoteEnv` harness cleanly wires up the fake service → RPC client → sandbox tools → agent → event server pipeline. All scenarios validate the right things: S1 verifies RPC dispatch path with multi-turn agent, S2 checks tier classification and snapshot metadata, S3 tests rollback state restoration, S4 confirms pause/resume continuity with identity preservation, S5 validates destroyed-session rejection and clean re-creation from base snapshot, S6 asserts canonical event ordering through remote stream. All 6 tests pass with `-race` and `go vet` is clean.

The P1 lock ordering issue should be fixed before the stress test harness (aiag-orl.2) runs against this fake. The P2 unit tests may be deferred to the harness bead if that's the plan.
