# Plan 21 Review: Serve Orchestrator Command

**Reviewer:** coder-2-sea
**Review round:** R1
**Date:** 2026-03-18
**Verdict:** Approve with required changes (P1 must be addressed)

---

## Summary

The plan is well-structured and correctly identifies the orchestrator as a thin proxy/router that wires existing building blocks. The three placement modes are correctly mapped to the existing SandboxControl implementations, and the use of `transport.WithAgentService(orch)` to make the orchestrator a drop-in AgentService is a clean design choice that gives clients zero-change compatibility.

Key strengths:
- Clear separation of placement-specific logic (sections 3.5-3.6)
- Correct use of existing interfaces (SandboxControl, AgentService, SandboxClient)
- Ordered teardown in DestroySession (agent first, then sandbox)
- Lossy vs non-lossy pause distinction for Direct vs Node

---

## Findings

### P1-1: Session ID namespace collision between orchestrator and remote agents

**Location:** Section 3.5 (CreateSession Flow)

**Problem:** The plan generates an orchestrator-side `sessionID` via `generateSessionID()` but then calls `agentClient.CreateSession(ctx, req)` which returns a *different* session ID assigned by the remote `AgentLoopService`. The plan returns `resp` directly to the client (step 8), meaning the client gets the **remote agent's** session ID. But the orchestrator's session registry maps `generateSessionID()` to the entry.

This creates two problems:
1. The client uses the remote session ID, but `o.getSession()` looks up by the orchestrator's generated ID. All proxy calls (`SendMessage`, `Steer`, etc.) will fail with "session not found."
2. For multi-backend deployments, remote agents on different hosts could generate colliding session IDs (e.g., both generate `session-1`).

**Fix options:**
- **(A) Use orchestrator-generated IDs externally, translate internally.** The orchestrator assigns its own session ID to clients, stores the remote session ID in the entry, and translates `req.SessionID` before proxying. This is the safer option — the orchestrator owns its namespace. Requires storing `remoteSessionID string` on `sessionEntry` and rewriting session IDs in all proxy calls and responses.
- **(B) Use the remote session ID as the registry key.** After `CreateSession` returns, set `entry.sessionID = resp.SessionID` and register under that ID. Simpler, but collisions between remote agents are possible (mitigated if remote agents use UUIDs).

Recommendation: Option A. It's more work but eliminates the collision risk and gives the orchestrator a clean session ID namespace for future features (persistent registry, cross-host migration).

### P1-2: Event fidelity through the double-hop proxy

**Location:** Section 3.6 (Stream Proxying)

**Problem:** For agent-direct and agent-sandbox modes, events traverse this conversion chain:

```
Remote agent -> AgentEventEnvelope (wire)
  -> AgentEvent (domain, via codec.UnwrapEventReceiver in AgentServiceClient)
    -> AgentEventEnvelope (wire, via AgentRPCServer codec in orchestrator's transport)
      -> Client
```

If the `AgentEvent <-> AgentEventEnvelope` conversion is not perfectly lossless for all event types and fields, the proxy degrades event data silently. This roundtrip was never tested because `AgentServiceClient` is normally a terminal consumer, not a proxy relay.

**Fix:** Add a test (or assertion in the plan's test section) that verifies roundtrip fidelity: for every `AgentEventType`, encode an `AgentEvent` to `AgentEventEnvelope` and back, asserting equality. If any fields are lossy, either fix the codec or add a raw-passthrough mode for the proxy.

If the codec is already verified lossless, document this in the plan.

---

### P2-1: FleetSandboxControl already exists — plan should decide on integration

**Location:** Section 3.11 (serve_orchestrator.go wiring), Section 8 (Future Work)

**Problem:** The plan says "For multiple [sandbox-hosts], use FleetSandboxControl (future, or just pick first for now)" and lists "FleetSandboxControl integration" as future work. But `FleetSandboxControl` (plan 20) is fully implemented in `internal/sandbox/control/fleet/`. It wraps multiple `NodeSandboxControl` instances with warm pool, health monitoring, and capacity-aware routing.

**Fix:** Make an explicit decision:
- If the plan intentionally defers fleet integration for simplicity, state: "FleetSandboxControl exists but is deferred for this initial implementation because [reason]."
- If it should be integrated now (it's a single-line change: `nodeControl = fleet.NewFleetSandboxControl(...)` instead of `node.NewNodeSandboxControl(...)`), add it to bead 1 or 2.

Given that the fleet controller handles health monitoring of sandbox-hosts (which the orchestrator's health.go also tries to do), integrating FleetSandboxControl would eliminate duplicate health check logic.

### P2-2: ResumeSession design is underspecified

**Location:** Section 3.6 (table row for ResumeSession), Section 3.8 (Resume)

**Problem:** The table in 3.6 says ResumeSession does "placement routing similar to CreateSession (re-provision sandbox if needed)" but doesn't explain how the orchestrator obtains the conversation log needed for resuming after a lossy pause.

For agent-direct (lossy pause — EC2 stop kills the agent process):
- Resume must re-launch the agent, create a new `AgentServiceClient`, and call `ResumeSession` with the conversation log.
- Where does the orchestrator get the conversation log? The `ResumeSessionRequest` from the CLIENT has `ConversationLog []AgentMessageRecord`. So the CLIENT must provide it. But the orchestrator doesn't save session state, so if the client doesn't have the log, the session is unrecoverable.

For agent-sandbox (non-lossy pause):
- The agent process survives inside the resumed sandbox. But the orchestrator's `AgentServiceClient` connection may have been closed during pause. The plan doesn't address reconnecting the `AgentServiceClient` after resume.

**Fix:** Clarify in section 3.8 Resume:
1. For agent-direct resume: the client provides the conversation log in `ResumeSessionRequest`. The orchestrator re-provisions the sandbox, re-launches the agent, and forwards the resume request. If the client doesn't have the log, resume fails.
2. For agent-sandbox resume: the orchestrator reconnects the `AgentServiceClient` to the same address and verifies via health check. Document what happens if the health check fails (retry? destroy?).

### P2-3: `--agent-max-sessions` enforcement not wired into CreateSession flow

**Location:** Section 3.2 (Configuration), Section 3.5 (CreateSession Flow)

**Problem:** The plan defines `--agent-max-sessions` flag for "max concurrent in-process agent sessions for tools-sandbox mode" but the CreateSession flow (section 3.5) only checks `o.validatePlacement(placement)`. There's no enforcement of the per-mode session limit.

For tools-sandbox, sessions consume orchestrator memory (in-process agent loop). For agent-direct/agent-sandbox, sessions consume remote infrastructure. These have very different scaling characteristics.

**Fix:** Add to `CreateSession` flow:
- Check `--max-sessions` (total across all modes) first
- For tools-sandbox, additionally check `--agent-max-sessions` before creating the in-process session
- Consider whether the in-process `AgentLoopService`'s own `maxSess` limit (which it already enforces) is sufficient, and if so, document that reliance.

### P2-4: CreateSession timeout for slow provisioning (agent-direct)

**Location:** Section 3.5 (CreateSession Flow)

**Problem:** `createAgentDirectSession` calls `sc.CreateSandbox()` which provisions an EC2 instance. This can take 30-120 seconds. The HTTP request context from the client may have a shorter deadline (e.g., 30s default). If the context expires mid-provision, the orchestrator has a partially-created sandbox with no session tracking (the entry isn't registered yet).

**Fix:** Address in section 3.5 or 5:
- Use a background context (derived from a shutdown context, not the request context) for provisioning, with its own timeout (e.g., `--provision-timeout`).
- Register the session entry in `sessionCreating` state BEFORE starting provisioning, so it can be cleaned up on shutdown even if the client disconnects.
- Return the session ID immediately with a `creating` state, and let the client poll via `GetSession` until it transitions to `active`. (This is a bigger design change but matches common patterns for long-running operations.)

### P2-5: Orchestrator-level `Close()` vs `AgentService.Close()`

**Location:** Section 3.10 (Shutdown), Section 3.3 (Orchestrator Struct)

**Problem:** The `AgentService` interface includes `Close() error`. The orchestrator implements `AgentService` (plugs into `transport.WithAgentService`). But the shutdown logic in section 3.10 describes behavior beyond what `AgentService.Close()` covers — stopping the health monitor, draining the HTTP server, closing the in-process AgentLoopService.

If the transport calls `orch.Close()` (as `AgentService.Close()`), it would trigger the full orchestrator shutdown. But `serve_agent.go`'s shutdown pattern calls `agentService.Close()` before `httpServer.Shutdown()`. The orchestrator's `Close()` must NOT close the HTTP server (that's done separately). But it should drain sessions and stop the health monitor.

**Fix:** Define `Close()` semantics clearly:
- `Close()` = stop accepting new sessions, drain active sessions, stop health monitor, close in-process AgentLoopService
- HTTP server shutdown is external (in `runServeOrchestrator`)
- Document that `Close()` blocks until drain completes or `shutdownTimeout` is reached

---

### P3-1: Double-conversion performance overhead in proxy path

**Location:** Section 3.6

**Problem:** As described in P1-2, events are deserialized from wire format to domain types and back. For high-throughput streaming (many events per second), this adds unnecessary allocations and CPU.

**Suggestion:** Document as a known cost. If it becomes a bottleneck, a raw-passthrough `EventReceiver` that skips domain conversion can be added later.

### P3-2: Diagram label imprecision

**Location:** Section 2.1 (Architecture Diagram)

**Problem:** The diagram shows `ALS -->|SandboxBackend RPC| SH`. The `AgentLoopService` doesn't talk to the sandbox-host directly — the tool catalog factory creates a `SandboxClient` (wrapping `api.SandboxService` RPC) which talks to the sandbox-host. The label "SandboxBackend RPC" conflates the tool backend concept with the RPC client.

**Fix:** Label as `Tool calls via SandboxClient RPC` or just `RPC`.

### P3-3: `placementFromMetadata` error handling

**Location:** Section 4

**Problem:** The plan defaults to `tools-sandbox` if `placement_mode` metadata is unset. But what if the metadata value is present but invalid (e.g., `"direct"` instead of `"agent-direct"`)? The plan should return `CodeInvalidArgument` for invalid values and only default for missing values.

### P3-4: Type assertion in DestroySession is brittle

**Location:** Section 3.7, step 5

**Problem:**
```go
if agentClient, ok := entry.agentService.(*client.AgentServiceClient); ok {
    agentClient.Close()
}
```
This type-asserts against a concrete type. If a different `AgentService` implementation needs cleanup, it won't be caught. Better: always call `entry.agentService.Close()` if it's not the shared `AgentLoopService` (which has its own lifecycle). Or add a `Closeable` check.

Actually, looking at `AgentServiceClient.Close()` it returns nil (no-op). So this is harmless either way. Still, calling Close unconditionally is cleaner.

### P3-5: Missing `Capabilities()` check before placement routing

**Location:** Section 3.5

**Problem:** `validatePlacement()` should verify that the target SandboxControl has the required capabilities. For example, agent-sandbox requires `Capabilities().LaunchProcess == true` and `Capabilities().Snapshots == true` (for the full feature set). The plan mentions checking capabilities at startup (section 2.2 step 1) but doesn't use them in placement validation.

---

## Interface Usage Verification

### SandboxControl (`internal/sandbox/control/control.go`)
- **CreateSandbox**: Used correctly — `sc.CreateSandbox(ctx, sandboxReq)` returns `*CreateSandboxResponse` with `SandboxID` and `Address`.
- **LaunchProcess**: Used correctly — `sc.LaunchProcess(ctx, LaunchProcessRequest{...})` returns `*LaunchProcessResponse` with `ProcessID` and `Address`.
- **KillProcess**: Used in DestroySession flow — `sc.KillProcess(ctx, KillProcessRequest{...})`.
- **DestroySandbox**: Used in DestroySession flow — `sc.DestroySandbox(ctx, sandboxID)`.
- **PauseSandbox/ResumeSandbox**: Used in section 3.8.
- **GetProcessStatus**: Used in health monitoring (section 3.9).
- **Capabilities**: Mentioned but not used in validation (see P3-5).

### AgentServiceClient (`internal/rpc/client/agent_client.go`)
- Used correctly as the remote proxy target for agent-direct and agent-sandbox.
- Constructor `NewAgentServiceClient(httpClient, baseURL, cfg)` requires an `httpClient` and `baseURL` — the plan needs to construct these from `LaunchProcessResponse.Address` (which is `"host:port"`). Use `control.AddressToURL(addr)` helper.
- All methods match the `AgentService` interface. Stream methods return `agentapi.EventReceiver`.

### SandboxClient (`internal/rpc/client/sandbox_client.go`)
- Not directly used by the orchestrator — used by the tool catalog factory when constructing sandbox-backed tools for tools-sandbox mode.
- The plan correctly sets `ToolEnvironmentConfig.Type = ToolEnvSandbox` with `SandboxHostAddr` and `SandboxSessionID`.
- The existing `newRuntimeToolCatalogFactory` in `cmd/flexagent` handles constructing the `SandboxClient` from these config values.

### RPC Transport Seam
- `transport.WithAgentService(orch)` correctly accepts `agentapi.AgentService`. The orchestrator implementing this interface is the right design.
- The transport wraps the service in `AgentRPCServer` which handles Connect RPC serialization.
- No seam issues identified beyond the double-conversion noted in P1-2.

---

## Test Coverage Assessment

The testing plan (section 7) covers the major paths but has gaps:

1. **Missing**: Roundtrip event fidelity test (per P1-2)
2. **Missing**: Session ID translation test (per P1-1) — need to verify that proxy calls use the correct (remote) session ID
3. **Missing**: CreateSession timeout/cancellation test — what happens when the client disconnects during provisioning (per P2-4)
4. **Missing**: Concurrent pause + SendMessage test — what happens if pause is called while a stream is active
5. **Good**: The concurrency test list (section 7.3) covers the important concurrent scenarios
6. **Good**: Mock-based unit test strategy is appropriate — mock SandboxControl + mock AgentService

---

## Disposition Summary

| # | Severity | Finding | Status |
|---|----------|---------|--------|
| 1 | P1 | Session ID namespace collision | Must fix |
| 2 | P1 | Event fidelity through proxy | Must fix (verify or add test) |
| 3 | P2 | FleetSandboxControl already exists | Should decide |
| 4 | P2 | ResumeSession underspecified | Should clarify |
| 5 | P2 | agent-max-sessions not enforced | Should wire in |
| 6 | P2 | CreateSession timeout for slow provisioning | Should address |
| 7 | P2 | Close() semantics unclear | Should define |
| 8 | P3 | Double-conversion performance | Document |
| 9 | P3 | Diagram label imprecision | Nit |
| 10 | P3 | placementFromMetadata error handling | Minor |
| 11 | P3 | Type assertion in DestroySession | Minor |
| 12 | P3 | Missing Capabilities() check | Minor |
