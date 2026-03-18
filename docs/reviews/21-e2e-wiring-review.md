# E2E Wiring Review: Serve Orchestrator (Actual Code)

## Scope

Traced end-to-end wiring in real code for:

1. CLI startup wiring in `cmd/flexagent/serve_orchestrator.go`
2. `agent-direct` session flow
3. `agent-sandbox` session flow
4. `tools-sandbox` session flow
5. stream proxy path (`SendMessage`/`Continue`/`SubscribeEvents`)
6. `DestroySession` teardown ordering
7. health-loop startup/check/state transitions

## Findings

### P1-1: Fleet startup path is still dead wiring in `serve_orchestrator`

- Location:
  - `cmd/flexagent/serve_orchestrator.go:121-123`
  - `cmd/flexagent/serve_orchestrator.go:333-335`
  - `cmd/flexagent/serve_orchestrator.go:309-313`

- Problem:
  - The CLI/config path explicitly rejects multiple `--sandbox-host-addr` values (`"fleet mode ... is not yet supported"`).
  - `buildNodeOrFleetControl` still returns an error for `len(addrs) > 1` instead of constructing a fleet control.
  - So the advertised Node/Fleet constructor path is only wired for single-host Node mode.

- Impact:
  - Multi-host/fleet orchestrator startup is not available through `serve orchestrator`.
  - `agent-sandbox` and `tools-sandbox` can only run against one configured sandbox-host in this command path.

## Verified Wiring (No breakages found)

- Startup chain is valid:
  - `runServeOrchestrator` builds direct control (`buildDirectControl`) and node control (`buildNodeOrFleetControl`), creates in-process `AgentLoopService`, constructs `orchestrator.New(...)`, and wires transport via `transport.WithAgentService(orch)` in `cmd/flexagent/serve_orchestrator.go:147-228`.
  - `*Orchestrator` satisfies `agentapi.AgentService` (`internal/orchestrator/orchestrator.go:36`).
  - `WithAgentService` wraps it with `rpcserver.NewAgentRPCServer` (`internal/rpc/transport/server.go:47-55`), and agent handlers dispatch correctly (`internal/rpc/transport/server.go:271-401`).

- `agent-direct` and `agent-sandbox` chains are intact:
  - `CreateSession` placement routing (`internal/orchestrator/proxy.go:17-58`) calls `createRemoteAgentSession` (`:71-143`) for both modes (`:60-66`).
  - Remote flow wires `CreateSandbox -> LaunchProcess -> AgentServiceFactory -> agentService.CreateSession`, persists session mapping, and rewrites outward session IDs.

- `tools-sandbox` chain is intact:
  - `createToolsSandboxSession` (`internal/orchestrator/proxy.go:145-199`) does `CreateSandbox`, resolves host (`resolveToolsSandboxHostAddr`, `:205-214`), extracts host-local session ID (`extractHostLocalSessionID`, `:219-228`), sets `ToolEnvironment`, and calls `AgentLoopService.CreateSession`.
  - Compensation cleanup on create failure is present (`:166-169`, `:187-194`).

- Stream proxying is intact:
  - `SendMessage`/`Continue`/`SubscribeEvents` proxy to backend and return `sessionIDRewritingReceiver` (`internal/orchestrator/proxy.go:392-504`, `:638-665`) so client sees orchestrator session IDs.

- Destroy ordering is intact:
  - `destroySessionEntry` performs teardown in order:
    1. `agentService.DestroySession`
    2. `sandboxControl.KillProcess`
    3. `sandboxControl.DestroySandbox`
    4. `agentService.Close`
  - See `internal/orchestrator/proxy.go:571-602`.

- Health loop wiring is intact:
  - Loop starts in `New` when interval > 0 (`internal/orchestrator/orchestrator.go:55-63`).
  - `runHealthLoop -> checkAllSessions -> checkSessionHealth` flow is wired (`internal/orchestrator/health.go:22-95`).
  - State transitions to `sessionUnhealthy`/`sessionActive` are guarded by `entry.mu` and consistent with proxy-side unhealthy gating (`internal/orchestrator/proxy.go:235-251`).
