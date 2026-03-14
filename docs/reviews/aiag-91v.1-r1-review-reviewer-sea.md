# Code Review: aiag-91v.1 (R1, reviewer-sea)

- Bead: aiag-91v.1
- Commit range: d6b22d7..2bb45d4
- Plan doc: docs/plans/15-mode2-e2e.md
- Reviewer: reviewer-sea
- Review commit: 53cd3bb

## Findings

### P2 - ResolveConflicts deduplicates by event type only, ignoring event identity

**Location:** `e2etests/mode2/harness/assertions.go:59-100`

**Problem**
`ResolveConflicts` groups events by `Event.Type` within a time window and keeps the highest-priority source. But it doesn't distinguish different logical events of the same type — e.g., two `tool_started` events for different tools (`read` and `write`) arriving 50ms apart would be incorrectly deduplicated, with one silently dropped. The algorithm should also compare identity fields (tool name, call ID, session ID) when deciding whether events are duplicates.

The S2 test only covers the single-tool case where OTEL and hook report the same tool call, so the bug isn't surfaced.

**Suggested fix**
Add an identity comparison to the inner loop: only treat events as conflicts when both the type AND identity fields (ToolName, CallID, or SessionID as appropriate) match. Alternatively, the `NormalizedEvent` could carry a dedup key computed from type + identity, and conflicts are only declared when dedup keys match.

---

### P2 - S4 pause/resume test doesn't exercise actual pause/resume lifecycle

**Location:** `e2etests/mode2/mode2_pause_resume_test.go`

**Problem**
The test name is `TestPauseResumeWithIdleSnapshot` and the plan §4.4 says "Pause session after idle transition snapshot. Resume later and continue prompt flow." But the test never calls any pause or resume API. It runs a simulator, creates a snapshot marker, then reads back workspace files and config credentials — which trivially succeed since nothing was paused or resumed. The test only proves file persistence on the same filesystem, not actual session lifecycle control.

This may be blocked by the harness not yet having pause/resume APIs. If so, this test is a placeholder and should be documented as such.

**Suggested fix**
Either add actual pause/resume calls to the test (if the termmux/adapter API supports it), or rename the test and add a comment indicating pause/resume lifecycle is deferred to when the API is available. A `t.Skip` with a reason would be appropriate for the pause/resume specific assertions.

---

### P2 - S5 config persistence test doesn't test actual cross-restart persistence

**Location:** `e2etests/mode2/mode2_config_persistence_test.go:47-79`

**Problem**
`TestConfigDirectoryPersistence` creates two independent `SandboxEnv` instances with the same session ID to "simulate restart." But each `SandboxEnv` calls `t.TempDir()` to get a different base directory, so `sandbox1.ConfigDir` and `sandbox2.ConfigDir` are at completely different absolute paths. The test then only compares the relative path suffix (`configs/<sessionID>`) — it doesn't verify that credentials injected in sandbox1 are readable from sandbox2.

The test proves path derivation is deterministic from session ID, but the plan §4.5 says "Verify per-session stable path and persistence across restart/resume." Actual persistence would require the two envs to share a data directory.

**Suggested fix**
Either (a) have the second `SandboxEnv` share the same data directory as the first (pass the base dir instead of creating a new temp dir), so credentials actually persist, or (b) add a comment acknowledging this tests path scheme determinism only, not actual persistence (which requires shared storage, deferred to real sandbox integration).

---

### P3 - No unit tests for harness package

**Location:** `e2etests/mode2/harness/`

**Problem**
The harness package has `[no test files]` (visible in test output). The `EventNormalizer` especially has non-trivial logic in `ResolveConflicts` (conflict detection, priority comparison, used-tracking) that would benefit from targeted unit tests — edge cases like zero events, single source, all same priority, events outside window, etc. The assertion helpers also have subtle subsequence-matching logic.

**Suggested fix**
Add `assertions_test.go` with unit tests for `ResolveConflicts` edge cases and `AssertEventSequence` boundary conditions. May be deferred to the test harness bead (aiag-91v.2) if planned.

---

### P3 - basic_session.jsonl fixture and LoadReplayScript are unused

**Location:** `e2etests/fixtures/driver_logs/basic_session.jsonl`, `e2etests/mode2/harness/driver_simulator.go:83-103`

**Problem**
The `basic_session.jsonl` fixture file is well-formed (9 entries covering a complete session lifecycle with mixed OTEL/PTY/hook sources) and `LoadReplayScript`/`ParseReplayScript` exist to load it. But no test uses either — all tests construct replay entries inline via helpers like `buildLaunchReplayScript`. The fixture and loader are dead code.

**Suggested fix**
Either add a test that uses `LoadReplayScript` with the fixture file (e.g., a round-trip parse test, or use it in one of the scenarios), or remove the unused code. Using the fixture would also exercise the JSONL parsing path end-to-end.

---

## Summary

5 findings: 0 P0, 0 P1, 3 P2, 2 P3

**Verdict**: Approved with revisions

The implementation is well-structured and covers all 6 plan scenarios with 12 test functions. The harness components are cleanly designed: `DeterministicDriverSimulator` provides reproducible replay with JSON-lines format and FastForward/RealTime modes; `TermmuxEnv` wraps real PTY sessions with thread-safe event capture; `SandboxEnv` manages isolated workspace/config lifecycle; `ConfigInjector` handles credential setup; and `EventNormalizer` implements the 3-source priority hierarchy (OTEL > hooks > session-log) with confidence tagging. The `basic_session.jsonl` fixture demonstrates a complete session lifecycle.

S1 validates credential injection + simulator replay + session events. S2 tests 3-source event normalization with conflict resolution and confidence levels. S3 exercises real PTY attach/detach with multi-client and rapid cycling. S4 tests workspace/config state and snapshot creation. S5 validates config path scheme determinism across sessions. S6 covers clean exit, crash recovery, forced stop, and config persistence. All 12 tests pass with `-race` and `go vet` is clean.

The P2 findings concern test depth rather than code correctness: the normalizer's dedup logic is too coarse for multi-tool scenarios, and S4/S5 don't fully exercise the lifecycle operations their names suggest. These are worth fixing to strengthen the test suite's coverage of plan requirements.
