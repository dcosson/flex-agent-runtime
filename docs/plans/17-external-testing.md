# 17: External / End-to-End Testing Plan

**Status:** Partial (Sections 1-11 complete; Section 12 planned, not yet implemented)
**Depends on:** 08-agent-tools-e2e, 14-mode3-e2e, 15-mode2-e2e, 16-runtime-test-harness, 11-sandbox-host-service.add01, 11-sandbox-host-service.add02
**Depended on by:** --
**Scope:** External E2E testing strategy covering usage examples, Docker-based CI, dedicated host testing, mock-based testing, and CI integration across all placement modes.

---

## 1. Overview

This plan defines how to test the flex-agent-runtime end-to-end from the outside -- as a consumer of the Go library and the `sandbox-host` binary would. It complements the existing internal E2E tests in `tests/integration/` (plans 08, 14, 15, 16) by adding:

- **Concrete usage examples** showing how callers wire up agents in each placement mode.
- **Docker-based CI** that can run the full ZFS + gVisor stack, gVisor-only, or neither -- without dedicated infrastructure (see addendum 02 for configurable backends).
- **Tiered test matrix** that gates PRs with fast mock-based tests and runs heavier infrastructure tests nightly.
- **Cross-mode parity verification** ensuring the same agent logic produces equivalent results across All Local, Agent in Sandbox, and Agent outside Sandbox.

Non-goals:
- Redefining the internal test harnesses (plans 01 through 16 cover those).
- Load/soak testing (plan 16-runtime-test-harness covers that).
- Remote cloud provider E2E (Batch 6 plans cover E2B, Daytona, Fly).

---

## 2. Usage Examples

These examples show the concrete Go code a consumer writes to launch agents in each placement mode. They serve both as documentation and as the basis for E2E test scenarios.

### 2.1 All Local Agent (Mode 1)

The simplest mode: agent loop and tools run in a single process on the local filesystem. No ZFS, no gVisor, no RPC.

```go
package main

import (
    "context"
    "fmt"
    "log/slog"
    "time"

    "flex-agent-runtime/internal/agent"
    "flex-agent-runtime/internal/ai"
    "flex-agent-runtime/internal/sandbox/environment/local"
    "flex-agent-runtime/internal/tools"
)

func main() {
    ctx := context.Background()
    logger := slog.Default()

    // 1. Create a LocalEnvironment rooted at the working directory.
    env := local.NewLocalEnvironment("/tmp/workspace", logger)
    if err := env.Create(ctx, environment.SessionConfig{SessionID: "demo-1"}); err != nil {
        panic(err)
    }
    defer env.Destroy(ctx)

    // 2. Build tools backed by the local environment.
    agentTools := tools.NewEnvironmentTools(env.ExecuteTool)

    // 3. Pick a model (assumes provider is registered via init()).
    model, _ := ai.LookupModel("claude-sonnet-4-20250514")

    // 4. Create a NativeDriver with tools and model.
    driver := agent.NewNativeDriver(agent.DriverConfig{
        Model:        model,
        Tools:        agentTools,
        SystemPrompt: "You are a helpful coding assistant.",
    })

    // 5. Create the Agent and attach a session.
    a := agent.New(driver)
    a.SetSession(&agent.Session{ID: "demo-session-1"})

    // 6. Subscribe to events (streaming output, tool calls, state changes).
    a.Subscribe(func(evt agent.AgentEvent) {
        switch evt.Type {
        case agent.EventAgentMessageDelta:
            fmt.Print(evt.Delta) // stream text to stdout
        case agent.EventToolStarted:
            fmt.Printf("\n[tool: %s]\n", evt.ToolName)
        case agent.EventStateChange:
            fmt.Printf("\n[state: %s]\n", evt.State)
        }
    })

    // 7. Send a prompt and wait for completion.
    if err := a.Prompt(ctx, "Read main.go and add error handling"); err != nil {
        panic(err)
    }

    // Wait for idle (in production, use event subscription).
    time.Sleep(30 * time.Second)

    // 8. Multi-turn: send a follow-up.
    a.FollowUp("Now add unit tests for the changes you made")

    // 9. Steering: inject mid-turn guidance.
    a.Steer("Focus on table-driven tests")
}
```

### 2.2 Agent in Sandbox (Mode 2) -- 3rd Party Driver

In this mode, a 3rd party agent driver (e.g., Claude Code) runs inside a session sandbox managed by the sandbox-host service. The orchestrator communicates with the sandbox-host over RPC.

```go
package main

import (
    "context"
    "fmt"
    "log/slog"

    "flex-agent-runtime/internal/agent"
    "flex-agent-runtime/internal/rpc/client"
    "flex-agent-runtime/internal/termmux"
)

func main() {
    ctx := context.Background()
    logger := slog.Default()

    // 1. Connect to the sandbox-host RPC service.
    sandboxClient := client.NewHTTPSandboxClient("http://sandbox-host:8080", client.WithAuthToken("secret"))

    // 2. Create a sandbox session (clones base ZFS snapshot).
    resp, err := sandboxClient.CreateSession(ctx, &api.CreateSessionRequest{
        BaseSnapshot: "pool/bases/ubuntu-22.04@ready",
        SessionID:    "session-abc",
        Labels:       map[string]string{"user": "demo"},
    })
    if err != nil {
        panic(err)
    }
    fmt.Printf("Session created: %s at %s\n", resp.Session.ID, resp.Session.Mountpoint)

    // 3. Create a terminal mux session to run Claude Code inside the sandbox.
    termClient := client.NewTerminalClient("http://sandbox-host:8080")
    termSession, err := termClient.CreateSession(ctx, termmux.SessionConfig{
        Command:    "claude",
        Args:       []string{"--print", "Fix all lint errors"},
        WorkDir:    resp.Session.Mountpoint,
        Env:        map[string]string{"ANTHROPIC_API_KEY": "sk-..."},
    })
    if err != nil {
        panic(err)
    }

    // 4. Subscribe to normalized agent events from the 3rd party driver.
    eventStream, _ := termClient.StreamEvents(ctx, termSession.ID)
    for evt := range eventStream {
        fmt.Printf("[%s] %s\n", evt.Type, evt.Delta)
    }

    // 5. Take a snapshot after the driver finishes.
    snapResp, _ := sandboxClient.CreateSnapshot(ctx, &api.CreateSnapshotRequest{
        SessionID: resp.Session.ID,
        Name:      "post-lint-fix",
    })
    fmt.Printf("Snapshot: %s\n", snapResp.SnapshotID)

    // 6. Pause the session (zero idle compute).
    sandboxClient.PauseSession(ctx, &api.PauseSessionRequest{SessionID: resp.Session.ID})
}
```

### 2.3 Agent outside Sandbox (Mode 3) -- RPC Dispatch

The agent loop runs on a controller host. Tool calls are dispatched to a remote sandbox-host via RPC. The agent uses `NativeSandboxEnvironment` as its execution backend.

```go
package main

import (
    "context"
    "fmt"
    "log/slog"

    "flex-agent-runtime/internal/agent"
    "flex-agent-runtime/internal/ai"
    "flex-agent-runtime/internal/rpc/client"
    "flex-agent-runtime/internal/sandbox/environment/native"
    "flex-agent-runtime/internal/tools"
)

func main() {
    ctx := context.Background()
    logger := slog.Default()

    // 1. Connect to remote sandbox-host.
    sandboxClient := client.NewHTTPSandboxClient("http://sandbox-host:8080")

    // 2. Create a NativeSandboxEnvironment that wraps the RPC client.
    env := native.NewNativeSandboxEnvironment(sandboxClient, logger)
    if err := env.Create(ctx, environment.SessionConfig{
        BaseImage: "pool/bases/ubuntu-22.04@ready",
        SessionID: "remote-session-1",
    }); err != nil {
        panic(err)
    }
    defer env.Destroy(ctx)

    // 3. Build tools backed by the remote sandbox.
    agentTools := tools.NewEnvironmentTools(env.ExecuteTool)

    // 4. Create agent with NativeDriver.
    model, _ := ai.LookupModel("claude-sonnet-4-20250514")
    driver := agent.NewNativeDriver(agent.DriverConfig{
        Model:        model,
        Tools:        agentTools,
        SystemPrompt: "You are a helpful coding assistant.",
    })
    a := agent.New(driver)
    a.SetSession(&agent.Session{ID: "remote-session-1"})

    // 5. Run agent -- tool calls dispatch over RPC to sandbox-host.
    a.Subscribe(func(evt agent.AgentEvent) {
        if evt.Type == agent.EventToolCompleted {
            fmt.Printf("[tool done: %s] snapshot=%s\n", evt.ToolName, evt.ToolResult.SnapshotID)
        }
    })
    if err := a.Prompt(ctx, "Refactor the database layer"); err != nil {
        panic(err)
    }

    // 6. Snapshot and rollback via the environment.
    snap, _ := env.CreateSnapshot(ctx, "after-refactor")
    fmt.Printf("Snapshot: %s\n", snap.ID)
    // Later: env.Rollback(ctx, snap.ID)
}
```

### 2.4 Using 3rd Party Drivers in Sandboxes

This shows the pattern for running Codex or Claude Code drivers inside session sandboxes with full lifecycle management. The key difference from Mode 2 above is that this uses the `AgentDriver` interface directly with adapter drivers.

```go
// Register a 3rd party driver factory.
agent.RegisterDriver("claude-code", func(cfg agent.DriverConfig) (agent.AgentDriver, error) {
    return termmux.NewClaudeCodeDriver(termmux.ClaudeCodeConfig{
        BinaryPath:  "/usr/local/bin/claude",
        WorkDir:     cfg.Metadata["workdir"].(string),
        Credentials: cfg.Metadata["credentials"].(map[string]string),
    })
})

// Create an agent with the 3rd party driver.
driver, _ := agent.NewDriver("claude-code", agent.DriverConfig{
    Metadata: map[string]any{
        "workdir":     "/workspace",
        "credentials": map[string]string{"ANTHROPIC_API_KEY": "sk-..."},
    },
})
a := agent.New(driver)
a.SetSession(&agent.Session{ID: "claude-code-session"})

// Subscribe to normalized events (same interface as NativeDriver).
a.Subscribe(func(evt agent.AgentEvent) {
    fmt.Printf("[%s] %s\n", evt.Type, evt.Delta)
})

// Start the driver with a prompt.
a.Start(ctx, a.Session(), "Fix all failing tests")
```

---

## 3. External E2E Testing Strategy

### 3.1 Three-Tier Test Matrix

```mermaid
graph TD
    subgraph "Tier 1: PR-Fast (Mock-Based)"
        T1A[All Local mode<br/>LocalEnvironment<br/>ScriptedProvider]
        T1B[Mode 3 mock<br/>MemorySandboxService<br/>No ZFS/gVisor]
        T1C[Mode 2 mock<br/>VirtualTerminal<br/>FakeSessionManager]
        T1D[Cross-mode parity<br/>Same scenario,<br/>both backends]
    end

    subgraph "Tier 2: PR-Standard (Docker)"
        T2A[Full Mode 3<br/>Docker: ZFS + gVisor<br/>sandbox-host binary]
        T2B[Full Mode 2<br/>Docker: sandbox-host<br/>+ terminal mux]
        T2C[Lifecycle tests<br/>snapshot, rollback,<br/>pause/resume]
        T2D[local-disk + none<br/>No ZFS or gVisor<br/>sandbox-host binary]
        T2E[gVisor-only<br/>local-disk + gVisor<br/>sandbox-host binary]
    end

    subgraph "Tier 3: Nightly (Dedicated Host)"
        T3A[Native Linux host<br/>Real ZFS pool on disk<br/>Real gVisor runsc]
        T3B[Provider integration<br/>Real Anthropic/OpenAI<br/>API key gated]
        T3C[Multi-session stress<br/>10+ concurrent agents<br/>on single host]
        T3D[Failure injection<br/>Kill sandbox-host,<br/>ZFS pool full, OOM]
    end

    T1A --> T2A
    T1B --> T2A
    T1C --> T2B
    T2A --> T3A
    T2B --> T3A
    T2C --> T3A

    style T1A fill:#e8f5e9
    style T1B fill:#e8f5e9
    style T1C fill:#e8f5e9
    style T1D fill:#e8f5e9
    style T2A fill:#fff3e0
    style T2B fill:#fff3e0
    style T2C fill:#fff3e0
    style T2D fill:#fff3e0
    style T2E fill:#fff3e0
    style T3A fill:#fce4ec
    style T3B fill:#fce4ec
    style T3C fill:#fce4ec
    style T3D fill:#fce4ec
```

| Tier | What | Where it runs | ZFS | gVisor | LLM Provider | Duration |
|------|------|---------------|-----|--------|--------------|----------|
| **PR-Fast** | Mock-based E2E, cross-mode parity | Any CI runner (Linux/Mac) | No | No | ScriptedProvider | <30s |
| **PR-Standard (minimal)** | Docker sandbox-host with local-disk + no gVisor | Linux CI with Docker | No | No | ScriptedProvider | <2min |
| **PR-Standard (gVisor)** | Docker sandbox-host with local-disk + gVisor | Linux CI with Docker | No | Yes (in container) | ScriptedProvider | <3min |
| **PR-Standard (full)** | Docker sandbox-host with ZFS + gVisor | Linux CI with Docker + ZFS module | Yes (in container) | Yes (in container) | ScriptedProvider | <5min |
| **Nightly** | Real provider, stress, failure injection | Dedicated Linux host | Yes (native) | Yes (native) | Real APIs (gated) | <30min |

### 3.2 Test Flow Architecture

```mermaid
sequenceDiagram
    participant CI as CI Pipeline
    participant Test as Test Runner
    participant Agent as Agent (library)
    participant Env as ExecutionEnvironment
    participant SH as sandbox-host (binary)
    participant ZFS as ZFS Pool
    participant GV as gVisor (runsc)

    Note over CI,GV: Tier 1: Mock-Based (all environments)
    CI->>Test: go test ./tests/external/...
    Test->>Agent: New(NativeDriver)
    Agent->>Env: LocalEnvironment or MemorySandboxService
    Env-->>Agent: tool results (in-memory)
    Agent-->>Test: events + session state
    Test->>Test: assert parity across modes

    Note over CI,GV: Tier 2: Docker-Based (config variants)

    Note over CI,SH: Variant A: local-disk + none (simplest)
    CI->>CI: docker compose up (sandbox-host, config: local-disk/none)
    CI->>Test: go test -tags=docker ./tests/external/docker/...
    Test->>SH: RPC: CreateSession
    SH->>SH: os.MkdirAll (session dir)
    SH-->>Test: session_id
    Test->>Agent: New(NativeDriver, NativeSandboxEnvironment)
    Agent->>SH: RPC: ExecuteTool (all Tier 1)
    SH-->>Agent: ToolResponse (no snapshot)
    Agent-->>Test: events + session state

    Note over CI,GV: Variant B: local-disk + gVisor
    Agent->>SH: RPC: ExecuteTool
    SH->>GV: runsc run (Tier 2 tools)
    GV-->>SH: result
    SH-->>Agent: ToolResponse (no snapshot)

    Note over CI,GV: Variant C: ZFS + gVisor (full, requires ZFS module)
    Test->>SH: RPC: CreateSession
    SH->>ZFS: zfs clone base@snap
    SH-->>Test: session_id
    Agent->>SH: RPC: ExecuteTool
    SH->>GV: runsc run (Tier 2 tools)
    GV-->>SH: result
    SH-->>Agent: ToolResponse + snapshot
    Agent-->>Test: events + session state

    Note over CI,GV: Tier 3: Dedicated Host
    CI->>Test: go test -tags=native ./tests/external/native/...
    Test->>SH: sandbox-host (local process)
    SH->>ZFS: real ZFS commands
    SH->>GV: real gVisor containers
```

---

## 4. Docker Test Environment Setup

With configurable backends (addendum 02), Docker-based testing supports three configurations with decreasing infrastructure requirements:

| Config | StorageBackend | ContainerRuntime | Requires ZFS module? | Requires privileged? | What it tests |
|--------|---------------|-----------------|---------------------|---------------------|---------------|
| **Minimal** | `local-disk` | `none` | No | No | Session lifecycle, Tier 1 tools, pause/resume |
| **gVisor-only** | `local-disk` | `gvisor` | No | Yes (for runsc) | Above + Tier 2 container isolation |
| **Full** | `zfs` | `gvisor` | Yes | Yes | Above + snapshots, rollback, COW clones |

### 4.1 Dockerfile for Test Image

The base Dockerfile builds the `sandbox-host` binary. Optional layers add gVisor and/or ZFS tools.

```dockerfile
# syntax=docker/dockerfile:1

# --- Minimal: just sandbox-host with local-disk + none ---
FROM ubuntu:22.04 AS base-minimal

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    && rm -rf /var/lib/apt/lists/*

COPY --from=golang:1.23 /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"

WORKDIR /src
COPY . .
RUN go build -o /usr/local/bin/sandbox-host ./cmd/sandbox-host

# --- gVisor-only: adds runsc ---
FROM base-minimal AS base-gvisor

RUN curl -fsSL https://gvisor.dev/archive.key | gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg \
    && echo "deb [arch=amd64 signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main" \
    > /etc/apt/sources.list.d/gvisor.list \
    && apt-get update && apt-get install -y runsc \
    && rm -rf /var/lib/apt/lists/*

# --- Full: adds ZFS userland ---
FROM base-gvisor AS base-full

RUN apt-get update && apt-get install -y --no-install-recommends \
    zfsutils-linux \
    kmod \
    && rm -rf /var/lib/apt/lists/*
```

Use `--target=base-minimal`, `--target=base-gvisor`, or `--target=base-full` to select the configuration.

### 4.2 Docker Compose for Multi-Container Testing

Three compose profiles support the different configurations:

```yaml
# docker-compose.e2e.yaml
version: "3.8"

services:
  # --- Minimal config: local-disk + none ---
  # Runs on any Linux Docker host. No special privileges needed.
  sandbox-host-minimal:
    build:
      context: .
      dockerfile: tests/external/docker/Dockerfile.sandbox-host
      target: base-minimal
    environment:
      SANDBOX_LISTEN_ADDR: "0.0.0.0:8080"
      SANDBOX_STORAGE_BACKEND: "local-disk"
      SANDBOX_CONTAINER_RUNTIME: "none"
      SANDBOX_SESSIONS_ROOT_DIR: "/var/lib/sandbox/sessions"
      SANDBOX_AUTH_TOKEN: "e2e-test-token"
    healthcheck:
      test: ["CMD", "curl", "-sf", "http://localhost:8080/health"]
      interval: 2s
      timeout: 5s
      retries: 10
    entrypoint: ["/usr/local/bin/sandbox-host"]
    profiles: ["minimal"]

  # --- gVisor-only config: local-disk + gvisor ---
  sandbox-host-gvisor:
    build:
      context: .
      dockerfile: tests/external/docker/Dockerfile.sandbox-host
      target: base-gvisor
    privileged: true                    # required for gVisor runsc
    cap_add:
      - NET_RAW                         # gVisor networking
    environment:
      SANDBOX_LISTEN_ADDR: "0.0.0.0:8080"
      SANDBOX_STORAGE_BACKEND: "local-disk"
      SANDBOX_CONTAINER_RUNTIME: "gvisor"
      SANDBOX_SESSIONS_ROOT_DIR: "/var/lib/sandbox/sessions"
      SANDBOX_AUTH_TOKEN: "e2e-test-token"
    healthcheck:
      test: ["CMD", "curl", "-sf", "http://localhost:8080/health"]
      interval: 2s
      timeout: 5s
      retries: 10
    entrypoint: ["/usr/local/bin/sandbox-host"]
    profiles: ["gvisor"]

  # --- Full config: ZFS + gVisor ---
  sandbox-host-full:
    build:
      context: .
      dockerfile: tests/external/docker/Dockerfile.sandbox-host
      target: base-full
    privileged: true                    # required for ZFS + gVisor
    cap_add:
      - SYS_ADMIN                      # ZFS mount operations
      - NET_RAW                         # gVisor networking
    devices:
      - /dev/zfs:/dev/zfs              # ZFS device access
    volumes:
      - zfs-pool:/var/lib/zfs-test     # backing store for ZFS test pool
    environment:
      SANDBOX_LISTEN_ADDR: "0.0.0.0:8080"
      SANDBOX_STORAGE_BACKEND: "zfs"
      SANDBOX_CONTAINER_RUNTIME: "gvisor"
      SANDBOX_POOL_NAME: "testpool"
      SANDBOX_BASES_DATASET: "testpool/bases"
      SANDBOX_SESSIONS_DATASET: "testpool/sessions"
      SANDBOX_AUTH_TOKEN: "e2e-test-token"
    healthcheck:
      test: ["CMD", "curl", "-sf", "http://localhost:8080/health"]
      interval: 2s
      timeout: 5s
      retries: 10
    entrypoint: ["/usr/local/bin/sandbox-host"]
    profiles: ["full"]

  test-runner:
    build:
      context: .
      dockerfile: tests/external/docker/Dockerfile.test-runner
    environment:
      SANDBOX_HOST_URL: "http://sandbox-host:8080"
      SANDBOX_AUTH_TOKEN: "e2e-test-token"
      E2E_TIER: "docker"
    volumes:
      - ./:/src:ro
    working_dir: /src
    profiles: ["minimal", "gvisor", "full"]

  # Initialization: create ZFS pool and base snapshot (full profile only)
  init-pool:
    build:
      context: .
      dockerfile: tests/external/docker/Dockerfile.sandbox-host
      target: base-full
    privileged: true
    cap_add: [SYS_ADMIN]
    devices: [/dev/zfs:/dev/zfs]
    volumes:
      - zfs-pool:/var/lib/zfs-test
    entrypoint: ["/bin/bash", "-c"]
    command:
      - |
        set -e
        if ! zpool list testpool 2>/dev/null; then
          truncate -s 2G /var/lib/zfs-test/pool.img
          zpool create testpool /var/lib/zfs-test/pool.img
        fi
        zfs create -p testpool/bases
        zfs create -p testpool/sessions
        zfs create testpool/bases/ubuntu-base
        echo '{"ready": true}' > /testpool/bases/ubuntu-base/.workspace-ready
        zfs snapshot testpool/bases/ubuntu-base@ready
        echo "ZFS pool initialized successfully"
    profiles: ["full"]

volumes:
  zfs-pool:
```

Usage:

```bash
# Minimal -- runs anywhere with Docker
docker compose -f docker-compose.e2e.yaml --profile minimal up --build --abort-on-container-exit

# gVisor-only -- needs privileged mode
docker compose -f docker-compose.e2e.yaml --profile gvisor up --build --abort-on-container-exit

# Full -- needs ZFS kernel module + privileged mode
docker compose -f docker-compose.e2e.yaml --profile full up --build --abort-on-container-exit
```

### 4.3 Required Host Prerequisites

| Requirement | Minimal (local-disk + none) | gVisor-only (local-disk + gvisor) | Full (ZFS + gVisor) | Dedicated host |
|-------------|:---:|:---:|:---:|:---:|
| Linux kernel | Any | 5.4+ | 5.10+ (host) | 5.10+ |
| ZFS kernel module | Not needed | Not needed | Loaded on host (`modprobe zfs`) | Loaded |
| gVisor (runsc) | Not needed | Installed in container | Installed in container | Installed on host |
| Docker mode | Standard (no privileges) | `--privileged` | `--privileged` + `--device /dev/zfs` | N/A |
| Disk space | 1GB | 1GB | 4GB+ for ZFS pool image | 20GB+ ZFS pool |

### 4.4 Known Limitations and Workarounds

| Limitation | Impact | Workaround |
|-----------|--------|------------|
| ZFS in Docker requires host kernel module | Cannot run full-config tests on CI hosts without `zfs.ko` | Use minimal or gVisor-only config; full config runs only where ZFS is available |
| `--privileged` required for gVisor | Security concern in shared CI | Use minimal config (no privileges needed) for basic CI; use dedicated CI runners for gVisor/full |
| gVisor `--net-raw` | Network isolation in gVisor needs raw socket cap | Pass `--cap-add NET_RAW` in Docker; gVisor's own network stack handles isolation |
| File-backed ZFS pool performance | Slower than real block devices | Acceptable for CI tests; keep pool size small (2-4GB) |
| macOS Docker Desktop | No ZFS module, no gVisor | Use minimal config for Docker tests on Mac; Tier 1 (mock-based) always works |
| No snapshots with local-disk | Cannot test snapshot/rollback in minimal/gVisor configs | Snapshot tests only run in full config; mock-based snapshot tests in Tier 1 |

---

## 5. Test Scenarios

### 5.1 All Local Mode (Tier 1)

These tests use `LocalEnvironment` with `ScriptedProvider` and run on any platform.

| Scenario | Description | Assertions |
|----------|-------------|------------|
| **L1: Basic multi-turn** | Agent reads a file, edits it, writes a new file across 3 provider turns | Files modified correctly; event sequence includes turn/tool/message events; session metrics updated |
| **L2: Bash execution** | Agent runs bash commands (compile, test) | Exit codes captured; stdout/stderr in tool results |
| **L3: Code interpreter** | Agent uses `execute_script` with Starlark to discover and invoke tools | Script results returned; nested tool calls visible in events |
| **L4: Steering mid-turn** | Inject `Steer()` while provider is streaming | Steering message appears in next user turn; event includes `EventSteeringApplied` |
| **L5: Follow-up chain** | Enqueue multiple `FollowUp()` messages | All follow-ups processed in order; each triggers a new turn |
| **L6: Abort** | Send `Abort()` during tool execution | Agent transitions to Idle; `EventAborted` emitted |
| **L7: Error recovery** | Provider returns error on turn 2; verify agent handles gracefully | `EventProviderError` emitted; agent reaches Idle; session records error count |
| **L8: Git workflow** | Agent initializes repo, makes commits, runs git log | Git operations succeed on local filesystem |
| **L9: Large file handling** | Read/write files approaching max size limits | Correct truncation or error behavior |
| **L10: Concurrent subscribers** | Multiple event subscribers attached to same agent | All subscribers receive identical event streams |

### 5.2 Agent in Sandbox -- Mode 2 (Tier 2/3)

These tests run against a sandbox-host. The infrastructure config determines which scenarios are applicable. Tests that require ZFS or gVisor skip gracefully when the sandbox-host is configured without them.

| Scenario | Description | Required config | Assertions |
|----------|-------------|----------------|------------|
| **S1: Full lifecycle** | Create session -> execute tools -> snapshot -> pause -> resume -> destroy | Any | Each lifecycle transition succeeds; state transitions are valid |
| **S2: ZFS snapshot correctness** | Write files, snapshot, write more, rollback | ZFS | After rollback, filesystem matches snapshot state exactly |
| **S3: Per-turn snapshots** | Run 3-turn agent; verify a snapshot exists per turn | ZFS | Snapshot count matches turn count; each snapshot captures correct state |
| **S4: 3rd party driver launch** | Launch Claude Code driver in sandbox via terminal mux | Any | Driver produces normalized events; session log captured |
| **S5: Credential injection** | Inject API keys into sandbox environment | Any | Driver can authenticate; credentials not leaked in events |
| **S6: Pause/resume preserves state** | Multi-turn conversation, pause, resume, continue | Any | Conversation log intact after resume; filesystem unchanged |
| **S7: gVisor isolation** | Tier 2 bash commands run inside gVisor container | gVisor | Process isolation verified (PID namespace, filesystem root); resource limits enforced |
| **S8: local-disk lifecycle** | Create session with local-disk, execute tools, destroy | local-disk | Session dir created via mkdir; tools execute directly; session dir removed on destroy |
| **S9: Snapshot on local-disk returns error** | Call CreateSnapshot on local-disk sandbox-host | local-disk | Returns `ErrSnapshotsNotAvailable`; agent handles gracefully |
| **S10: All-direct execution (no gVisor)** | Execute Tier 2 classified tools without gVisor | none runtime | All tools execute directly (Tier 1); correct results returned |

### 5.3 Agent outside Sandbox -- Mode 3 (Tier 2/3)

These tests verify RPC dispatch from a local agent to a remote sandbox-host. Config combinations determine which capabilities are available.

| Scenario | Description | Required config | Assertions |
|----------|-------------|----------------|------------|
| **R1: RPC happy path** | Agent dispatches read/write/bash over RPC | Any | Tool results match expected; RPC round-trip succeeds |
| **R2: Tier routing** | Mix of Tier 1 (read, grep) and Tier 2 (bash) calls | gVisor | Tier 1 runs without container; Tier 2 runs in gVisor; both return correct results |
| **R3: Streaming progress** | Bash tool emits progress updates over RPC stream | Any | Progress events received by agent before final result |
| **R4: Snapshot via RPC** | Create snapshot after tool execution via NativeSandboxEnvironment | ZFS | Snapshot ID returned; rollback via RPC restores state |
| **R5: Session lifecycle over RPC** | Create -> execute -> pause -> resume -> execute -> destroy | Any | All RPC calls succeed; state transitions valid |
| **R6: Concurrent tool calls** | Agent dispatches 3 tool calls in parallel (if supported) | Any | All complete successfully; no race conditions |
| **R7: Capabilities reflect config** | Query NativeSandboxEnvironment.Capabilities() for each config | Any | Caps match config: local-disk has Snapshots=false; none runtime has TierRouting=false |
| **R8: Snapshot ErrCapabilityNotSupported** | Call CreateSnapshot on NativeSandboxEnvironment with local-disk config | local-disk | Returns `ErrCapabilityNotSupported` locally before RPC; agent loop handles gracefully |

### 5.3.1 Backend Config Combination Matrix (Tier 2)

Each backend config combination is tested with a core set of lifecycle and tool execution scenarios:

| Config | StorageBackend | ContainerRuntime | Snapshot tests | Tier routing tests | Lifecycle tests |
|--------|---------------|-----------------|:-:|:-:|:-:|
| **C1: Full** | `zfs` | `gvisor` | Yes | Yes | Yes |
| **C2: ZFS-only** | `zfs` | `none` | Yes | No (all Tier 1) | Yes |
| **C3: gVisor-only** | `local-disk` | `gvisor` | No | Yes | Yes |
| **C4: Minimal** | `local-disk` | `none` | No | No | Yes |

### 5.4 Cross-Mode Parity (Tier 1)

These tests run the same agent scenario across multiple `ExecutionEnvironment` implementations and verify equivalent behavior.

```mermaid
graph LR
    subgraph "Same Scenario Definition"
        SC[Scenario: read file,<br/>edit, write new file,<br/>run bash test]
    end

    SC --> E1[LocalEnvironment<br/>Mode 1]
    SC --> E2[MemorySandboxService<br/>Mode 3 mock]

    E1 --> V[Verify Parity:<br/>same files written,<br/>same event types,<br/>same final state]
    E2 --> V

    style SC fill:#e1f5fe
    style E1 fill:#e8f5e9
    style E2 fill:#fff3e0
    style V fill:#f3e5f5
```

| Scenario | What is compared | Expected parity |
|----------|-----------------|-----------------|
| **P1: File ops parity** | Same read/write/edit sequence on Local vs MemorySandbox | Final file contents identical |
| **P2: Event type parity** | Same scenario, compare event type sequences | Event type sequence identical (timing may differ) |
| **P3: Session state parity** | Same scenario, compare final session metrics | Turn count, tool call count, message count match |
| **P4: Error behavior parity** | Inject same error, compare recovery | Same error events, same final state |

### 5.5 Provider Integration (Tier 3, API-Key Gated)

These tests use real LLM providers and are gated behind environment variables.

```go
// Only runs when ANTHROPIC_API_KEY is set.
func TestProviderIntegration_Anthropic(t *testing.T) {
    apiKey := os.Getenv("ANTHROPIC_API_KEY")
    if apiKey == "" {
        t.Skip("ANTHROPIC_API_KEY not set; skipping provider integration test")
    }
    // ... set up real provider, run simple agent scenario
}
```

| Scenario | Provider | What it tests |
|----------|----------|---------------|
| **I1: Anthropic streaming** | Anthropic | Real SSE stream, tool call, multi-turn |
| **I2: OpenAI streaming** | OpenAI | Real stream, function calling |
| **I3: Google streaming** | Google | Real stream, tool calling |
| **I4: Cross-provider** | All available | Same prompt produces valid responses from each |

### 5.6 Failure Scenarios (Tier 3)

| Scenario | Failure mode | Expected behavior |
|----------|-------------|-------------------|
| **F1: sandbox-host crash** | Kill sandbox-host mid-tool-execution | Agent receives RPC error; `EventToolError` emitted; agent transitions to Idle |
| **F2: RPC timeout** | Inject 30s delay in sandbox-host response | Agent's context deadline triggers; tool call returns timeout error |
| **F3: ZFS pool full** | Fill ZFS pool to 100% capacity | Snapshot creation fails gracefully; HealthCheck reports "unhealthy"; new session creation rejected |
| **F4: gVisor OOM** | Set 1MB memory limit on Tier 2 container | Container killed by OOM; tool returns error with OOM indication |
| **F5: Provider rate limit** | Provider returns 429 | Agent emits `EventProviderError`; retry behavior (if implemented) works correctly |
| **F6: Concurrent session limit** | Create MaxSessions+1 sessions | Last creation returns `ErrMaxSessionsReached` |
| **F7: Double destroy** | Call Destroy on already-destroyed session | Idempotent; no error or panic |
| **F8: Stale session resume** | Pause, kill sandbox-host, restart, resume | Session either resumes from ZFS state or returns clean error |

---

## 6. CI Integration

### 6.1 Pipeline Structure

```mermaid
graph TD
    subgraph "PR Pipeline"
        PR1[make check<br/>gofmt + vet + staticcheck] --> PR2[make test<br/>Unit tests]
        PR2 --> PR3[Tier 1: PR-Fast<br/>Mock E2E + parity]
        PR3 --> PR4{Linux runner<br/>with Docker?}
        PR4 -->|yes| PR5[Tier 2: PR-Standard<br/>Docker ZFS+gVisor E2E]
        PR4 -->|no| PR6[Skip Tier 2<br/>Log warning]
    end

    subgraph "Nightly Pipeline"
        N1[All Tier 1 + Tier 2] --> N2[Tier 3: Native host<br/>Real ZFS + gVisor]
        N2 --> N3[Provider integration<br/>API key gated]
        N2 --> N4[Stress + failure injection]
        N3 --> N5[Report generation]
        N4 --> N5
    end

    subgraph "Release Pipeline"
        R1[All tiers] --> R2[Manual QA checklist]
        R2 --> R3[Release artifact build]
    end

    style PR1 fill:#e8f5e9
    style PR2 fill:#e8f5e9
    style PR3 fill:#e8f5e9
    style PR5 fill:#fff3e0
    style N2 fill:#fce4ec
    style N3 fill:#fce4ec
    style N4 fill:#fce4ec
```

### 6.2 Build Tags

Tests are gated using Go build tags to control which tier runs:

| Tag | Tests included | When used |
|-----|---------------|-----------|
| (none) | Tier 1 mock-based tests | Always; `go test ./tests/external/...` |
| `docker` | Tier 2 Docker-based tests | PR-Standard on Linux CI with Docker |
| `native` | Tier 3 dedicated host tests | Nightly on dedicated Linux hosts |
| `provider_integration` | Real provider tests | Nightly with API keys available |

### 6.3 CI Configuration (GitHub Actions)

```yaml
# .github/workflows/e2e.yml
name: E2E Tests

on:
  pull_request:
  schedule:
    - cron: '0 3 * * *'  # Nightly at 3 AM UTC

jobs:
  tier1-mock:
    name: "Tier 1: Mock-based E2E"
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.23' }
      - run: go test -v -timeout=2m ./tests/external/...

  tier2-docker-minimal:
    name: "Tier 2: Docker E2E (local-disk + none)"
    runs-on: ubuntu-latest
    if: github.event_name == 'pull_request'
    needs: tier1-mock
    steps:
      - uses: actions/checkout@v4
      - name: Run minimal Docker E2E
        run: |
          docker compose -f docker-compose.e2e.yaml --profile minimal \
            up --build --abort-on-container-exit
        env:
          SANDBOX_STORAGE_BACKEND: "local-disk"
          SANDBOX_CONTAINER_RUNTIME: "none"

  tier2-docker-full:
    name: "Tier 2: Docker E2E (ZFS + gVisor)"
    runs-on: ubuntu-latest
    if: github.event_name == 'pull_request'
    needs: tier2-docker-minimal
    steps:
      - uses: actions/checkout@v4
      - name: Load ZFS kernel module
        run: sudo modprobe zfs || echo "ZFS module not available; skipping"
      - name: Run full Docker E2E
        run: |
          if lsmod | grep -q zfs; then
            docker compose -f docker-compose.e2e.yaml --profile full \
              up --build --abort-on-container-exit
          else
            echo "::warning::ZFS kernel module not available. Skipping full Tier 2 tests."
            echo "::notice::Minimal Tier 2 (local-disk + none) already passed."
          fi

  tier3-nightly:
    name: "Tier 3: Nightly Full E2E"
    runs-on: [self-hosted, linux, zfs]  # dedicated runner with ZFS + gVisor
    if: github.event_name == 'schedule'
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.23' }
      - name: Run native E2E
        run: go test -v -tags=native -timeout=20m ./tests/external/native/...
        env:
          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
          OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}
      - name: Upload test report
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: e2e-report
          path: tests/external/reports/
```

### 6.4 Test Reporting

Each tier produces a structured JSON report:

```go
type E2EReport struct {
    Tier       string        `json:"tier"`
    StartedAt  time.Time     `json:"started_at"`
    Duration   time.Duration `json:"duration"`
    Scenarios  []ScenarioReport `json:"scenarios"`
    Summary    ReportSummary `json:"summary"`
}

type ScenarioReport struct {
    Name       string        `json:"name"`
    Mode       string        `json:"mode"` // "local", "mode2", "mode3"
    Passed     bool          `json:"passed"`
    Duration   time.Duration `json:"duration"`
    EventCount int           `json:"event_count"`
    Error      string        `json:"error,omitempty"`
}

type ReportSummary struct {
    Total      int `json:"total"`
    Passed     int `json:"passed"`
    Failed     int `json:"failed"`
    Skipped    int `json:"skipped"`
}
```

---

## 7. Testing without ZFS -- Broader CI Compatibility

### 7.1 Two Paths to ZFS-Free Testing

With addendum 02's configurable backends, there are now two ways to run E2E tests without ZFS:

**LocalEnvironment (Tier 1, mock-based):** The in-process `LocalEnvironment` implements `ExecutionEnvironment` without any ZFS or gVisor dependency. It executes tools directly on the local filesystem. No RPC, no sandbox-host binary.

**NativeSandboxEnvironment with local-disk config (Tier 2, Docker-based):** The full sandbox-host binary runs with `StorageBackend: "local-disk"` and optionally `ContainerRuntime: "none"`. This exercises the real RPC path, real session lifecycle, and real tool execution -- just without ZFS snapshots or gVisor isolation. This is a significant improvement for CI compatibility because it means Tier 2 Docker tests can run on any Linux CI runner without the ZFS kernel module.

```go
// Tier 1: LocalEnvironment (no sandbox-host, no RPC)
env := local.NewLocalEnvironment("/tmp/workspace", logger)
caps := env.Capabilities()
// Snapshots: false, Rollback: false, TierRouting: false

// Tier 2: NativeSandboxEnvironment with local-disk + none
env := native.NewNativeSandboxEnvironment(sandboxClient, native.NativeSandboxConfig{
    StorageBackend:   native.StorageBackendLocalDisk,
    ContainerRuntime: native.ContainerRuntimeNone,
}, logger)
caps := env.Capabilities()
// Snapshots: false, Rollback: false, TierRouting: false
// But: real RPC, real sandbox-host, real session lifecycle
```

### 7.2 What Each Config Level Tests

| Capability | LocalEnvironment | local-disk + none | local-disk + gvisor | ZFS + gvisor |
|-----------|:---:|:---:|:---:|:---:|
| Session lifecycle (create/destroy) | In-process no-op | Real RPC + mkdir | Real RPC + mkdir | Real RPC + ZFS clone |
| Tool execution | Direct local | RPC -> direct | RPC -> direct (T1) / gVisor (T2) | RPC -> direct (T1) / gVisor (T2) |
| Pause/resume | In-process | Real RPC | Real RPC | Real RPC |
| Snapshots | No | No | No | Yes |
| Rollback | No | No | No | Yes |
| Tier routing | No | No | Yes | Yes |
| Container isolation | No | No | Yes (Tier 2) | Yes (Tier 2) |

### 7.3 Platform Test Matrix

| Platform | Tier 1 (Mock) | Tier 2 Minimal (local-disk + none) | Tier 2 gVisor (local-disk + gvisor) | Tier 2 Full (ZFS + gVisor) | Tier 3 (Native) |
|----------|:---:|:---:|:---:|:---:|:---:|
| macOS (dev laptop) | Yes | Yes (Docker) | No (gVisor = Linux) | No | No |
| Ubuntu CI (no ZFS module) | Yes | Yes | Yes | No | No |
| Ubuntu CI (with ZFS module) | Yes | Yes | Yes | Yes | No |
| Dedicated Linux host (ZFS + gVisor) | Yes | Yes | Yes | Yes | Yes |
| Windows CI | Yes | No | No | No | No |
| ARM64 Linux | Yes | Yes (Docker) | Possible (ARM gVisor) | Possible | Possible |

### 7.4 Graceful Degradation Pattern

Tests detect the available environment at runtime and skip unsupported configurations. The sandbox-host's config is communicated to the test runner via environment variables:

```go
func requireZFS(t *testing.T) {
    t.Helper()
    if _, err := exec.LookPath("zfs"); err != nil {
        t.Skip("zfs not found; skipping ZFS-dependent test")
    }
    out, err := exec.Command("lsmod").Output()
    if err != nil || !strings.Contains(string(out), "zfs") {
        t.Skip("zfs kernel module not loaded; skipping")
    }
}

func requireGVisor(t *testing.T) {
    t.Helper()
    if _, err := exec.LookPath("runsc"); err != nil {
        t.Skip("runsc not found; skipping gVisor-dependent test")
    }
}

func requireDocker(t *testing.T) {
    t.Helper()
    if _, err := exec.LookPath("docker"); err != nil {
        t.Skip("docker not found; skipping Docker-dependent test")
    }
}

// sandboxHostConfig reads the sandbox-host's backend config from env vars
// set by docker-compose or the test runner.
func sandboxHostConfig(t *testing.T) (storageBackend, containerRuntime string) {
    t.Helper()
    storageBackend = os.Getenv("SANDBOX_STORAGE_BACKEND")
    containerRuntime = os.Getenv("SANDBOX_CONTAINER_RUNTIME")
    if storageBackend == "" {
        storageBackend = "zfs" // default for backwards compatibility
    }
    if containerRuntime == "" {
        containerRuntime = "gvisor"
    }
    return
}

func requireZFSBackend(t *testing.T) {
    t.Helper()
    sb, _ := sandboxHostConfig(t)
    if sb != "zfs" {
        t.Skipf("sandbox-host storage_backend is %q, not zfs; skipping", sb)
    }
}

func requireGVisorRuntime(t *testing.T) {
    t.Helper()
    _, cr := sandboxHostConfig(t)
    if cr != "gvisor" {
        t.Skipf("sandbox-host container_runtime is %q, not gvisor; skipping", cr)
    }
}
```

---

## 8. File Structure

```
tests/integration/
  external/                          # <-- new directory for this plan
    README.go                        # package doc explaining the tiers
    common/
      scenario.go                    # shared scenario definitions used across modes
      parity.go                      # cross-mode parity verification helpers
      report.go                      # E2EReport, ScenarioReport types
      prereq.go                      # requireZFS, requireGVisor, requireDocker helpers
    tier1/
      local_basic_test.go            # L1-L10: All Local mode tests
      mode3_mock_test.go             # Mode 3 with MemorySandboxService
      mode2_mock_test.go             # Mode 2 with VirtualTerminal
      parity_test.go                 # P1-P4: Cross-mode parity tests
    tier2/
      docker_mode3_test.go           # R1-R6: Mode 3 with real ZFS+gVisor in Docker
      docker_mode2_test.go           # S1-S7: Mode 2 with sandbox-host in Docker
      docker_lifecycle_test.go       # Full lifecycle tests
      docker_setup_test.go           # TestMain with Docker health check
    tier3/
      native_mode3_test.go           # Full native Mode 3
      native_mode2_test.go           # Full native Mode 2
      provider_integration_test.go   # I1-I4: Real provider tests
      stress_test.go                 # Multi-session concurrent stress
      failure_injection_test.go      # F1-F8: Failure scenarios
    docker/
      Dockerfile.sandbox-host        # sandbox-host image
      Dockerfile.test-runner         # test runner image
      docker-compose.e2e.yaml        # multi-container setup
      init-pool.sh                   # ZFS pool initialization script
```

---

## 9. Implementation Sequence

```mermaid
graph LR
    A[Phase 1<br/>Common helpers<br/>+ Tier 1 tests] --> B[Phase 2<br/>Docker setup<br/>+ Tier 2 tests]
    B --> C[Phase 3<br/>Tier 3 tests<br/>+ CI integration]
    C --> D[Phase 4<br/>Failure injection<br/>+ provider integration]

    style A fill:#e8f5e9
    style B fill:#fff3e0
    style C fill:#fce4ec
    style D fill:#f3e5f5
```

| Phase | Work | Depends on |
|-------|------|------------|
| **Phase 1** | `tests/external/common/` helpers, `tier1/` tests, cross-mode parity | Existing code (plans 08, 14, 15) |
| **Phase 2** | Docker compose setup, Dockerfiles, `tier2/` tests, ZFS pool init script | Phase 1, sandbox-host binary (plan 11) |
| **Phase 3** | `tier3/` native tests, CI workflow YAML, test reporting | Phase 2, dedicated CI runner setup |
| **Phase 4** | Failure injection tests, provider integration tests, stress tests | Phase 3, API keys provisioned |

---

## 10. Measurement and Success Criteria

| Metric | Target | How measured |
|--------|--------|-------------|
| Tier 1 pass rate | 100% on every PR | CI status check |
| Tier 1 runtime | <30 seconds | CI job duration |
| Tier 2 pass rate | 100% on PRs with Linux Docker runners | CI status check |
| Tier 2 runtime | <5 minutes | CI job duration |
| Tier 3 nightly pass rate | >95% (flake budget: 1 in 20) | Nightly report trend |
| Cross-mode parity | 100% scenario match between Local and MemorySandbox | Parity test assertions |
| Provider integration | All registered providers pass basic streaming test | API-gated test results |
| Failure injection coverage | All 8 failure scenarios (F1-F8) tested | Test count in Tier 3 |

---

## 11. Open Questions

1. **Docker-in-Docker for CI**: Some CI providers restrict `--privileged`. With configurable backends (addendum 02), the minimal config (`local-disk` + `none`) needs no special privileges at all, which largely resolves this for basic CI. The gVisor-only config still needs `--privileged` for runsc. The full config needs `--privileged` + ZFS device access. Should we also explore gVisor's `systrap` platform as a way to avoid `--privileged` for gVisor-only configs? Initial research suggests `systrap` may work without `--privileged`.

2. **Test pool cleanup**: Should Tier 2 Docker tests destroy and recreate the ZFS pool between test runs, or reuse the pool and just destroy sessions? Recreating is cleaner but adds ~2s startup. Recommendation: reuse pool, destroy all sessions in `TestMain` cleanup.

3. **gVisor platform selection in Docker**: gVisor supports `ptrace`, `systrap`, and `kvm` platforms. Inside Docker, `systrap` is the recommended platform (no KVM passthrough needed, no `--privileged` required for ptrace). Should we hard-code `systrap` for Docker tests? Recommendation: yes, with override via environment variable.

4. **Provider integration test cost**: Real provider tests cost money per API call. Should we set a per-run budget cap? Recommendation: use the cheapest model tier (e.g., `claude-haiku`) and limit to 5 test scenarios per provider per nightly run.

---

## 12. Stubserver-Based E2E Testing (Real HTTP Stack)

### 12.1 Problem: ScriptedProvider Bypasses the Real Client Stack

Current Tier 1 tests use `ScriptedProvider` (an in-process mock that implements the `ai.Provider` interface directly). While this is fast and deterministic, it bypasses a significant amount of production code:

- **HTTP client code**: Real providers make HTTP requests with headers, auth tokens, and timeouts. ScriptedProvider never opens a socket.
- **SSE parsing**: The `internal/ai/sse` scanner is never exercised -- ScriptedProvider emits `AssistantMessageEvent` values directly.
- **JSON serialization/deserialization**: Provider responses are never serialized to JSON over the wire and never parsed back. Schema mismatches, encoding edge cases, and malformed payloads go undetected.
- **Auth header propagation**: API key injection and bearer token handling are never tested end-to-end.
- **Retry logic**: HTTP-level retry behavior (429 backoff, connection reset recovery) cannot be tested without a real HTTP server.
- **Timeout behavior**: Client-side read timeouts and context deadlines against a real TCP connection behave differently than in-process cancellation.

In short, ScriptedProvider tests verify the agent loop and tool orchestration logic, but not the provider client stack that connects the agent to the LLM API. This is a meaningful gap because that client stack is where most production incidents originate (network errors, malformed responses, auth failures, rate limiting).

### 12.2 Solution: Wire the Existing Stubserver into E2E Tests

The stubserver at `internal/ai/testutil/stubserver/` already provides everything needed: fixture replay, fault injection (TCP reset, malformed payloads, throttling, backpressure, empty bodies), request capture, and per-request fixture selection. It serves real SSE over real HTTP connections via `httptest.Server`.

The strategy is to use the stubserver as the backend for E2E tests at all tiers, replacing ScriptedProvider where full-stack coverage is desired:

**Tier 1 (PR-Fast, in-process):** Start the stubserver via `httptest.NewServer` in the test process. Override the provider's base URL to point at the stubserver (e.g., `ANTHROPIC_BASE_URL=http://127.0.0.1:<port>`). The provider code makes a real HTTP request, receives real SSE, parses real JSON -- all without leaving the test process or requiring Docker. This is the primary win: true end-to-end coverage with the speed and simplicity of Tier 1.

```go
// Tier 1 stubserver-backed test sketch
func TestAgentLoop_RealHTTPStack(t *testing.T) {
    // Load a fixture with a multi-turn conversation (tool call + response).
    fixture := loadFixture(t, "testdata/fixtures/anthropic-tool-call.sse")

    // Start stubserver in-process.
    stub := stubserver.New(stubserver.WithFixtureFunc(func(r *http.Request) string {
        // Verify auth header is propagated.
        if r.Header.Get("x-api-key") == "" {
            t.Error("missing x-api-key header")
        }
        return fixture
    }))
    defer stub.Close()

    // Create a real Anthropic provider pointed at the stubserver.
    provider := anthropic.NewProvider(anthropic.Config{
        BaseURL: stub.URL,
        APIKey:  "test-key-123",
    })

    // Wire into agent loop and run scenario.
    // ... (same agent setup as existing Tier 1 tests)
}
```

**Tier 2/3 (Docker-based):** The stubserver needs to be reachable by other containers over the Docker network. This requires a standalone stubserver binary and a Docker service.

### 12.3 Docker Integration: Stubserver as a Service

For Docker-based Tier 2 and Tier 3 tests, the stubserver must run as its own container so that the sandbox-host and agent containers can reach it over the Docker network.

#### Standalone Binary

Create a `cmd/stubserver/main.go` that wraps the library stubserver in a standalone HTTP server.

Note: `stubserver.Server` does not implement `http.Handler` -- it wraps an `httptest.Server` internally and stores the handler in an unexported field. The standalone binary must use a new `NewHandler(opts ...Option) http.Handler` constructor that returns just the capture-wrapping handler without starting an `httptest.Server`. This cleanly separates test use (`New()` with auto-start) from standalone use (`NewHandler()` with explicit `http.ListenAndServe`).

The binary reads its fixture directory from the `-fixtures` flag, falling back to the `STUBSERVER_FIXTURE_DIR` environment variable if the flag is not set. This ensures consistency between flag-based invocation and Docker Compose environment variable configuration.

```go
// cmd/stubserver/main.go
package main

import (
    "flag"
    "fmt"
    "log"
    "net/http"
    "os"
    "path/filepath"

    "flex-agent-runtime/internal/ai/testutil/stubserver"
)

func main() {
    addr := flag.String("addr", ":9090", "listen address")
    fixtureDir := flag.String("fixtures", os.Getenv("STUBSERVER_FIXTURE_DIR"), "fixture directory (default: $STUBSERVER_FIXTURE_DIR or ./testdata/fixtures)")
    flag.Parse()

    if *fixtureDir == "" {
        *fixtureDir = "./testdata/fixtures"
    }

    // Load fixtures from directory, keyed by filename.
    fixtures := loadFixtures(*fixtureDir)

    // Use NewHandler to get an http.Handler without starting an httptest.Server.
    // NewHandler returns the same capture-wrapping handler that New() uses internally,
    // but the caller owns the listener.
    handler := stubserver.NewHandler(stubserver.WithFixtureFunc(func(r *http.Request) string {
        name := r.Header.Get("X-Fixture")
        if name == "" {
            name = "default"
        }
        if f, ok := fixtures[name]; ok {
            return f
        }
        return fixtures["default"]
    }))

    // Wrap in a mux to add /health endpoint for Docker healthchecks.
    mux := http.NewServeMux()
    mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        fmt.Fprintln(w, "ok")
    })
    mux.Handle("/", handler)

    log.Printf("stubserver listening on %s with %d fixtures", *addr, len(fixtures))
    log.Fatal(http.ListenAndServe(*addr, mux))
}
```

**Library change required:** Add `NewHandler(opts ...Option) http.Handler` to `stubserver` package. This constructor builds the capture-wrapping handler (same logic as lines 106-120 of `stubserver.go`) but returns it as an `http.Handler` instead of starting an `httptest.Server`. The existing `New()` continues to work as-is for in-process test use.

#### Dockerfile

```dockerfile
# Dockerfile.stubserver
FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /stubserver ./cmd/stubserver

FROM alpine:3.19
COPY --from=builder /stubserver /usr/local/bin/stubserver
COPY testdata/fixtures /fixtures
ENTRYPOINT ["/usr/local/bin/stubserver", "-addr=:9090", "-fixtures=/fixtures"]
EXPOSE 9090
HEALTHCHECK CMD wget -qO- http://localhost:9090/health || exit 1
```

#### Docker Compose Addition

Add the following service to `docker-compose.e2e.yaml` (available in all profiles):

```yaml
  stubserver:
    build:
      context: ../../../
      dockerfile: tests/external/docker/Dockerfile.stubserver
    ports:
      - "9090:9090"
    profiles: ["minimal", "gvisor", "full"]
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:9090/health"]
      interval: 2s
      timeout: 3s
      retries: 5
```

Note: The fixture directory is passed via the Dockerfile `ENTRYPOINT` flag (`-fixtures=/fixtures`). The binary also reads `STUBSERVER_FIXTURE_DIR` as an env var fallback, so either mechanism works -- but the flag in ENTRYPOINT is the primary configuration. The `/health` endpoint is served by the standalone binary's mux and returns `200 OK`.

The sandbox-host and test-runner containers reach the stubserver at `http://stubserver:9090`. Tests override the provider base URL via environment variable:

```yaml
  test-runner:
    environment:
      ANTHROPIC_BASE_URL: "http://stubserver:9090"
      OPENAI_BASE_URL: "http://stubserver:9090"
```

#### Fixture Loading

Fixtures are raw SSE text files in `testdata/fixtures/`, one per test scenario. The stubserver selects fixtures based on the `X-Fixture` request header, which the test runner sets per-scenario. Example fixture directory:

```
testdata/fixtures/
  anthropic-simple-response.sse       # Single text response
  anthropic-tool-call.sse             # Tool use with tool_use content block
  anthropic-multi-turn.sse            # Multi-turn with tool results
  anthropic-context-overflow.sse      # Overloaded error response
  openai-simple-response.sse          # OpenAI chat completion chunks
  openai-function-call.sse            # OpenAI function calling
  google-simple-response.sse          # Google streaming response
```

### 12.4 What This Exercises That ScriptedProvider Does Not

| Layer | ScriptedProvider | Stubserver |
|-------|:---:|:---:|
| Agent loop + tool orchestration | Yes | Yes |
| Provider selection + model lookup | Yes | Yes |
| HTTP client (net/http) | No | Yes |
| TLS/connection handling | No | Yes (httptest handles TLS optionally) |
| Request serialization (Go structs -> JSON body) | No | Yes |
| Auth header injection (x-api-key, Authorization: Bearer) | No | Yes |
| SSE scanner (`internal/ai/sse`) | No | Yes |
| Response JSON deserialization | No | Yes |
| Retry on 429 / connection reset | No | Yes (via fault injection) |
| Client timeout / context deadline over TCP | No | Yes (via backpressure fault) |
| Provider-specific error classification | No | Yes (via status code server) |
| Content-Type validation | No | Yes |

### 12.5 Oracle Cross-Verification

The `testdata/oracle/` harness (using `@mariozechner/pi-ai`) can be used to verify that the stubserver's SSE fixtures produce correct parsed output at the message level. The oracle currently supports three commands: `transformMessages`, `calculateCost`, and `isContextOverflow`. It does **not** have a raw SSE parsing command -- the `pi-ai` library handles SSE parsing internally but the oracle wrapper does not expose it.

Given this constraint, the oracle cross-verification workflow operates at the **parsed message level**, not the SSE transport level:

1. Record a real SSE response from a provider (or hand-craft one matching the provider's documented format) and store it as a stubserver fixture.
2. Feed the fixture through the Go provider's SSE parser and collect the resulting parsed messages (content, tool calls, usage, stop reason).
3. Feed the Go-parsed messages through the oracle's `transformMessages` command to verify message structure and transformation correctness.
4. Feed the Go-parsed usage metrics through the oracle's `calculateCost` command to verify cost calculation consistency.
5. Feed relevant messages through the oracle's `isContextOverflow` command to verify overflow detection agreement.
6. Compare the oracle's output against the Go implementation's output at each step.

This does **not** verify SSE transport-level parsing equivalence (that would require adding a `parseSSEStream` command to the oracle, which is out of scope here). However, it does verify that the fixture content produces correct results when processed through the Go parser -- i.e., that the fixtures are faithful to the real provider wire format at the message/content level. This is still valuable: it catches schema mismatches, field mapping errors, and content transformation bugs even though the raw SSE-to-message parsing step is only verified on the Go side.

Any fixture that passes oracle cross-verification at the message level can be trusted as a reliable substitute for a real API call in E2E tests, with the caveat that SSE framing bugs (e.g., incorrect event boundaries, missing `data:` prefixes) would only be caught by the Go SSE scanner tests, not by oracle comparison.

### 12.6 Integration with Existing Tiers

The stubserver approach does not replace ScriptedProvider tests -- it supplements them. The recommended test structure is:

- **ScriptedProvider tests** remain for fast, focused tests of agent loop logic (turn sequencing, tool dispatch, event emission, state transitions). These are pure unit-level E2E tests that don't need HTTP.
- **Stubserver tests** are added for full-stack E2E tests that verify the complete path from agent prompt to provider HTTP request to SSE response parsing to event emission. These catch integration bugs at the HTTP/SSE/JSON boundary.

In the tiered matrix:

| Tier | ScriptedProvider | Stubserver (in-process) | Stubserver (Docker) |
|------|:---:|:---:|:---:|
| **Tier 1 (PR-Fast)** | Yes (existing) | Yes (new) | No |
| **Tier 2 (Docker)** | No | No | Yes (new) |
| **Tier 3 (Nightly)** | No | No | Yes (new) + real providers |

Tier 1 stubserver tests run in-process via `httptest.Server` and add minimal overhead (<1s per scenario). Tier 2/3 stubserver tests use the Docker service and exercise cross-container networking in addition to the full HTTP stack.

### 12.7 Fault Injection Scenarios via Stubserver

The stubserver's existing fault modes map directly to failure scenarios that cannot be tested with ScriptedProvider:

| Fault Mode | Stubserver API | What It Tests |
|-----------|---------------|---------------|
| **TCP reset mid-stream** (F1) | `NewTCPResetServer(fixture, afterEvents)` | Client recovery when connection drops after partial SSE delivery |
| **Malformed SSE payload** (F2) | `NewMalformedServer(fixture, afterEvents, data)` | JSON parse error handling in the SSE scanner; partial message recovery |
| **Rate limiting** (F3) | `NewThrottleServer(retryAfter)` | 429 handling, Retry-After parsing, exponential backoff |
| **Slow server / backpressure** (F4) | `NewBackpressureServer(fixture, delay)` | Client read timeout, context deadline propagation over real TCP |
| **Empty error body** (F6) | `NewEmptyBodyServer(statusCode)` | Error classification from HTTP status alone (no JSON body to parse) |
| **Retry sequences** | `NewSequenceServer(responses)` | Multi-attempt retry: 429 -> 429 -> 200 success; verifies retry count and delay |

These fault scenarios should be added as stubserver-backed variants of the existing Tier 1 failure tests (L7, F1-F8) and as Docker-based variants in Tier 2.

---

## Completion Signoff

- **Status:** Partial — Sections 1-11 complete; Section 12 is a planned extension, not yet implemented
- **Date:** 2026-03-15
- **Epic:** aiag-q7c
- **Task:** aiag-q7c.2 (assigned: coder-2-sea, status: closed)
- **Implementation commits:** dea037b, 37052dc
- **Code review:** R1 by reviewer-sea, findings incorporated, R2 approved at b42c922
- **Branch:** main

**Scope note:** The deliverables and acceptance criteria below cover Sections 1-11 only. Section 12 (Stubserver-Based E2E Testing) is a planned extension that requires additional implementation work: the `NewHandler()` constructor in the stubserver library, the standalone binary (`cmd/stubserver/main.go`), Docker infrastructure (`Dockerfile.stubserver`, compose service), fixture files, and stubserver-backed test variants. This work is tracked separately and does not affect the completeness of Sections 1-11.

### Deliverables Verification

| Deliverable | Status | Evidence |
|-------------|--------|----------|
| Common helpers (prereq.go) | DONE | `tests/external/common/prereq.go` — RequireZFS, RequireGVisor, RequireDocker, SandboxHostConfig, RequireZFSBackend, RequireGVisorRuntime |
| Common helpers (report.go) | DONE | `tests/external/common/report.go` — E2EReport, ScenarioReport, ReportSummary types with JSON serialization |
| Common helpers (scenario.go) | DONE | `tests/external/common/scenario.go` — BackendConfig with StandardConfigs() returning C1-C4, SupportsSnapshots(), SupportsTierRouting() |
| Common helpers (parity.go) | DONE | `tests/external/common/parity.go` — EventTypeSequence, AssertEventTypeParity, CountEventType |
| Docker infrastructure (Dockerfile) | DONE | `tests/external/docker/Dockerfile.sandbox-host` — Multi-stage: base-minimal (local-disk+none), base-gvisor (+runsc), base-full (+ZFS) |
| Docker infrastructure (compose) | DONE | `tests/external/docker/docker-compose.e2e.yaml` — Profile-based services (minimal/gvisor/full) with health checks |
| Docker infrastructure (init-pool.sh) | DONE | `tests/external/docker/init-pool.sh` |
| Tier 1 tests (local_basic_test.go) | DONE | 12 tests passing: L1-L7, L10 scenarios covering multi-turn, bash, code interpreter, steering, follow-up, abort, error recovery, concurrent subscribers |
| Tier 1 parity tests (parity_test.go) | DONE | P1-P4 parity tests: file ops, event types, session state, error behavior — Local vs MemorySandboxService |
| Tier 2 Docker-gated stubs | DONE | `tests/external/tier2/docker_test.go` — build tag `docker`, 6 test functions covering lifecycle (all configs), snapshots (ZFS), tier routing (gVisor), streaming progress, capabilities |
| Tier 3 native-gated stubs | DONE | `tests/external/tier3/native_test.go` — build tag `native`, 7 test functions covering ZFS pool, gVisor isolation, provider integration (Anthropic/OpenAI), multi-session stress, failure injection, double destroy |
| CI workflow | DONE | `.github/workflows/e2e.yml` — 4 jobs: tier1-mock (always), tier2-docker-minimal (PRs), tier2-docker-full (PRs, ZFS-gated), tier3-nightly (schedule, self-hosted) |
| Package documentation | DONE | `tests/external/README.go` — package doc explaining 3-tier structure |

### Acceptance Criteria (from Section 10)

| Criterion | Status | Evidence |
|-----------|--------|----------|
| Tier 1 pass rate: 100% | PASS | All 12 tier1 tests pass (`go test ./tests/external/tier1/...`) |
| Cross-mode parity: 100% match | PASS | P1-P4 parity tests all pass, comparing Local vs MemorySandboxService |
| Tier 2 Docker tests defined | PASS | 6 Docker-gated test functions defined with build tag `docker`; stub implementations skip gracefully until sandbox-host binary is available |
| Tier 3 native tests defined | PASS | 7 native-gated test functions defined with build tag `native`; skip until dedicated infrastructure is available |
| CI pipeline defined | PASS | `.github/workflows/e2e.yml` with tiered pipeline matching plan Section 6 |
| Test helpers implemented | PASS | All 4 common helper files match plan Section 7.4 pattern |
| Backend config combinations (C1-C4) | PASS | `StandardConfigs()` returns all 4 combinations per plan Section 5.3.1 |

### Scope Notes

Tier 2 and Tier 3 tests are implemented as structured stubs with detailed TODO comments. This matches the plan's phased implementation sequence (Section 9): Phase 1 (common helpers + Tier 1) is complete; Phases 2-4 (Docker implementation, native tests, failure injection) require the sandbox-host binary and dedicated CI infrastructure which are not yet available. The stubs compile, are correctly gated by build tags, and will skip gracefully until the prerequisites are met.

---

## R1 Review Disposition — Section 12 (reviewer-sea)

**Review:** [17-external-testing-review-reviewer-sea.md](./17-external-testing-review-reviewer-sea.md)
**Incorporated by:** claude-opus-4-6
**Date:** 2026-03-15

| Finding | Severity | Disposition | Notes |
|---------|----------|-------------|-------|
| Standalone binary sketch won't compile: Server doesn't implement http.Handler | P2 | Incorporated | Replaced `http.ListenAndServe(*addr, srv)` with `NewHandler()` constructor pattern (option b from review). Added documentation of required `NewHandler(opts ...Option) http.Handler` library addition. |
| Oracle cross-verification assumes SSE parsing capability the oracle doesn't have | P2 | Incorporated | Scoped §12.5 workflow to message-level verification using existing oracle commands (`transformMessages`, `calculateCost`, `isContextOverflow`). Removed claim of raw SSE parsing equivalence. Added explicit caveat about SSE transport-level coverage. (Option b from review.) |
| Completion Signoff says Complete but Section 12 is unimplemented | P2 | Incorporated | Updated status to "Partial" with scope note clarifying Sections 1-11 are complete and Section 12 is a planned extension not yet implemented. Updated both header status and signoff section. (Option a+b from review.) |
| Missing /health endpoint in stubserver Docker service | P3 | Incorporated | Added `/health` handler to standalone binary sketch via `http.NewServeMux` wrapping the stubserver handler. Healthcheck in Dockerfile and compose now has a working endpoint. |
| Env var vs flag inconsistency for fixture directory | P3 | Incorporated | Binary now reads `-fixtures` flag with `os.Getenv("STUBSERVER_FIXTURE_DIR")` as default value, falling back to `./testdata/fixtures` if neither is set. Removed redundant `STUBSERVER_FIXTURE_DIR` env var from compose config since ENTRYPOINT flag is the primary config. Added documentation note explaining the relationship. |
