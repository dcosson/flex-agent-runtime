# 21: TLA+ Specification for Agent Loop State Machine

**Status:** Draft
**Depends on:** 05-agent, 18-agent-loop-rpc
**Scope:** Define a TLA+ formal specification for the agent loop state machine to verify safety and liveness properties of concurrent session management, turn execution, and session lifecycle.
**Spec file location:** `specs/agent_loop.tla`

---

## 1. Overview

The agent loop (`internal/agent`) is a concurrent state machine managing multiple sessions, each with interleaved turn execution, tool calls, event streaming, and control-plane operations (steer, abort, follow-up). Several concurrent concerns make informal reasoning about correctness insufficient:

- **Turn mutual exclusion:** Only one turn may be active per session at a time. `Agent.Start`/`Prompt`/`Continue` check `a.running` under a mutex and return `BusyError` if a turn is already active. A missed edge case here can cause interleaved turns corrupting conversation state.
- **Event stream ordering and termination:** Events within a turn must be totally ordered and every turn-scoped stream must terminate with exactly one terminal event (`EventTurnCompleted`, `EventDriverError`, `EventProviderError`, or `EventAborted`). The `serviceEventReceiver` uses buffered channels with `sync.Once` close semantics -- subtle races between `push`, `closeWithError`, and `Close` could violate these guarantees.
- **Session lifecycle vs. active turns:** `DestroySession` forcefully cancels active turns, closes all event streams, and removes the session from the registry. A race between `DestroySession` and a concurrent `SendMessage`/`Continue` that has already resolved `getSession` but not yet started the turn could leak resources or produce events on a destroyed session.
- **Service-level close drain:** `Close()` tears down all sessions with a two-phase timeout. The interaction between `wg.Wait()`, per-session `cancel()`, and `completeActiveTurn()` under concurrent turn completion is non-trivial.
- **Session forking via ResumeSession:** Multiple sessions created from the same conversation log must be fully independent -- no shared mutable state between forked sessions. This is enforced by `Session.Clone()` and fresh `Agent`/`Driver` construction, but the invariant is worth verifying formally.

TLA+ is well-suited for this because it can exhaustively explore the state space of these concurrent interactions at the design level, catching invariant violations that would be extremely difficult to trigger in conventional tests.

---

## 2. What to Model

### 2.1 Agent State Machine (per session)

The core FSM from `internal/agent/agent.go`:

```
Idle --> Streaming --> ToolExecution --> Streaming (loop)
                  --> WaitingFollowUp --> Streaming
                  --> Idle (turn complete)
Any state --> Exited (stop/destroy)
```

Valid transitions are defined in `validTransitions`. The spec should encode these exactly and verify that no reachable state violates the transition table.

### 2.2 Session Lifecycle

Model the full lifecycle managed by `AgentLoopService`:

- **Created:** `CreateSession` or `ResumeSession` adds a `managedSession` to the registry
- **Active:** Session accepts `SendMessage`, `Continue`, `Steer`, `FollowUp`, `Abort`
- **Destroyed:** `DestroySession` marks destroyed, cancels context, closes streams, removes from registry
- **Service closed:** `Close()` destroys all sessions with drain timeout

Key transitions to verify: a session cannot be used after `DestroySession`, and `Close()` eventually terminates.

### 2.3 Turn Lifecycle

Model the turn as the unit of work within a session:

1. Caller invokes `SendMessage` or `Continue`
2. `newTurnScopedReceiver` creates a receiver and subscribes to agent events
3. `agent.Start`/`Prompt`/`Continue` is called (sets `a.running = true`)
4. Driver emits events: `EventTurnStarted`, deltas, tool calls, etc.
5. Terminal event fires (`EventTurnCompleted` / error / abort)
6. `doneTurn()` fires via `sync.Once`, calling `wg.Done()` and clearing `turnDone`
7. Receiver is closed with `io.EOF`

The spec should model the interleaving of steps 2-7 with concurrent `DestroySession`, `Abort`, and `Steer` operations.

### 2.4 Concurrent Session Management

Model `AgentLoopService` managing a set of sessions with:

- `maxSess` capacity limit
- Concurrent `CreateSession`, `DestroySession`, `SendMessage` across different sessions
- The service-level mutex (`s.mu`) protecting the session registry
- Per-session mutex (`ms.mu`) protecting session-local state

### 2.5 Session Forking

Model N calls to `ResumeSession` with the same conversation log content:

- Each creates an independent `managedSession` with its own `Agent` and `Driver`
- Verify no shared mutable state between forked sessions
- Verify each fork can independently execute turns without affecting siblings

### 2.6 Tool Execution Interleaving

Model the state transitions during tool execution within a turn:

- `Streaming --> ToolExecution` (tool call received from model)
- Tool executes (potentially long-running)
- `ToolExecution --> Streaming` (tool result sent back to model)
- Multiple tool calls can occur in sequence within one turn
- `Abort` can interrupt during tool execution

### 2.7 Failure Modes

- **Context cancellation** during an active turn (from `DestroySession` or external cancel)
- **Driver panic** caught by `safeCall` recovery
- **Service close** during active turns with drain timeout

---

## 3. Safety Properties

These are invariants that must hold in every reachable state:

1. **Turn mutual exclusion:** A session never has two concurrent turns. Formally: if `a.running == true`, then any concurrent `Start`/`Prompt`/`Continue` returns `BusyError`. No state exists where two goroutines are both past the `a.running = true` assignment for the same session.

2. **Valid state transitions only:** Every state transition follows the `validTransitions` table. No reachable state has a transition path not in the table.

3. **Terminal event uniqueness:** Every started turn produces exactly one terminal event (`EventTurnCompleted`, `EventDriverError`, `EventProviderError`, or `EventAborted`). Never zero, never more than one.

4. **Event ordering within a turn:** Events emitted by a single turn are totally ordered by their emission through `bus.publish`. No consumer observes events from the same turn in a different order.

5. **Post-destroy silence:** After `DestroySession` returns for session S, no further events are produced with `SessionID == S`. All subscribers have been unsubscribed and all streams closed.

6. **Fork independence:** Sessions created via `ResumeSession` with identical conversation logs share no mutable state. A state change in session A does not affect session B's state, conversation log, or metrics.

7. **WaitGroup consistency:** `s.wg` is incremented exactly once per turn start and decremented exactly once per turn completion. After `Close()` returns, `s.wg` is zero.

8. **Receiver close idempotency:** Multiple calls to `receiver.Close()` and `receiver.closeWithError()` are safe -- `sync.Once` ensures the channel is closed exactly once.

---

## 4. Liveness Properties

These are progress guarantees that must eventually hold:

1. **Turn termination:** Every `SendMessage`/`Continue` call that successfully starts a turn eventually produces a terminal event. The turn does not hang indefinitely (assuming the driver eventually completes or the context is cancelled).

2. **Cancellation responsiveness:** A cancelled context (from `DestroySession` or `Close`) eventually stops event production and causes the turn to reach a terminal state.

3. **Service close termination:** `Close()` eventually returns (possibly with a timeout error), and does not block indefinitely. All active turns are either drained or forcefully terminated.

4. **Receiver drain:** After a terminal event is pushed to a `serviceEventReceiver`, a consumer calling `Recv()` eventually receives it (the channel is buffered and not blocked by other operations).

---

## 5. Key Modeling Decisions

### What is in scope

- The `AgentLoopService` session registry and its mutex-protected operations
- The `Agent` FSM, `running` flag, and transition logic
- The `managedSession` lifecycle (created, started, destroyed) and its mutex
- Turn-scoped receivers: creation, event delivery, terminal detection, close
- The `wg` accounting for graceful shutdown
- `DestroySession` and `Close` teardown sequences
- Session forking as multiple `ResumeSession` calls

### What is abstracted away

- **Driver internals:** The driver is modeled as a non-deterministic process that eventually emits a sequence of events ending in a terminal event (or panics, which `safeCall` catches). We do not model the LLM provider interaction, SSE parsing, or specific driver implementations.
- **Event payload contents:** We model event types (the `AgentEventType` enum) but not the full event struct fields. The properties we verify are about event ordering and type, not content.
- **Tool execution semantics:** Tools are modeled as opaque operations that take time and produce a result. We do not model file system effects, sandbox interactions, or tool-specific logic.
- **Network transport / RPC layer:** The spec models the `AgentService` Go interface, not the ConnectRPC wire protocol. The RPC layer is a thin adapter that does not introduce new concurrency concerns.
- **Conversation log content:** We model conversation length and the fact that logs are cloned, but not individual message contents.

### Modeling approach

- **Processes:** Each session is modeled as a TLA+ process. The service itself is a process managing a set of session processes. Client callers (invoking `SendMessage`, `DestroySession`, etc.) are modeled as additional processes.
- **Granularity:** Mutex acquisitions are modeled as atomic steps. The critical sections in the Go code (lock, read/write, unlock) map to single TLA+ steps. Inter-step interleavings represent the concurrency between different goroutines.
- **Fairness:** Weak fairness on driver event emission (the driver does not stop emitting events forever if it has more to emit). Strong fairness on context cancellation propagation.

---

## 6. Scope Boundaries

This spec does NOT cover:

- The orchestrator layer (`RuntimeController`) or its interaction with multiple `AgentService` instances
- SQLite persistence or conversation log serialization/deserialization fidelity
- Sandbox lifecycle management (`SandboxControl`)
- Cross-agent resume conversion (our loop <-> Claude Code session log format)
- Provider-level concerns (rate limiting, token counting, context overflow)
- The `ControlQueue` internal implementation (modeled as a simple channel)
- The `eventBus` subscription/unsubscription mechanics (modeled as reliable delivery with unsubscribe semantics)

These could be separate TLA+ modules if formal verification is desired for those areas.
