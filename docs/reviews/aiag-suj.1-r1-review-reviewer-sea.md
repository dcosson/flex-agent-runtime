# Code Review: aiag-suj.1 (R1, reviewer-sea)

- Bead: aiag-suj.1
- Commit range: 105e1df (branch batch3/agent-core-types)
- Plan doc: docs/plans/05-agent.md §2-§6
- Reviewer: reviewer-sea
- Review commit: 105e1df

## Findings

### P1 - make check fails: two unused functions

**Location:** `internal/agent/event_bus.go:64` and `internal/agent/types.go:156`

**Problem**
`staticcheck` reports two unused functions:
- `(*eventBus).count` (U1000)
- `AgentEvent.validate` (U1000)

Neither is called anywhere in the package. Per CLAUDE.md: "Always run `make check` before committing. All must pass clean — no warnings, no unused code."

**Suggested fix**
Remove both functions. If they are intended for use by `NativeDriver` (aiag-suj.2), they should be added in that bead when they have callers. Dead code should not be committed.

---

### P2 - Steer() emits wrong event type

**Location:** `internal/agent/agent.go:211`

**Problem**
`Steer()` emits `EventFollowUpEnqueued` instead of `EventSteeringApplied`. The plan (§3.4) defines these as distinct event types: `steering_applied` vs `followup_enqueued`. Subscribers that distinguish between steering and follow-up events (e.g., for logging, metrics, or snapshot trigger logic) will misclassify steers as follow-ups.

**Suggested fix**
Change line 211 from `EventFollowUpEnqueued` to `EventSteeringApplied`.

---

### P2 - Steer/FollowUp use ErrorMessage field for message content

**Location:** `internal/agent/agent.go:211,219`

**Problem**
Both `Steer()` and `FollowUp()` store the control message in `AgentEvent.ErrorMessage`. This field is semantically for error descriptions (used by `EventDriverError`, `EventProviderError`, `EventToolError`). Overloading it for steering/follow-up message content is misleading to subscribers.

The `AgentEvent` struct has no dedicated field for control message content. Since this is a foundational type, adding a field now is easier than retrofitting later.

**Suggested fix**
Either: (a) add a `Content string` or `ControlMessage string` field to `AgentEvent` and use that, or (b) use the existing `Metadata` map (e.g., `Metadata: map[string]any{"message": message}`). Option (a) is cleaner since these are high-frequency events.

---

### P2 - ControlQueue.Enqueue blocks on full buffer

**Location:** `internal/agent/control_queue.go:77`

**Problem**
`ch <- cmd` is a blocking channel send. If the 64-slot buffer fills, `Enqueue` blocks the calling goroutine indefinitely. Plan §6 states: "Abort() is idempotent and safe from any goroutine" — blocking on a full queue contradicts this guarantee. A stuck `Abort()` call from a watchdog goroutine could deadlock the system if the queue consumer is stalled.

**Suggested fix**
Use a non-blocking send with `select`/`default` for at least `ControlAbort` commands (return a "queue full" error or evict the oldest entry). Alternatively, make all enqueue operations non-blocking:
```go
select {
case ch <- cmd:
    return nil
default:
    return fmt.Errorf("control queue full")
}
```

---

### P3 - Agent.Subscribe double-registers in both agent.subscribers and eventBus

**Location:** `internal/agent/agent.go:102-115`

**Problem**
`Subscribe()` registers the callback in both `a.subscribers` (map on Agent) and `a.bus` (eventBus). The `a.subscribers` map is never read — only written to during subscribe and deleted during unsubscribe. The actual fan-out happens through `a.bus.publish()`. The map appears to be unused bookkeeping.

**Suggested fix**
Either use `a.subscribers` for something (e.g., subscriber count, iteration) or remove it. If the intent is subscriber count tracking, `a.bus.count()` already exists (though it's currently flagged as unused by staticcheck).

---

### P3 - Terminal field on AgentTool deviates from plan

**Location:** `internal/agent/types.go:101`

**Problem**
The implementation adds a `Terminal bool` field directly on `AgentTool`. The plan (§5.4) says "Terminal tools are declared in options by tool name." This is a minor structural deviation — the implementation approach is arguably cleaner since it co-locates the terminal flag with the tool definition, avoiding a separate name-lookup table.

**Suggested fix**
No code change needed — the implementation is better than the plan's approach. Update the plan doc to match if/when signoff happens.

---

## Summary

6 findings: 0 P0, 1 P1, 3 P2, 2 P3

**Verdict**: Approved with revisions

**Review notes:**
- Overall this is a solid foundational implementation. Types, state machine, event bus, control queue, driver registry, and errors all closely match the plan.
- State machine transitions match the plan's FSM diagram exactly (§2.3)
- Event bus panic isolation works correctly (tested)
- Control queue FIFO ordering verified by test
- Session clone semantics for thread safety are well-implemented
- Typed error hierarchy with sentinel errors and Unwrap() is idiomatic Go
- Driver registry follows the same pattern as the provider registry from internal/ai
- Tests cover key scenarios: FSM transitions, exited terminal state, busy rejection, session-scoped events, conversation append, abort queueing, queue FIFO, queue close rejection, unknown command rejection, panic isolation, unsubscribe
- Race detector passes clean
