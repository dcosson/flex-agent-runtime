# Seam Review: 18-agent-loop-rpc

**Reviewer:** seam-review (automated)
**Date:** 2026-03-16
**Plan:** 18-agent-loop-rpc.md
**Referenced plans:** 00-architecture.md, 05-agent.md, 11-sandbox-host-service.add01.md, 13-rpc-layer.md

---

## Summary

This review analyzes the interface boundaries, type conversions, import flows, and error propagation paths introduced by plan 18. Seven primary seams and several cross-cutting concerns are evaluated against the existing codebase. 14 findings rated P0-P3.

---

## Seam 1: AgentService (internal/agent/api) <-> Agent (internal/agent)

### Analysis

The `AgentService` interface (section 3) wraps `Agent` methods through `AgentLoopService` (section 4). Mapping the plan's API to the existing `Agent` implementation:

| AgentService method | Agent method(s) called | Match? |
|---|---|---|
| `CreateSession` | `New(driver)`, `agent.SetSession(session)` | Indirect -- constructs Agent |
| `SendMessage` | `agent.Start(ctx, session, prompt)` or `agent.Prompt(ctx, prompt)` | Yes |
| `Continue` | `agent.Continue(ctx)` | Yes |
| `Steer` | `agent.Steer(message)` | Yes |
| `FollowUp` | `agent.FollowUp(message)` | Yes |
| `Abort` | `agent.Abort(reason)` | Yes |
| `SubscribeEvents` | `agent.Subscribe(fn)` | Yes |
| `GetSession` | `agent.Session()`, `agent.State()` | Yes |
| `DestroySession` | `agent.Stop(ctx)` | Yes |
| `ResumeSession` | `New(driver)`, `agent.SetSession(session)` | Yes (uses SetSession) |

### Findings

**F1 (P1): Agent.Start vs Agent.Prompt asymmetry creates fragile branching in AgentLoopService.SendMessage**

The plan's `SendMessage` flow (section 4, steps 2-5) branches on a `started` flag: if `!started`, call `agent.Start(ctx, session, message)`, otherwise call `agent.Prompt(ctx, prompt)`. Looking at the implementation, `Agent.Prompt()` is implemented as `a.Start(ctx, a.Session(), prompt)` -- it calls Start internally. However, `Agent.Start()` sets `a.session` from the passed session parameter (overwriting any existing session), while `Agent.Prompt()` passes the current session back through. The `AgentLoopService` must preserve the invariant that `session.started` is set to `true` after the first `SendMessage`, and subsequent calls must use `Prompt()` (which preserves the conversation log). If the `started` flag gets out of sync (e.g., after a failed Start that resets `running=false` but the flag was already set), the service could call `Prompt()` on an agent that never had `Start()` succeed, or call `Start()` again and overwrite the session.

The existing `Agent.Start()` does not distinguish between "first start" and "re-start" -- it always accepts a session parameter. But `AgentLoopService` stores the session in `managedSession.agent` and relies on the `started` flag to choose the right call path. If `Start()` fails (returns error), the plan says the `started` flag is NOT set to true (step 3: "set `started = true`" only after successful Start). But `Agent.Start()` itself sets `a.running = false` on failure after having already set `a.session` from the passed parameter. This means a retry would work correctly. The risk is low but the branching pattern is fragile and would benefit from a unified method on `Agent` that handles both cases.

**Recommendation:** Consider adding an `Agent.EnsureStartedAndPrompt(ctx, prompt)` method that internally handles the first-start vs subsequent-prompt distinction, removing the need for the `started` flag in `AgentLoopService`.

**F2 (P2): Agent.Abort() does not accept a reason string in the underlying implementation -- mismatch with plan**

The plan's `AbortRequest` (section 3.1) includes a `Reason string` field, and the plan's `AgentLoopService.Abort` says to call `agent.Abort(reason)`. Looking at the existing `Agent.Abort()` implementation, it does accept a `reason string` parameter and passes it through to `control.EnqueueAbort(reason)`. This matches. However, the `AbortResponse` in the plan is an empty struct. Checking whether the abort actually succeeded (vs. the queue being full) requires examining the error return. The queue-full case maps to `ErrQueueFull`, which the plan maps to `CodeResourceExhausted`. This is correct.

No issue found -- the signatures align.

**F3 (P2): GetSession response includes agent.SessionMetrics directly but plan also references ConversationLen -- how is ConversationLen computed?**

The plan's `GetAgentSessionResponse` includes both `Metrics agent.SessionMetrics` and `ConversationLen int`. The `Agent.Session()` returns a cloned `*Session` which has `ConversationLog []AgentMessage`. So `ConversationLen` would be `len(session.ConversationLog)`. However, calling `Agent.Session()` acquires the mutex and clones the entire session including the full conversation log just to count its length. For large conversations (thousands of messages), this is wasteful.

**Recommendation:** Add an `Agent.ConversationLen() int` method that acquires the lock and returns just the count without cloning, to avoid unnecessary allocation in the hot path of session queries.

---

## Seam 2: AgentService <-> RPC Transport (EventReceiver adaptation)

### Analysis

The plan introduces a new `EventReceiver` interface in `internal/agent/api` that returns `*agent.AgentEvent` directly (no envelope). The existing `AgentEventReceiver` in `internal/rpc/api` returns `*AgentEventEnvelope` (which wraps `AgentEvent` + `SessionID`). The `AgentRPCServer` must adapt between these two interfaces.

### Findings

**F4 (P1): EventReceiver and AgentEventReceiver have identical method names (Recv/Close) but incompatible return types -- adapter code must be careful about error semantics**

The new `EventReceiver.Recv()` returns `(*agent.AgentEvent, error)` while the existing `AgentEventReceiver.Recv()` returns `(*AgentEventEnvelope, error)`. The `AgentRPCServer` adapter must:
1. Call `EventReceiver.Recv()` to get the raw event
2. Wrap it in an `AgentEventEnvelope{SessionID: sessionID, Event: *event}`
3. Send via the ConnectRPC server stream

The critical concern is **error passthrough**. When the underlying `EventReceiver` returns `io.EOF` (turn completed or session ended), the adapter must translate this to the ConnectRPC stream closure. When it returns other errors, the adapter must map them via `toConnectError()`. The plan does not specify what errors `EventReceiver.Recv()` can return beyond `io.EOF`. Looking at the plan's turn-scoped filtering (section 3.2), the turn-scoped `EventReceiver` wraps `Agent.Subscribe()` and returns `io.EOF` when `EventTurnCompleted` is received. But if the agent crashes or the session is destroyed while the stream is open, what error does the `EventReceiver` return?

The existing `chanEventReceiver` in `internal/rpc/api/stream.go` returns `io.EOF` when the channel is closed and `ErrStreamClosed` if `Close()` was already called. The new `EventReceiver` implementation will need the same semantics. But if `DestroySession` is called while a `SendMessage` stream is open, the plan says streams are "closed with EOF" -- this implies the `EventReceiver.Close()` must be called, which closes the underlying channel, causing the blocking `Recv()` to return `io.EOF`.

**Risk:** If the adapter does not handle the `ErrStreamClosed` vs `io.EOF` distinction correctly, the ConnectRPC stream could return an error instead of a clean termination. The plan should specify the exact error contract for `EventReceiver.Recv()` beyond "returns io.EOF when done."

**F5 (P2): Dual delivery model creates double-counting risk for orchestrator persistence**

The plan states that `SendMessage`/`Continue` return per-turn `EventReceiver` streams, and `SubscribeEvents` returns a session-wide stream, and events are delivered to **both simultaneously** (dual delivery, section 3.2). The orchestrator subscribes via `SubscribeEvents` for persistence. If the orchestrator also consumes the per-turn stream (e.g., to get a response for the RPC call), it receives each event twice. The plan says "consuming the per-turn stream is NOT required for the turn to proceed," which is good. But if the orchestrator code persists events from both streams, it will double-persist. The plan should add an explicit note that the orchestrator MUST choose one stream for persistence, not both.

**Recommendation:** Document clearly that the per-turn stream is a convenience for simple callers, and the session-wide stream is the canonical source for persistence. Callers should not consume both for the same purpose.

---

## Seam 3: SandboxControl (internal/sandbox/control) <-> ExecutionEnvironment (internal/sandbox/environment)

### Analysis

`SandboxControl` is the orchestrator-level interface for provisioning sandboxes. `ExecutionEnvironment` is the agent-loop-level interface for executing tools within an already-provisioned sandbox. The plan positions these as complementary (section 6).

### Findings

**F6 (P1): SandboxCapabilities embedding environment.Capabilities creates semantic confusion at the SandboxControl level**

The plan acknowledges this issue (section 6, R2 review B-F4) and adds documentation about which fields are meaningful. However, the fundamental problem remains: `SandboxCapabilities` inherits fields like `StreamingProgress`, `TierRouting`, `MaxSessionDuration`, and `ConcurrentSessions` from `environment.Capabilities`, but their meanings differ at the two levels.

At the `ExecutionEnvironment` level, `ConcurrentSessions` means how many concurrent tool execution sessions the environment supports. At the `SandboxControl` level, the plan says it "refers to sandbox instances, not concurrent tool executions." These are fundamentally different quantities. Using the same struct field for different semantics is a type-safety violation that no amount of documentation can fully fix.

The `MaxSessionDuration` field is another example: at the environment level it means how long a tool execution session can last; at the SandboxControl level it would mean how long a sandbox can exist. These are different time horizons (minutes vs. hours/days).

**Recommendation:** Instead of embedding, define `SandboxCapabilities` as a separate struct that copies the relevant fields from `environment.Capabilities` with properly named fields (e.g., `MaxSandboxDuration` instead of `MaxSessionDuration`, `ConcurrentSandboxes` instead of `ConcurrentSessions`). Add a constructor `SandboxCapabilitiesFrom(env environment.Capabilities, extras ...)` that maps fields explicitly. This eliminates the semantic confusion while still preventing type drift through explicit mapping code that will fail to compile if `environment.Capabilities` changes.

**F7 (P2): SandboxControl.CreateSandbox returns an Address but the plan does not specify address format or validation**

`CreateSandboxResponse.Address` is described as "How to reach this sandbox (host:port or URL)." The `LaunchProcessResponse.Address` is described as "How to reach the launched process (proxy host:port)." There is no validation specified for these addresses, and the format is ambiguous (host:port vs URL). The `AgentServiceClient` constructor takes a `baseURL string` -- so the launched process address needs to be convertible to a URL. If `LaunchProcess` returns `sandbox-host:9100`, the orchestrator must prepend `http://` (or `https://`).

**Recommendation:** Standardize the address format to always be a URL (with scheme), or add explicit format documentation and a helper function to convert host:port to a ConnectRPC-compatible URL.

---

## Seam 4: AgentLoopService <-> EventPublisher

### Analysis

The plan defines `EventPublisher` as a minimal interface (`Publish(sessionID string, event agent.AgentEvent)`) in `internal/agent/service.go`. The `AgentEventServer` in `internal/rpc/server` implements this.

### Findings

**F8 (P2): EventPublisher is defined in internal/agent/service.go but AgentEventServer is the only planned implementor -- this creates a testing gap**

The `EventPublisher` interface exists to preserve the import direction (internal/agent does NOT import internal/rpc/server). This is correct. However, the plan's testing strategy (section 14.1) tests `AgentLoopService` with a mock driver but does not mention mocking the `EventPublisher`. Unit tests for `AgentLoopService` need a mock `EventPublisher` to verify that events are published with the correct session ID and event types. The existing `AgentEventServer` has its own unit tests, but the integration between `AgentLoopService` and `EventPublisher` needs coverage.

**Recommendation:** Add explicit test cases for `AgentLoopService` using a mock `EventPublisher` that records published events for assertion. Verify session ID propagation, event type correctness, and that destroyed sessions stop publishing events.

**F9 (P3): EventPublisher.Publish is fire-and-forget with no error return -- silent failures possible**

The `Publish` method returns no error. This matches the existing `AgentEventServer.Publish()` behavior (non-blocking channel send with `default` case that drops events). However, this means that if all consumers are slow and events are dropped, neither the `AgentLoopService` nor the agent loop knows that events were lost. For persistence-critical events like `EventAgentMessageCompleted` and `EventToolCompleted`, dropping events means the orchestrator's SQLite log has gaps.

This is acknowledged in the plan's backpressure section (12.4) but only for the UI relay. For the orchestrator's own persistence subscriber, the plan says events are "processed synchronously (blocking write to SQLite)" -- but the `AgentEventServer.Publish()` uses a non-blocking channel send. If the SQLite write is slow (e.g., during WAL checkpoint), the channel buffer fills and events are dropped.

**Recommendation:** Consider a separate, blocking publish path for persistence-critical subscribers, or add an event counter that allows the orchestrator to detect gaps and request a full state refresh.

---

## Seam 5: SandboxControl <-> Sandbox-Host RPC

### Analysis

`NativeSandboxControl` (section 6.1) wraps the existing `api.SandboxService` RPC interface for most operations and adds new `LaunchProcess` operations.

### Findings

**F10 (P1): NativeSandboxControl.CreateSandbox maps to SandboxService.CreateSession -- semantic name mismatch creates confusion**

The plan maps `CreateSandbox` -> `sandboxClient.CreateSession()`, `DestroySandbox` -> `sandboxClient.DestroySession()`, etc. At the SandboxControl level, the abstraction is "sandboxes" (a container/environment). At the SandboxService level, the abstraction is "sessions" (a sandbox-host session). These are the same underlying resource, but the naming divergence is confusing.

More critically, the existing `SandboxService.CreateSession` (in `internal/rpc/api/types.go`) takes a `CreateSessionRequest{BaseSnapshot, SessionID, Quota, Labels}`. The plan's `CreateSandboxRequest{Labels, Template, Resources}` has different fields. `Template` maps to `BaseSnapshot`, but `Resources ResourceSpec` (CPUs, MemMB) does not exist in the current `CreateSessionRequest`. The current `CreateSessionRequest` has `Quota int64` (storage quota) which is not present in `CreateSandboxRequest`.

This means `NativeSandboxControl.CreateSandbox` must map `CreateSandboxRequest` -> `CreateSessionRequest` with lossy field translation: `Template` -> `BaseSnapshot`, `Labels` -> `Labels`, `Resources` -> (dropped or mapped elsewhere). The `Quota` field from the existing API has no counterpart in `CreateSandboxRequest`.

**Recommendation:** Either: (a) extend `CreateSandboxRequest` to include a `Quota int64` field, or (b) document that NativeSandboxControl uses a default quota, or (c) add provider-specific options via an `Options any` escape hatch (similar to `environment.SessionConfig.Options`).

**F11 (P1): LaunchProcess requires a new RPC endpoint that does not exist on the sandbox-host**

The plan acknowledges this (section 6.1): "requires **new LaunchProcess RPC endpoint on sandbox-host**" and says a standalone addendum (`11-sandbox-host-service.add03`) should be created. This is a hard dependency: `NativeSandboxControl.LaunchProcess` cannot be implemented until the sandbox-host supports this endpoint. The plan's implementation order (section 15, step 13) says to use a "mock sandbox-host for testing."

However, the plan does not define the RPC procedure name, request/response wire format, or how the LaunchProcess endpoint is registered on the existing `transport.Server`. The current `transport.Server.Handler()` only registers `SandboxService` and `AgentEventService` procedures. Adding `LaunchProcess` requires either:
- A new `SandboxControlService` RPC service on the sandbox-host (separate from `SandboxService`)
- Extending the existing `SandboxService` interface with `LaunchProcess`, `KillProcess`, `GetProcessStatus` methods

Both approaches have implications for the existing `internal/rpc/api/types.go` `SandboxService` interface which is already implemented and tested.

**Recommendation:** The `11-sandbox-host-service.add03` addendum must be created and reviewed before implementation step 13 begins. It should specify whether LaunchProcess is added to `SandboxService` or to a new service interface.

---

## Seam 6: Cross-Agent Resume Conversion Chain

### Analysis

The chain: `AgentMessageRecord -> agent.AgentMessage -> ConversationEntry -> Claude Code session.jsonl`

Step 1: `AgentMessageRecord` to `agent.AgentMessage` via `codec.RecordToAgentMessage()` in `internal/agent/api/codec.go`.
Step 2: `agent.AgentMessage` to `ConversationEntry` (canonical termmux format) -- this conversion function is not specified in the plan.
Step 3: `ConversationEntry` to `session.jsonl` via `claudecode.WriteSessionLog()`.

### Findings

**F12 (P1): The agent.AgentMessage -> ConversationEntry conversion function does not exist and is not specified in the plan**

The plan says (section 10.4): "Converts to canonical `[]ConversationEntry` format" but does not specify which package this conversion lives in, what the function signature is, or how the type mapping works. This is the most complex step in the chain:

- `agent.AgentMessage` wraps `ai.Message`, which is an interface with concrete types `*ai.UserMessage`, `*ai.AssistantMessage`, `*ai.ToolResultMessage`.
- `ConversationEntry` (in `internal/termmux/driver/driver.go`) has a flat structure: `{Role, Content string, ToolCall *ToolCallRecord, Thinking *ThinkingBlock, Usage *TokenUsage}`.

Key mapping challenges:
1. `ai.AssistantMessage.Content []ai.ContentBlock` can contain multiple blocks (text + thinking + tool_use). `ConversationEntry` has a single `Content string`. The plan acknowledges this is lossy (section 10.4) but does not specify whether multi-block messages produce multiple `ConversationEntry` records or are concatenated.
2. `ai.ToolCall` in `AssistantMessage.Content` has `Arguments map[string]any`. `ConversationEntry.ToolCall.Args` is `json.RawMessage`. Conversion requires JSON marshaling.
3. `ai.ToolResultMessage.Content []ai.ContentBlock` must be flattened to `ConversationEntry.Content string` (the `ToolCallRecord.Result` field).
4. The `ConversationRole` values (`"user"`, `"assistant"`, `"tool_use"`, `"tool_result"`) don't exactly match the `ai.Role` values or `AgentMessageRecord.Role` values. `tool_use` is part of assistant content in `ai.AssistantMessage` but is a separate role in `ConversationEntry`.

Without this conversion function specified, the cross-agent resume chain has a gap. Looking at the existing code: `claudecode.convertClaudeToCanonical()` converts Claude's format TO canonical, and `convertCanonicalToClaude()` converts canonical back to Claude's format. But there is no existing function to convert FROM `agent.AgentMessage` TO `ConversationEntry`.

**Recommendation:** Specify the `AgentMessageToConversationEntries(msg agent.AgentMessage) []driver.ConversationEntry` function. It should live in `internal/agent/api/codec.go` or in a new shared conversion package. One `AgentMessage` with an `AssistantMessage` containing text + tool_use blocks should produce multiple `ConversationEntry` records (one per content block), matching the pattern used by `convertClaudeToCanonical()`.

---

## Seam 7: Agent-Server Binary <-> transport.Server

### Analysis

The plan specifies new ConnectRPC procedures for the `AgentService` (section 5.1) that must be registered on the existing `transport.Server`.

### Findings

**F13 (P0): transport.Server constructor and Handler() method require architectural changes to support AgentService**

The current `transport.Server` is constructed with:
```go
func NewServer(sandbox api.SandboxService, events api.AgentEventService, terms *termmux.SessionManager, cfg ServerConfig) *Server
```

The `Handler()` method registers all procedures in a `sync.Once` block. To add AgentService procedures, the `Server` struct must also hold a reference to `agentapi.AgentService` (from `internal/agent/api`). This requires:

1. **Changing the `Server` constructor signature** to accept an optional `AgentService` parameter. This is a breaking change to the existing API. The sandbox-host binary (which creates a `transport.Server`) does not have an `AgentService` and should not be required to provide one.

2. **Import chain validation**: `transport.Server` currently imports `internal/rpc/api` and `internal/termmux`. Adding `internal/agent/api` creates a new import: `internal/rpc/transport -> internal/agent/api -> internal/agent`. This should be safe (no cycle), but `internal/agent/api` also imports `internal/agent` for types like `AgentEvent`, `AgentMessage`, `SessionMetrics`. So the full chain is: `internal/rpc/transport -> internal/agent/api -> internal/agent -> internal/ai`. This is already the pattern for the existing `internal/rpc/api -> internal/agent` import. No cycle.

3. **Handler registration**: The `sync.Once` block in `Handler()` means procedures are registered exactly once. If the agent-server binary creates a `Server` with both `SandboxService` (nil, since agent-server doesn't serve sandboxes) and `AgentService`, the nil sandbox handlers need to be gated. Currently, all sandbox handlers are unconditionally registered.

**Recommendation:** Refactor `transport.Server` to accept optional service registrations, either via:
- Functional options: `NewServer(cfg, WithSandboxService(s), WithAgentService(as), WithTerminals(t))`
- A registration method: `server.RegisterAgentService(as)` called before `Handler()`

The current approach of passing all services in the constructor will not scale to additional services. This refactoring should be done in step 8 of the implementation order, but the plan should specify the approach to avoid breaking existing callers.

---

## Cross-Cutting Concerns

### Import Cycle Analysis

**F14 (P3): Import flow is safe but tightly coupled between internal/agent/api and internal/agent**

The plan specifies (section 16.4):
```
internal/agent/api -> internal/agent (types only: AgentMessage, AgentEvent, SessionMetrics)
```

And adds the invariant: "the import MUST remain types-only." This is enforced by convention, not by the compiler. If a future change to `codec.go` needs to call `Agent.Session()` or `Agent.ConversationLen()`, it would violate this invariant and potentially create a cycle (since `internal/agent/service.go` imports `internal/agent/api`).

The Go compiler would catch a direct import cycle, but the constraint is more subtle: `internal/agent/api/codec.go` must not import anything from `internal/agent` that imports `internal/agent/api`. Currently `internal/agent` does NOT import `internal/agent/api`, so there is no cycle risk today. But once `internal/agent/service.go` (which lives in the `internal/agent` package per the plan's package structure) imports `internal/agent/api`, the constraint becomes: `internal/agent/api` can import `internal/agent` types BUT `internal/agent/service.go` (same package as `internal/agent`) also imports `internal/agent/api`. Wait -- `service.go` is in `internal/agent/` (the plan shows `internal/agent/service.go`), so it's in the `agent` package. And `internal/agent/api/` is a sub-package (`agent/api`). Go packages are directory-scoped, so `internal/agent/service.go` is package `agent` and `internal/agent/api/agent.go` is package `api`. The import `internal/agent/api -> internal/agent` is fine. And `internal/agent/service.go` (package `agent`) importing `internal/agent/api` (package `api`) is also fine. No cycle because `agent/api` imports `agent` for types, and `agent` (specifically `service.go`) imports `agent/api` for the interface. This is a valid bidirectional import between parent and child packages in Go.

**Status:** Safe. No action needed, but the types-only constraint on the `agent/api -> agent` direction should be enforced in code review.

### Error Propagation

The plan's error mapping table (section 5.4) extends `rpc.MapError()`. Currently `MapError()` only handles sandbox errors. Adding agent errors requires importing `internal/agent` in `internal/rpc/errors.go`. The plan acknowledges this (R2 review A-F9: "Adding `internal/agent` as a dependency of `internal/rpc/errors.go` is acceptable"). This is correct -- `internal/rpc` already imports `internal/sandbox`.

However, the `MapError()` function uses `errors.Is()` and `errors.As()` for matching. The agent errors (`BusyError`, `StoppedError`, `InvalidStateError`) implement `Unwrap()` to return sentinel errors (`ErrBusy`, `ErrStopped`, `ErrInvalidState`). So `errors.Is(err, ErrBusy)` works for type-wrapped `BusyError`. The plan maps `agent.BusyError` to `CodeFailedPrecondition`, which is correct. `ErrQueueFull` is a sentinel (not wrapped), so `errors.Is(err, ErrQueueFull)` also works. No issues found.

### Capability/Feature Negotiation

The plan does not introduce a capability negotiation mechanism between the orchestrator and the agent-server. The `CreateAgentSessionRequest` specifies a driver name, model, and tools -- but the agent-server may not support a particular driver or model. The plan says `CreateSession` resolves the model from registry and driver from registry, returning errors if not found. This is adequate for basic cases.

However, there is no `ListCapabilities` or `GetServerInfo` method on `AgentService` that would let the orchestrator discover what drivers, models, and tools the agent-server supports before attempting to create a session. This is acceptable for the current plan scope (the orchestrator knows what agent-servers are running), but would be needed for a more dynamic deployment model.

---

## Findings Summary

| # | Seam | Severity | Summary |
|---|------|----------|---------|
| F1 | AgentService <-> Agent | P1 | Agent.Start vs Agent.Prompt asymmetry creates fragile branching in AgentLoopService.SendMessage with `started` flag that could get out of sync on partial failures |
| F2 | AgentService <-> Agent | P2 | _(Cleared)_ Agent.Abort signature matches plan |
| F3 | AgentService <-> Agent | P2 | GetSession clones entire conversation log just to compute ConversationLen -- wasteful for large conversations |
| F4 | AgentService <-> RPC | P1 | EventReceiver error contract underspecified -- adapter must handle io.EOF vs ErrStreamClosed vs other errors correctly for clean stream termination |
| F5 | AgentService <-> RPC | P2 | Dual delivery model risks double-counting/double-persistence if orchestrator consumes both per-turn and session-wide streams |
| F6 | SandboxControl <-> ExecutionEnvironment | P1 | SandboxCapabilities embedding environment.Capabilities creates semantic field confusion (ConcurrentSessions, MaxSessionDuration mean different things at each level) |
| F7 | SandboxControl <-> ExecutionEnvironment | P2 | Address format ambiguity (host:port vs URL) between SandboxControl responses and AgentServiceClient constructor |
| F8 | AgentLoopService <-> EventPublisher | P2 | Unit test strategy for AgentLoopService does not mention mocking EventPublisher for event verification |
| F9 | AgentLoopService <-> EventPublisher | P3 | EventPublisher.Publish is fire-and-forget with no error return -- persistence-critical events could be silently dropped under backpressure |
| F10 | SandboxControl <-> Sandbox-Host RPC | P1 | CreateSandbox -> CreateSession field mapping is lossy (Resources not present in CreateSessionRequest, Quota not present in CreateSandboxRequest) |
| F11 | SandboxControl <-> Sandbox-Host RPC | P1 | LaunchProcess requires new sandbox-host RPC endpoint that does not exist; addendum 11.add03 is a hard dependency not yet created |
| F12 | Cross-agent resume chain | P1 | agent.AgentMessage -> ConversationEntry conversion function is unspecified -- gap in the resume conversion chain |
| F13 | Agent-server <-> transport.Server | P0 | transport.Server constructor must be refactored to accept optional services; current constructor requires all services upfront and unconditionally registers all handlers |
| F14 | Import cycles | P3 | Import flow is safe but types-only constraint on agent/api -> agent import is enforced by convention only |

### By Severity

- **P0:** 1 (F13 -- transport.Server refactoring is blocking for the agent-server binary)
- **P1:** 6 (F1, F4, F6, F10, F11, F12 -- interface mismatches and missing specifications)
- **P2:** 5 (F3, F5, F7, F8, F2/cleared)
- **P3:** 2 (F9, F14)
