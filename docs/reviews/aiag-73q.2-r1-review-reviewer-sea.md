# Code Review: aiag-73q.2 (R1, reviewer-sea)

- Bead: aiag-73q.2
- Commit range: 63675f4..0b44aea
- Plan doc: docs/plans/17-external-testing.md (Section 12)
- Reviewer: reviewer-sea
- Review commit: 0b44aea

## Findings

### P3 - RetrySequence test doesn't assert on retry behavior

**Location:** `e2etests/external/tier1/stubserver_test.go:513-520`

**Problem**
The test logs "stubserver received 1 requests" — the provider doesn't retry on 429, so the 429→429→200 sequence is never exercised. The test passes but is functionally equivalent to F3 (single 429 error handling). The comment at line 518-519 acknowledges this ("Even if retry isn't implemented yet...") which is honest, but the test doesn't assert on anything retry-specific.

**Suggested fix**
Either: (a) assert that `len(reqs) >= 3` and skip the test with `t.Skip("provider retry not implemented")` if `len(reqs) < 3`, making it clear this test is waiting for retry support, or (b) at minimum assert on the provider error event from the 429 (like F3 does) so the test is actively verifying something rather than just reaching idle.

---

### P3 - Multi-turn test has tracked-but-unasserted variables

**Location:** `e2etests/external/tier1/stubserver_test.go:176-212`

**Problem**
`hasToolStarted` and `hasToolCompleted` are tracked through the event loop but then suppressed with blank identifier assignments (`_ = hasToolStarted`, `_ = hasToolCompleted`) at lines 211-212. This looks like assertions that were started but abandoned. The comment at lines 183-187 explains the tool isn't registered, but the blank identifier pattern is confusing — a reader expects these to be asserted on.

**Suggested fix**
Remove the tracking and blank assignments entirely. The test already asserts on `hasMessageCompleted` and request count, which is sufficient to verify SSE parsing worked. If tool event assertions are desired later, they can be added when a registered tool is used.

---

## Summary

2 findings: 0 P0, 0 P1, 0 P2, 2 P3

**Verdict**: Approved

The implementation is well-structured and thorough. The `stubAgentEnv` helper cleanly wires a real Anthropic provider to an in-process stubserver, exercising the full HTTP/SSE/JSON stack without Docker. All 9 tests cover meaningful scenarios: simple text response with delta verification, multi-turn with fixture rotation, auth header propagation (dual-verified via callback and request capture), and 5 fault injection modes (F1 TCP reset, F2 malformed SSE, F3 rate limit, F4 backpressure, F6 empty body). Provider registration cleanup via `ai.UnregisterProviders(sourceID)` ensures test isolation. All tests pass including with `-race`. The 2 P3s are minor style/documentation issues that can be addressed at convenience.
