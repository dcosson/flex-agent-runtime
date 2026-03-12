# Review: 13-rpc-layer (coder-1-sea)

- Source doc: `docs/plans/13-rpc-layer.md`
- Test harness: `docs/plans/13-rpc-layer-test-harness.md`
- Reviewed commit: 38b2e30
- Reviewer: coder-1-sea
- Round: R2

## Findings

### P1 - [IG] Server-streaming interface returns producer handle instead of receiver

**Problem**
The service contract states `StreamAgentEvents` is server-streaming and that the RuntimeController is the consumer, but the method signature returns `AgentEventSender` (`docs/plans/13-rpc-layer.md:133-138`). This reverses directionality at the type boundary and contradicts the interface comments and architecture text.

**Required fix**
Change the method contract so RuntimeController-side call sites receive `AgentEventReceiver` (or language/framework-native stream receiver type) and keep `AgentEventSender` server-side only.

---

### P1 - [IG] Event taxonomy drifts from canonical agent lifecycle event names

**Problem**
Terminal stream semantics use `session_paused`, `session_destroyed`, and `session_completed` (`docs/plans/13-rpc-layer.md:159-162`), while plan 05 defines lifecycle events as `session_started` / `session_ended` (with state transitions carried separately). This mismatch creates a cross-component contract break between RPC streaming, Mode 3 E2E, and agent event consumers.

**Required fix**
Align RPC event envelopes with canonical plan-05 event types (or explicitly introduce/approve a new canonical event taxonomy and update dependent plans 14/15/16 and acceptance criteria accordingly).

---

## Summary

2 findings: 0 P0, 2 P1, 0 P2, 0 P3

**Verdict**: Not approved
