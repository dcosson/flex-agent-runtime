# Review: 18-agent-loop-rpc (Round 2)

- Source doc: `docs/plans/18-agent-loop-rpc.md`
- Status reviewed: Draft (R1 review incorporated)
- Reviewer: R2-A
- R1 reviews considered: `18-agent-loop-rpc-review-r1-a.md`, `18-agent-loop-rpc-review-r1-b.md`
- Review date: 2026-03-16

---

## Part 1: R1 Incorporation Assessment

All 30+ R1 findings are tracked in the Round 1 Review Disposition table (section 17 of the plan). I checked each against the plan text. Summary:

**Well incorporated:**

- **F1 (circular import):** Correctly resolved. `AgentService` now lives in `internal/agent/api`, which imports `internal/agent` for types but is NOT imported by `internal/agent`. The import flow diagram in section 16.4 is consistent. Verified against actual codebase: `internal/rpc/api/types.go` imports `internal/agent`, confirming the original cycle would have existed. The `internal/agent/api` package breaks it cleanly.

- **B-P0 (SandboxControl vs ExecutionEnvironment):** Section 6 now has a clear comparison table and the coordination model ("SandboxControl hands off to ExecutionEnvironment"). `SandboxCapabilities` embeds `environment.Capabilities`. This is a solid resolution.

- **F2 (Close/shutdown):** `Close()` added to interface with drain logic in section 4 and graceful shutdown in section 7.4. Complete.

- **F3 (APIKey):** Removed from `SessionConfig`. Env var injection documented in sections 3.1, 7.3, 17. Clean resolution.

- **F5/B-P1-resume (AgentMessageRecord serialization):** Section 3.3 defines JSON schemas per role, codec functions, and lossy conversion documentation. This is thorough.

- **F7 (turn-scoped streams):** Section 3.2 explains the filtering mechanism via `EventTurnCompleted`. Dual delivery model documented.

- **F9 (session limits):** `WithMaxSessions` option added. `CreateSession` checks against it.

- **F10 (Steer when idle):** Returns `CodeFailedPrecondition`. Clear.

- **B-P1-RuntimeController:** Section 1 now has explicit layering explanation and diagram. Good.

- **B-P1-validation:** Section 3.4 covers schema version, record parsing, role validation, turn monotonicity, tool compatibility. Solid.

- **B-P1-stateless:** Changed to "ephemeral." Mid-turn crash behavior documented in section 10.1.

- **B-P2-StreamTerminal:** Removed from `AgentService`. Orchestrator uses existing `TerminalService` directly (section 12.2). Confirmed against codebase: `TerminalService` interface exists at `internal/rpc/api/terminal.go` with full `TerminalStreamHandle` protocol.

- **F11/B-P2 (section numbering):** Renumbered consistently (13-17). Subsections now match parent section numbers.

- **F12 (ToolEnvironmentType):** Typed constant with `ToolEnvLocal` and `ToolEnvSandbox`. Good.

- **B-P3-ResumeRequest-duplication:** Extracted shared `SessionConfig` type embedded by both `CreateAgentSessionRequest` and `ResumeSessionRequest`. Clean.

- **F19/B-P3-event-import:** `EventPublisher` interface defined in `internal/agent` (section 4), implemented by `AgentEventServer`. Import direction preserved. Verified: `AgentEventServer.Publish(sessionID string, evt agent.AgentEvent)` has exactly this signature, so it naturally implements the `EventPublisher` interface without needing to import `internal/agent/api`.

**Minor concern on incorporation:**

- **F8 (LaunchProcess):** The plan added `KillProcess`, `GetProcessStatus`, port management docs, and process monitoring semantics. However, the LaunchProcess specification is still in plan 18 rather than in a dedicated plan 11-sandbox-host-service.add02 document. The dependency header references `.add02` but this addendum document does not appear to exist yet. See finding F3 below.

---

## Part 2: New Issues Introduced by R1 Incorporation

### P2 - F1. EventPublisher interface is defined in `internal/agent/service.go` but should be in `internal/agent/api`

**Location:** Section 4

The plan defines `EventPublisher` inside `internal/agent/service.go`:

```go
type EventPublisher interface {
    Publish(sessionID string, event agent.AgentEvent)
}
```

But `AgentLoopService` in `service.go` is in package `agent` (same package as `Agent`, `AgentEvent`, etc.). So the reference to `agent.AgentEvent` in the interface would just be `AgentEvent` within that package. This is fine for the implementation.

However, the `AgentService` interface lives in `internal/agent/api`. The `EventPublisher` should arguably also live in `internal/agent/api` since it is part of the service contract -- constructing an `AgentLoopService` requires passing an `EventPublisher`, and callers who construct the service (like `cmd/agent-server/main.go`) need to reference this type. If `EventPublisher` lives in `internal/agent`, then `cmd/agent-server` must import both `internal/agent` AND `internal/agent/api`. This works but is slightly awkward.

A cleaner alternative: define `EventPublisher` in `internal/agent/api` alongside `AgentService`, since they are both part of the service API surface. The `internal/agent/api` package already imports `internal/agent` for `AgentEvent`, so the import stays clean.

This is not a blocker -- the current placement works. But the implementor should consider this during step 3.

---

## Part 3: Remaining Gaps

### P1 - F2. `AgentService.SendMessage` and `Continue` return `AgentEventReceiver` from `internal/rpc/api`, creating an import from `internal/agent/api` to `internal/rpc/api`

**Location:** Sections 3, 16.4

The `AgentService` interface in `internal/agent/api/agent.go` declares:

```go
SendMessage(ctx, req) (AgentEventReceiver, error)
SubscribeEvents(ctx, req) (AgentEventReceiver, error)
```

The `AgentEventReceiver` type is currently defined in `internal/rpc/api/types.go`:

```go
type AgentEventReceiver interface {
    Recv() (*AgentEventEnvelope, error)
    Close() error
}
```

And `AgentEventEnvelope` in the same file references `agent.AgentEvent`.

The import flow in section 16.4 shows:
```
internal/agent/api -> internal/agent (AgentMessage, SessionMetrics -- types only)
```

But it does NOT show `internal/agent/api -> internal/rpc/api`, even though `AgentService` returns `AgentEventReceiver` which is defined in `internal/rpc/api`. This creates the import:
```
internal/agent/api -> internal/rpc/api -> internal/agent
```

This is not a cycle (it's a diamond), but it means `internal/agent/api` imports `internal/rpc/api`. This violates the plan's stated design principle that the agent API package should be transport-neutral ("The RPC layer wraps AgentService; it does not define it."). Having the transport-neutral interface return a type from `internal/rpc/api` is contradictory.

**Required fix:** Either:
1. Move `AgentEventReceiver` and `AgentEventEnvelope` to `internal/agent/api` (since they are used by the transport-neutral interface), or
2. Define a new event receiver type in `internal/agent/api` that the `AgentService` interface returns, and have the RPC layer use an adapter. For example:

```go
// internal/agent/api/events.go
type EventReceiver interface {
    Recv() (*agent.AgentEvent, error)
    Close() error
}
```

This keeps `internal/agent/api` free from `internal/rpc/api` imports. The existing `AgentEventReceiver` in `internal/rpc/api` (which wraps events in `AgentEventEnvelope` with a `SessionID` field) would remain for the RPC transport layer.

Option 2 is cleaner because `AgentEventEnvelope` adds a `SessionID` field that is RPC-transport specific (the session ID is already known to the caller in the `AgentService` context). The transport-neutral interface can return events without the envelope.

### P1 - F3. Dependency `11-sandbox-host-service.add02` is referenced but does not exist as a plan document

**Location:** Dependency header, sections 6, 6.1, 15

The plan header says: `Depends on: ... 11-sandbox-host-service.add02 (LaunchProcess)`. Section 6.1 says `LaunchProcess` "requires new LaunchProcess RPC endpoint on sandbox-host (tracked as dependency 11-sandbox-host-service.add02)." Implementation step 13 also references it.

However, this addendum document does not exist. The LaunchProcess specification (RPC types, lifecycle, port proxying, process monitoring) is defined HERE in plan 18 section 6, not in a plan 11 addendum. This creates a situation where:

1. Plan 18 specifies the sandbox-host changes inline, but labels them as belonging to a different plan
2. There is no standalone plan document that a sandbox-host implementor can reference
3. The implementation order (step 13) says "real LaunchProcess depends on 11-sandbox-host-service.add02" but that document does not exist to be implemented

**Required fix:** Either:
1. Create the `11-sandbox-host-service.add02.md` document extracting the LaunchProcess sandbox-host specification from plan 18, or
2. Acknowledge that the LaunchProcess sandbox-host specification lives in plan 18 section 6 and update the dependency header and implementation order to reflect this (remove the fiction that it's a separate tracked plan).

Option 1 is preferred since the sandbox-host changes (new RPC endpoint, PID tracking, TCP reverse proxy, gVisor integration for launched processes) are substantial enough to warrant their own plan and review cycle.

### P2 - F4. `CreateSession` flow references `ai.GetModel(provider, modelID)` but this function does not exist

**Location:** Section 4, CreateSession flow step 5

The plan says: "Resolve model from registry (`ai.GetModel(provider, modelID)`)". Looking at the actual codebase, the model registry in `internal/ai` uses `ai.ModelByID()` or similar. The plan should reference the actual API or note that this function needs to be created.

This is a minor detail but matters because if the function signature is wrong, the implementor will need to figure out the right call. The `DriverConfig` (in `internal/agent/driver.go`) takes an `ai.Model` and an `ai.SimpleStreamOptions`, so the service needs to construct these from the `SessionConfig` fields (`Model`, `Provider`). The plan should be explicit about how `Provider` and `Model` strings map to the `ai.Model` interface value the driver needs.

### P2 - F5. `ResumeSession` validation does not check role alternation

**Location:** Section 3.4

The R1-B review (P1-validation finding) specifically requested: "at minimum, verify all records parse, roles alternate correctly, and turn numbers are monotonically non-decreasing."

Section 3.4 addresses parsing, role validation (known role strings), turn monotonicity, and tool compatibility. However, it does NOT check role alternation. In a well-formed conversation:
- `user` is followed by `assistant`
- `assistant` may be followed by `tool_result` (if tool calls) or `user` (if no tool calls)
- `tool_result` is followed by `assistant` (model processes tool results)

Two consecutive `user` messages, or two consecutive `assistant` messages without intervening tool results, would indicate a malformed log. The plan should specify whether this is validated (error or warning) or intentionally ignored.

Given that ResumeSession injects these messages into the agent's `Session.ConversationLog` (which is passed to the provider via `callProvider`), malformed alternation could cause provider API errors (e.g., Anthropic's API requires strict role alternation). This should be caught at validation time with a descriptive error rather than surfacing as a cryptic provider error on the next `SendMessage`.

### P2 - F6. `AgentLoopService` codec lives in two places

**Location:** Sections 3.3, 5.5, 13

The plan defines two codec locations:
1. `internal/agent/api/codec.go` -- `AgentMessageToRecord` / `RecordToAgentMessage` (section 3.3)
2. `internal/rpc/codec/agent_map.go` -- wire-format codec for agent RPC types (section 5.5)

The split is conceptually sound (domain codec vs wire codec), but the plan does not clearly explain what `agent_map.go` does that `codec.go` does not. Section 5.5 says it handles:
- `agent.SessionMetrics` -> wire format and back
- `AgentMessageRecord` serialization
- `AgentEvent` envelope wrapping

But `AgentMessageRecord` serialization is already handled by `codec.go` (section 3.3). And `SessionMetrics` is used directly (section 3.1 note: "The response types use `agent.SessionMetrics` directly"). If `SessionMetrics` is used directly, what does the wire-format codec do for it?

The plan should clarify the exact boundary: `internal/agent/api/codec.go` converts between Go domain types (`AgentMessage` <-> `AgentMessageRecord`). `internal/rpc/codec/agent_map.go` converts between Go types and ConnectRPC wire format (JSON/protobuf). If the ConnectRPC transport uses JSON and the Go types are already JSON-serializable, the wire codec may be trivial or unnecessary. The implementor needs clarity on what needs to be built.

### P2 - F7. No specification for how `EventPublisher` connects to `Agent.Subscribe()`

**Location:** Section 4

The plan defines `EventPublisher` as an injected dependency for publishing events externally (to `AgentEventServer`). But it does not specify the wiring between `Agent.Subscribe()` (internal event bus) and `EventPublisher.Publish()`.

Looking at the existing `Agent.Subscribe(fn func(AgentEvent))` and `eventBus.publish()`, events are delivered synchronously to subscribers. When `AgentLoopService` creates a new session (step 9 in CreateSession), it needs to:

1. Create the `Agent`
2. Subscribe to the agent's event bus via `agent.Subscribe(func(event AgentEvent) { ... })`
3. Forward events to `publisher.Publish(sessionID, event)`

This is straightforward but should be explicit in the plan because:
- The subscriber callback runs synchronously in the event bus's `publish()` goroutine (which is the driver's goroutine). If `EventPublisher.Publish()` is slow (e.g., due to lock contention in `AgentEventServer`), it will slow down the agent loop.
- The existing `AgentEventServer.Publish()` uses non-blocking channel sends, so it should be fast. But this assumption should be stated.
- The `unsubscribe` function returned by `Agent.Subscribe()` needs to be stored and called during `DestroySession`. The plan's `managedSession` struct does not include an unsubscribe field.

### P3 - F8. `ResourceSpec` reference in `CreateSandboxRequest` is ambiguous

**Location:** Section 6

Section 6 says `Resources ResourceSpec` with a comment "uses existing `api.ResourceSpec`". The R1-A finding F14 raised this and the disposition says "Acknowledged. Uses existing `api.ResourceSpec`. Noted in section 6."

However, the actual comment in the code block says:
```go
Resources ResourceSpec // CPU, memory (uses existing api.ResourceSpec)
```

But the `SandboxControl` interface is in package `internal/sandbox/control/control.go`, and it imports `github.com/anthropics/flex-agent-runtime/internal/sandbox/environment`. It does NOT show an import of `internal/rpc/api`. Using `api.ResourceSpec` (from `internal/rpc/api`) would add a dependency from `internal/sandbox/control` to `internal/rpc/api`, which seems wrong for a domain interface.

Should this be `environment.ResourceSpec`? But `environment` does not have a `ResourceSpec` type (verified: `capabilities.go` only defines `Capabilities`). Or should `ResourceSpec` be a new type in `internal/sandbox/control`? The plan should be explicit. Since the existing `api.ResourceSpec` only has `CPUs float64` and `MemMB int`, redefining it locally in the `control` package would be trivial and avoid the cross-layer import.

### P3 - F9. Implementation order: codec before service means testing codec in isolation

**Location:** Section 15

Step 2 is "AgentMessageRecord codec + unit tests." Step 3 is "AgentLoopService." This means the codec is built and tested before the service that uses it. This is fine for unit testing of the codec in isolation.

However, step 5 (Agent error mapping in `internal/rpc/errors.go`) extends the existing `MapError` function. Looking at the actual `MapError` implementation, it imports `internal/sandbox` and `internal/sandbox/zfs`. Adding agent error mappings means it will also need to import `internal/agent` (for `BusyError`, `StoppedError`, `InvalidStateError`, `ErrQueueFull`). This adds `internal/agent` as a dependency of `internal/rpc/errors.go`.

Currently `internal/rpc/errors.go` imports `internal/sandbox` and `internal/sandbox/zfs`. Adding `internal/agent` is a new cross-domain dependency. This is probably fine (the `rpc` package is already a cross-cutting concern), but should be noted. An alternative is to have a separate `MapAgentError` function in a different file or have the `AgentRPCServer` do its own error mapping without extending `MapError`. The plan should be explicit about the chosen approach.

### P3 - F10. No mention of context propagation for session-scoped contexts

**Location:** Section 4

The `managedSession` struct has a `cancel context.CancelFunc` but no corresponding `context.Context`. The `CreateSession` flow does not mention creating a session-scoped context. When `SendMessage` is called, it passes `ctx` directly to `agent.Start()` or `agent.Prompt()`. But the existing `NativeDriver.startLoop` creates its own `runCtx, cancel := context.WithCancel(ctx)`.

This means each turn gets its own context derived from the RPC request context. If the RPC connection drops, the context is cancelled, which cancels the turn. This may or may not be the desired behavior:

- For in-process use, the caller controls the context and this is fine.
- For RPC use, the ConnectRPC framework typically provides a request-scoped context. When the client disconnects, the context is cancelled. This means an agent turn is tied to the RPC connection's lifetime.

For `SubscribeEvents` (persistent stream), this is expected -- disconnection ends the subscription. But for `SendMessage`, should the turn survive the RPC connection? The plan says the orchestrator uses `SubscribeEvents` for persistence, so if the `SendMessage` connection drops, the turn should probably continue running (the orchestrator can still observe via `SubscribeEvents`).

If this is the desired behavior, the `AgentLoopService` should use the session-scoped context (from `managedSession.cancel`) as the parent for turn contexts, NOT the RPC request context. The plan should specify this.

---

## Part 4: Connected Components Verification

Section 16 is substantially improved from R1. The "Consumed Seams" table (16.3) covers the key dependencies I verified against the codebase:

- `internal/agent/agent.go` -- correctly lists `Start`, `Prompt`, `Continue`, `Steer`, `FollowUp`, `Abort`, `Stop`, `Subscribe`. Verified all exist.
- `internal/agent/types.go` -- lists `AgentMessage`, `SessionMetrics`, `AgentEvent`, `AgentEventType`. All verified.
- `internal/sandbox/environment` -- `ExecutionEnvironment` and `Capabilities`. Verified.
- `internal/rpc/api/types.go` -- `AgentEventReceiver`, `AgentEventEnvelope`. Verified. But note finding F2 above -- this creates an import from `internal/agent/api` to `internal/rpc/api` that is not shown in the import flow.
- `internal/rpc/api/terminal.go` -- `TerminalService`. Verified.
- `internal/termmux/driver/driver.go` -- `ConversationEntry`. Not verified (file not examined), but the reference is reasonable.

**Import flow (section 16.4) issue:** The flow shows `internal/agent/api -> internal/agent` but does not show `internal/agent/api -> internal/rpc/api` (needed for `AgentEventReceiver`). This is the same issue as finding F2 above. The import flow diagram needs updating regardless of how F2 is resolved.

---

## Part 5: Implementation Order Verification

Section 15 lists 14 steps. Checking dependency correctness:

1. AgentService interface + types -- no deps, correct first step
2. AgentMessageRecord codec -- depends on step 1 types, correct
3. AgentLoopService -- depends on steps 1-2, correct
4. Unit tests -- depends on step 3, correct
5. Agent error mapping -- independent of steps 3-4 but logically part of the RPC layer, could be parallel
6. Wire-format codec -- depends on step 1 types, could be parallel with 3-5
7. AgentRPCServer -- depends on steps 1, 3, 5, 6, correct placement
8. Agent procedures in transport -- depends on step 7, correct
9. AgentServiceClient -- depends on steps 1, 7, 8, correct
10. Integration tests -- depends on steps 1-9, correct
11. cmd/agent-server binary -- depends on steps 3, 7, 8, correct
12. SandboxControl interface -- independent of agent service, could be parallel
13. NativeSandboxControl -- depends on step 12, correct
14. E2E test -- depends on all above, correct

**One concern:** Step 11 (cmd/agent-server binary) comes before step 12 (SandboxControl). This is fine since the binary does not require SandboxControl (it's the orchestrator that uses SandboxControl). But the graceful shutdown logic (section 7.4) is part of step 11 and depends on `AgentLoopService.Close()` (step 3). This dependency is satisfied.

The implementation order is dependency-correct.

---

## Summary

**Findings: 0 P0, 2 P1, 4 P2, 3 P3**

| # | Finding | Priority | Category |
|---|---------|----------|----------|
| F1 | `EventPublisher` placement in `internal/agent` vs `internal/agent/api` | P2 | New (from R1 incorporation) |
| F2 | `AgentEventReceiver` from `internal/rpc/api` used in transport-neutral `AgentService` | P1 | Gap not caught in R1 |
| F3 | Dependency `11-sandbox-host-service.add02` referenced but does not exist | P1 | Gap not caught in R1 |
| F4 | `ai.GetModel()` referenced but may not exist | P2 | Gap |
| F5 | `ResumeSession` validation does not check role alternation | P2 | Partial R1 incorporation |
| F6 | Two codec locations with unclear boundary | P2 | Gap |
| F7 | Wiring between `Agent.Subscribe()` and `EventPublisher` unspecified | P2 (downgraded from P1 since straightforward) | Gap |
| F8 | `ResourceSpec` import path ambiguous for `internal/sandbox/control` | P3 | Residual from R1 |
| F9 | Error mapping extends `MapError` adding cross-domain import | P3 | Gap |
| F10 | Context propagation: RPC request context vs session-scoped context | P3 | Gap |

---

## Verdict

**Approved for implementation with conditions.**

The plan is substantially improved from R1. All P0 findings from R1 were correctly incorporated. The architecture is sound: the `internal/agent/api` package placement, the `EventPublisher` pattern, the SandboxControl/ExecutionEnvironment boundary, and the session recovery model are all well-designed and consistent with codebase patterns.

The two P1 findings should be resolved before implementation begins:

1. **F2 (AgentEventReceiver import):** The transport-neutral `AgentService` interface should not return a type from `internal/rpc/api`. Define a simpler `EventReceiver` in `internal/agent/api` that returns `*agent.AgentEvent` directly (without the envelope). This is a small but important change to keep the package layering clean.

2. **F3 (missing .add02 plan):** Either create the plan document or explicitly acknowledge the LaunchProcess sandbox-host specification lives here and remove the phantom dependency reference. This is a process concern that blocks step 13 of implementation.

The P2 and P3 findings can be resolved during implementation. None of them represent architectural risks -- they are specification gaps that an implementor can fill following the established patterns.

The plan is ready for implementation once F2 and F3 are addressed.
