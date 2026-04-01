# Review: 21-serve-orchestrator (r1-review-1)

- Source doc: `docs/plans/21-serve-orchestrator.md`
- Reviewed commit: db4f653
- Reviewer: coder-1-sea

## Findings

### P1 - Session ID Contract Is Internally Inconsistent

**Problem**
The plan creates an orchestrator-owned `sessionID` (`docs/plans/21-serve-orchestrator.md:333-339`) but then delegates `CreateSession` to backend agent services and returns their response as-is (`docs/plans/21-serve-orchestrator.md:368-375`). Subsequent proxy methods route by client-provided `SessionID` (`docs/plans/21-serve-orchestrator.md:402-429`). If the backend-generated session ID differs from the pre-created registry key, lookups and teardown will fail or leak resources.

**Required fix**
Specify one authoritative session ID strategy: either (a) force all backends to use orchestrator-generated IDs by setting `req.SessionConfig.SessionID` before delegation, or (b) maintain explicit clientID<->backendID mapping and rewrite all proxied requests/responses accordingly. Add failure-path cleanup requirements for "backend session created but registry insert fails".

---

### P1 - Placement Metadata Field Uses the Wrong API Shape

**Problem**
The doc references `req.Metadata` repeatedly (`docs/plans/21-serve-orchestrator.md:322-327`, `docs/plans/21-serve-orchestrator.md:600-609`), but the current API puts metadata under `SessionConfig.Metadata` (embedded in `CreateAgentSessionRequest`). As written, this design does not match existing types and will not compile.

**Required fix**
Update all plan snippets and task text to read/write placement from `req.SessionConfig.Metadata` (or from a newly added first-class placement field). Update test cases and helper names accordingly.

---

### P1 - tools-sandbox Host Routing Is Underspecified and Breaks in Multi-Host Configurations

**Problem**
The tools-sandbox flow sets `SandboxHostAddr: sandboxHostAddr` but never defines where `sandboxHostAddr` comes from (`docs/plans/21-serve-orchestrator.md:383-390`). The command wiring currently picks the first configured host (`docs/plans/21-serve-orchestrator.md:537-541`) while the doc also says fleet integration is out-of-scope (`docs/plans/21-serve-orchestrator.md:731-732`) despite depending on fleet-management (`docs/plans/21-serve-orchestrator.md:4`). Health logic also assumes per-host session mapping (`docs/plans/21-serve-orchestrator.md:481-483`) but `sessionEntry` has no host identity field (`docs/plans/21-serve-orchestrator.md:289-307`).

**Required fix**
Define and persist a per-session host identity in `sessionEntry` (selected node address or fleet instance ID+address), and require that same value to populate `ToolEnvironment.SandboxHostAddr` and host-level health bookkeeping. Clarify fleet scope unambiguously: either implement fleet routing in this plan or remove the dependency and all fleet claims from this doc.

---

### P1 - DestroySession Teardown Order Is Not Failure-Tolerant

**Problem**
Destroy flow is linear (`docs/plans/21-serve-orchestrator.md:438-449`) but does not define behavior when `entry.agentService.DestroySession` fails or times out. This conflicts with the failure model where agents/sandbox-hosts can be unhealthy (`docs/plans/21-serve-orchestrator.md:625-635`): a failure at step 2 can prevent process kill and sandbox destruction, leaking compute/storage.

**Required fix**
Specify idempotent best-effort teardown semantics: attempt agent destroy, process kill, sandbox destroy, client close, and unregister independently with bounded per-step timeouts; aggregate errors; treat not-found cases as successful cleanup. Add explicit retry/compensation behavior for partial failures.

---

### P2 - Test Plan Misses Critical Cancellation and Partial-Failure Cases

**Problem**
The current test list (`docs/plans/21-serve-orchestrator.md:695-724`) covers happy-path delegation and some concurrency, but it omits key failure matrices implied by the design: partial CreateSession failures after side effects, stream cancellation across the double-RPC proxy hop, and cleanup guarantees under mid-stream DestroySession.

**Required fix**
Add required tests for:
1. CreateSession rollback at each step (sandbox created, process launched, remote session created, registry insertion).
2. Client-side stream cancellation proving upstream receiver close/abort behavior.
3. DestroySession racing with active streams without goroutine/resource leaks.

---

## Summary

5 findings: 0 P0, 4 P1, 1 P2, 0 P3

**Verdict**: Not approved
