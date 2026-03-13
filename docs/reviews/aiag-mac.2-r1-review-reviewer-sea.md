# Code Review: aiag-mac.2 (R1, reviewer-sea)

- Bead: aiag-mac.2
- Commit range: 3f2764c (single commit)
- Plan doc: `docs/plans/03-provider-openai-test-harness.md`
- Reviewer: reviewer-sea
- Review commit: 3f2764c

## Findings

### P2 - Missing plan items should be tracked as deferred

**Location:** `internal/ai/provider/openai/harness_test.go` (overall coverage)

**Problem**
The plan specifies S1 (Event Transition Validator FSM), O1 (cross-implementation oracle), O2 (cross-provider semantic), B2 (allocation budget), and B4 (end-to-end latency overhead). None of these are implemented. S1 (FSM-based illegal trace validator) and B2 (allocs-per-event budget) are the most valuable of the missing items — S1 catches protocol violations that other tests can't, and B2 prevents allocation regressions. O1/O2 are reasonable deferrals since they need external TypeScript/cross-provider infrastructure.

**Suggested fix**
Add TODO comments at the top of harness_test.go and benchmark_test.go listing the deferred items (S1, B2, B4) with a reference to the plan sections, so they don't get lost. O1/O2 can be tracked in a follow-up bead since they need cross-system infrastructure.

---

### P3 - SEC1 large_string subtest is slow (2.8s)

**Location:** `internal/ai/provider/openai/harness_test.go:914`

**Problem**
The `large_string` case in `TestSEC1_MaliciousToolJSON` uses a 64KB string split into 50-byte chunks, producing ~1300 SSE events. This single subtest takes 2.8s of the total 4.9s test runtime. Since this runs in the `test-harness-openai` target (not stress tier), it slows down the fast feedback loop. The plan suggested 1MB which would be worse.

**Suggested fix**
Reduce to 8KB or 16KB for the harness test (the security invariant is about parser safety, not throughput). If you want to test truly large payloads, add a separate stress-tier test.

---

### P3 - F5 uses deprecated math/rand.Intn

**Location:** `internal/ai/provider/openai/harness_test.go:628`

**Problem**
`rand.Intn(20)` uses the deprecated `math/rand` top-level function. Go 1.20+ uses auto-seeded global source, so it works, but `math/rand/v2` is the current recommended API. Minor, and staticcheck doesn't flag it yet.

**Suggested fix**
Switch to `rand.IntN(20)` from `math/rand/v2`, or use `rapid.IntRange(0, 19).Draw(t, "cancelDelay")` since you're already using rapid in the same file.

---

### P3 - Stress test concurrency levels reduced from plan

**Location:** `internal/ai/provider/openai/benchmark_test.go:202,245`

**Problem**
Plan specifies ST2 at 500 concurrent and ST3 at 100 concurrent. Implementation uses 200 and 50 respectively. The tests pass at these levels, but the reduced numbers provide less confidence in concurrency safety. This is likely a pragmatic choice for CI stability.

**Suggested fix**
Add a comment noting the plan targets and why the numbers were reduced (e.g., CI stability). Consider adding a build tag or env var gated version that runs at the plan's full concurrency levels for weekly CI.

---

## Summary

4 findings: 0 P0, 0 P1, 1 P2, 3 P3

**Verdict**: Approved with revisions

Excellent test harness implementation. The property tests (P1-P6 using rapid) are well-designed — P2 tool JSON convergence with random chunk boundaries and P4 multi-tool index isolation are particularly valuable. Fault injection (F1-F6) covers all key failure modes including TCP reset, malformed payloads, throttling, backpressure, and context cancellation races. The SSE fixture builders use `json.Marshal` for safe escaping. Security tests cover injection, API key leakage, and oversized payloads. Stress tests verify no memory leaks (ST1), concurrent safety (ST2 200/200 success), and tool call burst stability (ST3 50/50 success). All tests pass clean with -race. The single P2 is about tracking deferred plan items so they don't get lost.
