# 17: External / End-to-End Testing Plan

**Status:** Draft
**Depends on:** 08-agent-tools-e2e, 14-mode3-e2e, 15-mode2-e2e, 16-runtime-test-harness, 11-sandbox-host-service.add01
**Depended on by:** --
**Scope:** External E2E testing strategy covering usage examples, Docker-based CI, dedicated host testing, mock-based testing, and CI integration across all placement modes.

---

## 1. Overview

This plan defines how to test the h2-agent-runtime end-to-end from the outside -- as a consumer of the Go library and the `sandbox-host` binary would. It complements the existing internal E2E tests in `e2etests/` (plans 08, 14, 15, 16) by adding:

- **Concrete usage examples** showing how callers wire up agents in each placement mode.
- **Docker-based CI** that runs the full ZFS + gVisor stack without dedicated infrastructure.
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

    "h2-agent-runtime/internal/agent"
    "h2-agent-runtime/internal/ai"
    "h2-agent-runtime/internal/sandbox/environment/local"
    "h2-agent-runtime/internal/tools"
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

    "h2-agent-runtime/internal/agent"
    "h2-agent-runtime/internal/rpc/client"
    "h2-agent-runtime/internal/termmux"
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

    "h2-agent-runtime/internal/agent"
    "h2-agent-runtime/internal/ai"
    "h2-agent-runtime/internal/rpc/client"
    "h2-agent-runtime/internal/sandbox/environment/native"
    "h2-agent-runtime/internal/tools"
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
    style T3A fill:#fce4ec
    style T3B fill:#fce4ec
    style T3C fill:#fce4ec
    style T3D fill:#fce4ec
```

| Tier | What | Where it runs | ZFS | gVisor | LLM Provider | Duration |
|------|------|---------------|-----|--------|--------------|----------|
| **PR-Fast** | Mock-based E2E, cross-mode parity | Any CI runner (Linux/Mac) | No | No | ScriptedProvider | <30s |
| **PR-Standard** | Docker-based full stack E2E | Linux CI with Docker | Yes (in container) | Yes (in container) | ScriptedProvider | <5min |
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
    CI->>Test: go test ./e2etests/external/...
    Test->>Agent: New(NativeDriver)
    Agent->>Env: LocalEnvironment or MemorySandboxService
    Env-->>Agent: tool results (in-memory)
    Agent-->>Test: events + session state
    Test->>Test: assert parity across modes

    Note over CI,GV: Tier 2: Docker-Based
    CI->>CI: docker compose up (sandbox-host)
    CI->>Test: go test -tags=docker ./e2etests/external/docker/...
    Test->>SH: RPC: CreateSession
    SH->>ZFS: zfs clone base@snap
    SH-->>Test: session_id
    Test->>Agent: New(NativeDriver, NativeSandboxEnvironment)
    Agent->>SH: RPC: ExecuteTool
    SH->>GV: runsc run (Tier 2 tools)
    GV-->>SH: result
    SH-->>Agent: ToolResponse + snapshot
    Agent-->>Test: events + session state

    Note over CI,GV: Tier 3: Dedicated Host
    CI->>Test: go test -tags=native ./e2etests/external/native/...
    Test->>SH: sandbox-host (local process)
    SH->>ZFS: real ZFS commands
    SH->>GV: real gVisor containers
```

---

## 4. Docker Test Environment Setup

### 4.1 Dockerfile for Test Image

The test image bundles ZFS userland tools, gVisor (runsc), the `sandbox-host` binary, and the Go test runner.

```dockerfile
# syntax=docker/dockerfile:1
FROM ubuntu:22.04 AS base

# ZFS userland
RUN apt-get update && apt-get install -y --no-install-recommends \
    zfsutils-linux \
    kmod \
    ca-certificates \
    curl \
    && rm -rf /var/lib/apt/lists/*

# gVisor runsc
RUN curl -fsSL https://gvisor.dev/archive.key | gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg \
    && echo "deb [arch=amd64 signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main" \
    > /etc/apt/sources.list.d/gvisor.list \
    && apt-get update && apt-get install -y runsc \
    && rm -rf /var/lib/apt/lists/*

# Go (for running tests inside the container)
COPY --from=golang:1.23 /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"

WORKDIR /src
COPY . .

# Build sandbox-host binary
RUN go build -o /usr/local/bin/sandbox-host ./cmd/sandbox-host

# Test entrypoint
ENTRYPOINT ["go", "test"]
CMD ["-tags=docker", "-v", "-timeout=5m", "./e2etests/external/docker/..."]
```

### 4.2 Docker Compose for Multi-Container Testing

```yaml
# docker-compose.e2e.yaml
version: "3.8"

services:
  sandbox-host:
    build:
      context: .
      dockerfile: e2etests/external/docker/Dockerfile.sandbox-host
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
    command: []

  test-runner:
    build:
      context: .
      dockerfile: e2etests/external/docker/Dockerfile.test-runner
    depends_on:
      sandbox-host:
        condition: service_healthy
    environment:
      SANDBOX_HOST_URL: "http://sandbox-host:8080"
      SANDBOX_AUTH_TOKEN: "e2e-test-token"
      E2E_TIER: "docker"
    volumes:
      - ./:/src:ro
    working_dir: /src

  # Initialization: create ZFS pool and base snapshot
  init-pool:
    build:
      context: .
      dockerfile: e2etests/external/docker/Dockerfile.sandbox-host
    privileged: true
    cap_add: [SYS_ADMIN]
    devices: [/dev/zfs:/dev/zfs]
    volumes:
      - zfs-pool:/var/lib/zfs-test
    entrypoint: ["/bin/bash", "-c"]
    command:
      - |
        set -e
        # Create a file-backed ZFS pool if it doesn't exist
        if ! zpool list testpool 2>/dev/null; then
          truncate -s 2G /var/lib/zfs-test/pool.img
          zpool create testpool /var/lib/zfs-test/pool.img
        fi
        # Create base datasets
        zfs create -p testpool/bases
        zfs create -p testpool/sessions
        # Create a base snapshot with a minimal workspace
        zfs create testpool/bases/ubuntu-base
        echo '{"ready": true}' > /testpool/bases/ubuntu-base/.workspace-ready
        zfs snapshot testpool/bases/ubuntu-base@ready
        echo "ZFS pool initialized successfully"

volumes:
  zfs-pool:
```

### 4.3 Required Host Prerequisites

| Requirement | Docker-based | Dedicated host |
|-------------|-------------|----------------|
| Linux kernel | 5.10+ (host) | 5.10+ |
| ZFS kernel module | Loaded on host (`modprobe zfs`) | Loaded (`zfs-dkms` or built-in) |
| gVisor (runsc) | Installed in container | Installed on host |
| Docker mode | `--privileged` or `--cap-add SYS_ADMIN,NET_RAW` + `--device /dev/zfs` | N/A |
| Disk space | 4GB+ for ZFS pool image | 20GB+ ZFS pool on real disk/EBS |

### 4.4 Known Limitations and Workarounds

| Limitation | Impact | Workaround |
|-----------|--------|------------|
| ZFS in Docker requires host kernel module | Cannot run on CI hosts without `zfs.ko` | Use CI runners with ZFS support (e.g., Ubuntu runners with `zfs-dkms`), or skip Tier 2 tests |
| `--privileged` required | Security concern in shared CI | Use dedicated CI runners for Tier 2, or use rootless containers with user-namespace ZFS (experimental) |
| gVisor `--net-raw` | Network isolation in gVisor needs raw socket cap | Pass `--cap-add NET_RAW` in Docker; gVisor's own network stack handles isolation |
| File-backed ZFS pool performance | Slower than real block devices | Acceptable for CI tests; keep pool size small (2-4GB) |
| macOS Docker Desktop | No ZFS kernel module available | Skip Tier 2; use Tier 1 (mock-based) for Mac development |

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

These tests require ZFS + gVisor (Docker or dedicated host).

| Scenario | Description | Assertions |
|----------|-------------|------------|
| **S1: Full lifecycle** | Create session -> execute tools -> snapshot -> pause -> resume -> destroy | Each lifecycle transition succeeds; state transitions are valid |
| **S2: ZFS snapshot correctness** | Write files, snapshot, write more, rollback | After rollback, filesystem matches snapshot state exactly |
| **S3: Per-turn snapshots** | Run 3-turn agent; verify a snapshot exists per turn | Snapshot count matches turn count; each snapshot captures correct state |
| **S4: 3rd party driver launch** | Launch Claude Code driver in sandbox via terminal mux | Driver produces normalized events; session log captured |
| **S5: Credential injection** | Inject API keys into sandbox environment | Driver can authenticate; credentials not leaked in events |
| **S6: Pause/resume preserves state** | Multi-turn conversation, pause, resume, continue | Conversation log intact after resume; filesystem unchanged |
| **S7: gVisor isolation** | Tier 2 bash commands run inside gVisor container | Process isolation verified (PID namespace, filesystem root); resource limits enforced |

### 5.3 Agent outside Sandbox -- Mode 3 (Tier 2/3)

These tests verify RPC dispatch from a local agent to a remote sandbox-host.

| Scenario | Description | Assertions |
|----------|-------------|------------|
| **R1: RPC happy path** | Agent dispatches read/write/bash over RPC | Tool results match expected; RPC round-trip succeeds |
| **R2: Tier routing** | Mix of Tier 1 (read, grep) and Tier 2 (bash) calls | Tier 1 runs without container; Tier 2 runs in gVisor; both return correct results |
| **R3: Streaming progress** | Bash tool emits progress updates over RPC stream | Progress events received by agent before final result |
| **R4: Snapshot via RPC** | Create snapshot after tool execution via NativeSandboxEnvironment | Snapshot ID returned; rollback via RPC restores state |
| **R5: Session lifecycle over RPC** | Create -> execute -> pause -> resume -> execute -> destroy | All RPC calls succeed; state transitions valid |
| **R6: Concurrent tool calls** | Agent dispatches 3 tool calls in parallel (if supported) | All complete successfully; no race conditions |

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
| (none) | Tier 1 mock-based tests | Always; `go test ./e2etests/external/...` |
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
      - run: go test -v -timeout=2m ./e2etests/external/...

  tier2-docker:
    name: "Tier 2: Docker E2E"
    runs-on: ubuntu-latest
    if: github.event_name == 'pull_request'
    needs: tier1-mock
    steps:
      - uses: actions/checkout@v4
      - name: Load ZFS kernel module
        run: sudo modprobe zfs || echo "ZFS module not available; skipping"
      - name: Run Docker E2E
        run: |
          if lsmod | grep -q zfs; then
            docker compose -f docker-compose.e2e.yaml up --build --abort-on-container-exit
          else
            echo "::warning::ZFS kernel module not available. Skipping Tier 2 tests."
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
        run: go test -v -tags=native -timeout=20m ./e2etests/external/native/...
        env:
          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
          OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}
      - name: Upload test report
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: e2e-report
          path: e2etests/external/reports/
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

## 7. Testing without ZFS -- LocalEnvironment on Any Platform

### 7.1 How LocalEnvironment Enables Universal Testing

`LocalEnvironment` (`internal/sandbox/environment/local/local.go`) implements the `ExecutionEnvironment` interface without any ZFS or gVisor dependency. It executes tools directly on the local filesystem. This means:

- **Mac development**: Developers can run the full Tier 1 E2E suite on macOS without any special setup.
- **Windows CI**: The same tests run on Windows (tools use Go's `os` package, which is cross-platform).
- **Cloud CI without ZFS**: Standard GitHub Actions Ubuntu runners can run Tier 1 without `zfs.ko`.
- **ARM64 CI**: Works on ARM-based runners (e.g., GitHub's ARM64 runners, Graviton EC2).

The key constraint is that `LocalEnvironment` does not support snapshots or rollback:

```go
func (e *LocalEnvironment) Capabilities() environment.Capabilities {
    return environment.LocalCapabilities
    // LocalCapabilities = { Snapshots: false, Rollback: false, Pause: true, ... }
}

func (e *LocalEnvironment) CreateSnapshot(context.Context, string) (*environment.SnapshotInfo, error) {
    return nil, environment.ErrCapabilityNotSupported
}
```

Tests that exercise snapshot/rollback behavior use `MemorySandboxService` (the in-memory fake from `e2etests/mode3/harness/`) which simulates ZFS semantics without requiring the kernel module.

### 7.2 Platform Test Matrix

| Platform | Tier 1 (Mock) | Tier 2 (Docker) | Tier 3 (Native) |
|----------|:---:|:---:|:---:|
| macOS (dev laptop) | Yes | No | No |
| Ubuntu CI (no ZFS module) | Yes | No | No |
| Ubuntu CI (with ZFS module) | Yes | Yes | No |
| Dedicated Linux host (ZFS + gVisor) | Yes | Yes | Yes |
| Windows CI | Yes | No | No |
| ARM64 Linux | Yes | Possible (with ARM gVisor) | Possible |

### 7.3 Graceful Degradation Pattern

Tests detect the available environment at runtime and skip unsupported tiers:

```go
func requireZFS(t *testing.T) {
    t.Helper()
    if _, err := exec.LookPath("zfs"); err != nil {
        t.Skip("zfs not found; skipping ZFS-dependent test")
    }
    // Verify the module is loaded
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
```

---

## 8. File Structure

```
e2etests/
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
| **Phase 1** | `e2etests/external/common/` helpers, `tier1/` tests, cross-mode parity | Existing code (plans 08, 14, 15) |
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

1. **Docker-in-Docker for CI**: Some CI providers restrict `--privileged`. Should we support a `--device` + `--cap-add` approach as a less-privileged alternative? Initial research suggests `SYS_ADMIN` + device access is sufficient for ZFS but not for gVisor's `ptrace` platform. The `systrap` platform may work without `--privileged`.

2. **Test pool cleanup**: Should Tier 2 Docker tests destroy and recreate the ZFS pool between test runs, or reuse the pool and just destroy sessions? Recreating is cleaner but adds ~2s startup. Recommendation: reuse pool, destroy all sessions in `TestMain` cleanup.

3. **gVisor platform selection in Docker**: gVisor supports `ptrace`, `systrap`, and `kvm` platforms. Inside Docker, `systrap` is the recommended platform (no KVM passthrough needed, no `--privileged` required for ptrace). Should we hard-code `systrap` for Docker tests? Recommendation: yes, with override via environment variable.

4. **Provider integration test cost**: Real provider tests cost money per API call. Should we set a per-run budget cap? Recommendation: use the cheapest model tier (e.g., `claude-haiku`) and limit to 5 test scenarios per provider per nightly run.
