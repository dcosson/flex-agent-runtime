# Code Review: aiag-qkh.1 (R1, reviewer-sea)

- Bead: aiag-qkh.1
- Commit range: 1ccea6d..efa83a8
- Plan doc: docs/plans/01-ai-core.md §3, §4
- Test harness: docs/plans/01-ai-core-test-harness.md §1 (P1, P5), §4 (S1), §5 (B1), §6 (D1, D2)
- Reviewer: reviewer-sea
- Review commit: efa83a8

## Findings

### P3 - Simplified error wrapping in EventStream.Send deviates from plan

**Location:** `internal/ai/event_stream.go:50-54`

**Problem**
The plan (§4.2) specifies that error events should use `providerErrorFromMessage(event.Error)` to produce a `*ProviderError`-typed error. The implementation uses `errors.New(event.Error.ErrorMessage)` with a fallback to `"provider error"` for empty messages. This means callers doing `errors.As(&providerErr)` on the Result() error will get false, which deviates from the intended error typing contract.

**Suggested fix**
This is an expected deviation since `ProviderError` and `providerErrorFromMessage` are defined in §11 (errors.go), which is part of a later bead (qkh.4 or similar). When that bead is implemented, the error path in `Send()` should be updated to use `providerErrorFromMessage`. No action needed now — just noting the deviation for the implementer of the later bead.

---

### P3 - Bead closed before review

**Location:** `.beads/issues/closed/aiag-qkh.1.json`

**Problem**
Per CLAUDE.md: "Coding and plan writing task beads should normally be reviewed by a reviewer agent before closing them out." The bead was moved to `closed` in the same commit as the implementation, before review was completed.

**Suggested fix**
In future beads, leave the bead open (or set a "review pending" status) until the reviewer approves. The bead can be closed as part of the incorporate step after review findings are resolved.

---

## Plan Compliance Check

| Deliverable | Status |
|---|---|
| Message interface (sealed) with UserMessage, AssistantMessage, ToolResultMessage | ✅ |
| ContentBlock interface (sealed) with TextContent, ThinkingContent, ImageContent, ToolCall | ✅ |
| Usage, UsageCost structs | ✅ |
| StopReason constants (stop, length, toolUse, error, aborted) | ✅ |
| Tool struct (Name, Description, Parameters as json.RawMessage) | ✅ |
| Context struct (SystemPrompt, Messages, Tools) | ✅ |
| StreamOptions, SimpleStreamOptions, ThinkingLevel, CacheRetention, ThinkingBudgets | ✅ |
| EventType enum (12 variants) | ✅ |
| AssistantMessageEvent tagged union | ✅ |
| EventStream with terminal-state guarantee (atomic.Bool + CompareAndSwap) | ✅ |
| NewEventStream, Send, Close (idempotent via sync.Once), Result, Drain | ✅ |
| ErrStreamClosedWithoutTerminalEvent sentinel error | ✅ |
| TimeToMillis, MillisToTime helpers | ✅ |
| No StopReasonMaxTokens (implementation guide §1.3) | ✅ |
| Buffer size 32 (implementation guide §1.2) | ✅ |

## Test Harness Coverage

| Test | Status | Notes |
|---|---|---|
| P1: EventStream ordering (rapid) | ✅ | 100 rapid iterations, verifies sequential ordering |
| P5: Terminal-state guarantee | ✅ | 6 cases: done, error, close-without-terminal, panic, double-done, done-then-close |
| D1: Slow consumer | ✅ | 200 events with 100μs delay per event |
| D2: Context cancellation timing | ✅ | Cancel mid-stream, verify terminal result still arrives |
| S1: 1M event throughput | ✅ | Passes in ~0.28s (well under 8s threshold) |
| B1: EventStream benchmark | ✅ | BenchmarkEventStreamSendReceive |
| Unit tests: types, roles, timestamps | ✅ | TestMessageRolesAndTimestamps, TestContentTypes |
| Unit tests: time round-trip | ✅ | TestTimeMillisRoundTrip |
| Unit tests: close idempotency | ✅ | TestEventStreamCloseIdempotent |
| Unit tests: drain | ✅ | TestEventStreamDrain |
| All tests pass with `-race` | ✅ | 0 race warnings |

## Implementation Quality Notes

- **Thread safety**: EventStream uses `sync.Once` for Close idempotency and `atomic.Bool` with CompareAndSwap for terminal-state guarantee. No lock contention on hot path. Correct.
- **Channel design**: Separate `ch` (events) and `result` (terminal) channels with appropriate buffer sizes (32 and 1). Prevents Result() from requiring event draining.
- **Sealed interfaces**: Both Message and ContentBlock use unexported methods (`messageRole()`, `contentBlockType()`), correctly sealing them to the package.
- **Pointer receivers**: All Message/ContentBlock methods use pointer receivers, consistent with plan and Go conventions for types holding slices.
- **Code organization**: Clean separation into types.go, events.go, event_stream.go, options.go, time.go — matches plan §13 file layout.

## Summary

2 findings: 0 P0, 0 P1, 0 P2, 2 P3

**Verdict**: Approved

Clean, well-structured implementation that faithfully follows the plan. All required deliverables are present, all test harness tests pass including with `-race`. The two P3 findings are minor: a justified deviation in error typing (to be wired up when ProviderError is implemented in a later bead), and a process nit about closing beads before review.
