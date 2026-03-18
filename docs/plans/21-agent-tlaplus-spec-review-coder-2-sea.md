# Review: 21-agent-tlaplus-spec — coder-2-sea

**Reviewer:** coder-2-sea
**Date:** 2026-03-17
**Review type:** Independent (no other review docs read)
**Source:** docs/plans/21-agent-tlaplus-spec.md
**Implementation reference:** internal/agent/agent.go, internal/agent/service.go

---

## Findings

### F1 [P1] — `running` flag is cleared by driver, not by terminal event — spec misses this asymmetry

**Section:** §2.3 Turn Lifecycle, §3.1 Turn mutual exclusion

The spec models the turn lifecycle as: `Start` sets `running=true` → terminal event fires → `doneTurn()` → turn complete. But in the actual code, `running` is NOT cleared by `doneTurn()`. It's cleared by the driver calling `a.onDriverIdle()` (agent.go:270-274), which is separate from the terminal event emission. The `doneTurn()` func in `newTurnScopedReceiver` only calls `s.wg.Done()` and `ms.clearTurnDone()` — it never touches `a.running`.

This means there are TWO independent "turn completion" signals:
1. `a.running = false` (set by driver via `onDriverIdle`, or by `Stop`)
2. Terminal event → `doneTurn()` → `wg.Done()` (set by the event subscriber)

These can complete in different orders. The spec should model both signals and verify they eventually both fire, and that `BusyError` is guarded by `running` (not by `turnDone`).

Also: `Start` rolls back `running=false` on transition failure (agent.go:161-164) and on driver.Start failure (agent.go:171-173). These rollback paths should be in the model.

**Recommendation:** Model `running` and `turnDone` as separate variables. Verify: (a) both are eventually cleared for every completed turn, (b) `BusyError` is keyed on `running`, (c) `wg.Wait()` is keyed on `turnDone`.

---

### F2 [P1] — DestroySession race window between getSession and markDestroyed is under-specified

**Section:** §2.3, §3.5 Post-destroy silence

The spec mentions the race in §1 ("A race between `DestroySession` and a concurrent `SendMessage`/`Continue` that has already resolved `getSession` but not yet started the turn") but does not commit to a specific modeling approach for it.

In the actual code:
1. `SendMessage` calls `s.getSession(req.SessionID)` under `s.mu` (service.go:221)
2. Returns `ms` pointer
3. `s.mu` is released
4. Meanwhile `DestroySession` can run: acquires `s.mu`, calls `ms.markDestroyed()`, releases `s.mu`, then proceeds to `agent.Stop()`, `ms.cancel()`, `ms.completeActiveTurn()`, `ms.closeAllStreams()`
5. `SendMessage` continues with the now-destroyed `ms`, calls `newTurnScopedReceiver`, then `safeCallStartOrPrompt`

The `getSession` check `ms.isDestroyed()` (service.go:569) is under `s.mu`, but there's no second check after the turn machinery is set up. If `DestroySession` runs between steps 2 and 5:
- `newTurnScopedReceiver` does `s.wg.Add(1)` (service.go:601)
- The agent `Start`/`Prompt` may succeed because `agent.Stop` hasn't been called yet
- `DestroySession` then calls `ms.completeActiveTurn()` which fires `doneTurn()`
- But the turn may still be running, and `agent.running` is still true

This is a real concurrency hazard. The spec should explicitly model this interleaving and verify that Post-destroy silence (safety property 5) still holds despite the race.

**Recommendation:** Model `getSession` → `(yield)` → `startTurn` as separate steps with `DestroySession` interleaved between them. This is the highest-value scenario for TLA+ to verify.

---

### F3 [P1] — Missing safety property: `running` flag must not be left permanently true

**Section:** §3 Safety Properties

None of the 8 safety properties or 4 liveness properties directly state that `a.running` is eventually cleared. Liveness property 1 (Turn termination) says "eventually produces a terminal event" but the terminal event alone doesn't clear `running` (see F1).

If `running` is permanently stuck at `true`:
- All subsequent `SendMessage`/`Continue`/`Prompt` calls return `BusyError` forever
- The session appears live but is permanently wedged

The code has several paths that clear `running`:
- `onDriverIdle()` (normal completion)
- `Stop()` force-clears it (agent.go:225)
- `Start` rollback on failure (agent.go:162, 172)

But `Continue` does NOT have the same rollback — if `driver.Resume` fails, it calls `onDriverIdle()` (agent.go:212), which is correct. However, if `driver.Resume` panics, `safeCall` catches it and returns an error, but `running` was already set to `true` at line 202. The `Continue` caller in service.go (line 256-261) calls `doneTurn()` and `receiver.Close()` on error, but neither clears `running`.

**Recommendation:** Add safety property: "If `a.running == true`, then eventually `a.running == false` (either via normal completion, Stop, or error recovery)." Also verify the `Continue` panic path.

---

### F4 [P2] — `Abort` emits `EventAborted` immediately, but spec models it as a terminal event that ends the turn

**Section:** §3.3 Terminal event uniqueness, §2.6 Tool Execution

The spec lists `EventAborted` as a terminal event (§3.3) and `isTerminalTurnEvent` in the code confirms this (service.go:746-753). However, `Agent.Abort()` (agent.go:262-268) immediately emits `EventAborted` via `a.emit()`. It does NOT stop the turn — it merely enqueues an abort via `control.EnqueueAbort()`.

This means:
1. `Abort()` emits `EventAborted` (terminal event) immediately
2. The driver may still be running, producing more events
3. The driver eventually processes the abort from the control queue and stops
4. The driver may emit its own terminal event (e.g., `EventTurnCompleted`)

This creates a scenario where two terminal events fire: `EventAborted` from `Abort()` and `EventTurnCompleted` from the driver. Safety property 3 ("exactly one terminal event") would be violated under this model.

The `doneTurn()` uses `sync.Once` so `wg.Done()` is called only once. But the receiver will see both events. The `closeWithError(io.EOF)` in the subscriber (service.go:625) also uses `sync.Once`, so the receiver closes on the first terminal event.

**Recommendation:** The spec should clarify that "exactly one terminal event" means "exactly one terminal event is delivered to the receiver" (not "exactly one is emitted"). Model the `sync.Once` gate that closes the receiver on the first terminal event seen.

---

### F5 [P2] — `SubscribeEvents` close hook ordering creates double-hook with overwrite potential

**Section:** §3.8 Receiver close idempotency

In `SubscribeEvents` (service.go:354-363):
```go
receiver.setCloseHook(func() { unsub() })
ms.addStream(receiver)
receiver.setCloseHook(func() { ms.removeStream(receiver) })
```

Two `setCloseHook` calls are made. Looking at the implementation (service.go:944-955), `setCloseHook` appends to `r.closeHooks` slice — it does not replace. So both hooks fire on close.

But in `newTurnScopedReceiver` (service.go:630-635):
```go
receiver.setCloseHook(func() {
    unsubTurn()
    unsubTracker()
})
ms.addStream(receiver)
receiver.setCloseHook(func() { ms.removeStream(receiver) })
```

Same pattern — two hooks appended. This is correct (append semantics), but the spec should explicitly model the hook list and verify that ALL hooks fire exactly once on close, not just the last one.

The spec's current statement "Multiple calls to `receiver.Close()` and `receiver.closeWithError()` are safe" (§3.8) is correct but incomplete — it should also cover "all registered hooks fire exactly once."

**Recommendation:** Extend property 8 to state that all close hooks registered before close fire exactly once, and hooks registered after close fire immediately.

---

### F6 [P2] — `Continue` does not call `Transition(StateStreaming)` — FSM state may not match reality

**Section:** §2.1 Agent State Machine, §3.2 Valid state transitions

`Agent.Start()` explicitly calls `a.Transition(StateStreaming)` (agent.go:160). But `Agent.Continue()` (agent.go:187-216) does NOT call any transition. It sets `running = true` and calls `driver.Resume()`, but the FSM state remains whatever it was before (likely `StateIdle`).

This means during a `Continue`-initiated turn, the agent's `state` field may show `StateIdle` while `running == true` and the driver is actively streaming. The FSM model in §2.1 shows `Idle --> Streaming` for all turn starts, but this doesn't match the `Continue` path.

The driver is responsible for calling `a.Transition()` during its execution — but between `Continue` return and the driver's first transition, there's a window where state and running are inconsistent.

**Recommendation:** The spec should model `Continue` as a separate action from `Start`/`Prompt` with its own transition semantics (or lack thereof). Verify that the FSM state eventually transitions out of Idle during a Continue-initiated turn.

---

### F7 [P2] — `Close()` double-cancels and double-`completeActiveTurn` — model should verify idempotency

**Section:** §4.3 Service close termination

`Close()` (service.go:454-506) has two phases:
1. Phase 1 (lines 468-477): For each session: `markDestroyed`, `agent.Stop`, `cancel()`, `completeActiveTurn()`, `closeAllStreams()`
2. Phase 2 timeout (lines 494-498): For each session again: `cancel()`, `completeActiveTurn()`

`cancel()` is called twice. `completeActiveTurn()` is called twice. The code is safe because:
- `context.CancelFunc` is idempotent
- `completeActiveTurn` sets `ms.turnDone = nil` before calling the func, so the second call is a no-op
- `doneTurn` uses `sync.Once` internally

But the spec should verify these idempotency guarantees formally, since they are load-bearing for the Close termination guarantee.

**Recommendation:** Model Close as a two-phase process and verify that the second phase's operations are no-ops if the first phase completed successfully.

---

### F8 [P2] — Missing concurrency hazard: `setCloseHook` after close fires hook immediately — interaction with subscriber setup

**Section:** §2.3 Turn Lifecycle

`setCloseHook` (service.go:944-955) has a special case: if the receiver is already closed, the hook fires immediately and synchronously. In `newTurnScopedReceiver`:

```go
receiver.setCloseHook(func() { unsubTurn(); unsubTracker() })   // line 630
ms.addStream(receiver)                                           // line 634
receiver.setCloseHook(func() { ms.removeStream(receiver) })     // line 635
```

If `DestroySession` runs concurrently and calls `closeAllStreams()` between lines 634 and 635, then:
1. `closeAllStreams` closes the receiver
2. Line 635 `setCloseHook` detects `r.closed == true` and calls `ms.removeStream(receiver)` synchronously
3. This is fine — but the hook that fires the `unsubTurn()` (registered at line 630) has already fired during close

The concern is whether `ms.removeStream` can deadlock if called under `r.mu` → `ms.mu` lock ordering while `closeAllStreams` holds `ms.mu`. Looking at the code: `setCloseHook` holds `r.mu`, and calls the hook which acquires `ms.mu`. `closeAllStreams` acquires `ms.mu`, then calls `receiver.Close()` which acquires `r.mu`. This is a potential deadlock: `r.mu → ms.mu` vs `ms.mu → r.mu`.

BUT: `closeAllStreams` copies the streams map and releases `ms.mu` before calling Close (service.go:1026-1037). So the deadlock is avoided. This is subtle and worth modeling.

**Recommendation:** Model the lock ordering between `serviceEventReceiver.mu` and `managedSession.mu`. Verify no deadlock exists in the interleaving of `closeAllStreams` and `setCloseHook`.

---

### F9 [P3] — Scope could include ControlQueue drain semantics

**Section:** §6 Scope Boundaries

The spec explicitly excludes "The `ControlQueue` internal implementation (modeled as a simple channel)." However, `ControlQueue` has `Close()` semantics (agent.go:237) that interact with `Steer`, `FollowUp`, and `Abort`. If `Stop()` closes the control queue before the driver drains it, enqueued steer/follow-up messages are lost.

This may be acceptable behavior, but the spec should at least state whether "messages enqueued before Stop are eventually processed" is a guarantee or not. If it's not a guarantee, that's fine — but it should be an explicit non-goal.

**Recommendation:** Add a note in §6 Scope Boundaries clarifying whether control queue drain is a guarantee. If not, add a note to §3 that steering/follow-up messages may be lost on Stop/Destroy.

---

### F10 [P3] — eventBus close + publish race should be in scope

**Section:** §6 Scope Boundaries

The spec excludes "The `eventBus` subscription/unsubscription mechanics." However, `Agent.Stop()` calls `a.bus.close()` (agent.go:238) AFTER emitting `EventSessionEnded` (agent.go:236). If a concurrent goroutine (e.g., a driver callback) tries to emit an event after the bus is closed, this could panic or silently drop events.

The `emit` method (agent.go:328-349) calls `a.bus.publish(event)`. If the bus is closed, this needs to be safe. This is a real concurrency hazard between `Stop` and a slow driver still emitting events.

**Recommendation:** Either include eventBus close semantics in scope, or add a safety property: "After `bus.close()`, no `publish` call panics."

---

### F11 [P3] — Spec says `WaitingFollowUp` state exists but no code path triggers it from the service layer

**Section:** §2.1 Agent State Machine

The `validTransitions` table includes `StateWaitingFollowUp` (agent.go:25-29), and the spec models it. But looking at the service layer, there is no code path that transitions to `WaitingFollowUp` — this transition would be initiated by the driver when it decides to wait for follow-up input. The `FollowUp` service method (service.go:291-307) enqueues a message but doesn't trigger a transition.

This isn't wrong — the driver drives the FSM — but the spec should clarify that `WaitingFollowUp` is a driver-initiated state that the service layer observes but never triggers. This distinction matters for modeling because the spec's process model should assign the `WaitingFollowUp` transition to the driver process, not the service process.

**Recommendation:** Clarify in §2.1 which transitions are service-initiated vs driver-initiated.

---

## Summary

| Severity | Count | Key Theme |
|----------|-------|-----------|
| P1 | 3 | `running` flag lifecycle, DestroySession race, missing stuck-running property |
| P2 | 5 | Abort double-terminal, Close idempotency, Continue FSM gap, hook ordering, lock ordering |
| P3 | 3 | ControlQueue drain, eventBus close, WaitingFollowUp attribution |

**Overall assessment:** The spec is well-structured and identifies the right concurrency concerns. The safety and liveness properties are mostly correct but have significant gaps around the `running` flag lifecycle (which is separate from the terminal event / `doneTurn` mechanism). The highest-value area for TLA+ verification is the DestroySession race window (F2) — this is exactly the kind of interleaving that's nearly impossible to test conventionally. The Abort double-terminal issue (F4) should be resolved at the spec level before writing TLA+ code, as it affects the fundamental terminal-event invariant.
