# Plan Review: 18 - Agent Loop RPC Service

**Reviewer:** Agent R1-A
**Plan version reviewed:** Draft (current as of 2026-03-16)
**Review date:** 2026-03-16

---

## Summary

This plan adds an RPC interface around the existing agent loop so it can run as a standalone service, introduces the `SandboxControl` interface for sandbox lifecycle management, and defines session resume/fork/migration capabilities. The plan is well-structured and follows established codebase patterns (mirroring the `SandboxService`/`SandboxServer` pattern). However, there are several gaps in interface design, error handling, type consistency, security, and testing that should be addressed before implementation.

---

## Findings

### P0 - Critical (must fix before implementation)

#### F1. `AgentService` interface is placed in `internal/rpc/api` but defines a different return type than the existing pattern

**Location:** Section 3, `AgentService` interface

The plan places the `AgentService` interface in `internal/rpc/api/agent.go`. However, `SendMessage` and `Continue` return `(AgentEventReceiver, error)`. The existing `AgentEventReceiver` in `internal/rpc/api/types.go` returns `*AgentEventEnvelope` (which wraps `agent.AgentEvent` with a `SessionID`). But the plan's `AgentService` interface and the in-process `AgentLoopService` live in `internal/agent/service.go`.

This creates a **circular import**: `internal/agent/service.go` would need to import `internal/rpc/api` for the `AgentEventReceiver` type, but `internal/rpc/api/types.go` already imports `internal/agent` for the `agent.AgentEvent` type.

The existing codebase has `internal/rpc/api/types.go` importing `github.com/anthropics/flex-agent-runtime/internal/agent`, so `internal/agent` cannot import `internal/rpc/api`. Either:
- The `AgentService` interface must live in a separate package (not `internal/rpc/api`), or
- A new event receiver type must be defined in `internal/agent` that doesn't reference `api` types, or
- The interface needs to be split into a transport-neutral core (in `internal/agent` or a new package like `internal/agent/api`) and an RPC-specific layer.

This is the same pattern the codebase uses with `internal/sandbox/environment` (transport-neutral interface) vs `internal/rpc/api` (RPC-specific types). The plan should define a similar separation for the agent service.

#### F2. Missing `Close()` / graceful shutdown on `AgentLoopService`

**Location:** Section 4

The `SandboxServer` implements `Close() error` which stops the sweep goroutine. The `AgentLoopService` manages multiple sessions with agent loops (each running goroutines via `NativeDriver.run()`), but has no `Close()` method defined. On server shutdown:
- All active agent sessions need to be stopped (calling `agent.Stop()` on each)
- The `AgentEventServer` used for fan-out needs to be cleaned up
- Any background goroutines need to be terminated

Without this, the standalone `agent-server` binary cannot shut down gracefully, and resource leaks will occur in tests.

#### F3. `CreateAgentSessionRequest.APIKey` transmitted in plain Go struct over RPC

**Location:** Section 3.1, Open Question 3

The plan acknowledges this as an open question but does not resolve it. The `APIKey` field is sent over the wire in the `CreateAgentSessionRequest`. The existing `SandboxServer` pattern does not handle any secrets. This is a new concern.

At minimum:
- The API key should NOT be persisted in the `managedSession.config` after being consumed to create the provider client. Storing it in memory longer than necessary is a liability.
- The plan should specify that TLS is required for the agent-server binary (not optional).
- The plan should consider whether API keys should be injected via environment variables on the agent-server process (via `LaunchProcess.Env`) rather than transmitted over RPC, which would be a more secure pattern matching how sandbox-host already handles environment setup.

---

### P1 - Important (should fix before implementation)

#### F4. Type divergence: `SessionMetrics` defined in two places

**Location:** Section 3.1 types vs `internal/agent/types.go`

The plan defines `SessionMetrics` in `internal/rpc/api/agent_types.go` with the same field names and types as the existing `agent.SessionMetrics` in `internal/agent/types.go`. This creates a duplicate type that will need manual mapping (like the `codec` package does for sandbox types). However, the plan does not mention any codec/mapping layer for agent types. This is inconsistent with the sandbox pattern where `internal/rpc/codec/sandbox_map.go` handles all conversions.

Either:
- Define the RPC types in `internal/rpc/api/` and add a `codec` mapping layer (consistent with sandbox pattern), or
- Have the RPC layer use the `internal/agent` types directly where possible (simpler but creates tighter coupling).

The plan should be explicit about which approach is taken and include the codec/mapping work in the implementation order.

#### F5. `AgentMessageRecord` vs `AgentMessage` type mismatch

**Location:** Section 3.1 (`ResumeSessionRequest.ConversationLog`)

The plan defines `AgentMessageRecord` with `Content json.RawMessage` for the resume conversation log. The existing `AgentMessage` in `internal/agent/types.go` uses `Message ai.Message` (an interface type). Converting between `json.RawMessage` and `ai.Message` requires deserialization with type discrimination (user vs assistant vs tool_result).

The plan does not define:
- How `Role` string maps to the correct `ai.Message` concrete type during deserialization
- Whether there's an existing serialization/deserialization mechanism for `ai.Message` that can be reused
- Error handling for malformed or unrecognized message types in the conversation log

This conversion logic is non-trivial and should be explicitly planned, including edge cases like unknown message roles, truncated content, and version mismatches in serialized content.

#### F6. `SubscribeEvents` overlaps with existing `AgentEventService.StreamAgentEvents`

**Location:** Section 3 interface, Section 5.1 procedures

The existing codebase already has `AgentEventService` with `StreamAgentEvents` (in `internal/rpc/api/types.go`) and a corresponding `ProcedureEventsStream` in the transport layer. The plan's `AgentService.SubscribeEvents` appears to duplicate this functionality.

The plan should clarify:
- Is `SubscribeEvents` the per-session counterpart to the existing `StreamAgentEvents` (which also takes a session ID)?
- If so, why not reuse the existing `AgentEventService` directly?
- If different (e.g., `SubscribeEvents` also streams events from sub-sessions or has different lifecycle semantics), the distinction must be documented.
- The existing event server (`server.AgentEventServer`) already supports per-session streaming. The plan should explain why a new method on `AgentService` is needed vs using the existing event service.

#### F7. `SendMessage` and `Continue` return `AgentEventReceiver` but the underlying `Agent` API is fire-and-forget

**Location:** Section 3, Section 4

`Agent.Start()` and `Agent.Prompt()` return `error` synchronously but the actual work runs asynchronously in a goroutine (via `NativeDriver.run()`). The plan says `SendMessage` returns an `AgentEventReceiver` "for this turn," but:
- There is no mechanism in the existing `Agent` to scope event subscriptions to a single turn. `Agent.Subscribe()` receives ALL events.
- The plan does not describe how to filter/demarcate events belonging to the current turn vs. future turns.
- The plan does not describe when the returned `AgentEventReceiver` stream ends. Does it end at `EventTurnCompleted`? What if the agent processes follow-ups automatically (via the control queue) before the turn "completes" from the caller's perspective?

The plan should specify:
- Stream termination conditions for per-turn receivers
- How turn boundaries are detected and used to close the stream
- Whether multiple concurrent `SendMessage` calls are allowed (current `Agent.Start()` returns `BusyError` if already running)

#### F8. `SandboxControl.LaunchProcess` requires new sandbox-host RPC but details are deferred

**Location:** Section 6.1, Section 15

The plan states `LaunchProcess` is a "new capability needed on sandbox-host" but does not define:
- The RPC request/response types for the sandbox-host side
- How the sandbox-host manages the lifecycle of launched processes (PID tracking, health checks, restart policy)
- How port proxying is implemented (acknowledged as Open Question 2 but not resolved)
- What happens when the launched process crashes (does the sandbox-host detect it? Does it notify the orchestrator?)
- Whether `LaunchProcess` is a one-shot operation or if the sandbox-host keeps the process alive

This is a significant piece of new functionality on an existing service. It should either be fully specified in this plan or explicitly deferred to a separate plan document (with a dependency tracked).

#### F9. No rate limiting or resource bounds on session creation

**Location:** Section 4, Section 7

The `AgentLoopService` manages sessions in an unbounded `map[string]*managedSession`. There is no:
- Maximum session count limit
- Resource accounting per session
- Session timeout/reaping for abandoned sessions
- Protection against session ID collision (UUID generation mentioned but not specified)

The existing `SandboxHostService` has `MaxSessions` configuration. The agent service should have equivalent protection, especially since each session holds an active LLM connection and potentially multiple goroutines.

#### F10. `Steer` behavior during non-streaming state is undefined

**Location:** Section 3, Section 4

`Steer` is described as "injects a steering instruction into the current turn." The `Agent.Steer()` method calls `ControlQueue.EnqueueSteer()` which succeeds regardless of agent state (as long as the queue isn't full/closed). But:
- What happens if `Steer` is called when the agent is in `StateIdle` (no active turn)?
- The plan says "Return immediately (these are enqueue operations)" but the steering message will sit in the queue until the next `SendMessage`/`Continue` call.
- Should `Steer` fail with a `FailedPrecondition` error when no turn is active?

The semantic difference between `Steer` (inject into current turn) and `FollowUp` (queue for after current turn) is unclear when the agent is idle. Both would behave the same way.

---

### P2 - Moderate (should address, can be done during implementation)

#### F11. Duplicate section numbering

**Location:** Sections 13 (Package Structure) and 13 (Testing Strategy)

The plan has two sections numbered 13: "Package Structure" and "Testing Strategy." The Testing Strategy section's subsections are numbered 11.1, 11.2, 11.3 (remnants of a renumbering). Connected Components subsections are labeled 13.1, 13.2, 13.3 but the section is numbered 15. This makes it hard to reference specific sections unambiguously.

#### F12. `ToolEnvironmentConfig.Type` should be a typed constant, not a raw string

**Location:** Section 3.1

The `Type` field on `ToolEnvironmentConfig` is a `string` with values `"local"` and `"sandbox"`. Following codebase patterns (e.g., `AgentState` is a typed string, `SessionState` in `environment/types.go` uses typed strings), this should be a named type with constants. This prevents typos and enables exhaustive switch checking.

#### F13. `StreamTerminal` in `AgentService` inconsistent with existing architecture

**Location:** Section 12.4

The plan adds `StreamTerminal` to the `AgentService` interface, but the existing codebase has `TerminalService` as a **separate** interface (in `internal/rpc/api/terminal.go`) and the terminal stream is registered as a separate procedure (`ProcedureTerminalStream`). Adding `StreamTerminal` to `AgentService` conflates two different concerns:
- Agent session management (creating, messaging, steering)
- Terminal I/O streaming (raw PTY bytes)

The plan even acknowledges this returns an error for non-termmux sessions. This suggests it should remain a separate service. The orchestrator can manage the mapping between agent sessions and terminal sessions.

#### F14. `ResourceSpec` in `SandboxControl.CreateSandboxRequest` is a new type

**Location:** Section 6

The plan defines `ResourceSpec` as a field of `CreateSandboxRequest` but does not specify which `ResourceSpec` type. The codebase already has:
- `gvisor.ResourceSpec` (with CPUs, MemoryMB, MaxPIDs, Timeout, MaxOutputBytes)
- `api.ResourceSpec` (with CPUs, MemMB)
- `tools.ResourceSpec` (with CPUs, MemMB)

The plan should specify which type is used or define a new one. Given that `SandboxControl` is a higher-level abstraction, it likely needs a different `ResourceSpec` than the gVisor-specific one.

#### F15. No idempotency for `CreateSession` / `ResumeSession`

**Location:** Section 4

The existing `SandboxServer.ExecuteTool` has idempotency protection (keyed by `sessionID:toolCallID`). The plan's `CreateSession` and `ResumeSession` allow the caller to specify a `SessionID`. If the client retries a `CreateSession` call (e.g., after a timeout where the server actually processed it), the service will return `ErrSessionExists` or similar. The plan should specify:
- Whether `CreateSession` with an existing session ID is idempotent (returns existing session) or an error
- Whether `ResumeSession` with an existing session ID replaces or errors

#### F16. Missing `ListSessions` method

**Location:** Section 3

The `AgentService` interface has `GetSession` but no `ListSessions`. For operational visibility and orchestrator state reconciliation (especially after crashes), listing active sessions is essential. The existing `SandboxService` doesn't have `ListSessions` either, but the agent service has a stronger need for it because sessions are long-lived and the orchestrator needs to reconcile state after restart.

#### F17. Error mapping for agent-specific errors not defined

**Location:** Section 5.2

The plan says the `AgentRPCServer` follows the same error mapping pattern as `SandboxServer`. But the existing `rpc.MapError()` in `internal/rpc/errors.go` only maps `sandbox.*` and `zfs.*` errors. Agent-specific errors (`agent.BusyError`, `agent.StoppedError`, `agent.InvalidStateError`, `agent.ErrQueueFull`) need to be mapped to appropriate RPC codes:
- `BusyError` -> `CodeFailedPrecondition` or `CodeUnavailable`
- `StoppedError` -> `CodeFailedPrecondition`
- `InvalidStateError` -> `CodeFailedPrecondition`
- `ErrQueueFull` -> `CodeResourceExhausted`
- Session not found -> `CodeNotFound`

The plan should specify these mappings and whether they extend the existing `MapError` function or use a separate one.

---

### P3 - Minor (nice to have, can be follow-up)

#### F18. Section 10.4 references `internal/termmux/driver/claudecode/session_log.go` - verify path exists

**Location:** Section 10.4

The plan references specific file paths for Claude Code session log conversion. These should be verified to exist or planned as dependencies. If this conversion logic doesn't exist yet, it should be listed as a dependency or implementation task.

#### F19. `AgentEventServer` as a field of `AgentLoopService` vs constructor injection

**Location:** Section 4

The plan shows `AgentLoopService` containing `events *AgentEventServer` (from the `server` package). But `AgentLoopService` lives in `internal/agent/service.go`. This creates a dependency from `internal/agent` to `internal/rpc/server`, which inverts the expected import direction. The event publishing mechanism should be injected as an interface, not a concrete type.

#### F20. Testing section doesn't mention testing the codec/mapping layer

**Location:** Section 13 (Testing Strategy)

If agent types need codec mapping (per F4), the testing plan should include codec round-trip tests. The existing `internal/rpc/codec/sandbox_map_test.go` provides the pattern.

#### F21. `Continue` semantic gap with existing `Agent.Continue()`

**Location:** Section 3, Section 4

The plan's `ContinueRequest` has only a `SessionID`. The existing `Agent.Continue()` calls `driver.Resume()`, which re-runs the agent loop without a new prompt. However, the plan's `SendMessage` flow says "Call `agent.Prompt(ctx, message)` (or `agent.Start(ctx, session, message)` for first message)." The distinction between first message (`Start`) and subsequent messages (`Prompt`) needs to be managed by `AgentLoopService`, tracking whether a session has been started. The plan should specify how the service distinguishes between first message and subsequent messages.

#### F22. `DestroySession` vs. session that is currently streaming

**Location:** Section 4

The plan's `DestroySession` flow calls `agent.Stop(ctx)` then removes from the sessions map. But if the agent is actively streaming (`NativeDriver.run()` is executing), `Stop` calls `cancel()` on the context and sets `running = false`. The goroutine in `run()` will detect `ctx.Err()` and exit, but there's a race window. The plan should specify:
- Whether `DestroySession` waits for the active turn to finish or forcefully cancels
- What events are emitted during forceful destruction
- Whether open event streams for the destroyed session are closed with an error or EOF

#### F23. Implementation order should include `MapError` extension earlier

**Location:** Section 14

Step 4 (AgentRPCServer) needs error mapping, but the error mapping extension for agent errors is not listed as a task. It should be added as a sub-task of step 2 or 4.

---

## Overall Assessment

The plan is architecturally sound and correctly follows the established pattern of Go interface + in-process implementation + RPC transport wrapper. The session resume/fork capabilities are well-thought-out and the separation of concerns between orchestrator (state owner) and agent-server (stateless executor) is clean.

The critical issue is the circular import between `internal/agent` and `internal/rpc/api` (F1), which will block implementation if not resolved upfront. The security concern around API key handling (F3) and missing graceful shutdown (F2) should also be resolved before implementation begins.

The most significant architectural risks are:
1. **Import cycle** (F1) - requires package reorganization
2. **Event stream scoping** (F7) - the plan assumes per-turn event streams but the existing agent infrastructure only supports session-wide subscriptions
3. **LaunchProcess under-specification** (F8) - a substantial new feature on sandbox-host that needs its own plan or much more detail

Recommendation: Address P0 and P1 findings, then proceed with implementation.
