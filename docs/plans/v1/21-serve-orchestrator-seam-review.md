# Seam Review: orchestrator-integration (horizontal) — coder-1-sea

- Mode: horizontal
- Seam: orchestrator integration boundaries
- Reviewed commit: a217dda
- Reviewer: coder-1-sea
- Plan docs reviewed:
  - `docs/plans/21-serve-orchestrator.md`
- Source contracts checked:
  - `internal/agent/api/agent.go`
  - `internal/sandbox/control/control.go`
  - `internal/sandbox/control/node/node.go`
  - `internal/sandbox/control/fleet/sandbox_id.go`
  - `internal/rpc/client/agent_client.go`
  - `internal/rpc/transport/server.go`
  - `cmd/flexagent/tool_catalog_factory.go`
  - `cmd/flexagent/serve_agent.go`
  - `cmd/flexagent/serve_all.go`

## Seam Boundaries Analyzed

### Seam: Orchestrator plan ↔ AgentService contract

**Plan docs**: `docs/plans/21-serve-orchestrator.md` (consumer/implementor), `internal/agent/api/agent.go` (provider contract)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Plan covers the 11 RPC-facing AgentService methods in the proxy table and addresses `Close()` semantics in shutdown section. |
| Data formats/types | PASS | Request/response shapes used in plan match `agentapi` types (Create/Get/List/Send/Continue/Steer/FollowUp/Abort/Subscribe/Resume/Destroy). |
| Lifecycle ordering | PASS | Close/drain semantics are explicitly defined and compatible with service lifecycle expectations. |
| Configuration contracts | PASS | No signature-level config mismatch for AgentService seam. |
| Error handling | PASS | Plan keeps RPC-code aware error semantics (`CodeUnavailable`, `CodeInternal`) and method-level propagation through transport. |

---

### Seam: Orchestrator plan ↔ SandboxControl contract

**Plan docs**: `docs/plans/21-serve-orchestrator.md` (consumer), `internal/sandbox/control/control.go` + implementations (providers)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Plan uses the correct SandboxControl method set and request/response types. |
| Data formats/types | FAIL | Plan assumes `CreateSandboxResponse.Address` is routable host:port for tools-sandbox dispatch, but Node implementation currently returns ZFS mountpoint path. |
| Lifecycle ordering | PASS | Create/launch/kill/destroy and pause/resume ordering is compatible with interface semantics. |
| Configuration contracts | PASS | Mode-to-backend config routing is coherent (Direct vs Node/Fleet). |
| Error handling | PASS | Best-effort teardown and partial-failure handling align with provider behavior. |

---

### Seam: Orchestrator plan ↔ AgentServiceClient

**Plan docs**: `docs/plans/21-serve-orchestrator.md` (consumer), `internal/rpc/client/agent_client.go` (provider)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Constructor and method usage align with client contract. |
| Data formats/types | PASS | Session ID translation model and stream receiver pass-through fit client API types. |
| Lifecycle ordering | PASS | Client creation after process launch and close during teardown are aligned. |
| Configuration contracts | PASS | `AddressToURL(process.Address)` usage matches client baseURL expectations. |
| Error handling | PASS | Client-side connect->rpc error mapping is compatible with plan’s proxy/error model. |

---

### Seam: Orchestrator plan ↔ RPC transport server

**Plan docs**: `docs/plans/21-serve-orchestrator.md` (consumer), `internal/rpc/transport/server.go` (provider)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | `transport.WithAgentService(orch)` is the correct integration seam. |
| Data formats/types | PASS | Streaming methods return `EventReceiver`, matching transport server-stream adapter requirements. |
| Lifecycle ordering | PASS | Plan’s server wiring/shutdown ordering matches existing serve command pattern. |
| Configuration contracts | PASS | APIVersion/Auth/MaxMessage config shape matches transport server config. |
| Error handling | PASS | Transport maps service errors through `toConnectError`, consistent with plan assumptions. |

---

### Seam: Orchestrator plan ↔ Tool catalog factory (tools-sandbox)

**Plan docs**: `docs/plans/21-serve-orchestrator.md` (producer of ToolEnvironment), `cmd/flexagent/tool_catalog_factory.go` (consumer)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Plan sets `ToolEnvironment.Type/SandboxHostAddr/SandboxSessionID`, matching required fields. |
| Data formats/types | FAIL | `SandboxHostAddr` must be host:port (or URL) for RPC client creation; Node `CreateSandbox.Address` currently provides mountpoint. |
| Lifecycle ordering | PASS | AgentLoopService + tool catalog initialization ordering matches serve-all pattern. |
| Configuration contracts | PASS | Tool catalog factory usage is consistent with existing construction flow. |
| Error handling | PASS | Plan includes compensation cleanup when in-process CreateSession fails. |

---

### Seam: Orchestrator plan ↔ CLI/config conventions

**Plan docs**: `docs/plans/21-serve-orchestrator.md` (new command), existing serve command configs (agent/all)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Flag set and env mapping follow existing command patterns. |
| Data formats/types | PASS | Duration/int/string env parsing conventions are consistent. |
| Lifecycle ordering | PASS | Config validation -> service wiring -> transport wiring follows established pattern. |
| Configuration contracts | PASS | Orchestrator-specific env vars use `ORCHESTRATOR_*`; shared transport/auth vars reuse `FLEXAGENT_*` consistently. |
| Error handling | PASS | Invalid config handling pattern matches existing serve commands. |

## Seam Compatibility Matrix

| Seam | Signatures | Data | Lifecycle | Config | Errors | Overall |
|------|-----------|------|-----------|--------|--------|---------|
| Orchestrator ↔ AgentService | PASS | PASS | PASS | PASS | PASS | PASS |
| Orchestrator ↔ SandboxControl | PASS | FAIL | PASS | PASS | PASS | FAIL |
| Orchestrator ↔ AgentServiceClient | PASS | PASS | PASS | PASS | PASS | PASS |
| Orchestrator ↔ RPC transport | PASS | PASS | PASS | PASS | PASS | PASS |
| Orchestrator ↔ Tool catalog | PASS | FAIL | PASS | PASS | PASS | FAIL |
| Orchestrator ↔ Config/env | PASS | PASS | PASS | PASS | PASS | PASS |

## Findings

### P1 - Node CreateSandbox Address Semantics Break tools-sandbox Wiring

**Seam**: Orchestrator ↔ SandboxControl / Tool catalog
**Category**: Data formats/types

**Problem**
The plan now uses `hostAddr := sandbox.Address` to populate `ToolEnvironment.SandboxHostAddr` for tools-sandbox (`docs/plans/21-serve-orchestrator.md:437-449`). `tool_catalog_factory` requires this to be a routable host:port/URL (`cmd/flexagent/tool_catalog_factory.go:43-56`).

But current Node implementation returns `CreateSandboxResponse.Address = resp.Session.Mountpoint` (`internal/sandbox/control/node/node.go:72-75`), while the SandboxControl contract documents `Address` as reachability host:port (`internal/sandbox/control/control.go:56-59`).

Result: single-host Node mode can pass a filesystem path into sandbox RPC wiring and fail tool dispatch.

**Required fix**
Choose one and make both sides consistent:
1. Fix `NodeSandboxControl.CreateSandbox` to return routable host:port in `Address` (preferred, aligns with contract and Fleet behavior), or
2. In orchestrator plan/implementation, branch tools-sandbox host resolution: use configured sandbox-host address for Node mode and `CreateSandboxResponse.Address` only for Fleet mode.

Also add a seam regression test for single-node tools-sandbox create/send flow that verifies `SandboxHostAddr` is routable.

---

## Summary

6 seam boundaries analyzed, 1 finding: 0 P0, 1 P1, 0 P2, 0 P3

Seam compatibility: 4/6 seams fully compatible

**Verdict**: Seams compatible with revisions
