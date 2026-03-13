# Code Review: aiag-qkh.4 (R1, reviewer-sea)

- Bead: aiag-qkh.4
- Commit range: 66d0313..eed201d
- Plan doc: docs/plans/01-ai-core.md §10, §11, §14
- Test harness: docs/plans/01-ai-core-test-harness.md §1 (P2, P6, P8), §3 (F4), §5 (B3)
- Reviewer: reviewer-sea
- Review commit: eed201d

## Findings

### P2 - EventStream.Send not updated to use providerErrorFromMessage

**Location:** `internal/ai/event_stream.go:49-54`

**Problem**
Now that `providerErrorFromMessage` exists in errors.go (this commit), the EventStream.Send error path should use it instead of the simplified `errors.New(event.Error.ErrorMessage)` from the qkh.1 implementation. The plan (§4.2) specifies:
```go
Err: providerErrorFromMessage(event.Error),
```
Without this, callers doing `errors.As(&providerErr)` on the `Result()` error will get false, breaking typed error handling for downstream consumers.

**Suggested fix**
Update the EventError case in `Send()`:
```go
case EventError:
    if event.Error != nil && s.terminated.CompareAndSwap(false, true) {
        s.result <- resultOrError{
            Message: *event.Error,
            Err:     providerErrorFromMessage(event.Error),
        }
    }
```
This also removes the manual empty-message fallback since `providerErrorFromMessage` handles nil and empty messages.

---

### P3 - ErrStreamClosedWithoutTerminalEvent not in public API re-exports

**Location:** `ai/reexport.go`

**Problem**
The sentinel error `ErrStreamClosedWithoutTerminalEvent` is not re-exported in the public API. Consumers who call `Result()` and want to check for this specific error can't do `errors.Is(err, ai.ErrStreamClosedWithoutTerminalEvent)`.

**Suggested fix**
Add to the `var` block:
```go
ErrStreamClosedWithoutTerminalEvent = internal.ErrStreamClosedWithoutTerminalEvent
```

---

### P3 - Bead closed before review

**Location:** `.beads/issues/closed/aiag-qkh.4.json`

**Problem**
Same process issue as prior beads.

---

## Plan Compliance Check

| Deliverable | Status |
|---|---|
| TransformMessages two-pass algorithm | ✅ |
| transformAssistantMessage (skip error/aborted, sameModel, thinking→text, signature stripping) | ✅ |
| transformToolResult (ID remapping) | ✅ |
| insertSyntheticToolResults (sorted IDs, deterministic) | ✅ |
| ToolCallIDNormalizer type | ✅ |
| ProviderError struct (Code, Message, StatusCode, Provider, RetryAfter) | ✅ |
| ProviderErrorCode constants (5 variants) | ✅ |
| providerErrorFromMessage (classifyErrorMessage) | ✅ |
| IsContextOverflow (pattern + silent overflow) | ✅ |
| All 15 overflow regex patterns | ✅ |
| 4xx empty body pattern | ✅ |
| Public API re-exports (types, constants, functions) | ✅ |
| Implementation guide §1.3: No StopReasonMaxTokens | ✅ |

## Test Harness Coverage

| Test | Status | Notes |
|---|---|---|
| P2: TransformMessages idempotency (rapid) | ✅ | 100 iterations with random conversations |
| P6: Transform message count bound | ✅ | Verifies len(output) <= len(input) + toolCallCount |
| P8: Deterministic output (orphaned tool calls) | ✅ | 100 iterations with 3 unsorted tool call IDs |
| B3: Transform benchmark | ✅ | 50-message conversation with tool calls |
| F4: Overflow pattern fuzzing | ✅ | 4 seeds, exercises both classifyErrorMessage and IsContextOverflow |
| Unit tests: cross-model transform, error/aborted skip, overflow patterns, providerErrorFromMessage | ✅ | |
| All tests pass with `-race` | ✅ | 0 race warnings |

## Implementation Quality Notes

- **Deterministic synthetic results**: Sort by tool call ID before inserting, matching plan §10.4.
- **Timestamp handling improvement**: End-of-conversation flush uses `lastTimestamp` from conversation rather than `time.Now()`, keeping synthetic results temporally consistent with the conversation being transformed.
- **Nil guard on IsContextOverflow**: Defensive nil check not in plan but prevents panics from callers passing nil messages.
- **flush helper function**: Clean refactor of the plan's inlined flush logic into a reusable function.

## Summary

3 findings: 0 P0, 0 P1, 1 P2, 2 P3

**Verdict**: Approved with revisions

Faithful implementation matching the plan closely. All required transform, overflow, and re-export functionality is present with comprehensive test coverage including property-based tests. The P2 finding (wiring providerErrorFromMessage into EventStream.Send) should be addressed since the function now exists in the same package.
