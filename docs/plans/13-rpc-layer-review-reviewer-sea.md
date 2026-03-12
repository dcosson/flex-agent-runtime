# 13: RPC Layer — Review Findings (reviewer-sea, R1)

**Plan:** [13-rpc-layer.md](./13-rpc-layer.md)
**Test Harness:** [13-rpc-layer-test-harness.md](./13-rpc-layer-test-harness.md)
**Reviewer:** reviewer-sea
**Round:** 1
**Date:** 2026-03-12

---

## Summary

The plan is well-scoped and clearly defines the RPC service contracts and error model. The separation between transport DTOs and domain types via the codec layer is good. However, the plan is underspecified in several areas that will cause implementation ambiguity: the AgentEventStream interface semantics, event stream lifecycle during session state changes, version negotiation, and missing ToolCallID in response paths.

## Findings

### [F1] AgentEventStream interface is bidirectional but usage is unidirectional — P1

**Section:** Plan §4.2 (Event Stream API)
**Issue:** The `AgentEventStream` interface has both `Send()` and `Recv()` methods, implying bidirectional streaming. But the sequence diagram (§3.3) shows only unidirectional flow: Agent → EventStream → Orchestrator. If the stream is unidirectional (server-streaming), the interface should use ConnectRPC's `ServerStream` pattern with only `Recv()` on the consumer side and `Send()` on the producer side. If it's truly bidirectional, the plan needs to explain what flows in the reverse direction (e.g., flow control signals, subscription filters). This ambiguity will cause the implementor to make an arbitrary choice that may not match the architecture's intent.
**Recommendation:** Clarify the streaming direction. If server-streaming (agent → orchestrator), split the interface:
- Producer side: `AgentEventSender` with `Send(*AgentEventEnvelope) error` and `Close() error`
- Consumer side: `AgentEventReceiver` with `Recv() (*AgentEventEnvelope, error)` and `Close() error`

### [F2] No specification of event stream lifecycle during session state changes — P2

**Section:** Plan §4.2, §7 (Event Stream + Error Model)
**Issue:** The plan defines event streaming and session lifecycle (create/pause/resume/destroy) as separate RPC methods but doesn't specify what happens to an active event stream when:
- Session is paused: Are events drained? Does the stream stay open?
- Session is destroyed: Does the stream receive a terminal event then close? What error code?
- Session crashes/fails: How does the consumer learn the stream is dead?
- Agent run completes: Is there a terminal "session_complete" event?
Without this specification, consumers won't know whether to interpret stream closure as "session ended normally" vs "connection lost."
**Recommendation:** Define terminal event semantics. At minimum: stream sends a `session_completed` or `session_paused` event before closing. On destroy, stream sends `session_destroyed` then closes. On crash/connection loss, consumer receives an RPC error (`unavailable` or `internal`).

### [F3] Missing ToolCallID in RPC response path — P2

**Section:** Plan §4.1 (Sandbox Service API), §6 (SandboxBackend Design)
**Issue:** The `ExecuteToolResponse` mapped from the RPC response doesn't mention `ToolCallID`. The architecture doc and plan 14 both emphasize tool_call_id traceability across agent → RPC → sandbox boundaries. Without ToolCallID in the response, the consumer must correlate by request ordering, which is fragile under retries or concurrent calls.
**Recommendation:** Include `ToolCallID` in both the RPC request and response messages. The server should echo back the caller's ToolCallID in the response to enable unambiguous correlation.

### [F4] Version negotiation mechanism is unspecified — P2

**Section:** Plan §4.3 (Message Mapping), §12 URP
**Issue:** Section 4.3 mentions "explicit versioning" for transport DTOs, and URP item 1 mentions "cross-version client/server compatibility tests." But the plan doesn't specify: what version field exists, how client and server negotiate compatible versions, or what happens when versions are incompatible. For a system where client and server may be deployed independently, this is critical.
**Recommendation:** Specify the versioning strategy. Options: (a) ConnectRPC doesn't have built-in version negotiation, so use a version interceptor that checks a `x-api-version` header. (b) Use protobuf evolution rules (additive-only field changes) and avoid breaking changes. (c) Define a `Handshake` RPC method that negotiates capabilities. Whichever is chosen, document it.

### [F5] SandboxBackend lifecycle management unspecified — P2

**Section:** Plan §6 (SandboxBackend Design)
**Issue:** `SandboxBackend` is bound to a single `sessionID` at construction. The plan doesn't specify: who creates SandboxBackend instances, when they're created relative to `CreateSession`, or how they're cleaned up when the session is destroyed. If the agent loop creates one and holds it, what happens when the session is paused and resumed — is the same SandboxBackend reused? What if the underlying RPC connection was lost during pause?
**Recommendation:** Document the lifecycle: SandboxBackend is created after CreateSession succeeds (receiving sessionID), passed to the agent loop, and discarded when the session is destroyed. On resume, a new SandboxBackend is created. Specify whether SandboxBackend holds a persistent connection or makes independent RPC calls per tool invocation.

### [F6] No max payload size or chunking specification — P3

**Section:** Plan §13 (Extreme Optimization)
**Issue:** The plan mentions "zero-copy marshaling paths for large tool outputs" but doesn't specify max message sizes. Large tool outputs (e.g., reading a 10MB file, or bash producing large stdout) could exceed default gRPC/ConnectRPC message size limits (typically 4MB). Without explicit limits and a chunking strategy, large payloads will fail with opaque transport errors.
**Recommendation:** Specify max single-message payload size (e.g., 16MB) and document the behavior when tool output exceeds it (truncation with metadata, or streaming chunked responses). Configure ConnectRPC max message size to match.

### [F7] Test harness P5 (Session ID Authority) needs stronger specification — P3

**Section:** Test Harness §1, P5
**Issue:** The property test states "All RPC calls and stream envelopes use runtime session ID fields consistently" but doesn't define what consistency means. Does it mean: the session_id field is present in every message? That it matches the session_id used in CreateSession? That driver-native IDs never leak into the session_id field?
**Recommendation:** Define the invariant precisely: "Every RPC request/response and event envelope contains a `session_id` field that matches the runtime session ID from CreateSession. No driver-native session IDs appear in any `session_id` field."

### [F8] Retry policy for session-mutating calls needs idempotency keys — P2

**Section:** Plan §7.2 (Retry Policy)
**Issue:** The plan says "idempotency safeguards for session-mutating calls" but doesn't specify the mechanism. Which calls are session-mutating? (CreateSession, ExecuteTool, TurnComplete, RollbackSession, DestroySession — all of them.) What makes a retry safe? An idempotency key in the request header? Server-side deduplication? Without this, retrying `ExecuteTool` could run the same bash command twice.
**Recommendation:** Specify the idempotency mechanism. For `ExecuteTool`: include a client-generated `idempotency_key` (the `tool_call_id` serves this purpose if unique). Server deduplicates within a short window. For `CreateSession`/`DestroySession`: idempotent by nature if using the same session_id. Document which operations are naturally idempotent vs which need explicit keys.

---

## Statistics

- Total findings: 8
- P0 (blocking): 0
- P1 (significant): 1
- P2 (moderate): 5
- P3 (minor): 2
