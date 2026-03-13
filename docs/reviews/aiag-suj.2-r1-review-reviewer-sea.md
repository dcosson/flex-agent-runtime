# Code Review: aiag-suj.2 (R1, reviewer-sea)

- Bead: aiag-suj.2
- Commit range: 3091d28 (branch batch3/agent-loop)
- Plan doc: docs/plans/05-agent.md §5, §7.1
- Reviewer: reviewer-sea
- Review commit: 3091d28

## Findings

### P2 - ControlMessage field reused for text/thinking deltas

**Location:** `internal/agent/loop.go:192-195`

**Problem**
`callProvider()` uses `ControlMessage` to carry text and thinking delta content:
```go
case ai.EventTextDelta:
    d.emit(AgentEvent{Type: EventAgentMessageDelta, Turn: d.turn, ControlMessage: ev.Delta})
case ai.EventThinkingDelta:
    d.emit(AgentEvent{Type: EventThinkingDelta, Turn: d.turn, ControlMessage: ev.Delta})
```

We just established in the suj.1 review that `ControlMessage` is for control-plane messages (steer, follow-up, abort). Reusing it for streaming content deltas creates the same semantic confusion we fixed — subscribers filtering on `ControlMessage != ""` to detect control events will get false positives on every text delta.

**Suggested fix**
Add a `Delta string` field to `AgentEvent` for streaming content. Use `ControlMessage` only for control events.

---

### P2 - Agent.Start no longer transitions to Streaming before calling driver

**Location:** `internal/agent/agent.go:146-160`

**Problem**
In suj.1, `Start()` called `a.Transition(StateStreaming)` before calling `a.driver.Start()`. That was removed — now the driver's `run()` goroutine does the transition. This means there's a window between `Start()` returning and the goroutine executing where `a.State()` is still `StateIdle` and `a.running` is `true`. A concurrent caller checking state would see `Idle` but get `BusyError` on any operation.

This is a minor race in observable state but could confuse consumers. The plan §2.3 shows `Idle → Streaming` happening on `Prompt`, not asynchronously.

**Suggested fix**
Either: (a) have `startLoop()` transition to `StateStreaming` synchronously before spawning the goroutine (and have the goroutine skip the transition if already streaming), or (b) document that the transition is asynchronous and state may briefly lag. Option (a) is cleaner.

---

### P3 - appendUserMessage uses turn+1 before turn is incremented

**Location:** `internal/agent/loop.go:284`

**Problem**
`appendUserMessage()` sets `Turn: d.turn + 1`, but `executeSingleTurn()` increments `d.turn++` at the start. So user messages are tagged with the correct next turn number. However, this creates a subtle coupling — the caller must know that `appendUserMessage` is always called before `executeSingleTurn`. If the call order ever changes, turn numbers would be wrong.

**Suggested fix**
Minor — current usage is correct. Consider adding a comment documenting the ordering assumption, or deferring turn assignment to `executeSingleTurn` where both user message append and turn increment happen together.

---

### P3 - lookupTool is O(n) per tool call

**Location:** `internal/agent/loop.go:299-304`

**Problem**
`lookupTool()` does a linear scan over `d.cfg.Tools` for every tool call. With typical tool counts (5-20), this is negligible. But if tool counts grow or tool calls are frequent, a map would be more efficient.

**Suggested fix**
No change needed now. Consider building a `map[string]AgentTool` in `NewNativeDriver` if tool counts grow beyond ~50.

---

## Summary

4 findings: 0 P0, 2 P2, 2 P3

**Verdict**: Approved with revisions

**Review notes:**
- NativeDriver turn execution loop correctly implements plan §5.1: snapshot conversation → transform → stream → tool execute → follow-up drain
- Provider event relay works correctly via `callProvider()` consuming `es.C` channel then calling `es.Result()`
- Follow-up FIFO ordering tested and working — `nextPromptFromControlQueue()` correctly drains control queue, prioritizes abort, then steer, then follow-ups
- Terminal tool short-circuit correctly prevents extra LLM continuation (provider called once, not twice)
- Snapshot trigger seam: `turn_completed` emitted before `state_change(idle)`, verified by test
- `agentBinder` interface pattern for bidirectional agent↔driver binding is clean
- Tool execution correctly handles tool-not-found with error result appended to conversation
- Tests cover the three key scenarios: tool loop + snapshot boundary, follow-up FIFO, terminal tool short-circuit
- `DriverConfig.Model` correctly changed from `string` to `ai.Model` (needed for `ai.StreamSimple`)
- Race detector passes clean on `internal/agent`
- Pre-existing `make check` failure in `internal/tools` (undefined `LocalBackend`) is not from this branch
