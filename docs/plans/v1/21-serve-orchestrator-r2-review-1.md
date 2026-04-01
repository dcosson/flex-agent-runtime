# Plan 21 R2 Convergence Review: Serve Orchestrator Command

**Reviewer:** coder-2-sea
**Review round:** R2 (convergence)
**Base commit:** 6cf6b6e
**Date:** 2026-03-18
**Verdict:** Approve

---

## R1 Finding Disposition

### From coder-2-sea R1:

| # | R1 Finding | Severity | Disposition |
|---|-----------|----------|-------------|
| P1-1 | Session ID namespace collision | P1 | **Addressed.** Section 3.3 maps `clientSessionID -> entry`, section 3.4 adds `remoteSessionID` field, section 3.5 generates orchestrator-owned IDs and sets `createReq.SessionConfig.SessionID = clientSessionID`, section 3.6 describes full ID rewriting on proxy calls. Bead 1 includes "Session-ID translation helpers and tests." Tests include "Client-facing session IDs remain stable when backend IDs differ." |
| P1-2 | Event fidelity through proxy | P1 | **Addressed.** Section 3.6 adds "Stream Proxying and Event Fidelity" heading with `newSessionIDRewritingReceiver` that rewrites only SessionID. Bead 3 adds "Event fidelity verification tests." Test list includes "Stream proxy rewrites only `SessionID` and preserves all non-ID fields after codec roundtrip." |
| P2-1 | FleetSandboxControl integration | P2 | **Addressed.** New section 2.3 makes explicit decision: Fleet is in scope, 1 host -> Node, N hosts -> Fleet. Section 3.11 uses `buildNodeOrFleetControl`. Bead 2 includes "Explicit Node/Fleet selection." |
| P2-2 | ResumeSession underspecified | P2 | **Addressed.** Section 3.8 Resume step 3 clarifies: "Conversation-log source is always caller-provided (ResumeSessionRequest.ConversationLog); orchestrator only rewrites IDs and forwards." Step 2 covers agent-direct reconnection. Test list includes ResumeSession with ID translation. |
| P2-3 | agent-max-sessions enforcement | P2 | **Addressed.** The plan relies on AgentLoopService's built-in `maxSess` limit (set via `agent.WithMaxSessions(cfg.AgentMaxSessions)` in section 3.11). Test list includes "`agent-max-sessions` applies only to tools-sandbox." Enforcement is implicit via the backend, which is acceptable. |
| P2-4 | CreateSession provisioning timeout | P2 | **Addressed.** New `--create-session-timeout` flag (default 3m) in section 3.2. Section 3.5 uses `context.WithTimeout(context.Background(), o.createTimeout)`. New section 5.2a covers client deadline/cancel. Tests include client-canceled CreateSession with bounded cleanup. |
| P2-5 | Close() semantics | P2 | **Addressed.** New section 5.6 defines Close() as idempotent: sets closing, rejects new sessions, stops health loop, waits in-flight, runs best-effort teardown. Subsequent calls return nil. Test list includes "Close() is idempotent and waits for in-flight operations." |
| P3-1 | Double-conversion performance | P3 | **Implicitly addressed** by the event fidelity test. Performance overhead acknowledged as a known cost of the proxy pattern. |
| P3-2 | Diagram label imprecision | P3 | **Not addressed.** Diagram still shows `SandboxBackend RPC`. Minor — won't block implementation. |
| P3-3 | placementFromMetadata error handling | P3 | **Not explicitly addressed** but the test "CreateSession with unsupported placement returns error" covers invalid values. Acceptable. |
| P3-4 | Type assertion in DestroySession | P3 | **Addressed.** Section 3.7 step 2d now says "close AgentServiceClient when applicable" without the concrete type assertion. |
| P3-5 | Capabilities() check | P3 | **Not addressed.** Startup capabilities check (section 2.2) still mentioned but not wired into placement validation. Minor. |

### From coder-1-sea R1:

| # | R1 Finding | Severity | Disposition |
|---|-----------|----------|-------------|
| P1 | Session ID contract inconsistent | P1 | **Addressed.** Same fix as my P1-1 above. Orchestrator owns client IDs, maintains remoteSessionID mapping. |
| P1 | Placement metadata wrong API shape | P1 | **Addressed.** Section 3.5 now reads `req.SessionConfig.Metadata` (not `req.Metadata`). Section 4 updated similarly. Test explicitly verifies `SessionConfig.Metadata` path. |
| P1 | tools-sandbox host routing underspecified | P1 | **Addressed.** sessionEntry now has `sandboxHostAddr` and `sandboxHostID` fields. New `selectToolsSandboxHost()` helper. Section 2.3 makes Fleet scope explicit. See P2-NEW-1 below for a remaining concern. |
| P1 | DestroySession not failure-tolerant | P1 | **Addressed.** Section 3.7 completely rewritten as best-effort with per-step bounded timeouts, ignore not-found, aggregate errors, unregister unconditionally. Test covers partial failure aggregation. |
| P2 | Test plan misses cancellation/partial-failure | P2 | **Addressed.** Test list adds: CreateSession compensation cleanup at each step, client-canceled stream goroutine leak check, client-canceled CreateSession with bounded cleanup. |

---

## New Findings in R2

### P2-NEW-1: `selectToolsSandboxHost()` pattern doesn't compose with FleetSandboxControl

**Location:** Section 3.5, tools-sandbox flow step 1

**Problem:** The tools-sandbox flow calls `selectToolsSandboxHost()` which returns `{hostID, hostAddr, sandboxControl}` BEFORE calling `CreateSandbox`. When `sandboxControl` is `FleetSandboxControl`, the Fleet internally selects which instance to place the sandbox on during `CreateSandbox()`. So `hostAddr` from step 1 is meaningless — the actual host isn't known until step 2 returns `CreateSandboxResponse.Address`.

For single-node (`NodeSandboxControl`), this works because the address is known upfront. For Fleet, the flow should be:
```
1. sandbox := o.nodeControl.CreateSandbox(ctx, sandboxReq)
2. hostAddr := sandbox.Address  // Fleet-selected host
3. hostID := derived from sandbox.SandboxID or fleet metadata
```

**Fix:** Reorder the tools-sandbox flow to derive `hostAddr` from `CreateSandboxResponse.Address` after CreateSandbox returns, rather than from pre-selection. `selectToolsSandboxHost()` should only determine which SandboxControl to use (Node vs Fleet), not the target host address. The host identity fields on sessionEntry should be populated from the CreateSandbox response.

This also applies (to a lesser degree) to agent-sandbox mode, but there the `LaunchProcessResponse.Address` provides the agent's address separately.

### P2-NEW-2: tools-sandbox compensation cleanup not specified

**Location:** Section 3.5, tools-sandbox flow

**Problem:** The agent-direct/agent-sandbox shared flow states "On failure at any step, previously created resources are cleaned up." The tools-sandbox flow has no equivalent statement. If step 4 (`agentLoopService.CreateSession`) fails after step 2 (`CreateSandbox` succeeds), the sandbox leaks.

This can happen when:
- The AgentLoopService is at its `maxSess` limit (returns `CodeResourceExhausted`)
- The orchestrator is closing (AgentLoopService rejects with `CodeUnavailable`)
- Any other session creation failure in the in-process loop

**Fix:** Add compensation cleanup to the tools-sandbox flow: if AgentLoopService.CreateSession fails, call `host.sandboxControl.DestroySandbox(ctx, sandbox.SandboxID)` before returning the error.

---

## Convergence Assessment

All 4 P1 findings from coder-1-sea and both P1 findings from my R1 review are properly addressed. The session ID translation design is coherent, placement metadata uses the correct API shape, DestroySession is failure-tolerant, and event fidelity will be tested. Fleet scope is explicitly decided.

The 2 new P2 findings (Fleet composition ordering, tools-sandbox compensation) are straightforward to fix in the pseudocode. Neither requires architectural changes.

**Verdict: Approve.** The plan is ready for bead decomposition. The two P2-NEW findings should be addressed inline during implementation — the implementer will naturally discover the correct ordering when wiring FleetSandboxControl, and compensation cleanup is a standard pattern.
