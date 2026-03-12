# Seam Review: full-stack-runtime-contracts (horizontal+vertical) — coder-1-sea

- Mode: Combined (horizontal seam checks + key vertical slices)
- Seam spec: AI/provider, agent/tools, sandbox host/RPC, and Mode 2/3 E2E seams
- Reviewed commit: a298323
- Reviewer: coder-1-sea
- Plan docs reviewed:
  - `docs/plans/01-ai-core.md`
  - `docs/plans/02-provider-anthropic.md`
  - `docs/plans/03-provider-openai.md`
  - `docs/plans/04-provider-google.md`
  - `docs/plans/05-agent.md`
  - `docs/plans/06-built-in-tools.md`
  - `docs/plans/07-code-interpreter.md`
  - `docs/plans/08-agent-tools-e2e.md`
  - `docs/plans/09-sandbox-zfs.md`
  - `docs/plans/09-h2-termmux-port.md`
  - `docs/plans/10-sandbox-gvisor.md`
  - `docs/plans/11-sandbox-host-service.md`
  - `docs/plans/13-rpc-layer.md`
  - `docs/plans/14-mode3-e2e.md`
  - `docs/plans/15-mode2-e2e.md`

## Seam Boundaries Analyzed

### Seam: 01-ai-core ↔ 02/03/04 providers

**Plan docs**: `01-ai-core.md` (canonical interface/types), `02-provider-anthropic.md`, `03-provider-openai.md`, `04-provider-google.md` (implementations)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Providers align to `Provider.Stream/StreamSimple` and `EventStream` usage patterns from plan 01. |
| Data formats/types | PASS | `StopReason` constants and `AssistantMessageEvent` usage are now aligned (including latest OpenAI/Google `StopReasonLength` mapping). |
| Lifecycle ordering | PASS | Streaming and finalization semantics are coherent with plan 01 event model. |
| Configuration contracts | PASS | Provider option mappings are provider-specific but consistent with shared option abstractions. |
| Error handling | PASS | Shared context-overflow classification helper usage is aligned in reviewed provider plans. |

---

### Seam: 01-ai-core ↔ 05-agent

**Plan docs**: `01-ai-core.md` (provider/event contracts), `05-agent.md` (consumer/orchestrator)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Agent consumes `ai.Provider` and stream events through canonical interfaces. |
| Data formats/types | PASS | Canonical lifecycle event taxonomy in plan 05 is internally consistent with downstream consumers. |
| Lifecycle ordering | PASS | `turn_completed` snapshot trigger and `state_change(idle)` informational semantics are now explicit and coherent. |
| Configuration contracts | PASS | No conflicting shared config seams identified. |
| Error handling | PASS | Provider/tool error event classes are mapped in agent taxonomy. |

---

### Seam: 02-provider-anthropic ↔ 05-agent

**Plan docs**: `02-provider-anthropic.md`, `05-agent.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Anthropic stream outputs match agent-side provider consumption assumptions. |
| Data formats/types | PASS | Tool call finalization strictness aligns with agent expectations for completed tool-call payloads. |
| Lifecycle ordering | PASS | Event ordering assumptions are compatible. |
| Configuration contracts | PASS | No cross-doc config drift detected. |
| Error handling | PASS | Terminal error semantics are compatible. |

---

### Seam: 05-agent ↔ 06-built-in-tools

**Plan docs**: `05-agent.md`, `06-built-in-tools.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | `AgentTool.Execute(..., onUpdate ...)` and tool backend mediation are aligned. |
| Data formats/types | PASS | `tool_call_id` propagation intent is consistent. |
| Lifecycle ordering | PASS | Tool events and turn boundaries are coherent. |
| Configuration contracts | PASS | No conflicting shared config seams identified. |
| Error handling | PASS | Structured tool/backend errors are consistently expected. |

---

### Seam: 05-agent ↔ 07-code-interpreter

**Plan docs**: `05-agent.md`, `07-code-interpreter.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Code interpreter remains an `AgentTool` meta-tool; no interface drift found. |
| Data formats/types | PASS | Tool-result and event assumptions remain compatible at this abstraction level. |
| Lifecycle ordering | PASS | No contradictory turn/lifecycle assumptions found. |
| Configuration contracts | PASS | Tiering config is internal to interpreter and does not break agent contracts. |
| Error handling | PASS | Interpreter error behavior fits tool-error channel expectations. |

---

### Seam: 06-built-in-tools ↔ 07-code-interpreter

**Plan docs**: `06-built-in-tools.md`, `07-code-interpreter.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Interpreter’s built-in-tool invocation assumptions are compatible with tool catalog abstraction. |
| Data formats/types | PASS | No schema mismatch identified. |
| Lifecycle ordering | PASS | No ordering conflicts identified. |
| Configuration contracts | PASS | No shared-config drift identified. |
| Error handling | PASS | Error propagation assumptions are compatible. |

---

### Seam: 05/06/07 ↔ 08-agent-tools-e2e

**Plan docs**: `05-agent.md`, `06-built-in-tools.md`, `07-code-interpreter.md`, `08-agent-tools-e2e.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | E2E plan consumes the intended high-level interfaces without contradictory assumptions. |
| Data formats/types | PASS | No type-level seam drift detected in documented scenarios. |
| Lifecycle ordering | PASS | E2E turn/lifecycle expectations are consistent with plan 05. |
| Configuration contracts | PASS | Deterministic harness assumptions are compatible. |
| Error handling | PASS | Error-path validation expectations are consistent. |

---

### Seam: 09-sandbox-zfs ↔ 11-sandbox-host-service

**Plan docs**: `09-sandbox-zfs.md`, `11-sandbox-host-service.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | `ZFSManager` methods consumed by host service match plan 09 signatures. |
| Data formats/types | PASS | Snapshot/dataset structures and rollback options are consistent. |
| Lifecycle ordering | PASS | Session clone/snapshot/rollback/destroy flow is coherent across docs. |
| Configuration contracts | PASS | Pool/dataset naming and quotas are compatible. |
| Error handling | PASS | Error classes and failure handling are compatible at seam level. |

---

### Seam: 10-sandbox-gvisor ↔ 11-sandbox-host-service

**Plan docs**: `10-sandbox-gvisor.md`, `11-sandbox-host-service.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | `GVisorManager.Run(ctx, ContainerOptions) -> (*ContainerResult, error)` usage is aligned. |
| Data formats/types | PASS | Resource and result field assumptions are compatible. |
| Lifecycle ordering | PASS | Per-call container lifecycle expectations match host routing flow. |
| Configuration contracts | PASS | RootFS/resource contract remains aligned. |
| Error handling | PASS | Timeout/OOM/error status semantics are now coherent with host expectations. |

---

### Seam: 06-built-in-tools ↔ 11-sandbox-host-service

**Plan docs**: `06-built-in-tools.md`, `11-sandbox-host-service.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Request side (`SessionID`, `ToolName`, `ToolCallID`, params/resources) aligns. |
| Data formats/types | FAIL | `ToolResponse` in plan 06 carries `SnapshotID`; host `ExecuteToolResponse` in plan 11 does not expose snapshot metadata. |
| Lifecycle ordering | PASS | Tier routing and execution flow are compatible. |
| Configuration contracts | PASS | Tier classifier and resource routing are coherent. |
| Error handling | PASS | Tier error handling is broadly aligned. |

---

### Seam: 11-sandbox-host-service ↔ 13-rpc-layer

**Plan docs**: `11-sandbox-host-service.md`, `13-rpc-layer.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | FAIL | RPC layer states `SandboxBackend` implements `ToolBackend.ExecuteTool(ToolRequest, onProgress)` behavior, but the documented method signature in plan 13 omits `onProgress`. |
| Data formats/types | FAIL | RPC plan expects `snapshot_id` propagation and response metadata handling that host `ExecuteToolResponse` does not define. |
| Lifecycle ordering | PASS | Session/event stream ordering is generally aligned after R2 fixes. |
| Configuration contracts | PASS | No high-confidence config seam break detected. |
| Error handling | FAIL | RPC idempotency table claims natural idempotence for `CreateSession/PauseSession/ResumeSession`, while host service defines state errors for those repeated operations. |

---

### Seam: 05-agent ↔ 13-rpc-layer

**Plan docs**: `05-agent.md`, `13-rpc-layer.md`, `06-built-in-tools.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | FAIL | Plan 06 is the canonical `ToolBackend` contract consumed by agent path; plan 13’s `SandboxBackend.ExecuteTool` signature does not match the updated callback-bearing interface. |
| Data formats/types | PASS | Lifecycle event names are now consistent (`session_started/session_ended`). |
| Lifecycle ordering | PASS | Event stream terminal semantics align with plan 05 taxonomy. |
| Configuration contracts | PASS | No config seam drift detected. |
| Error handling | PASS | Error class concepts align at high level. |

---

### Seam: 13-rpc-layer ↔ 14-mode3-e2e

**Plan docs**: `13-rpc-layer.md`, `14-mode3-e2e.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Event stream and lifecycle call surfaces align at scenario level. |
| Data formats/types | FAIL | Mode 3 assertions require response-level `snapshot_id` sequencing; RPC/server side contract remains under-specified/inconsistent with host response fields. |
| Lifecycle ordering | PASS | Canonical event ordering is aligned post-R2. |
| Configuration contracts | PASS | Harness mode/config expectations are coherent. |
| Error handling | PASS | Retry/failure scenarios are compatible conceptually. |

---

### Seam: 05-agent ↔ 09-h2-termmux-port

**Plan docs**: `05-agent.md`, `09-h2-termmux-port.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | `TermmuxDriverAdapter` vs `AgentDriver` layering is explicit and non-conflicting. |
| Data formats/types | PASS | Canonical `AgentEvent` ownership in plan 05 is mirrored in plan 09. |
| Lifecycle ordering | PASS | Session/event normalization flow assumptions are compatible. |
| Configuration contracts | PASS | Driver config-dir lifecycle is additive and compatible. |
| Error handling | PASS | Adapter-level error boundaries are coherent. |

---

### Seam: 09-h2-termmux-port + 11-sandbox-host-service ↔ 15-mode2-e2e

**Plan docs**: `09-h2-termmux-port.md`, `11-sandbox-host-service.md`, `15-mode2-e2e.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Mode 2 depends on termmux adapter and session lifecycle APIs consistently. |
| Data formats/types | PASS | Event normalization + runtime session identity expectations align. |
| Lifecycle ordering | PASS | Launch/monitor/recover paths are coherent across docs. |
| Configuration contracts | PASS | Managed config-dir seam is documented and consistent. |
| Error handling | PASS | Crash/recovery semantics are compatible at plan level. |

## Acceptance Criteria Cross-Reference

| Acceptance criterion (source doc) | Seams touched | Status | Details |
|-----------------------------------|---------------|--------|---------|
| 14-mode3-e2e AC3/AC4/event assertions | 05↔13, 11↔13, 13↔14 | FAIL | Response snapshot metadata seam is not consistently defined through host -> RPC -> backend mapping. |
| 13-rpc-layer remote tool dispatch + retry/idempotency criteria | 11↔13, 05↔13 | FAIL | `ToolBackend` signature drift and lifecycle idempotency mismatch create integration risk. |
| 11-sandbox-host-service AC1/AC2 lifecycle + snapshots | 09↔11, 10↔11 | PASS | Host’s ZFS/gVisor seams are internally consistent. |
| 05-agent cross-driver event model criteria | 05↔09, 05↔13 | PASS | Canonical lifecycle taxonomy now aligned across driver and RPC event seams. |

## Seam Compatibility Matrix

| Seam | Signatures | Data | Lifecycle | Config | Errors | Overall |
|------|-----------|------|-----------|--------|--------|---------|
| 01 ↔ 02/03/04 | PASS | PASS | PASS | PASS | PASS | PASS |
| 01 ↔ 05 | PASS | PASS | PASS | PASS | PASS | PASS |
| 02 ↔ 05 | PASS | PASS | PASS | PASS | PASS | PASS |
| 05 ↔ 06 | PASS | PASS | PASS | PASS | PASS | PASS |
| 05 ↔ 07 | PASS | PASS | PASS | PASS | PASS | PASS |
| 06 ↔ 07 | PASS | PASS | PASS | PASS | PASS | PASS |
| 05/06/07 ↔ 08 | PASS | PASS | PASS | PASS | PASS | PASS |
| 09-zfs ↔ 11 | PASS | PASS | PASS | PASS | PASS | PASS |
| 10-gvisor ↔ 11 | PASS | PASS | PASS | PASS | PASS | PASS |
| 06 ↔ 11 | PASS | FAIL | PASS | PASS | PASS | FAIL |
| 11 ↔ 13 | FAIL | FAIL | PASS | PASS | FAIL | FAIL |
| 05 ↔ 13 | FAIL | PASS | PASS | PASS | PASS | FAIL |
| 13 ↔ 14 | PASS | FAIL | PASS | PASS | PASS | FAIL |
| 05 ↔ 09-termmux | PASS | PASS | PASS | PASS | PASS | PASS |
| 09-termmux + 11 ↔ 15 | PASS | PASS | PASS | PASS | PASS | PASS |

## Findings

### P1 [IG] - `ToolBackend` signature drift between canonical contract and RPC `SandboxBackend`

**Seam**: 06-built-in-tools ↔ 13-rpc-layer (also impacts 05-agent ↔ 13-rpc-layer)
**Category**: Interface signatures

**Problem**
Plan 06 defines the canonical `ToolBackend` contract as:
`ExecuteTool(ctx context.Context, req ToolRequest, onProgress func(ToolProgress)) (*ToolResponse, error)`.
Plan 13’s `SandboxBackend` example still defines:
`ExecuteTool(ctx context.Context, req ToolRequest) (*ToolResponse, error)` (no progress callback).
This is a direct interface mismatch at the seam where RPC-backed execution is intended to implement the same backend contract consumed by agent tools.

**Required fix**
Update plan 13 `SandboxBackend` method signature and surrounding call flow to include `onProgress func(ToolProgress)` and specify how unary/streaming RPC events are bridged into this callback path.

---

### P1 [IG] - Snapshot metadata contract is inconsistent across tools, host service, RPC, and Mode 3 E2E

**Seam**: 06-built-in-tools ↔ 11-sandbox-host-service ↔ 13-rpc-layer ↔ 14-mode3-e2e
**Category**: Data formats/types

**Problem**
Plan 06 `ToolResponse` includes `SnapshotID`, and plan 14 Mode 3 assertions require `snapshot_id` sequencing in tool responses. Plan 13 also states snapshot metadata is propagated through RPC.
However, plan 11 `ExecuteToolResponse` does not define snapshot metadata fields, so the host-side source of truth for per-tool response snapshot propagation is missing at this seam.

**Required fix**
Choose one canonical contract and align all four docs:
1. If per-tool response `snapshot_id` is required, add it to host `ExecuteToolResponse` and document generation/propagation path through RPC and `ToolResponse`.
2. If snapshots are strictly turn-boundary (`TurnComplete`) and not tool-response-bound, remove/adjust plan 14 and plan 13 response-level snapshot assertions accordingly.

---

### P2 - RPC lifecycle idempotency table conflicts with host service state-transition behavior

**Seam**: 11-sandbox-host-service ↔ 13-rpc-layer
**Category**: Error handling

**Problem**
Plan 13 marks `CreateSession`, `PauseSession`, and `ResumeSession` as naturally idempotent no-ops on repeats.
Plan 11 pseudocode currently returns state errors for repeated pause/resume in non-target states and returns `ErrSessionExists` on duplicate create.
Without an explicitly documented adapter reconciliation layer, the seam contract is ambiguous and likely divergent under retries.

**Required fix**
Document and enforce one behavior:
1. Either host service methods become idempotent as claimed by RPC, or
2. RPC server adapters explicitly translate host errors into idempotent semantics (including exact mapping rules), and both docs call this out as the contract boundary.

---

## Summary

15 seam boundaries analyzed, 3 findings: 0 P0, 2 P1, 1 P2, 0 P3

Seam compatibility: 11/15 seams fully compatible

**Verdict**: Seams compatible with revisions
