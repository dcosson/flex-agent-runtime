# E2E Wiring Review: Serve Orchestrator (coder-2-sea)

- Plan doc: docs/plans/21-serve-orchestrator.md
- Scope: Outside-in trace of serve orchestrator wiring through actual code
- Reviewer: coder-2-sea
- Date: 2026-03-18

## Methodology

Traced the full request path from client to orchestrator through six areas:
1. Client connection (flexchat)
2. Transport layer wiring (ConnectRPC)
3. SandboxControl interface compliance
4. AgentService interface compliance
5. Config/env var wiring
6. Error and compensation paths

---

## 1. Client Connection (flexchat)

**Path:** `cmd/flexchat/main.go` → ConnectRPC client → orchestrator

**Finding: PASS** — flexchat is fully compatible with the orchestrator.

- flexchat constructs a ConnectRPC `AgentServiceClient` targeting the same `agentservicev1connect` generated interface
- Uses the same `policyInterceptor` for auth token and API version headers
- The `AgentService` interface is the same whether the backend is a direct agent or the orchestrator — the orchestrator transparently proxies all 12 methods
- Integration test `TestFlexchatIntegration` validates this path end-to-end

**No issues.**

---

## 2. Transport Layer Wiring

**Path:** HTTP request → ConnectRPC mux → `policyInterceptor` → `AgentRPCServer` → `Orchestrator`

**Finding: PASS** — request path is correctly wired.

- `transport.WithAgentService(orchestrator)` registers the ConnectRPC handler on the HTTP mux
- `policyInterceptor` validates `Authorization: Bearer <token>` and `X-API-Version` headers before the request reaches the orchestrator
- `AgentRPCServer` is a thin adapter converting ConnectRPC request/response types to the internal `agentapi.AgentService` interface
- Session ID management is entirely within the orchestrator — the transport layer does no ID manipulation
- The orchestrator generates `orch-XXXXXXXX` session IDs and translates to backend `remoteSessionID` for all proxy calls

**No issues.**

---

## 3. SandboxControl Interface Compliance

**Interface:** `internal/sandbox/control.SandboxControl` — 8 methods

**Finding: PASS with observations**

All four implementations satisfy the interface:

| Implementation | Compiles | Compile-time assertion |
|---------------|----------|----------------------|
| DirectSandboxControl | Yes | **Missing** |
| NodeSandboxControl | Yes | **Missing** |
| FleetSandboxControl | Yes | Present |
| CloudSandboxControl | Yes | **Missing** |

### F1 — Missing compile-time interface assertions (Low)

**Location:** `internal/sandbox/control/direct/direct.go`, `node/node.go`, `cloud/cloud.go`

Only `FleetSandboxControl` has `var _ control.SandboxControl = (*FleetSandboxControl)(nil)`. The other three implementations lack this guard. While the code compiles today, adding the assertion prevents future method-signature drift from going unnoticed until runtime.

### F2 — Semantic differences in Address field (Informational)

The `CreateSandboxResponse.Address` field has different semantics per implementation:
- **Direct**: EC2 instance IP with `:0` port placeholder (e.g., `10.0.1.5:0`)
- **Node**: Filesystem mountpoint path (e.g., `/var/lib/gvisor/sandbox-abc`)
- **Fleet**: Full `host:port` RPC address (e.g., `10.0.1.5:9090`)

This is by design — the orchestrator's `createRemoteAgentSession` uses the address appropriately for each placement mode. However, the differing semantics are not documented anywhere.

### F3 — Different SandboxID prefix formats (Informational)

- **Direct**: `direct-<uuid>` (prefixed by DirectSandboxControl)
- **Node**: `node-<uuid>` (prefixed by NodeSandboxControl)
- **Fleet**: `fleet-<hostindex>-<uuid>` (includes host routing info, parsed by `fleet.ParseSandboxID`)
- **Cloud**: `cloud-<uuid>`

Fleet's prefix carries routing semantics. `ParseSandboxID` is exported and used correctly.

---

## 4. AgentService Interface Compliance

**Interface:** `internal/agent/agentapi.AgentService` — 12 methods + `Close()`

**Finding: PASS** — orchestrator fully implements the interface.

- Compile-time assertion present: `var _ agentapi.AgentService = (*Orchestrator)(nil)`
- All 12 methods implemented with correct parameter and return types
- Proxy methods use `snapshotProxyTarget()` for lock-protected access to session state
- Proxy methods check for unhealthy state before forwarding
- `Close()` properly shuts down health monitor and destroys all sessions

**No issues.**

---

## 5. Config/Env Var Wiring

**Path:** CLI flags / env vars → `serveOrchestratorConfig` → `OrchestratorConfig`

**Finding: FAIL — 3 mismatches with provision scripts**

### F4 — Wrong env var name for sandbox-host address (High)

**Location:** `cmd/flexagent/serve_orchestrator.go` line ~61 vs `scripts/ec2-sandbox/provision.sh` line ~243

The orchestrator code expects `ORCHESTRATOR_SANDBOX_HOST_ADDR` but provision.sh sets `SANDBOX_HOST_ADDR`:
```
# provision.sh
SANDBOX_HOST_ADDR=${SANDBOX_HOST_ADDR}

# serve_orchestrator.go
flag.StringVar(&cfg.SandboxHostAddr, "sandbox-host-addr", "", "...")
// env: ORCHESTRATOR_SANDBOX_HOST_ADDR
```

This means the orchestrator will not see the sandbox host address when deployed via provision.sh.

### F5 — Wrong service command in provision.sh (High)

**Location:** `scripts/ec2-sandbox/provision.sh` line ~256

The orchestrator systemd unit's ExecStart is set to:
```
ExecStart=/usr/local/bin/flexagent serve agent
```

This should be `serve orchestrator`. The orchestrator will start as a standalone agent instead.

### F6 — Unused FLEXAGENT_ADVERTISE_ADDR (Low)

**Location:** `scripts/ec2-sandbox/provision.sh` line ~241

provision.sh sets `FLEXAGENT_ADVERTISE_ADDR=${private_ip}` but no code in serve_orchestrator.go reads this env var. Either the code should consume it (for orchestrator discovery by clients) or the provision script should not set it.

### F7 — Missing auth token in provision.sh (Low)

provision.sh does not set `FLEXAGENT_AUTH_TOKEN` for the orchestrator. This means deployed orchestrators accept unauthenticated requests. May be intentional for internal VPC deployments but should be documented.

---

## 6. Error and Compensation Paths

**Finding: PASS** — all cleanup paths are correct.

| Scenario | Compensation | Status |
|----------|-------------|--------|
| CreateSandbox OK, LaunchProcess fails | DestroySandbox called | Correct |
| LaunchProcess OK, AgentServiceFactory fails | KillProcess + DestroySandbox | Correct |
| Resume: LaunchProcess OK, factory fails | KillProcess on orphaned process | Correct |
| destroySessionEntry | DestroySession → KillProcess → DestroySandbox → Close() | Correct order with per-step timeouts |
| Orchestrator.Close() | Cancel health loop → wait → destroy all sessions → close backends | Correct |

All failure paths prevent resource leaks. Compensation handlers are invoked in the correct order.

**No issues.**

---

## Summary

| Area | Status | Findings |
|------|--------|----------|
| 1. Client connection | PASS | None |
| 2. Transport wiring | PASS | None |
| 3. SandboxControl interface | PASS | F1 (missing assertions), F2-F3 (informational) |
| 4. AgentService interface | PASS | None |
| 5. Config/env var wiring | **FAIL** | F4-F5 (high), F6-F7 (low) |
| 6. Error/compensation paths | PASS | None |

### High Priority
- **F4**: Fix env var name mismatch (`SANDBOX_HOST_ADDR` → `ORCHESTRATOR_SANDBOX_HOST_ADDR`) in provision.sh
- **F5**: Fix service command (`serve agent` → `serve orchestrator`) in provision.sh

### Low Priority
- **F1**: Add compile-time interface assertions to Direct, Node, Cloud SandboxControl implementations
- **F6**: Remove or wire up `FLEXAGENT_ADVERTISE_ADDR`
- **F7**: Document auth token expectations for deployed orchestrators
