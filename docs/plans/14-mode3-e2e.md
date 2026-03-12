# 14: Mode 3 End-to-End — Remote Tool Dispatch via RPC

**Status:** Draft
**Depends on:** 13-rpc-layer, 08-agent-tools-e2e
**Depended on by:** 16-runtime-test-harness
**Implements:** Full Mode-3 E2E suite validating local agent loop + remote sandbox-host tool execution over ConnectRPC, including session lifecycle and snapshot operations.

---

## 1. Overview

This plan defines end-to-end tests for Mode 3 deployment:
- Agent loop runs locally on workflow/orchestrator compute.
- Tool calls dispatch through `SandboxBackend` over ConnectRPC.
- Sandbox host executes tools and returns results with snapshot metadata.

Primary goals:
- Validate remote-dispatch equivalence with local E2E behavior.
- Validate full sandbox session lifecycle via RPC (`create -> execute -> snapshot -> rollback -> pause/resume -> destroy`).
- Validate snapshot-boundary correctness and rollback semantics across multi-turn workflows.

Non-goals:
- Mode 2 third-party driver E2E (plan 15).
- Cross-host load/soak at large scale (plan 16).

---

## 2. Architecture

### 2.1 Mode 3 Placement Under Test

```mermaid
graph LR
    subgraph "Workflow Host"
        AG[Agent Loop\nNativeDriver]
        SB[SandboxBackend\nToolBackend adapter]
        RPCC[RPC Client\nConnectRPC]
    end

    subgraph "Sandbox Host (remote)"
        RPCS[RPC Server]
        SH[Sandbox Host Service]
        ZFS[Session datasets + snapshots]
        EX[Tool executors\nTier1/Tier2]
    end

    AG --> SB --> RPCC --> RPCS --> SH
    SH --> EX
    SH --> ZFS
```

### 2.2 Lifecycle Sequence (Canonical Scenario)

```mermaid
sequenceDiagram
    participant Test as E2E runner
    participant Client as RPC client
    participant Host as Sandbox host
    participant Agent as Agent loop

    Test->>Client: CreateSession(base snapshot)
    Client->>Host: create session
    Host-->>Client: session_id

    Test->>Agent: run prompt with SandboxBackend(session_id)
    Agent->>Client: ExecuteTool(...)
    Client->>Host: ExecuteTool
    Host-->>Client: ToolResponse + snapshot_id

    Test->>Client: ListSnapshots(session_id)
    Client->>Host: list snapshots
    Host-->>Client: [snap-001, snap-002, ...]

    Test->>Client: RollbackSession(session_id, snap-001)
    Client->>Host: rollback
    Host-->>Client: ok

    Test->>Client: PauseSession / ResumeSession
    Client->>Host: pause / resume

    Test->>Client: DestroySession
    Client->>Host: destroy
```

### 2.3 Test Matrix

| Axis | Values |
|------|--------|
| Backend placement | Local (baseline from plan 08), Mode 3 remote sandbox |
| Tool mix | Tier 1 only, Tier 1 + Tier 2 (`bash`) |
| Control ops | normal run, rollback path, pause/resume path |
| Provider mode | deterministic fake provider, optional live provider smoke |

---

## 3. Test Suite Structure

```text
e2etests/
├── mode3/
│   ├── harness/
│   │   ├── remote_env.go          # boots RPC client + remote host fixture
│   │   ├── lifecycle.go           # session create/pause/resume/destroy helpers
│   │   └── assertions.go          # snapshot + remote-dispatch assertions
│   ├── mode3_happy_path_test.go
│   ├── mode3_tier_mix_test.go
│   ├── mode3_rollback_test.go
│   ├── mode3_pause_resume_test.go
│   ├── mode3_session_recovery_test.go
│   └── mode3_event_stream_test.go
└── fixtures/
    └── repos/...
```

Harness requirements:
- Shared setup to obtain remote `session_id` before agent run.
- Agent instantiated with `NewSandboxTools(client, session_id)`.
- Fixture repo seeded in sandbox session base snapshot.

---

## 4. Scenario Definitions

### 4.1 Happy-path Remote Workflow

- Run multi-turn task requiring read/edit/write and one bash validation step.
- Assert final file state and command success.
- Assert every tool call traversed RPC path (no local fallback).

### 4.2 Tier Routing with Snapshot Metadata

- Exercise Tier 1 and Tier 2 tools in same run.
- Assert responses include `snapshot_id` sequence and non-empty snapshot list.

### 4.3 Rollback Correctness

- Execute mutating actions producing snapshots.
- Roll back to earlier snapshot.
- Re-run read assertions to confirm prior state restoration.

### 4.4 Pause/Resume Continuity

- Pause session mid-workflow after at least one turn.
- Resume and continue workflow.
- Assert continuity of workspace and session identity.

### 4.5 Session Recovery / Destroy Semantics

- Validate destroyed session rejects further ExecuteTool calls with typed `not_found`.
- Validate create-new-session from same base snapshot produces clean state.

### 4.6 Event Stream Remote Visibility

- Stream `AgentEvent`s to remote observer during Mode 3 run.
- Assert key milestones and order consistency per stream.

---

## 5. Connected Components (Seams)

| Component | Seam | Contract |
|-----------|------|----------|
| `internal/agent` | Tool dispatch boundary | Agent uses `ToolBackend` abstraction with `SandboxBackend` injection |
| `internal/rpc` | Remote transport | ConnectRPC methods for lifecycle, tool execution, snapshots, events |
| `internal/sandbox` | Execution backend | remote session lifecycle and tiered tool execution |
| `internal/tools` | Remote tool adapter | `NewSandboxTools` + `ToolResponse.snapshot_id` propagation |
| `e2etests/mode3` | Validation harness | deterministic remote environment and assertions |

---

## 6. Acceptance Criteria

1. **Mode 3 happy path works end-to-end**
- Steps: create remote session, run multi-turn agent task via SandboxBackend, destroy session.
- Expected: successful completion with expected workspace output and RPC-observed tool dispatch.

2. **Full lifecycle operations work**
- Steps: create, pause, resume, rollback, destroy in one suite.
- Expected: each operation succeeds in valid state and fails with typed errors in invalid state.

3. **Snapshot behavior is correct**
- Steps: run mutating operations, capture snapshot IDs, rollback.
- Expected: rollback restores prior state and subsequent reads reflect restored snapshot.

4. **Tier mix works remotely**
- Steps: run both Tier 1 and Tier 2 tools in one task.
- Expected: both succeed through remote host path with correct result metadata.

5. **Event streaming works in distributed run**
- Steps: subscribe to remote event stream during scenario.
- Expected: ordered milestone events are received and correlated with session/tool call IDs.

6. **No MCP dependency in Mode 3 path**
- Steps: inspect runtime wiring and execute scenarios.
- Expected: all remote tool dispatch goes through ConnectRPC sandbox services only.

---

## 7. Testing Strategy

### 7.1 Deterministic First

- Default PR suite uses deterministic provider traces and reproducible fixture repos.
- Normalize volatile fields (timestamps, token counts) in assertions.

### 7.2 Environment Modes

- `mode3-fake-host`: in-process fake sandbox service for fast deterministic checks.
- `mode3-real-host`: integration environment with real sandbox host (gated lane).

### 7.3 Diagnostics

On failure, capture:
- RPC method timeline with request IDs
- Agent event timeline
- Snapshot list and selected rollback target
- Workspace diff before/after rollback

---

## 8. URP (Unreasonably Robust Programming)

1. **Dual-host failover scenario:** simulate host loss and session recreation from snapshot transfer.
2. **Replayable distributed trace bundles:** package RPC + agent + filesystem traces for deterministic repro.
3. **Cross-run semantic diffing:** compare Mode 1 baseline and Mode 3 outcomes to detect subtle regressions.

---

## 9. Extreme Optimization

1. Parallel scenario execution across isolated remote sessions.
2. Reusable base snapshots to amortize setup time across Mode 3 tests.
3. Streaming assertion engine that validates invariants online to fail fast.

---

## 10. Alien Artifacts

1. **Distributed invariant checker:** enforce temporal invariants spanning agent events + RPC calls + snapshot state.
2. **Causal trace stitching:** correlate tool-call causality across process boundaries automatically.
3. **Counterexample shrinking for distributed failures:** minimize failing event/RPC traces for fast root-cause isolation.

---

## 11. Dependencies

| Dependency | Purpose |
|-----------|---------|
| `internal/rpc` | ConnectRPC client/server and event stream contracts |
| `internal/agent` | runtime under test with SandboxBackend tool dispatch |
| `internal/tools` | `NewSandboxTools` factory and remote tool mapping |
| `internal/sandbox` | remote execution target for lifecycle/snapshot operations |

---

## 12. Exit Criteria

1. Mode 3 E2E suite exists under `e2etests/mode3/` and is runnable in deterministic mode.
2. Lifecycle scenarios cover create/execute/list snapshots/rollback/pause/resume/destroy.
3. Tier-mixed remote tool workflows pass with snapshot metadata assertions.
4. Remote event streaming scenario passes with ordered milestone checks.
5. Gated real-host integration lane is defined and passing.
6. Mode 3 gate (G6 precursor behaviors) is demonstrated by suite results.
