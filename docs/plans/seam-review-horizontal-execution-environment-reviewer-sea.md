# Seam Review: execution-environment (horizontal) — reviewer-sea

- Mode: horizontal
- Seam: ExecutionEnvironment interface (11-sandbox-host-service.add01) against all connected components
- Reviewed commit: 92feda3
- Reviewer: reviewer-sea
- Plan docs reviewed:
  - `docs/plans/11-sandbox-host-service.add01.md` (ExecutionEnvironment Abstraction)
  - `docs/plans/06-built-in-tools.md` (ToolBackend / Built-in Tools)
  - `docs/plans/11-sandbox-host-service.md` (SandboxHostService)
  - `docs/plans/13-rpc-layer.md` (RPC Layer)
  - `docs/plans/00-architecture.md` (Placement Modes)

## Seam Boundaries Analyzed

### Seam: Addendum (ExecutionEnvironment) ↔ Plan 13 (RPC Layer)

**Plan docs**: `11-sandbox-host-service.add01.md` (consumer), `13-rpc-layer.md` (provider)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | FAIL | `CreateSnapshot` method used by NativeSandboxEnvironment (add01 §5.2 line 636) does not exist on plan 13's `SandboxService` interface (§4.1). Plan 13 has `ListSnapshots` and `RollbackSession` but no `CreateSnapshot`. |
| Data formats/types | PASS | ToolRequest/ToolResponse type aliases point to `internal/tools` types matching plan 13's transport DTOs. ExecuteToolRequest field mapping (SessionID, ToolCallID, ToolName, Params, Resources) is consistent. |
| Lifecycle ordering | PASS | NativeSandboxEnvironment.Create calls CreateSession, lifecycle methods map 1:1 to plan 13's session CRUD. |
| Configuration contracts | PASS | Both use ConnectRPC client interfaces. |
| Error handling | PASS | Plan 13 defines error classes (unavailable, not_found, etc.) and the addendum wraps them with `fmt.Errorf`. Sentinel errors (ErrUnavailable, ErrNotActive) align with plan 13's error model. |

**Note on ExecuteToolStream:** NativeSandboxEnvironment calls `service.ExecuteToolStream()` (add01 §5.2 line 595). Plan 13's `SandboxService` interface (§4.1) shows `ExecuteTool` as unary, but plan 13's SandboxBackend code (§6 line 233) uses `ExecuteToolStream`. This is consistent if the proto defines a server-streaming RPC — ConnectRPC generates both unary and streaming client methods. The `SandboxService` Go interface just doesn't show the streaming variant. Ambiguous but likely fine.

---

### Seam: Addendum (ExecutionEnvironment) ↔ Plan 11 (SandboxHostService)

**Plan docs**: `11-sandbox-host-service.add01.md` (consumer, via RPC), `11-sandbox-host-service.md` (provider)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | NativeSandboxEnvironment.Create/Pause/Resume/Destroy/ExecuteTool/CreateSnapshot/Rollback map to corresponding SandboxHostService methods. |
| Data formats/types | FAIL | Addendum code uses `StateActive`, `StatePaused`, `StateDestroyed` (e.g., E2B line 738, Fly line 955). Plan 11 defines `SessionState` with different constant names: `SessionCreating`, `SessionActive`, `SessionPaused`, `SessionDestroying`, `SessionFailed`. The addendum declares `State() SessionState` but never defines the `SessionState` type or its constants. |
| Lifecycle ordering | PASS | State machine transitions match: Creating→Active→Paused→Active→Destroying. |
| Configuration contracts | PASS | SessionConfig maps cleanly to CreateSessionRequest (BaseImage→BaseSnapshot, SessionID, Labels). |
| Error handling | PASS | Plan 11 returns `ErrInvalidState` for wrong-state transitions, consistent with addendum's `ErrNotActive`. |

**Note on Quota:** Plan 11's `CreateSessionRequest` has a `Quota` field (§3.2 line 247). NativeSandboxEnvironment.Create (add01 §5.2 line 544-554) doesn't pass Quota. This could be set via `SessionConfig.Options` but no `NativeOptions` struct is defined.

**Note on TurnComplete:** Plan 11 has `TurnComplete(ctx, sessionID)` for turn-boundary snapshots. The addendum only has `CreateSnapshot(ctx, name)`. The RPC layer (plan 13) also lacks TurnComplete. This is likely intentional — `CreateSnapshot` is the universal interface and the orchestrator handles turn-boundary naming. But the addendum doesn't document this mapping.

---

### Seam: Addendum (ExecutionEnvironment) ↔ Plan 06 (ToolBackend / Built-in Tools)

**Plan docs**: `11-sandbox-host-service.add01.md` (replacement), `06-built-in-tools.md` (replaced)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | `ExecutionEnvironment.ExecuteTool(ctx, req, onProgress)` matches `ToolBackend.ExecuteTool(ctx, req, onProgress)` exactly. Type aliases (`ToolRequest = tools.ToolRequest`) ensure same types. |
| Data formats/types | PASS | ToolRequest, ToolResponse, ToolProgress are identical (type aliases). |
| Lifecycle ordering | PASS | LocalEnvironment replaces LocalBackend with same behavior. NativeSandboxEnvironment replaces SandboxBackend with same dispatch logic. |
| Configuration contracts | PASS | `NewLocalTools` / `NewSandboxTools` factories would be replaced by environment-based wiring, documented in add01 §7.4. |
| Error handling | PASS | Same error patterns. |

**Note on ToolRequest.SessionID:** Plan 06's `ToolRequest` has a `SessionID` field (§3.1 line 130). In the addendum, all ExecutionEnvironment implementations use their own internal session identity. `ToolRequest.SessionID` is never read by any environment implementation — NativeSandboxEnvironment uses `e.sessionID` (line 582), remote environments use their own IDs. This field becomes dead. Not a compatibility break, but a vestigial field.

---

### Seam: Addendum (ExecutionEnvironment) ↔ Architecture Doc (Placement Modes)

**Plan docs**: `11-sandbox-host-service.add01.md`, `00-architecture.md`

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | ExecutionEnvironment aligns with placement mode descriptions. All Local → LocalEnvironment, Agent in Sandbox → LocalEnvironment (inside) + sandbox env (lifecycle), Agent outside Sandbox → sandbox env (both). |
| Data formats/types | PASS | |
| Lifecycle ordering | PASS | |
| Configuration contracts | PASS | |
| Error handling | PASS | |

**Note on terminology:** Architecture doc still uses `ToolBackend`, `LocalBackend`, `SandboxBackend` terminology (Key Terminology §lines 67-71). The addendum's §9.4 lists the architecture doc sections that need updating. This is expected — the architecture doc will be updated when the addendum is implemented.

---

## Acceptance Criteria Cross-Reference

| Acceptance criterion (source doc) | Seams touched | Status | Details |
|-----------------------------------|---------------|--------|---------|
| AC1: Agent executes tools through E2B (add01) | add01↔E2B API | PASS | Self-contained within addendum |
| AC2: Pause/resume native sandbox session (add01) | add01↔plan13↔plan11 | PASS | NativeSandboxEnvironment.Pause maps through RPC to SandboxHostService.PauseSession |
| AC3: Capability-gated fallback (add01) | add01↔plan06 | PASS | Daytona Pause=false returns ErrCapabilityNotSupported, orchestrator falls back |
| AC4: Environment swap transparency (add01) | add01↔plan06 | PASS | ToolResponse types identical across environments |
| AC5: Post-Destroy error (add01) | add01 internal | PASS | |
| AC2 plan13: Remote tool dispatch via SandboxBackend | plan13↔plan06↔plan11 | FAIL | Plan 13's SandboxBackend is replaced by NativeSandboxEnvironment. Plan 13's acceptance criteria still reference SandboxBackend. |
| AC3 plan13: Snapshot operations over RPC | plan13↔plan11 | FAIL | Plan 13 does not define CreateSnapshot RPC, but addendum's NativeSandboxEnvironment depends on it for AC2 (snapshot after write). |

## Seam Compatibility Matrix

| Seam | Signatures | Data | Lifecycle | Config | Errors | Overall |
|------|-----------|------|-----------|--------|--------|---------|
| add01 ↔ plan 13 (RPC) | FAIL | PASS | PASS | PASS | PASS | FAIL |
| add01 ↔ plan 11 (SandboxHostService) | PASS | FAIL | PASS | PASS | PASS | FAIL |
| add01 ↔ plan 06 (ToolBackend) | PASS | PASS | PASS | PASS | PASS | PASS |
| add01 ↔ architecture | PASS | PASS | PASS | PASS | PASS | PASS |

## Findings

### P1 [IG] - CreateSnapshot RPC missing from plan 13

**Seam**: Addendum (ExecutionEnvironment) ↔ Plan 13 (RPC Layer)
**Category**: Interface signatures

**Problem**
NativeSandboxEnvironment.CreateSnapshot (add01 §5.2 lines 635-648) calls `e.service.CreateSnapshot(ctx, &api.CreateSnapshotRequest{SessionID, Name})`. But plan 13's `SandboxService` interface (§4.1 lines 111-125) does not include a `CreateSnapshot` method. Plan 13 has `ListSnapshots` and `RollbackSession` but no `CreateSnapshot`.

Plan 11's `SandboxHostService` has `CreateSnapshot(ctx, sessionID, name)` (§3.1 line 228), so the server-side method exists. But the RPC contract (plan 13) doesn't expose it, meaning there's no transport path from `NativeSandboxEnvironment` to `SandboxHostService.CreateSnapshot`.

This also means the addendum's NativeSandboxEnvironment cannot implement `CreateSnapshot` — calling the method would fail at compile time because `api.SandboxService` doesn't have it.

**Required fix**
Add `CreateSnapshot(ctx, *CreateSnapshotRequest) (*CreateSnapshotResponse, error)` to plan 13's `SandboxService` interface (§4.1). Define `CreateSnapshotRequest` (SessionID, Name) and `CreateSnapshotResponse` (SnapshotID, SpaceUsed). Also consider whether `TurnComplete` should be exposed as a separate RPC or if `CreateSnapshot` subsumes it.

---

### P2 - SessionState type undefined in addendum, constant names differ from plan 11

**Seam**: Addendum (ExecutionEnvironment) ↔ Plan 11 (SandboxHostService)
**Category**: Data formats/types

**Problem**
The addendum adds `State() SessionState` to the ExecutionEnvironment interface (§3.1 line 240) and uses `SessionState` as a field type in E2B (line 694), Daytona (line 837), and Fly (line 908) structs. The code references constants `StateActive`, `StatePaused`, `StateDestroyed` (e.g., E2B line 738: `e.state = StateActive`).

But the addendum never defines the `SessionState` type or its constants. Plan 11 defines `type SessionState string` with constants using a different naming convention: `SessionCreating`, `SessionActive`, `SessionPaused`, `SessionDestroying`, `SessionFailed` (plan 11 §3.2 lines 262-270).

If the addendum defines its own `SessionState` type in `internal/sandbox/environment`, it must either (a) use the same string values as plan 11 so that NativeSandboxEnvironment can return server-side state values directly, or (b) map between the two type systems. Neither is specified.

Additionally, the addendum has `StateDestroyed` while plan 11 has `SessionDestroying` (transitional) — different semantics (terminal vs transitional).

**Required fix**
Define `SessionState` in the addendum's types.go with explicit constants. Ensure the string values match plan 11's values ("creating", "active", "paused", "destroying", "failed") even if Go constant names differ. Add `StateDestroyed` as an addendum-specific state for client-side tracking (not present in plan 11 since server-side sessions are deleted, not "destroyed"). Document the mapping.

---

### P2 - ToolRequest.SessionID becomes dead field after migration

**Seam**: Addendum (ExecutionEnvironment) ↔ Plan 06 (ToolBackend)
**Category**: Data formats/types

**Problem**
Plan 06's `ToolRequest` struct (§3.1 line 129) includes a `SessionID` field. The addendum uses `type ToolRequest = tools.ToolRequest` (add01 §3.2 line 288), preserving this field. However, no `ExecutionEnvironment` implementation reads `req.SessionID`:

- `NativeSandboxEnvironment` uses `e.sessionID` (internally stored after Create) — see add01 §5.2 line 582
- `E2BSandboxEnvironment` uses `e.sandboxID`
- `DaytonaSandboxEnvironment` uses `e.workspaceID`
- `FlySandboxEnvironment` uses `e.machineID`
- `LocalEnvironment` has no sessions

The `SessionID` field is now vestigial. Callers may still set it (plan 06's factory code does), but it's ignored. This won't cause bugs but may confuse implementors who assume it's used for routing.

**Required fix**
Document in the addendum that `ToolRequest.SessionID` is not used by ExecutionEnvironment implementations — the session identity is internal to each environment, set during `Create()`. Alternatively, if the field should be removed, add it to the migration plan (§7.3) as something to clean up after the ToolBackend removal.

---

### P3 - Architecture doc terminology drift

**Seam**: Addendum ↔ Architecture doc
**Category**: Data formats/types

**Problem**
The architecture doc's Key Terminology section (lines 67-71) still defines `ToolBackend`, `LocalBackend`, and `SandboxBackend`. The addendum replaces all three. The addendum's §9.4 lists the specific architecture doc sections that need updating, which is good. But until updated, new readers will see conflicting terminology between the architecture doc and the addendum.

**Required fix**
Note in the addendum's §9.4 that the updates should be applied as part of the first implementation task (Phase 4: Agent Loop Migration), not deferred. Alternatively, apply the architecture doc updates now since the addendum is approved.

---

### P3 - Quota and TurnComplete not exposed through ExecutionEnvironment

**Seam**: Addendum ↔ Plan 11 (SandboxHostService)
**Category**: Configuration contracts

**Problem**
Plan 11's `CreateSessionRequest` has a `Quota` field (§3.2 line 247) for per-session dataset quotas. The addendum's `NativeSandboxEnvironment.Create` (§5.2 line 544-554) doesn't pass Quota. There's no `NativeOptions` struct defined for environment-specific native configuration.

Similarly, plan 11 has `TurnComplete(ctx, sessionID)` for turn-boundary snapshots with auto-naming. The addendum only exposes `CreateSnapshot(ctx, name)`. The mapping between `CreateSnapshot` and `TurnComplete` is not documented.

Neither issue is blocking — Quota defaults to the server config and CreateSnapshot with a turn-named snapshot achieves the same result as TurnComplete. But both represent undocumented gaps in the native environment configuration surface.

**Required fix**
Either (a) define a `NativeOptions` struct with Quota (and any other native-specific fields) passed via `SessionConfig.Options`, or (b) document that Quota is controlled server-side via `ServiceConfig.DefaultSessionQuota`. For TurnComplete, document that the orchestrator synthesizes turn-boundary behavior by calling `CreateSnapshot("turn-N")`.

---

## Summary

4 seam boundaries analyzed, 5 findings: 1 P1, 2 P2, 2 P3

Seam compatibility: 2/4 seams fully compatible

**Verdict**: Seams compatible with revisions

The critical finding is the missing `CreateSnapshot` RPC in plan 13 — this is a compile-time failure that blocks NativeSandboxEnvironment's snapshot functionality. The SessionState type inconsistency and dead SessionID field are integration-time issues that should be resolved before implementation.
