# Review: 18-agent-loop-rpc (r1-b)

- Source doc: `docs/plans/18-agent-loop-rpc.md`
- Reviewed commit: a257fb7
- Reviewer: r1-b

## Findings

### P0 - SandboxControl interface conflicts with and partially duplicates ExecutionEnvironment

**Problem**

Plan 18 introduces `SandboxControl` (section 6) with methods `CreateSandbox`, `DestroySandbox`, `PauseSandbox`, `ResumeSandbox`, and a `SandboxCapabilities` struct. However, `ExecutionEnvironment` (plan 11-add01, implemented at `internal/sandbox/environment/environment.go`) already covers the identical lifecycle surface: `Create`, `Destroy`, `Pause`, `Resume`, with `Capabilities` including `Snapshots`, `Rollback`, `Pause`.

The two interfaces overlap on lifecycle but diverge on semantics:
- `ExecutionEnvironment` covers lifecycle + tool execution in a unified interface. The architecture doc and plan 11-add01 explicitly state: "There is no meaningful distinction between 'how tools execute' (ToolBackend) and 'what sandbox to use' (provider). They are the same decision."
- `SandboxControl` carves out lifecycle as a separate concern and adds `LaunchProcess`, which `ExecutionEnvironment` does not have.

This creates two competing lifecycle abstractions. The orchestrator now must decide: does it call `SandboxControl.CreateSandbox()` or `ExecutionEnvironment.Create()`? The plan says SandboxControl is the "control-plane counterpart to ExecutionEnvironment" but does not explain how they coordinate. In the "Agent in Sandbox" placement mode, who owns the sandbox lifecycle? Both interfaces claim to.

Additionally, `SandboxCapabilities` (plan 18: Snapshots, Rollback, Pause, DeepPause) overlaps with `environment.Capabilities` (existing: Snapshots, Rollback, Pause, StreamingProgress, TierRouting, MaxSessionDuration, ConcurrentSessions) but has different fields. This will confuse implementors.

**Required fix**

Either:
1. Extend `ExecutionEnvironment` with a `LaunchProcess` method (the one genuinely new capability) and drop `SandboxControl` entirely, OR
2. Clearly define the boundary: `SandboxControl` is ONLY for "Agent in Sandbox" mode where the orchestrator manages the sandbox and the agent runs inside it. `ExecutionEnvironment` is for "Tools in Sandbox" and "All Local." Document that they never both apply to the same sandbox simultaneously, and unify the `Capabilities` types to avoid drift.

The plan must explicitly address how `SandboxControl` interacts with `ExecutionEnvironment` in each placement mode and reference plan 11-add01's unification decision.

---

### P1 - AgentService interface duplicates RuntimeController without acknowledging it

**Problem**

The architecture doc (section "RuntimeController Interface") defines:
```go
type RuntimeController interface {
    CreateSession(ctx, opts) (*Session, error)
    GetSession(ctx, id) (*Session, error)
    ListSessions(ctx) ([]*Session, error)
    StopSession(ctx, id) error
    PauseSession(ctx, id) error
    ResumeSession(ctx, id) error
    Subscribe(ctx, sessionID) (<-chan AgentEvent, error)
    SessionState(ctx, id) (SessionState, error)
}
```

Plan 18 introduces `AgentService` (section 3) which covers nearly identical territory: `CreateSession`, `GetSession`, `DestroySession`, `SendMessage`, `Continue`, `Steer`, `FollowUp`, `Abort`, `SubscribeEvents`, `ResumeSession`. The overlap is substantial (create, get, destroy/stop, subscribe) but the interfaces differ in shape and don't share types.

The plan doesn't explain the relationship. Is `AgentService` meant to replace `RuntimeController`? Is it the RPC-facing subset? Should `RuntimeController` delegate to `AgentService`? Or should `AgentService` be a transport layer under `RuntimeController`? Without clarity, implementors will build two parallel session management systems.

**Required fix**

Define the precise relationship between `AgentService` and `RuntimeController`. Recommended approach: `AgentService` is the agent-loop-facing API (what an agent loop server exposes). `RuntimeController` is the orchestrator-facing API (what application code calls). `RuntimeController` implementations may delegate to `AgentService` for the agent loop interaction. This should be documented with an explicit layering diagram.

---

### P1 - ResumeSession lacks conversation log validation and version safety

**Problem**

`ResumeSession` (section 10.2) accepts `[]AgentMessageRecord` and replays it into the agent session. The plan specifies no validation:

1. **No schema version**: `AgentMessageRecord.Content` is `json.RawMessage`. When models, providers, or tool schemas change, old conversation logs may contain content blocks in formats the current agent doesn't understand. There's no version field to detect this.

2. **No integrity checks**: Conversation logs can be arbitrarily corrupted (truncated writes, concurrent modifications during crash). The plan doesn't specify how to detect or handle malformed records.

3. **No tool compatibility check**: The resumed session may reference tools (in tool_result records) that are not in the new session's `Tools` list. The agent loop will see tool results for tools it doesn't know about.

4. **Turn numbering**: `AgentMessageRecord.Turn` may not be contiguous if the log was from a crash. The plan doesn't specify how the agent handles gaps.

For session forking (section 10.3), the same conversation log is replayed into multiple sessions. If the log contains non-deterministic tool results (e.g., file reads with timestamps), the forked agents will have divergent views of what the tools returned vs. what the filesystem actually contains. The plan doesn't address this.

**Required fix**

1. Add a `SchemaVersion` field to `ResumeSessionRequest` or embed version metadata in the conversation log format.
2. Specify validation rules for `ResumeSession`: at minimum, verify all records parse, roles alternate correctly, and turn numbers are monotonically non-decreasing.
3. Document the forking caveat about stale tool results and specify whether tool results are replayed as-is or re-executed.

---

### P1 - Cross-agent resume conversion chain has lossy steps and missing intermediate type

**Problem**

Section 10.4 defines the conversion chain:
```
AgentMessageRecord (orchestrator/SQLite)
    -> ai.Message (internal types)
    -> ConversationEntry (canonical termmux format)
    -> Claude Code session.jsonl (native format)
```

Several issues:

1. **Missing first conversion**: There's no code or specification for `AgentMessageRecord -> ai.Message`. `AgentMessageRecord.Content` is `json.RawMessage` while `ai.Message` is a strongly typed Go struct hierarchy. The mapping is non-trivial and unspecified.

2. **Lossy conversions**: Looking at the actual `ConversationEntry` type in `internal/termmux/driver/driver.go`, it has: `Timestamp`, `Role`, `Content` (string), `ToolCall`, `Thinking`, `Usage`. But `AgentMessage` (in `internal/agent/types.go`) wraps `ai.Message` which has `ContentBlocks` (structured array of text, image, tool_use, tool_result blocks). Flattening `ContentBlocks` to a single `Content string` in `ConversationEntry` is lossy -- multi-block messages, image content, and structured content blocks lose their structure.

3. **Type mismatch**: The existing `AgentMessage` (plan 05, implemented in `internal/agent/types.go`) stores `ai.Message`, but plan 18 introduces `AgentMessageRecord` which has `Role string` and `Content json.RawMessage`. The plan doesn't define the mapping between these two types.

4. **Tool name/schema fidelity**: When converting from our native format to Claude Code session.jsonl, tool names and argument schemas may differ. Our tools (`bash`, `read_file`, `edit_file`) may not map 1:1 to Claude Code's tool names. The conversion code in `session_log.go` does not handle tool name translation.

**Required fix**

1. Define the `AgentMessageRecord <-> AgentMessage` bidirectional mapping explicitly in the plan, including which fields are lost in each direction.
2. Acknowledge image content, multi-block messages, and structured tool arguments as lossy conversion points and specify fallback behavior.
3. Specify tool name/schema mapping rules or state that cross-agent resume is only valid when both agents share the same tool definitions.

---

### P1 - AgentLoopService is stateless but holds mutable session state -- contradiction

**Problem**

Section 10.1 states: "The agent loop server is stateless -- it runs whatever conversation it's given." Section 4 shows `AgentLoopService` holding `sessions map[string]*managedSession` with `mu sync.Mutex`. This is in-process mutable state.

The plan conflates two meanings of "stateless":
1. "Does not persist state to disk" (true)
2. "Has no in-memory state" (false -- it holds active sessions)

This matters because:
- If the agent-server process restarts, all in-flight sessions are lost. The plan says the orchestrator recovers via `ResumeSession`, but what about mid-turn state? If a tool is executing when the crash happens, the tool's side effects (file writes, bash commands) may have partially completed. `ResumeSession` replays the conversation log but cannot replay or undo partial tool effects.
- Multiple concurrent `ResumeSession` calls for the same session ID could race. The plan specifies session ID is "Optional; generated if empty" but doesn't address what happens if the orchestrator retries a `ResumeSession` and the first one already created the session.

**Required fix**

1. Replace "stateless" language with "ephemeral" or "non-durable" to avoid confusion.
2. Document the mid-turn crash scenario explicitly: what guarantees does the orchestrator have about tool side effects? State that partial tool effects are not rolled back (unless ZFS snapshots are used) and the orchestrator must handle this at the application level.
3. Specify idempotency behavior for `ResumeSession` (and `CreateSession`): if session ID already exists, return the existing session or error?

---

### P1 - [IG] Dual-view streaming relay lacks backpressure propagation and failure isolation

**Problem**

Section 12.3 shows the orchestrator maintaining two parallel streams per termmux agent session: an event subscription (structured) and a terminal subscription (raw bytes). Issues:

1. **No backpressure specification**: Looking at the existing `AgentEventServer.Publish()` in `internal/rpc/server/event_server.go`, slow consumers get events dropped (non-blocking channel send with `default` case). The plan doesn't address whether the orchestrator relay inherits this behavior. If the UI client is slow, do events get dropped at the orchestrator? At the tailer? Both?

2. **Relay failure modes**: If the event stream from the tailer fails but the terminal stream is fine (or vice versa), what happens? The plan shows them as independent streams but doesn't specify failure correlation. Should the orchestrator tear down both if one fails?

3. **Ordering between views**: The structured event view and terminal view are derived from different sources (session.jsonl tailer vs. PTY output). They can go out of sync: the terminal shows output before the session.jsonl file is flushed, or vice versa. The plan doesn't acknowledge this timing skew or specify whether it matters.

4. **Buffer sizes**: The session log tailer polls at 500ms intervals (from `internal/termmux/eventsrc/sessionlog/tailer.go`). The terminal stream is real-time. This creates an inherent latency gap between the two views. The plan doesn't quantify or address this.

**Required fix**

1. Specify backpressure policy for the orchestrator relay: drop-oldest, block-producer, or error-and-reconnect.
2. Define failure correlation policy: independent or coupled lifecycle.
3. Acknowledge the timing skew between structured and terminal views and specify whether the UI should handle reconciliation.
4. Consider adding sequence numbers or timestamps to enable cross-view correlation at the UI layer.

---

### P2 - StreamTerminal added to AgentService but should be on a separate interface

**Problem**

Section 12.4 adds `StreamTerminal` to the `AgentService` interface. But the existing codebase already has a separate `TerminalService` interface in `internal/rpc/api/terminal.go`:

```go
type TerminalService interface {
    StreamTerminal(ctx, req) (TerminalStreamHandle, error)
}
```

With its own procedure (`ProcedureTerminalStream = "/rpc.v1.TerminalService/StreamTerminal"` in `internal/rpc/transport/procedures.go`).

Adding `StreamTerminal` to `AgentService` creates two places where terminal streaming lives. This violates the existing separation of concerns: `TerminalService` is specifically for terminal I/O, separate from sandbox/agent session management. The plan should keep this separation.

Additionally, plan 18's `StreamTerminal` returns a different type (`TerminalStream` with `Send([]byte)/Recv() ([]byte, error)`) than the existing `TerminalStreamHandle` (which uses `TerminalClientMessage`/`TerminalServerMessage` with structured fields for input, resize, attached, output, detached). The plan's simplified byte-level interface loses resize, attach, and detach semantics.

**Required fix**

1. Remove `StreamTerminal` from `AgentService`. Instead, document that the existing `TerminalService` is used for terminal access to termmux-backed sessions.
2. If the agent loop server needs to expose terminal streaming, register the existing `TerminalService` procedures on the agent-server's HTTP handler -- don't duplicate the interface.
3. If a simpler terminal interface is needed for the agent RPC specifically, justify why the existing `TerminalStreamHandle` is insufficient and propose extending it rather than creating a parallel type.

---

### P2 - Section numbering is broken -- duplicate section 13

**Problem**

The plan has two sections numbered 13:
- Section 13 "Package Structure" (line 811)
- Section 13 "Testing Strategy" (line 844, should be section 14)

The testing strategy subsections use 11.x numbering ("11.1 Unit Tests", "11.2 Integration Tests", "11.3 Harness Tests") which doesn't match any section.

Section 15 "Connected Components" has subsections numbered 13.1, 13.2, 13.3.

**Required fix**

Renumber sections consistently. The intended numbering appears to be: 13 Package Structure, 14 Testing Strategy (with 14.1, 14.2, 14.3), 15 Connected Components (with 15.1, 15.2, 15.3), 16 Open Questions.

---

### P2 - Connected components table is incomplete -- missing key seam impacts

**Problem**

Section 15 (labeled 13) lists only 3 modified seams and 3 new seams. Missing:

1. **`internal/sandbox/environment`**: If `SandboxControl` is introduced (even if it shouldn't be per finding P0), it must reference the `ExecutionEnvironment` interface and explain the relationship. If it's not introduced, `ExecutionEnvironment` should be listed as a seam that `AgentLoopService` consumes.

2. **`internal/agent/types.go`**: The new `AgentMessageRecord` type must map to/from `AgentMessage`. This seam is not listed.

3. **`internal/termmux/driver/driver.go`**: The `ConversationEntry` type is used in the cross-agent resume conversion chain. This seam is not listed.

4. **`internal/rpc/api/types.go`**: The existing `AgentEventReceiver` interface is reused by `AgentService.SendMessage()` and `AgentService.SubscribeEvents()`. This dependency is not listed.

5. **`internal/agent/agent.go`**: `AgentLoopService` creates and manages `Agent` instances. The plan references `Agent.Prompt()`, `Agent.Start()`, `Agent.Continue()`, etc., but doesn't list the `Agent` API as a seam even though the service is a wrapper around it.

**Required fix**

Add all missing seams to the connected components table with specific contract details (types, methods, direction of dependency).

---

### P2 - Implementation ordering has a dependency violation: SandboxControl before AgentLoopService integration

**Problem**

Implementation order (section 14):
- Step 8: SandboxControl interface
- Step 9: NativeSandboxControl with LaunchProcess

But LaunchProcess is needed to deploy agent-server inside a sandbox (step 10: cmd/agent-server binary, step 11: e2e test). However, the plan lists the sandbox-host as needing a new `LaunchProcess` RPC endpoint (connected components table), which is a change to the sandbox-host service (plan 11). This cross-plan dependency is not captured in the plan header: "Depends on: 05-agent, 13-rpc-layer, 11-sandbox-host-service.add01" -- the add01 doesn't include LaunchProcess.

Furthermore, the NativeSandboxControl tests (step 9) require the sandbox-host to actually support `LaunchProcess`. Unless a mock is used, this step depends on changes to plan 11's sandbox-host binary.

**Required fix**

1. Update the dependency header to include the LaunchProcess capability requirement on sandbox-host (either as a new addendum to plan 11 or as a cross-plan note).
2. Specify that step 9 uses a mock sandbox-host service for testing, with the real integration deferred to step 11.
3. Consider moving SandboxControl (steps 8-9) after the agent-server binary (step 10) since the binary is usable standalone without SandboxControl, but SandboxControl without the binary has limited value.

---

### P2 - APIKey in CreateAgentSessionRequest is a security concern with no mitigation

**Problem**

Section 16 (Open Questions) item 3 acknowledges this: "The CreateAgentSessionRequest includes an APIKey field. How is this secured in transit?"

This is listed as an open question but the plan proceeds to include `APIKey` as a plain string field in the request type (section 3.1). In the "Agent in Sandbox" placement mode, the API key traverses the network from orchestrator to agent-server. Even with TLS, this means:
- The key is visible in request logs if logging is enabled
- The key is stored in memory on the agent-server process
- If the agent-server is compromised (it runs in a sandbox, which is specifically designed for untrusted workloads), the key is exposed

The plan doesn't specify key rotation, credential injection alternatives, or key scoping.

**Required fix**

1. Resolve the open question before implementation. At minimum: document that TLS is required, keys must not be logged, and the key should be passed via a secure channel (e.g., environment variable injection into the sandbox rather than over the RPC).
2. Consider a credential injection model where the orchestrator passes a short-lived token or credential reference rather than the raw API key.

---

### P2 - SubscribeEvents and SendMessage/Continue both return event streams -- overlap is underspecified

**Problem**

Section 3 defines three methods that return `AgentEventReceiver`:
- `SendMessage` -- "Returns a stream of events for this turn"
- `Continue` -- returns event stream
- `SubscribeEvents` -- "streams ALL events for the session lifecycle"

The relationship is unclear:
1. If the orchestrator calls `SubscribeEvents` and then `SendMessage`, do events appear on both streams? Is this double-delivery?
2. If the orchestrator only uses `SubscribeEvents`, does it still need to call `SendMessage` to start a turn? Or does `SubscribeEvents` automatically receive events from turns started by other methods?
3. What happens if `SendMessage`'s event stream is not consumed but `SubscribeEvents` is active? Does the turn still proceed?
4. Can `SubscribeEvents` be called before `CreateSession`? Before the first `SendMessage`?

**Required fix**

Specify the event delivery model precisely:
- Whether per-turn streams and the session-wide stream deliver duplicate events
- Whether consuming the per-turn stream is required for the turn to proceed
- The lifecycle constraints on when each method can be called

---

### P2 - No graceful shutdown protocol for agent-server

**Problem**

Section 7 defines the agent-server binary but doesn't specify shutdown behavior. When the agent-server receives SIGTERM:
1. Should it wait for in-flight turns to complete?
2. Should it send terminal events to subscribers?
3. Should it refuse new `CreateSession` calls while draining?
4. What's the deadline for graceful shutdown?

For the "launched inside sandbox" mode (section 7.3), the sandbox-host may kill the agent-server process at any time. The plan relies on the orchestrator's crash recovery (section 10.1) but doesn't specify how the agent-server cooperates with shutdown.

**Required fix**

Specify graceful shutdown semantics: drain period, event delivery guarantees during shutdown, and behavior when shutdown deadline is exceeded.

---

### P3 - No test harness document

**Problem**

All other plans in this project have companion test harness documents (e.g., `05-agent-test-harness.md`, `13-rpc-layer-test-harness.md`). Plan 18 has a testing strategy section (incorrectly numbered 11.x) but no dedicated test harness document. The testing section is thin: 3 unit test categories, 3 integration test categories, and 3 harness tests, with no detail on test infrastructure, mocking strategy, or acceptance test automation.

**Required fix**

Create `18-agent-loop-rpc-test-harness.md` following the project's established pattern. It should detail:
- Mock agent driver for service-level tests
- In-memory ConnectRPC test server setup
- Session forking stress tests
- Cross-agent resume round-trip property tests
- Event stream fidelity tests (no dropped events, ordering preserved)

---

### P3 - AgentEventServer is shared from existing RPC layer but plan 18 lists it in AgentLoopService struct

**Problem**

Section 4 shows `AgentLoopService` containing `events *AgentEventServer`. The `AgentEventServer` type already exists at `internal/rpc/server/event_server.go` and implements `api.AgentEventService`. Plan 18 proposes having `AgentLoopService` own an instance of this, but `AgentLoopService` is defined in `internal/agent/service.go`.

This creates an import from `internal/agent` to `internal/rpc/server`, which violates the project's import flow: agent should not import RPC server code. The existing architecture has the RPC layer importing agent, not the reverse.

**Required fix**

Use a callback or interface instead. `AgentLoopService` should accept a `func(sessionID string, event AgentEvent)` publish callback, or define a minimal `EventPublisher` interface in `internal/agent` that `AgentEventServer` implements. This preserves the import direction.

---

### P3 - ResumeSessionRequest duplicates all fields from CreateAgentSessionRequest

**Problem**

`ResumeSessionRequest` (section 3.1) contains all the same fields as `CreateAgentSessionRequest` plus `ConversationLog`. This duplication means any changes to session configuration must be made in two places.

**Required fix**

Consider embedding `CreateAgentSessionRequest` inside `ResumeSessionRequest`:
```go
type ResumeSessionRequest struct {
    CreateAgentSessionRequest
    ConversationLog []AgentMessageRecord
}
```

Or extract a shared `SessionConfig` type used by both.

---

## Summary

14 findings: 1 P0, 5 P1, 6 P2, 2 P3

**Verdict**: Not approved

The P0 finding (SandboxControl conflicting with ExecutionEnvironment) represents a fundamental architectural conflict that must be resolved before implementation. The P1 findings around ResumeSession validation, cross-agent conversion fidelity, the AgentService/RuntimeController relationship, and backpressure handling all represent significant design gaps that would lead to implementation problems. The section numbering errors and incomplete connected components table suggest the plan needs another editing pass for internal consistency.
