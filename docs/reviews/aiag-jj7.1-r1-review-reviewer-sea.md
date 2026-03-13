# Code Review: aiag-jj7.1 (R1, reviewer-sea)

- Bead: aiag-jj7.1
- Commit range: 1baf2fd..c366545
- Plan doc: docs/plans/02-provider-anthropic-test-harness.md §2, docs/plans/03-provider-openai-test-harness.md §2, docs/plans/04-provider-google-test-harness.md §2
- Reviewer: reviewer-sea
- Review commit: db9a654

## Findings

### P3 - Malformed fault injects extra event instead of replacing the target event

**Location:** `internal/ai/testutil/stubserver/stubserver.go:219-229`

**Problem**
When `mode == Malformed && eventsSent == fault.AfterEvents`, the code injects the malformed data and increments `eventsSent`, but uses `continue` to skip the original event at that index. This means the malformed data is inserted *before* the event at position `AfterEvents`, and the original event is skipped entirely. The total event count stays the same, but the semantics are "replace event N with malformed data" rather than "inject malformed data after N events" as the struct field name `AfterEvents` suggests.

The name `AfterEvents` implies injection *after* N normal events have been sent — which is what TCPReset does (fires when `eventsSent >= fault.AfterEvents`). But Malformed fires when `eventsSent == fault.AfterEvents`, meaning it replaces the Nth event rather than injecting after it. This naming inconsistency between the two fault modes could confuse provider test authors.

**Suggested fix**
Either rename `AfterEvents` to `AtEventIndex` (accurate for Malformed), or change the Malformed logic to inject malformed data *in addition to* the original event (making it truly "after"). Alternatively, just add a doc comment on `AfterEvents` clarifying the per-mode semantics: "For TCPReset, disconnects after this many events. For Malformed, replaces the event at this index."

---

### P3 - TestTCPReset cutAfter > events count not tested

**Location:** `internal/ai/testutil/stubserver/stubserver_test.go:165`

**Problem**
`TestTCPReset` tests `cutAfter` values `{0, 1, 3, 5}` against the anthropic fixture which has 7 events. There's no case where `cutAfter` exceeds the total event count (e.g., `cutAfter=10`), which would verify the server gracefully serves all events and closes normally when the fault threshold is never reached. This is a minor gap — the TCPReset code uses `>=` so it would fire after the last event in this case, which is probably fine, but a `cutAfter > len(events)` case would document that behavior.

**Suggested fix**
Add `cutAfter=10` to the test table to verify full delivery when the threshold exceeds event count.

---

## Plan Compliance Check

| Deliverable | Status |
|---|---|
| Shared stub server at `internal/ai/testutil/stubserver/` | ✅ |
| Provider-agnostic design (works with any SSE format) | ✅ (Anthropic + OpenAI fixtures tested) |
| S1: Correctness replay mode (fixture replay over HTTP) | ✅ (`WithFixture`, `WithFixtureFunc`, `Content-Type: text/event-stream`) |
| F1: Mid-stream TCP reset | ✅ (`TCPReset` fault mode, `NewTCPResetServer`, hijack + close) |
| F2: Malformed SSE payload | ✅ (`Malformed` fault mode, 4 corruption variants tested) |
| F3: API throttling (429 + Retry-After) | ✅ (`Throttle` fault mode, with/without Retry-After) |
| F4: Slow-consumer backpressure | ✅ (`Backpressure` fault mode, per-event delay) |
| F5: Context cancellation races | ✅ (TestContextCancellationRace, 50 goroutines with varying timeouts) |
| F6: Empty error body (status code only) | ✅ (`EmptyBody` fault mode, 7 status codes tested) |
| Request capture for assertion | ✅ (`CapturedRequest` with method, path, headers, body) |
| Concurrent safety | ✅ (mutex-protected request capture, 20-goroutine test) |
| All tests pass with `-race` | ✅ |
| `make check` clean | ✅ |

## Implementation Quality Notes

- **Options pattern**: Clean `Option func(*Server)` design with `WithFixture`, `WithFixtureFunc`, `WithFault`, `WithHandler`. Composable and extensible for provider-specific needs.
- **Convenience constructors**: `NewTCPResetServer`, `NewMalformedServer`, `NewThrottleServer`, `NewBackpressureServer`, `NewEmptyBodyServer`, `NewStatusCodeServer`, `NewSequenceServer` — reduce boilerplate for common test scenarios.
- **`NewSequenceServer`**: Nice addition beyond the plan spec. Enables clean retry-behavior testing with per-request response sequences.
- **`splitSSEEvents` helper**: Well-tested with 7 cases (empty, single, multi, event types, trailing whitespace, CRLF). Correctly normalizes line endings and handles edge cases.
- **Fixture format**: Raw SSE text — fully provider-agnostic. No Anthropic/OpenAI-specific types in the server; provider awareness lives entirely in the fixture data. This is the right separation.

## Summary

2 findings: 0 P0, 0 P1, 0 P2, 2 P3

**Verdict**: Approved

Well-designed shared test infrastructure that cleanly implements all 6 fault modes specified across the three provider test harness plans. The provider-agnostic design (raw SSE fixtures, no provider-specific types) is exactly right — it lets each provider's test suite bring its own fixtures while sharing the server mechanics. Both P3 findings are minor documentation/completeness nits.
