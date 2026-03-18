# Plan Review: 21-agent-tlaplus-spec (reviewer-sea)

- Plan doc: docs/plans/21-agent-tlaplus-spec.md
- Reviewer: reviewer-sea
- Review date: 2026-03-17
- Implementation files consulted: internal/agent/service.go, internal/agent/agent.go, internal/agent/control_queue.go, internal/agent/event_bus.go

---

## Findings

### P1-1 - Abort dual-emission hazard not captured in terminal event uniqueness property

**Location:** Plan §3 Safety Property 3, §2.6

**Problem**
`Agent.Abort()` (agent.go:262-268) directly emits `EventAborted` via the event bus immediately upon enqueuing the abort command to the ControlQueue. However, the driver — which receives the abort command asynchronously via the ControlQueue — may *also* emit a terminal event (e.g., `EventAborted` or `EventDriverError`) when it processes the abort. This creates a dual-emission path for terminal events within the same turn.

The spec's safety property 3 ("every started turn produces exactly one terminal event — never zero, never more than one") is the single most valuable property to verify, but the spec does not model this dual-path emission. The actual deduplication relies on the `sync.Once` in `doneTurn()` and the receiver's `sync.Once` in `closeWithError`, but these operate at different layers. If the driver emits `EventTurnCompleted` and the caller emits `EventAborted` concurrently, both reach the turn-scoped subscriber (service.go:619-628). The subscriber pushes both to the channel and calls `closeWithError` twice (the second is a no-op via `sync.Once`), but the consumer may receive two terminal events before the channel closes.

**Suggested fix**
The spec must model two independent sources of terminal events: (a) the caller-side `Abort()` direct emit, and (b) the driver-side terminal event emit. The TLA+ spec should verify that the consumer observes at most one terminal event, accounting for the buffered channel window where both events could be enqueued before `closeWithError` takes effect. This is the highest-value verification target for the entire spec.

---

### P1-2 - Close() drain second-pass completeActiveTurn is load-bearing but not explicitly modeled

**Location:** Plan §2.2, §4 Liveness Property 3

**Problem**
`Close()` (service.go:454-506) has a two-phase timeout. The first pass destroys all sessions and calls `completeActiveTurn()`. If wg.Wait() times out, a second pass calls `cancel()` + `completeActiveTurn()` again (lines 495-498). This second pass is load-bearing for the following race:

1. `Close()` first pass: `completeActiveTurn()` — `turnDone` is nil (no active turn), no-op
2. A concurrent `SendMessage` that already passed `getSession()` now executes `newTurnScopedReceiver` — increments `wg`, sets a new `turnDone`
3. `Close()` reaches `wg.Wait()` — blocked by the new turn
4. Drain timeout fires, second-pass `completeActiveTurn()` clears this late-arriving turn

Without modeling this race, the spec cannot verify liveness property 3 (Close terminates). The spec mentions the `wg.Wait()` and timeout but doesn't call out why the second `completeActiveTurn` exists or what interleaving makes it necessary.

**Suggested fix**
Explicitly model the window between `getSession` returning a session and `newTurnScopedReceiver` incrementing `wg`. The Close() drain model should include both passes of `completeActiveTurn` and verify that liveness holds even when a turn starts between the first pass and `wg.Wait()`.

---

### P1-3 - ControlQueue backpressure can violate cancellation liveness

**Location:** Plan §6 Scope Boundaries, §4 Liveness Property 2

**Problem**
The spec abstracts ControlQueue as "a simple channel" (§6) and does not model the non-blocking send with `ErrQueueFull` backpressure (control_queue.go:77-82). In the actual implementation, `Enqueue()` uses a `select` with a `default` case — if the buffered channel (capacity 64) is full, the enqueue silently fails with `ErrQueueFull`. The caller (`Steer`, `FollowUp`, `Abort`) propagates this error, but the error is returned to the RPC caller, not retried.

This directly affects liveness property 2 ("A cancelled context eventually stops event production and causes the turn to reach a terminal state"). If `Abort()` fails because the ControlQueue is full, the only remaining cancellation path is context cancellation via `DestroySession`. If the spec models ControlQueue as a reliable channel, it will incorrectly prove that Abort always leads to turn termination.

**Suggested fix**
Either: (a) model the ControlQueue with bounded capacity and non-blocking send, verifying that liveness holds even when Abort fails due to a full queue (context cancellation via session destroy must serve as the fallback), or (b) explicitly state the assumption that the ControlQueue never fills and document this as a model limitation.

---

### P2-1 - `started` flag creates a hidden state dimension not in the FSM

**Location:** Plan §2.1, §2.3

**Problem**
The Agent FSM (§2.1) models states `{Idle, Streaming, ToolExecution, WaitingFollowUp, Exited}`. But `managedSession.started` (service.go:89) is a separate boolean that determines whether `SendMessage` calls `agent.Start()` (first message) or `agent.Prompt()` (subsequent messages). `ResumeSession` sets `started=true` immediately (service.go:408), meaning resumed sessions always take the `Prompt` path.

The `Start` vs `Prompt` distinction matters because `Start` calls `session.Clone()` and sets the session, while `Prompt` calls `a.Start(ctx, a.Session(), prompt)` which re-clones. If the spec doesn't model this flag, it can't verify that resumed sessions behave correctly on their first `SendMessage` (which calls Prompt → Start with a re-cloned session, not Start with the original session).

**Suggested fix**
Add `started` as a modeled variable on the session, or explicitly document why the Start/Prompt distinction is abstracted away (e.g., "both paths end up in Start with a cloned session, so the distinction is irrelevant to the properties we verify").

---

### P2-2 - Event ordering property (safety 4) conflates emission order with observation order

**Location:** Plan §3 Safety Property 4

**Problem**
Safety property 4 states: "Events emitted by a single turn are totally ordered by their emission through `bus.publish`. No consumer observes events from the same turn in a different order."

The first sentence is true — `bus.publish` is called sequentially from the driver goroutine. But `bus.publish` iterates over a map snapshot (event_bus.go:43-47), and Go map iteration order is non-deterministic. This means if there are two subscribers (e.g., the turn tracker and the turn receiver in `newTurnScopedReceiver`), they may process the *same* event at different relative times, but each individual subscriber sees events in emission order because it's the same goroutine calling them sequentially.

The property as stated is correct but the reasoning is subtle. The TLA+ spec should model this carefully: the ordering guarantee is per-subscriber (the subscriber callback is invoked in emission order), not cross-subscriber (different subscribers may not have processed event N when another has already processed event N+1).

**Suggested fix**
Restate the property as: "For any single subscriber, events from a turn are observed in emission order." Note that cross-subscriber ordering is not guaranteed and doesn't need to be — each receiver has its own channel.

---

### P2-3 - State space explosion risk needs explicit mitigation strategy

**Location:** Plan §2.4, §2.5, §2.6

**Problem**
The spec proposes modeling: N sessions × concurrent callers per session × tool execution steps × session forking × failure modes. Even with small constants (2 sessions, 2 callers, 2 tool calls per turn), the state space grows combinatorially. TLC's explicit-state model checking is exponential in the number of concurrent processes. Based on similar TLA+ specs in the literature, the proposed scope would likely exceed 10^9 states with even modest constants, making TLC infeasible without aggressive abstraction.

Session forking (§2.5) is particularly expensive to verify via interleaving — independence is better proven by a structural argument (Clone() creates fresh objects with no shared pointers) than by exhaustive state exploration.

**Suggested fix**
Specify a layered verification strategy:
1. **Single-session model**: Verify turn mutual exclusion, FSM transitions, terminal event uniqueness, and event ordering with one session and 2-3 concurrent callers. This is the highest-value, most feasible model.
2. **Service-level model**: Verify Close() drain, capacity limits, and session registry invariants with sessions abstracted as simple state variables (not full FSMs).
3. **Fork independence**: Prove by construction (Clone() returns disjoint object graphs) rather than by state exploration. A brief structural argument in the spec suffices.
4. Include target state counts and TLC runtime estimates for each layer.

---

### P2-4 - `setCloseHook` append chain not captured in receiver close model

**Location:** Plan §3 Safety Property 8

**Problem**
Safety property 8 models receiver close as a single `sync.Once` operation. In reality, both `SubscribeEvents` (service.go:360+362) and `newTurnScopedReceiver` (service.go:630+635) call `setCloseHook` multiple times on the same receiver. `setCloseHook` appends hooks to a slice (service.go:954). The `closeWithError` method (lines 958-978) executes all hooks after closing the channels. If a hook panics, subsequent hooks are skipped (no panic recovery on hooks).

While receiver close idempotency (the `sync.Once` guarantee) is correctly identified, the hook chain execution is a separate concern. A panicking hook could prevent `removeStream` from running, leaving a dangling entry in `ms.streams`.

**Suggested fix**
Either model the hook chain with potential failure (panic in a hook skips remaining hooks) or add panic recovery to the hook execution loop in the implementation and note the simplification in the spec.

---

### P2-5 - Post-destroy silence (safety 5) has a publisher-unsubscribe ordering subtlety

**Location:** Plan §3 Safety Property 5

**Problem**
In `DestroySession` (service.go:427-452), the ordering is: `markDestroyed` → `unsubscribe` (publisher) → `Stop` → `cancel` → `completeActiveTurn` → `closeAllStreams` → `delete`. `Stop()` emits `EventSessionEnded` (agent.go:236). Since the publisher unsubscribe happens *before* `Stop`, the `EventSessionEnded` event is not published to the external sink. But it *is* delivered to any still-active turn-scoped subscribers (they haven't been closed yet at this point).

Safety property 5 says "After DestroySession returns, no further events are produced with SessionID == S." The events emitted *during* DestroySession (EventSessionEnded from Stop, plus any events the driver emits as it winds down) are technically emitted before DestroySession returns, so they don't violate the property. But the spec should model this explicitly because the boundary is subtle — an event emitted by Stop could race with closeAllStreams.

**Suggested fix**
The spec should model the DestroySession sequence step by step and verify that all events emitted during the sequence are delivered to subscribers that are still active at the time of emission. The property should clarify that "no further events" means "after the function returns," not "after markDestroyed is called."

---

### P3-1 - Fairness assumptions need Go runtime justification

**Location:** Plan §5 Modeling approach

**Problem**
The spec states "weak fairness on driver event emission" and "strong fairness on context cancellation propagation" without justifying the mapping to Go's runtime guarantees. Go's context cancellation propagates via channel close, which is immediate — strong fairness is appropriate. Driver event emission depends on goroutine scheduling, which Go's runtime provides with at least weak fairness (goroutines yield at function calls, channel ops, etc.). The justification is important for anyone validating whether the TLA+ model faithfully represents Go behavior.

**Suggested fix**
Add a brief paragraph justifying each fairness choice in terms of Go's concurrency model: context cancellation is channel-based (immediate propagation → strong fairness), driver emission is goroutine-scheduled (cooperative yielding → weak fairness).

---

### P3-2 - bus.close() timing relative to final events could miss edge case

**Location:** Plan §3 Safety Property 5, §2.2

**Problem**
In `Agent.Stop()` (agent.go:218-240), after emitting `EventSessionEnded`, `a.control.Close()` and `a.bus.close()` are called. `bus.close()` sets `closed=true` and nils out subscribers. If a concurrent `emit()` call (from a driver goroutine that hasn't stopped yet) races with `bus.close()`, the emit is silently dropped (event_bus.go:39-42). This is safe but means the spec's assumption "the driver eventually stops emitting" needs to account for the fact that some late events may be silently dropped rather than delivered.

**Suggested fix**
Note in the spec that `bus.close()` provides a hard cutoff — any events emitted after close are dropped, not queued. This simplifies the liveness argument (we don't need the driver to stop emitting — we just need the bus to close).

---

## Summary

10 findings: 0 P0, 3 P1, 5 P2, 2 P3

**Verdict**: Approved with revisions

The spec correctly identifies the core safety and liveness properties for the agent loop state machine. The modeling decisions are sound at a high level — abstracting driver internals, event payloads, and tool execution semantics is the right call. The scope boundaries are reasonable.

The three P1 findings address gaps that would undermine the spec's primary value:
1. The Abort dual-emission hazard (P1-1) is the most interesting concurrency bug in the system and must be modeled to make safety property 3 meaningful.
2. The Close() drain second-pass race (P1-2) is load-bearing for liveness property 3 and the spec needs to capture the specific interleaving that makes it necessary.
3. The ControlQueue backpressure (P1-3) affects whether Abort reliably leads to turn termination — a core liveness claim.

The P2 findings on state space explosion (P2-3) deserve particular attention. The spec is ambitious in scope, and without a layered verification strategy, TLC may not terminate in reasonable time. Starting with a single-session model and adding complexity incrementally is strongly recommended.
