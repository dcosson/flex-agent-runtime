# Review: 21-serve-orchestrator (r2-review-2)

- Source doc: `docs/plans/21-serve-orchestrator.md`
- Reviewed commit: 6cf6b6e
- Reviewer: coder-1-sea

## Findings

### P1 - tools-sandbox Fleet Routing Still Uses the Wrong Session ID Shape

**Problem**
The updated plan now enables Fleet for multi-host (`docs/plans/21-serve-orchestrator.md:575-581`) and in tools-sandbox it sets `ToolEnvironment.SandboxSessionID = sandbox.SandboxID` (`docs/plans/21-serve-orchestrator.md:409-415`). In Fleet mode, `sandbox.SandboxID` is fleet-scoped (encoded for control-plane delegation), while tool execution uses direct sandbox-host RPC by host address. The plan does not define conversion to host-local session ID, so tools-sandbox can fail against fleet-backed hosts.

**Required fix**
Define dual IDs explicitly in the plan:
1. control-plane sandbox ID (used for `PauseSandbox/DestroySandbox` via Fleet control)
2. host-local sandbox session ID (used in `ToolEnvironment.SandboxSessionID` for direct sandbox-host RPC)

Require decoding/translation when Fleet is selected, and add tests covering multi-host tools-sandbox create/send/destroy.

---

### P1 - Client Session-ID Ownership Is Not Enforced on CreateSession Response

**Problem**
The plan says orchestrator owns client-visible IDs (`docs/plans/21-serve-orchestrator.md:339`, `docs/plans/21-serve-orchestrator.md:431-435`), but both creation flows still return backend response directly (`docs/plans/21-serve-orchestrator.md:393-401`, `docs/plans/21-serve-orchestrator.md:417-426`). If backend `CreateSession` returns a different ID, the client receives backend ID while registry key remains orchestrator ID.

**Required fix**
Specify explicit response rewrite on create: returned `CreateAgentSessionResponse.SessionID` must always be `entry.sessionID` (client ID). Keep backend ID only in `entry.remoteSessionID`, and define behavior when backend returns an unexpected ID.

---

### P2 - CreateSession Background Context Is Not Tied to Orchestrator Shutdown

**Problem**
CreateSession provisioning uses `context.WithTimeout(context.Background(), o.createTimeout)` (`docs/plans/21-serve-orchestrator.md:345-346`). This ignores orchestrator shutdown cancellation despite `Close()` semantics that stop/drain operations (`docs/plans/21-serve-orchestrator.md:686-689`). Long-running provisioning can continue during shutdown unnecessarily.

**Required fix**
Derive create context from an orchestrator lifecycle context (e.g., shutdown-aware parent) rather than bare `context.Background()`, while still decoupling from client cancellation.

---

## Summary

3 findings: 0 P0, 2 P1, 1 P2, 0 P3

**Verdict**: Not approved
