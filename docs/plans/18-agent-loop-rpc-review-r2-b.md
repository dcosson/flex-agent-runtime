# Review: 18-agent-loop-rpc (Round 2, Reviewer B)

- Source doc: `docs/plans/18-agent-loop-rpc.md`
- Reviewed against: R1-incorporated revision (status: "Draft (R1 review incorporated)")
- Prior reviews checked: `18-agent-loop-rpc-review-r1-a.md`, `18-agent-loop-rpc-review-r1-b.md`
- Reviewer: r2-b

---

## R1 Incorporation Assessment

The plan incorporated all 28 R1 findings from both reviewers. The incorporation quality is generally high. The most significant structural changes (new `internal/agent/api` package for import cycle resolution, `EventPublisher` interface for import direction, `SessionConfig` extraction, `Close()` with drain logic, section renumbering, API key removal from wire protocol) are all correctly done and improve the plan materially. Below I note a few areas where the incorporation is incomplete or introduced new issues, followed by net-new findings.

---

## Findings

### P1 - Turn-scoped event stream has a subscription race window

**Location:** Section 3.2, Section 4 (SendMessage flow)

The SendMessage flow (section 4) is: (1) look up session, (2) call `agent.Start()` or `agent.Prompt()`, (3) subscribe to session events, (4) wrap in turn-scoped receiver, (5) return. But `agent.Start()` launches `NativeDriver.run()` in a goroutine (line 69 of `internal/agent/loop.go`: `go d.run(runCtx, session.Clone(), prompt)`). The driver immediately emits `EventTurnStarted` and begins streaming. If the subscription in step 3 happens after the goroutine has already emitted events, those events are lost for the per-turn stream.

The existing `Agent.Subscribe()` calls `eventBus.subscribe()` which only receives events published *after* subscription. There is no replay mechanism.

This is a real race condition: on a fast provider response or a cached/short prompt, the `EventTurnStarted` (and potentially early `EventAgentMessageDelta` events) could fire before the subscription is established.

**Required fix:** The subscription must be established *before* calling `agent.Start()`/`agent.Prompt()`. The SendMessage flow should be reordered to: (1) look up session, (2) subscribe to session events and wrap in turn-scoped receiver, (3) call `agent.Start()`/`agent.Prompt()`, (4) return the receiver. If `Start()` fails, close and discard the subscription.

---

### P1 - Close() drain logic has no synchronization primitive to wait on session teardown

**Location:** Section 4, Close() description

The Close() flow says: (1) set `closed = true`, (2) for each session call `DestroySession`, (3) wait for all sessions to clean up with a configurable deadline. But there is no specification of *how* to wait. `DestroySession` calls `agent.Stop()` which cancels the context and sets `running = false`, but the `NativeDriver.run()` goroutine runs asynchronously. `Stop()` does not block until the goroutine exits -- it just calls `cancel()` and returns.

Looking at the actual `NativeDriver.Stop()` (line 73-84 of `loop.go`): it sets `d.running = false`, calls `cancel()`, and returns. The goroutine in `run()` will eventually notice `ctx.Err()` and exit, but there is no `sync.WaitGroup` or channel to signal completion.

This means `Close()` has no way to know when all goroutines have actually finished. The "wait for all sessions to clean up" has nothing to wait on. In tests this will cause goroutine leaks and racy resource access; in production the 30-second deadline will always fire because there is no completion signal.

**Required fix:** Either:
1. Specify that `AgentLoopService` tracks a `sync.WaitGroup` for all active session goroutines, incremented when a turn starts and decremented in the deferred cleanup of `NativeDriver.run()`, or
2. Specify that `Agent.Stop()` should block until the driver goroutine exits (this would require changes to the existing Agent API, which may be out of scope for this plan -- if so, note it as a prerequisite change).

---

### P2 - `Steer` returns FailedPrecondition when idle, but `Agent.Steer()` does not check state

**Location:** Section 3, Section 4 (Steer)

The plan says `Steer` returns `CodeFailedPrecondition` if the agent is in `StateIdle`. The `AgentLoopService` is responsible for this check, since the underlying `Agent.Steer()` (line 235 of `agent.go`) calls `control.EnqueueSteer()` which succeeds as long as the queue is not full or closed -- it does not check agent state at all.

The issue is the race window between when `AgentLoopService` checks the state and when the steer is enqueued. Between the check and the enqueue:
- A turn could complete (state goes Idle), making the steer meaningless
- A turn could start (state leaves Idle), making a previously-rejected steer valid

This is a minor race, and for a best-effort steering API it is probably acceptable. But the plan should acknowledge this TOCTOU gap explicitly rather than presenting the behavior as deterministic. The alternative would be to add a state-checked `SteerIfActive()` method to `Agent` itself, but that adds complexity for marginal benefit.

**Required fix:** Add a note to section 4's Steer flow acknowledging the TOCTOU nature of the idle check: "Note: There is an inherent race between checking the agent state and enqueuing the steer command. A steer call may succeed but arrive after the turn has already completed. This is acceptable for best-effort steering semantics."

---

### P2 - `internal/agent/api` imports `internal/agent` types -- potential for future circular import

**Location:** Section 16.4 (Import Flow), Section 3

The import flow shows: `internal/agent/api -> internal/agent (AgentMessage, SessionMetrics -- types only)`. This works today because `internal/agent/api` only references type definitions from `internal/agent`. However, this creates a fragile dependency: if `internal/agent/api/codec.go` ever needs to call a method on `Agent` (e.g., for validation or state queries during codec operations), it would create a true circular import.

More concretely, the codec functions `AgentMessageToRecord` and `RecordToAgentMessage` take/return `agent.AgentMessage` which wraps `ai.Message` (an interface). The concrete implementations (`ai.UserMessage`, `ai.AssistantMessage`, `ai.ToolResultMessage`) require type-switching. This is purely a types-to-types dependency today, which is safe.

**Required fix:** No code change needed, but add a comment to the import flow section: "The `internal/agent/api -> internal/agent` import MUST remain types-only. If codec or interface logic needs to call Agent methods, those methods should be expressed as function parameters or interfaces defined in `internal/agent/api`, not direct imports of `internal/agent` functions."

---

### P2 - `SandboxCapabilities` embedding `environment.Capabilities` creates a leaky abstraction

**Location:** Section 6

R1-B's P0 finding about SandboxControl conflicting with ExecutionEnvironment was addressed by clarifying their complementary roles and having `SandboxCapabilities` embed `environment.Capabilities`. The embedding approach prevents type drift (good), but it leaks agent-level detail into the orchestrator-level interface:

- `environment.Capabilities` includes `StreamingProgress` and `TierRouting` -- these are tool-execution-level concerns that have no meaning at the orchestrator's `SandboxControl` level. An orchestrator calling `SandboxControl.Capabilities()` will see `StreamingProgress: true` and wonder what it means for sandbox lifecycle management.
- `environment.Capabilities` includes `MaxSessionDuration` and `ConcurrentSessions` -- these are resource-limit fields that make sense for both levels but with different semantics. At the environment level, `ConcurrentSessions` means "how many tools can run concurrently." At the SandboxControl level, it could mean "how many sandboxes this provider supports."

The embedding approach trades one problem (type drift) for another (semantic mismatch). The original R1-B finding suggested either extending `ExecutionEnvironment` to include `LaunchProcess` (option 1) or clearly documenting the boundary (option 2). The plan chose a hybrid that creates semantic ambiguity.

**Required fix:** Either:
1. Have `SandboxCapabilities` define its own fields instead of embedding, with explicit documentation of which `environment.Capabilities` fields are relevant at the orchestrator level (accepting the drift risk but gaining semantic clarity), or
2. Keep the embedding but add a clear note: "Fields inherited from `environment.Capabilities` that are not meaningful at the SandboxControl level (e.g., `StreamingProgress`, `TierRouting`) should be ignored by orchestrator-level code. They are present because of the embedding and reflect the underlying environment's capabilities, not the sandbox control interface's semantics."

Option 2 is simpler and sufficient.

---

### P2 - `ResourceSpec` in `CreateSandboxRequest` still not fully resolved

**Location:** Section 6, Round 1 Disposition table (F14: "Acknowledged")

R1-A's F14 noted that `ResourceSpec` is ambiguous -- there are three existing types: `gvisor.ResourceSpec`, `api.ResourceSpec`, and `tools.ResourceSpec`. The disposition says "Uses existing `api.ResourceSpec`. Noted in section 6." But looking at section 6, the `CreateSandboxRequest` struct says:

```go
Resources ResourceSpec  // CPU, memory (uses existing api.ResourceSpec)
```

This is a comment, not a type annotation. The field type is `ResourceSpec` (unqualified), but this struct is in `internal/sandbox/control/control.go`. If it uses `api.ResourceSpec` from `internal/rpc/api`, that creates an import from `internal/sandbox/control` to `internal/rpc/api` -- which is an unusual dependency direction (the sandbox/control package importing RPC API types). The import flow in section 16.4 shows `internal/sandbox/control -> internal/sandbox/environment (Capabilities)` but does NOT show an import of `internal/rpc/api`.

**Required fix:** Explicitly specify which `ResourceSpec` is used and verify the import direction is acceptable. If `api.ResourceSpec` creates an unwanted import, define a local `ResourceSpec` in `internal/sandbox/control/control.go` (it is only two fields: CPUs and MemMB) and add codec mapping if needed.

---

### P3 - `AgentMessageRecord.Content` JSON schema for assistant role lacks `stop_reason`

**Location:** Section 3.3

The JSON schema for Role "assistant" includes `content_blocks`, `model`, and `usage`. But `ai.AssistantMessage` also has `StopReason`, `ErrorMessage`, `API`, and `Provider` fields. The codec's `AgentMessageToRecord` must either include these in the JSON or document that they are intentionally dropped.

`StopReason` in particular is important for resume -- if the last assistant message stopped with `StopReason = "toolUse"`, the agent knows it needs to continue with tool execution. If stopped with `"length"`, the agent knows the context window was exhausted. Dropping this field means a resumed session cannot distinguish why the previous assistant turn ended.

**Required fix:** Add `stop_reason` and `error_message` to the assistant role JSON schema. `API` and `Provider` can reasonably be omitted since they are metadata about which provider generated the response, not semantic conversation content.

---

### P3 - Test harness document TODO is acceptable but should have a clear scope marker

**Location:** Section 14.3

The plan acknowledges the missing test harness document as a TODO. Both R1 reviewers flagged this (R1-B as P3). The plan says it will be created "as a follow-up." This is acceptable for the plan to proceed to implementation, but the follow-up should be created before implementation step 10 (integration tests) begins, since the harness document would inform the test infrastructure design.

**Required fix:** Change the TODO note to specify when the harness document should be created: "This document should be created before or during implementation step 10 (integration tests), as it will inform the test infrastructure design."

---

### P3 - `ResumeSession` validation does not check role alternation

**Location:** Section 3.4

R1-B's P1 finding on resume validation said: "at minimum, verify all records parse, roles alternate correctly, and turn numbers are monotonically non-decreasing." The plan incorporated turn monotonicity and record parsing, but role alternation is not mentioned. A valid conversation must alternate: user -> assistant -> (tool_result -> assistant ->)* user -> ... An invalid sequence like user -> user or assistant -> assistant would indicate a corrupted log.

Strict alternation checking may be overly rigid for crash recovery scenarios (a crash mid-turn could produce a log ending with user without a matching assistant). But completely omitting it means garbage input is silently accepted.

**Required fix:** Add role sequence validation as a warning (not an error): "If consecutive records have the same role (e.g., two user messages in a row without an intervening assistant message), a warning is produced. This can occur legitimately after a crash but may also indicate log corruption."

---

### P3 - Missing specification for how `ResumeSession` replays the conversation into the Agent

**Location:** Section 10.2

The sequence diagram shows "Replay conversation log into Agent.Session" but the plan does not specify the mechanism. Looking at `Agent.SetSession()` (line 84 of `agent.go`), it takes a `*Session` and clones it. So the replay would be:
1. Create a new `Session` with the conversation log populated from the `AgentMessageRecord` records
2. Call `agent.SetSession(session)`

But this requires converting `[]AgentMessageRecord` to `[]AgentMessage` (via `codec.RecordToAgentMessage`), then building a `Session` with `ConversationLog` populated. The plan's `AgentLoopService` flow for `ResumeSession` should explicitly state this: "Convert each `AgentMessageRecord` to `agent.AgentMessage` via `codec.RecordToAgentMessage()`, build a `Session` with the resulting `ConversationLog`, and call `agent.SetSession(session)` before returning."

This is straightforward but should be documented to avoid implementation ambiguity.

---

## R1 Incorporation Quality Check

| R1 Finding | Incorporation Quality | Notes |
|---|---|---|
| F1 (circular import) | Good | `internal/agent/api` package is clean. Import flow is documented. |
| B-P0 (SandboxControl vs ExecutionEnvironment) | Adequate | Coordination table is clear. Capabilities embedding has semantic issues (see P2 finding above) but is workable. |
| F2 (Close/shutdown) | Partially incomplete | Close() described but lacks synchronization primitive (see P1 finding above). |
| F3 (APIKey) | Good | Clean removal. Env var injection is well-documented. |
| F4 (SessionMetrics) | Good | Reuses `agent.SessionMetrics` directly. Codec handles wire format. |
| F5 (AgentMessageRecord codec) | Good | Section 3.3 with JSON schema is well-specified. Missing `stop_reason` (see P3 finding). |
| B-P1 (resume validation) | Good | Section 3.4 covers key validation rules. Missing role alternation (see P3). |
| B-P1 (lossy conversion) | Good | Lossy steps documented clearly in sections 3.3 and 10.4. |
| F6 (SubscribeEvents overlap) | Good | Dual delivery model clearly documented in section 3.2. |
| F7 (turn-scoped streams) | Partially incomplete | Mechanism specified but has subscription race (see P1 finding). |
| F8 (LaunchProcess) | Good | Lifecycle, port management, process monitoring all specified. KillProcess/GetProcessStatus added. |
| F9 (session limits) | Good | MaxSessions with WithMaxSessions option. |
| F10 (Steer when idle) | Adequate | FailedPrecondition specified but TOCTOU gap not acknowledged (see P2). |
| B-P1 (RuntimeController) | Good | Clear layering diagram and explanation in section 1. |
| B-P1 (stateless -> ephemeral) | Good | Language corrected. Mid-turn crash behavior well-documented. |
| B-P1 (backpressure) | Good | Section 12.4 covers all three concerns (backpressure, failure correlation, timing skew). |
| P2-StreamTerminal | Good | Correctly removed from AgentService. Uses existing TerminalService. |
| F11 (section numbering) | Good | Sections 13-17 renumbered correctly. Subsection numbering consistent. |
| F12 (ToolEnvironmentType) | Good | `ToolEnvironmentType` with constants. |
| B-P2 (seams) | Good | Consumed seams table (16.3) is comprehensive. Import flow diagram (16.4) is clear. |
| B-P2 (impl order) | Good | Dependency header updated. Mock sandbox-host specified. |
| F14 (ResourceSpec) | Incomplete | "Acknowledged" but still ambiguous (see P2 finding). |
| F15 (idempotency) | Good | CreateSession with existing ID returns error. Clearly specified. |
| F16 (ListSessions) | Good | Added to interface. |
| F17 (error mapping) | Good | Complete error mapping table in section 5.4. |
| F19 (EventPublisher) | Good | Clean interface, correct import direction. |
| B-P3 (SessionConfig extraction) | Good | Shared type embedded by both request types. |
| P3 (test harness) | Acceptable | TODO with follow-up note. Could be more specific on timing (see P3 finding). |

---

## Summary

9 findings: 0 P0, 2 P1, 3 P2, 4 P3

**Verdict: Conditionally approved for implementation.**

The two P1 findings (turn-scoped event stream race and Close() drain synchronization) are real implementation issues but can be resolved during implementation steps 3-4 (AgentLoopService + unit tests) without requiring further plan revision -- they are implementation-detail-level fixes with clear solutions. The P2 and P3 findings are either documentation clarifications or minor specification gaps that can be addressed as the code is written.

The R1 findings were incorporated thoroughly and correctly. The plan's overall architecture is sound: the `internal/agent/api` package separation, `EventPublisher` injection, `SessionConfig` extraction, turn-scoped stream design, and SandboxControl/ExecutionEnvironment boundary clarification all represent clean, well-reasoned design choices that follow established codebase patterns.

Recommendation: Proceed with implementation. Address the P1 findings during implementation steps 3-4. Create the test harness document before step 10.
