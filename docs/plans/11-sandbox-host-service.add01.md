# 11 Addendum 01: ExecutionEnvironment Abstraction

**Parent plan:** [11-sandbox-host-service.md](./11-sandbox-host-service.md)
**Status:** Approved
**Scope:** Unified `ExecutionEnvironment` interface replacing both `ToolBackend` (LocalBackend + SandboxBackend) and the sandbox provider layer. Implementations for local, native sandbox (ZFS/gVisor), E2B, Daytona, and Fly.io. Migration path from current `ToolBackend` split.
**Integrates with:** Plan 06 (ToolBackend), Plan 11 (SandboxHostService), Plan 13 (RPC Layer), Architecture (Placement Modes)

---

## 1. Overview

The current sandbox architecture has two separate interface hierarchies that need to be unified:

1. **ToolBackend** (Plan 06) with `LocalBackend` and `SandboxBackend` — determines *how* tools execute
2. **Sandbox lifecycle** managed directly by the RuntimeController via RPC calls

This addendum replaces both with a single **`ExecutionEnvironment`** interface that covers lifecycle, tool execution, and optional capabilities (snapshots, rollback). Every environment — from "run everything locally with no sandbox" to "use E2B cloud sandboxes" — implements the same interface.

**Key insight:** There is no meaningful distinction between "how tools execute" (ToolBackend) and "what sandbox to use" (provider). They are the same decision. An `ExecutionEnvironment` IS the backend. The agent loop calls `ExecutionEnvironment.ExecuteTool()`. The RuntimeController calls lifecycle methods on the same interface.

**What changes:**
- New `ExecutionEnvironment` interface in `internal/sandbox/environment` — the single unified contract
- `ToolBackend` interface goes away — `ExecutionEnvironment` subsumes it
- `SandboxBackend` wrapper goes away — no more intermediary layer
- `LocalBackend` becomes `LocalEnvironment` (lifecycle methods are no-ops)
- `NativeSandboxEnvironment` wraps ConnectRPC to SandboxHostService
- `E2BSandboxEnvironment`, `DaytonaSandboxEnvironment`, `FlySandboxEnvironment` implement the same interface against their respective APIs
- Capability detection system so the RuntimeController/orchestrator knows what each environment supports

**What does NOT change:**
- `SandboxHostService` and all ZFS/gVisor internals (plan 11 core)
- RPC layer (plan 13) — unchanged, becomes an implementation detail of `NativeSandboxEnvironment`
- Agent loop tool dispatch — still calls `ExecuteTool()`, just on `ExecutionEnvironment` instead of `ToolBackend`

---

## 2. Architecture

### 2.1 Component Diagram

```mermaid
graph TB
    subgraph "Agent Loop (internal/agent)"
        agent[Agent / NativeDriver<br/>calls ExecutionEnvironment.ExecuteTool]
    end

    subgraph "Execution Environment (internal/sandbox/environment)"
        iface[ExecutionEnvironment interface]
        caps[Capabilities struct]

        subgraph "Implementations"
            localenv[LocalEnvironment<br/>Direct local execution<br/>Lifecycle = no-ops]
            native[NativeSandboxEnvironment<br/>ConnectRPC → SandboxHostService]
            e2b[E2BSandboxEnvironment<br/>E2B REST API]
            daytona[DaytonaSandboxEnvironment<br/>Daytona API]
            fly[FlySandboxEnvironment<br/>Fly Machines API]
        end
    end

    subgraph "Existing Infrastructure"
        subgraph "internal/rpc/client"
            rpcclient[SandboxClient<br/>ConnectRPC client]
        end
        subgraph "internal/sandbox"
            shs[SandboxHostService<br/>ZFS + gVisor coordinator]
        end
        subgraph "External APIs"
            e2bapi[E2B API]
            dayapi[Daytona API]
            flyapi[Fly Machines API]
        end
    end

    agent --> iface
    iface --> localenv
    iface --> native
    iface --> e2b
    iface --> daytona
    iface --> fly
    native --> rpcclient
    rpcclient --> shs
    e2b --> e2bapi
    daytona --> dayapi
    fly --> flyapi

    style iface fill:#fce4ec
    style localenv fill:#e8f5e9
    style native fill:#e8f5e9
    style e2b fill:#e1f5fe
    style daytona fill:#e1f5fe
    style fly fill:#e1f5fe
```

### 2.2 Placement Mode Mapping

```mermaid
graph LR
    subgraph "All Local"
        al_agent[Agent Loop] --> al_env[LocalEnvironment]
        al_env --> al_fs[Local Filesystem]
    end

    subgraph "Agent in Sandbox"
        ais_rc[RuntimeController] -->|lifecycle| ais_sandbox[Sandbox Environment<br/>Native/E2B/Daytona/Fly]
        ais_agent[Agent inside sandbox] --> ais_env[LocalEnvironment]
        ais_env --> ais_fs[Sandbox Filesystem]
    end

    subgraph "Agent outside Sandbox"
        aos_rc[RuntimeController] -->|lifecycle| aos_env[Sandbox Environment<br/>Native/E2B/Daytona/Fly]
        aos_agent[Agent Loop] -->|ExecuteTool| aos_env
    end
```

**All Local** -- `LocalEnvironment`. Create/Pause/Resume/Destroy are no-ops. ExecuteTool runs tools directly on the local filesystem. No sandbox involved.

**Agent in Sandbox** -- The RuntimeController uses a sandbox environment (NativeSandboxEnvironment, E2BSandboxEnvironment, etc.) for lifecycle: create the sandbox, put the agent in it. The agent running *inside* the sandbox uses `LocalEnvironment` for tool execution (tools are local to that sandbox).

**Agent outside Sandbox** -- The RuntimeController uses a sandbox environment for *both* lifecycle AND tool execution. The agent loop calls `ExecuteTool()` on the sandbox environment directly. Tool calls are dispatched to the sandbox infrastructure.

### 2.3 Call Flow: Agent Loop to Environment

```mermaid
sequenceDiagram
    participant RC as RuntimeController
    participant Agent as Agent Loop
    participant Env as ExecutionEnvironment

    Note over RC,Env: Session creation (before agent starts)
    RC->>Env: Create(ctx, SessionConfig)
    Env-->>RC: nil (success)

    Note over Agent,Env: Agent execution
    Agent->>Env: ExecuteTool(ctx, ToolRequest, onProgress)
    Env-->>Agent: *ToolResponse

    Note over Agent,Env: Optional: snapshot at turn boundary
    Agent->>Env: CreateSnapshot(ctx, "turn-3")
    Env-->>Agent: *SnapshotInfo (or ErrCapabilityNotSupported)

    Note over RC,Env: Session pause (between turns)
    RC->>Env: Pause(ctx)
    Env-->>RC: nil

    Note over RC,Env: Session resume
    RC->>Env: Resume(ctx)
    Env-->>RC: nil

    Note over RC,Env: Session teardown
    RC->>Env: Destroy(ctx)
    Env-->>RC: nil
```

### 2.4 Import Flow

```
internal/sandbox/environment             → internal/tools (type aliases: ToolRequest, ToolResponse, ToolProgress)
internal/sandbox/environment/local       → internal/sandbox/environment, internal/tools (tool execution logic)
internal/sandbox/environment/native      → internal/sandbox/environment, internal/rpc/client
internal/sandbox/environment/e2b         → internal/sandbox/environment, net/http
internal/sandbox/environment/daytona     → internal/sandbox/environment, net/http
internal/sandbox/environment/fly         → internal/sandbox/environment, net/http
internal/agent                           → internal/sandbox/environment (calls ExecuteTool)
```

No circular imports. The environment interface package imports `internal/tools` for type aliases (`ToolRequest`, `ToolResponse`, `ToolProgress`). This is a one-way dependency — `internal/tools` must NOT import `internal/sandbox/environment`. Each implementation imports only the interface package and its own dependencies.

---

## 3. ExecutionEnvironment Interface

### 3.1 Core Interface

```go
// Package: internal/sandbox/environment
// File: environment.go

// ExecutionEnvironment is the unified interface for tool execution environments.
// It covers lifecycle management, tool execution, and optional capabilities
// like snapshots and rollback.
//
// Every placement mode maps to an ExecutionEnvironment:
//   - All Local: LocalEnvironment (lifecycle no-ops, direct local exec)
//   - Agent in Sandbox: agent inside uses LocalEnvironment; RuntimeController
//     uses a sandbox environment for lifecycle
//   - Agent outside Sandbox: sandbox environment for both lifecycle and tool exec
//
// Implementations:
//   - LocalEnvironment: direct local filesystem execution, no sandbox
//   - NativeSandboxEnvironment: ConnectRPC to SandboxHostService (ZFS + gVisor)
//   - E2BSandboxEnvironment: E2B sandbox API
//   - DaytonaSandboxEnvironment: Daytona workspace API
//   - FlySandboxEnvironment: Fly.io Machines API
type ExecutionEnvironment interface {
    // --- Lifecycle ---

    // Create initializes the execution environment.
    // For LocalEnvironment: no-op (local filesystem is always available).
    // For NativeSandboxEnvironment: creates a ZFS dataset clone from base snapshot.
    // For E2BSandboxEnvironment: creates a sandbox from a template.
    // For DaytonaSandboxEnvironment: creates a workspace.
    // For FlySandboxEnvironment: creates a Machine with a volume.
    Create(ctx context.Context, config SessionConfig) error

    // Pause suspends the environment with minimal idle compute cost.
    // For LocalEnvironment: no-op (succeeds immediately).
    // For NativeSandboxEnvironment: transitions session state to paused (ZFS persists).
    // For E2BSandboxEnvironment: sandbox.pause() (full state preserved).
    // For DaytonaSandboxEnvironment: returns ErrCapabilityNotSupported (auto-stop is lossy).
    // For FlySandboxEnvironment: machine.suspend() (memory saved to disk).
    //
    // Capability contract: if Capabilities().Pause is true, Pause()/Resume()
    // will succeed (even if the implementation is a no-op). If Capabilities().Pause
    // is false, Pause()/Resume() MUST return ErrCapabilityNotSupported.
    Pause(ctx context.Context) error

    // Resume re-activates a previously paused environment.
    // Returns ErrCapabilityNotSupported if Capabilities().Pause is false.
    Resume(ctx context.Context) error

    // Destroy tears down the environment and all associated resources.
    // For LocalEnvironment: no-op.
    // For sandbox environments: destroys the sandbox/machine/workspace and volumes.
    Destroy(ctx context.Context) error

    // --- Tool Execution ---

    // ExecuteTool runs a tool in this environment.
    // For LocalEnvironment: executes directly on local filesystem/processes.
    // For sandbox environments: dispatches to the sandbox via RPC/API.
    // onProgress streams incremental output for long-running tools (e.g., bash).
    ExecuteTool(ctx context.Context, req ToolRequest, onProgress func(ToolProgress)) (*ToolResponse, error)

    // --- State & Capabilities ---

    // State returns the current lifecycle state of this environment.
    // Remote environments track state internally (protected by mu).
    // NativeSandboxEnvironment queries the server via RPC.
    // LocalEnvironment returns StateActive (or StateDestroyed if Destroy was called).
    State() SessionState

    // Capabilities returns the static capability set for this environment.
    // Callers use this to adapt behavior (e.g., skip snapshot calls if not supported).
    Capabilities() Capabilities

    // CreateSnapshot creates a named snapshot of the environment's current state.
    // For NativeSandboxEnvironment: creates a ZFS snapshot (instant, COW).
    // Returns ErrCapabilityNotSupported for environments without snapshot support.
    CreateSnapshot(ctx context.Context, name string) (*SnapshotInfo, error)

    // Rollback restores the environment to a prior snapshot.
    // For NativeSandboxEnvironment: ZFS rollback (<100ms).
    // Returns ErrCapabilityNotSupported for environments without rollback support.
    Rollback(ctx context.Context, snapshotID string) error
}
```

### 3.2 Shared Types

```go
// Package: internal/sandbox/environment
// File: types.go

// SessionConfig carries parameters for environment creation.
// Environment-specific fields are passed via Options.
type SessionConfig struct {
    // BaseImage identifies the starting filesystem state.
    // For NativeSandboxEnvironment: ZFS snapshot name (e.g., "pool/bases/repo-v1@initial").
    // For E2BSandboxEnvironment: template ID.
    // For DaytonaSandboxEnvironment: workspace template/image.
    // For FlySandboxEnvironment: Docker image reference or volume snapshot.
    // For LocalEnvironment: ignored (uses local filesystem as-is).
    BaseImage string

    // SessionID is the caller-assigned session ID. Required — Create() returns
    // an error if empty. The caller (RuntimeController) owns session identity;
    // environments do not generate IDs.
    SessionID string

    // Labels are arbitrary metadata attached to the session.
    Labels map[string]string

    // Options carries environment-specific configuration.
    // Each environment implementation defines its own options struct.
    Options any
}

// SnapshotInfo describes a snapshot of the environment state.
type SnapshotInfo struct {
    ID        string
    Name      string
    CreatedAt time.Time
    SpaceUsed int64 // bytes; 0 if environment doesn't track this
}

// SessionState represents the lifecycle state of an execution environment.
// String values match plan 11's SessionState constants for NativeSandboxEnvironment
// compatibility — the server returns these values and NativeSandboxEnvironment
// passes them through without mapping.
type SessionState string

const (
    StateCreating   SessionState = "creating"   // Create() in progress
    StateActive     SessionState = "active"      // ready for ExecuteTool
    StatePaused     SessionState = "paused"      // Pause() called, Resume() to reactivate
    StateDestroying SessionState = "destroying"  // Destroy() in progress
    StateDestroyed  SessionState = "destroyed"   // client-side only: Destroy() completed
    StateFailed     SessionState = "failed"      // unrecoverable error
)

// Note: StateDestroyed is addendum-specific. Plan 11's server-side sessions
// transition to "destroying" and then are deleted (no "destroyed" state).
// Remote environments (E2B, Daytona, Fly) use StateDestroyed to track
// client-side cleanup completion. NativeSandboxEnvironment never enters
// StateDestroyed — after Destroy(), the session is gone from the server.

// ToolRequest and ToolResponse are re-exported from internal/tools.
// This avoids duplicating types. Environments import internal/tools for these.
// Note: ToolRequest.SessionID is vestigial — ExecutionEnvironment implementations
// do not read it. Session identity is internal to each environment, set during
// Create(). This field will be removed as part of the ToolBackend migration (§7.3).
type ToolRequest = tools.ToolRequest
type ToolResponse = tools.ToolResponse
type ToolProgress = tools.ToolProgress
```

### 3.3 Error Sentinels

```go
// Package: internal/sandbox/environment
// File: errors.go

var (
    // ErrCapabilityNotSupported is returned when an environment does not support
    // the requested operation (e.g., snapshots on E2B, rollback on Daytona).
    ErrCapabilityNotSupported = errors.New("capability not supported by environment")

    // ErrNotActive is returned when an operation requires an active environment
    // but the environment is in a different state (paused, destroyed, etc.).
    ErrNotActive = errors.New("environment not in active state")

    // ErrUnavailable is returned when the environment's backing service is unreachable.
    ErrUnavailable = errors.New("environment unavailable")

    // ErrSessionLimitReached is returned when no more environments can be created.
    ErrSessionLimitReached = errors.New("session limit reached")
)
```

### 3.4 Concurrency Contract

All `ExecutionEnvironment` implementations **MUST** be goroutine-safe. The RuntimeController may call lifecycle methods (Pause, Resume, Destroy) from a management goroutine while the Agent Loop concurrently calls `ExecuteTool` from a worker goroutine. The sequence diagram (§2.3) illustrates this concurrent access pattern.

**Required synchronization strategy:**

- Remote environments (E2B, Daytona, Fly) MUST protect mutable state fields (`state`, `sandboxID`/`workspaceID`/`machineID`, `labels`, timestamps) with a `sync.RWMutex`.
- Lifecycle methods (`Create`, `Pause`, `Resume`, `Destroy`) take a **write lock** since they mutate state.
- `ExecuteTool` takes a **read lock** for the state check (`if e.state != StateActive`) before proceeding with the API call. The API call itself runs outside the lock.
- `Capabilities()` is static and requires no lock.
- `State()` takes a **read lock**.
- `NativeSandboxEnvironment` delegates all state management to the server-side `SandboxHostService` via RPC, so it does not need internal locking (the server handles concurrency).
- `LocalEnvironment` requires no mutex because its only mutable field (`destroyed`) transitions monotonically from false to true. If concurrent Destroy + ExecuteTool is needed, a simple `atomic.Bool` suffices.

**Permitted concurrent method pairs:**

| Method A | Method B | Allowed? | Notes |
|----------|----------|----------|-------|
| `ExecuteTool` | `ExecuteTool` | Yes | Multiple concurrent tool calls |
| `ExecuteTool` | `Pause` | Yes | Pause waits for state lock; in-flight ExecuteTool continues |
| `ExecuteTool` | `Destroy` | Yes | Destroy waits for state lock; in-flight ExecuteTool may see error |
| `Pause` | `Resume` | No | Serialized by write lock |
| `Create` | any | No | Create must complete before other calls |

---

## 4. Capability System

### 4.1 Capabilities Struct

```go
// Package: internal/sandbox/environment
// File: capabilities.go

// Capabilities describes what an ExecutionEnvironment supports.
// This is a static description — it does not change after Create().
// The RuntimeController and orchestrator check capabilities to adapt behavior:
//   - Skip snapshot calls if Snapshots is false
//   - Skip rollback strategy if Rollback is false
//   - Avoid calling Pause if Pause is false
type Capabilities struct {
    // Snapshots indicates the environment supports CreateSnapshot().
    // Only true for NativeSandboxEnvironment (ZFS snapshots).
    Snapshots bool

    // Rollback indicates the environment can restore to a prior snapshot.
    // Only true for NativeSandboxEnvironment (ZFS rollback).
    Rollback bool

    // Pause indicates the environment supports Pause/Resume.
    // If true, Pause()/Resume() succeed. The degree of state preservation varies:
    //   - LocalEnvironment: no-op (Pause:true — always succeeds, nothing to preserve)
    //   - NativeSandboxEnvironment: full (ZFS dataset persists, instant resume)
    //   - E2BSandboxEnvironment: full (pause() preserves memory + disk)
    //   - FlySandboxEnvironment: full (suspend() saves memory to disk)
    // DaytonaSandboxEnvironment has Pause:false — auto-stop is lossy, not true pause.
    Pause bool

    // StreamingProgress indicates the environment supports incremental progress
    // callbacks during tool execution (onProgress).
    StreamingProgress bool

    // TierRouting indicates the environment distinguishes Tier 1 (in-process)
    // and Tier 2 (container) execution. Only true for NativeSandboxEnvironment.
    // Remote environments execute all tools uniformly.
    TierRouting bool

    // MaxSessionDuration is the maximum session lifetime. Zero means unlimited.
    MaxSessionDuration time.Duration

    // ConcurrentSessions is the maximum concurrent sessions. Zero means unlimited.
    ConcurrentSessions int
}
```

### 4.2 Capability Constants Per Environment

```go
// LocalCapabilities: LocalEnvironment has no sandbox features.
// Pause is true because calling Pause()/Resume() succeeds (no-op) — callers
// do not need special handling. Pause:true means "Pause() will succeed,"
// even if the implementation is a no-op. Pause:false means "Pause() will
// return ErrCapabilityNotSupported."
var LocalCapabilities = Capabilities{
    Snapshots:         false,
    Rollback:          false,
    Pause:             true,  // no-op pause/resume always succeeds
    TierRouting:       false,
    StreamingProgress: true,  // local exec can stream output
}

// NativeSandboxCapabilities: full capability set for ZFS + gVisor.
var NativeSandboxCapabilities = Capabilities{
    Snapshots:         true,
    Rollback:          true,
    Pause:             true,
    TierRouting:       true,
    StreamingProgress: true,
}

// E2BCapabilities: E2B sandbox limitations.
var E2BCapabilities = Capabilities{
    Snapshots:          false,
    Rollback:           false,
    Pause:              true,  // sandbox.pause() / sandbox.resume()
    TierRouting:        false,
    StreamingProgress:  true,  // sandbox.commands.run() supports streaming
    MaxSessionDuration: 24 * time.Hour,
}

// DaytonaCapabilities: Daytona workspace limitations.
var DaytonaCapabilities = Capabilities{
    Snapshots:         false,
    Rollback:          false,
    Pause:             false, // auto-stop + recreate is lossy, not true pause
    TierRouting:       false,
    StreamingProgress: true,  // code_run() supports streaming
}

// FlyCapabilities: Fly.io Machine limitations.
var FlyCapabilities = Capabilities{
    Snapshots:         false, // volume snapshots are coarse-grained
    Rollback:          false,
    Pause:             true,  // machine.suspend() saves memory to disk
    TierRouting:       false,
    StreamingProgress: false, // no direct exec API; SSH/agent required
}
```

---

## 5. Environment Implementations

### 5.1 LocalEnvironment

`LocalEnvironment` is the simplest implementation. Lifecycle methods are no-ops. Tool execution runs directly on the local filesystem and processes. Used in **All Local** mode and by agents running **inside** sandboxes in **Agent in Sandbox** mode.

```go
// Package: internal/sandbox/environment/local
// File: local.go

// LocalEnvironment executes tools directly on the local filesystem.
// Create and Pause/Resume are no-ops. Destroy marks the environment as
// destroyed — subsequent ExecuteTool calls return ErrNotActive, matching
// the compliance suite contract that all environments enforce post-Destroy.
// Used in All Local mode and by agents running inside sandboxes
// in Agent in Sandbox mode (from the agent's perspective, tools are local).
type LocalEnvironment struct {
    workDir   string       // working directory for tool execution
    logger    *slog.Logger
    destroyed atomic.Bool  // set by Destroy(); guards ExecuteTool (per §3.4)
}

func NewLocalEnvironment(workDir string, logger *slog.Logger) *LocalEnvironment {
    return &LocalEnvironment{workDir: workDir, logger: logger}
}

func (e *LocalEnvironment) Create(ctx context.Context, config environment.SessionConfig) error {
    return nil // no-op: local filesystem is always available
}

func (e *LocalEnvironment) Pause(ctx context.Context) error {
    return nil // no-op (Capabilities().Pause == true)
}

func (e *LocalEnvironment) Resume(ctx context.Context) error {
    return nil // no-op (Capabilities().Pause == true)
}

func (e *LocalEnvironment) Destroy(ctx context.Context) error {
    e.destroyed.Store(true)
    return nil
}

func (e *LocalEnvironment) ExecuteTool(ctx context.Context, req environment.ToolRequest, onProgress func(environment.ToolProgress)) (*environment.ToolResponse, error) {
    if e.destroyed.Load() {
        return nil, environment.ErrNotActive
    }
    // Dispatch to the local tool execution engine.
    // File ops (read, write, edit, grep, glob) execute as Go functions.
    // Process ops (bash) execute as os/exec commands.
    // This reuses the existing tool execution logic from internal/tools.
    return executeLocalTool(ctx, e.workDir, req, onProgress)
}

func (e *LocalEnvironment) Capabilities() environment.Capabilities {
    return environment.LocalCapabilities
}

func (e *LocalEnvironment) CreateSnapshot(ctx context.Context, name string) (*environment.SnapshotInfo, error) {
    return nil, environment.ErrCapabilityNotSupported
}

func (e *LocalEnvironment) Rollback(ctx context.Context, snapshotID string) error {
    return environment.ErrCapabilityNotSupported
}
```

### 5.2 NativeSandboxEnvironment

Wraps the existing `SandboxClient` (ConnectRPC client) and delegates all calls to the `SandboxHostService`. This is a thin adapter -- the real work happens in plan 11's `SandboxHostService`. Used in **Agent outside Sandbox** mode with our own ZFS + gVisor stack, and by the RuntimeController for lifecycle management in **Agent in Sandbox** mode.

```go
// Package: internal/sandbox/environment/native
// File: native.go

// NativeSandboxEnvironment adapts the existing ConnectRPC SandboxClient to the
// ExecutionEnvironment interface. Wraps api.SandboxService (the RPC client
// interface) and translates between environment-level types and RPC-level types.
type NativeSandboxEnvironment struct {
    service   api.SandboxService
    logger    *slog.Logger
    sessionID string // set after Create()
}

func NewNativeSandboxEnvironment(service api.SandboxService, logger *slog.Logger) *NativeSandboxEnvironment {
    return &NativeSandboxEnvironment{service: service, logger: logger}
}

func (e *NativeSandboxEnvironment) Create(ctx context.Context, config environment.SessionConfig) error {
    rpcResp, err := e.service.CreateSession(ctx, &api.CreateSessionRequest{
        BaseSnapshot: config.BaseImage,
        SessionID:    config.SessionID,
        Labels:       config.Labels,
    })
    if err != nil {
        return fmt.Errorf("native sandbox: create: %w", err)
    }
    e.sessionID = rpcResp.Session.ID
    return nil
}

func (e *NativeSandboxEnvironment) Pause(ctx context.Context) error {
    _, err := e.service.PauseSession(ctx, &api.PauseSessionRequest{SessionID: e.sessionID})
    if err != nil {
        return fmt.Errorf("native sandbox: pause: %w", err)
    }
    return nil
}

func (e *NativeSandboxEnvironment) Resume(ctx context.Context) error {
    _, err := e.service.ResumeSession(ctx, &api.ResumeSessionRequest{SessionID: e.sessionID})
    if err != nil {
        return fmt.Errorf("native sandbox: resume: %w", err)
    }
    return nil
}

func (e *NativeSandboxEnvironment) Destroy(ctx context.Context) error {
    _, err := e.service.DestroySession(ctx, &api.DestroySessionRequest{SessionID: e.sessionID})
    if err != nil {
        return fmt.Errorf("native sandbox: destroy: %w", err)
    }
    return nil
}

func (e *NativeSandboxEnvironment) ExecuteTool(ctx context.Context, req environment.ToolRequest, onProgress func(environment.ToolProgress)) (*environment.ToolResponse, error) {
    rpcReq := &api.ExecuteToolRequest{
        SessionID:  e.sessionID,
        ToolCallID: req.ToolCallID,
        ToolName:   req.ToolName,
        Params:     req.Params,
    }
    if req.Resources != nil {
        rpcReq.Resources = &api.ResourceSpec{
            CPUs:  float64(req.Resources.CPUs),
            MemMB: req.Resources.MemMB,
        }
    }

    stream, err := e.service.ExecuteToolStream(ctx, rpcReq)
    if err != nil {
        return nil, fmt.Errorf("native sandbox: execute tool %s: %w", req.ToolName, err)
    }
    defer stream.Close()

    // The server sends zero or more Progress messages followed by exactly one
    // Response message, then closes the stream. We break on Response.
    var final *api.ExecuteToolResponse
    for {
        msg, recvErr := stream.Recv()
        if recvErr != nil {
            return nil, fmt.Errorf("native sandbox: stream recv: %w", recvErr)
        }
        if msg == nil {
            continue
        }
        if msg.Progress != nil && onProgress != nil {
            onProgress(environment.ToolProgress{Content: msg.Progress.Content, IsError: msg.Progress.IsError})
        }
        if msg.Response != nil {
            final = msg.Response
            break
        }
    }
    if final == nil {
        return nil, fmt.Errorf("native sandbox: missing final response for tool %s", req.ToolName)
    }

    return &environment.ToolResponse{
        Content:    decodeResponseContent(final),
        SnapshotID: final.SnapshotID,
        ExitCode:   final.ExitCode,
    }, nil
}

func (e *NativeSandboxEnvironment) Capabilities() environment.Capabilities {
    return environment.NativeSandboxCapabilities
}

func (e *NativeSandboxEnvironment) CreateSnapshot(ctx context.Context, name string) (*environment.SnapshotInfo, error) {
    resp, err := e.service.CreateSnapshot(ctx, &api.CreateSnapshotRequest{
        SessionID: e.sessionID,
        Name:      name,
    })
    if err != nil {
        return nil, fmt.Errorf("native sandbox: create snapshot: %w", err)
    }
    return &environment.SnapshotInfo{
        ID:        resp.SnapshotID,
        Name:      name,
        SpaceUsed: resp.SpaceUsed,
    }, nil
}

func (e *NativeSandboxEnvironment) Rollback(ctx context.Context, snapshotID string) error {
    _, err := e.service.RollbackSession(ctx, &api.RollbackSessionRequest{
        SessionID:  e.sessionID,
        SnapshotID: snapshotID,
    })
    if err != nil {
        return fmt.Errorf("native sandbox: rollback: %w", err)
    }
    return nil
}
```

### 5.3 E2BSandboxEnvironment

E2B is the best-fit remote environment. Its sandbox model maps cleanly to our session concept.

```go
// Package: internal/sandbox/environment/e2b
// File: e2b.go

// E2BSandboxEnvironment implements ExecutionEnvironment using the E2B sandbox API.
//
// Key mapping:
//   Create      → e2b.Sandbox.create(template_id)
//   ExecuteTool → sandbox.commands.run(cmd) for process ops,
//                 sandbox.filesystem for file ops
//   Pause       → sandbox.pause()
//   Resume      → e2b.Sandbox.resume(sandbox_id)
//   Destroy     → sandbox.kill()
//
// Limitations:
//   - 24-hour maximum sandbox lifetime
//   - No snapshot/rollback API
//   - pause() preserves full state but has ~2-5s resume latency
type E2BSandboxEnvironment struct {
    apiKey     string
    httpClient *http.Client
    baseURL    string
    logger     *slog.Logger

    // Session state (set after Create). Protected by mu per §3.4 concurrency contract.
    mu        sync.RWMutex
    sandboxID string
    state     SessionState
    created   time.Time
    labels    map[string]string
}

// E2BOptions carries E2B-specific environment creation parameters.
type E2BOptions struct {
    TemplateID string        // E2B template to use
    Timeout    time.Duration // sandbox timeout (max 24h, default 5m)
    Metadata   map[string]string
}

func NewE2BSandboxEnvironment(apiKey string, opts ...Option) *E2BSandboxEnvironment {
    e := &E2BSandboxEnvironment{
        apiKey:     apiKey,
        httpClient: &http.Client{Timeout: 30 * time.Second},
        baseURL:    "https://api.e2b.dev/v1",
    }
    for _, opt := range opts {
        opt(e)
    }
    return e
}

func (e *E2BSandboxEnvironment) Create(ctx context.Context, config environment.SessionConfig) error {
    var e2bOpts E2BOptions
    if config.Options != nil {
        var ok bool
        e2bOpts, ok = config.Options.(E2BOptions)
        if !ok {
            return fmt.Errorf("e2b: Options must be e2b.E2BOptions, got %T", config.Options)
        }
    }
    templateID := e2bOpts.TemplateID
    if templateID == "" {
        templateID = config.BaseImage // fallback: treat BaseImage as template ID
    }

    // POST /sandboxes { template_id, timeout, metadata }
    sandboxID, err := e.apiCreateSandbox(ctx, templateID, e2bOpts)
    if err != nil {
        return fmt.Errorf("e2b: create sandbox: %w", err)
    }

    e.sandboxID = sandboxID
    e.state = StateActive
    e.created = time.Now()
    e.labels = config.Labels
    return nil
}

func (e *E2BSandboxEnvironment) ExecuteTool(ctx context.Context, req environment.ToolRequest, onProgress func(environment.ToolProgress)) (*environment.ToolResponse, error) {
    if e.state != StateActive {
        return nil, environment.ErrNotActive
    }

    // Remote environments treat all tools uniformly — no tier distinction.
    // File ops (read, write, edit, grep, glob) use the filesystem API.
    // Process ops (bash, git) use the commands API.
    switch {
    case isFileOp(req.ToolName):
        return e.executeFileOp(ctx, req)
    default:
        return e.executeCommand(ctx, req, onProgress)
    }
}

func (e *E2BSandboxEnvironment) Pause(ctx context.Context) error {
    if e.state != StateActive {
        return environment.ErrNotActive
    }
    // POST /sandboxes/{sandbox_id}/pause
    if err := e.apiPauseSandbox(ctx, e.sandboxID); err != nil {
        return fmt.Errorf("e2b: pause: %w", err)
    }
    e.state = StatePaused
    return nil
}

func (e *E2BSandboxEnvironment) Resume(ctx context.Context) error {
    if e.state != StatePaused {
        return environment.ErrNotActive
    }
    // POST /sandboxes/{sandbox_id}/resume
    if err := e.apiResumeSandbox(ctx, e.sandboxID); err != nil {
        return fmt.Errorf("e2b: resume: %w", err)
    }
    e.state = StateActive
    return nil
}

func (e *E2BSandboxEnvironment) Destroy(ctx context.Context) error {
    // DELETE /sandboxes/{sandbox_id}
    if err := e.apiKillSandbox(ctx, e.sandboxID); err != nil {
        return fmt.Errorf("e2b: destroy: %w", err)
    }
    e.state = StateDestroyed
    return nil
}

func (e *E2BSandboxEnvironment) Capabilities() environment.Capabilities {
    return environment.E2BCapabilities
}

func (e *E2BSandboxEnvironment) CreateSnapshot(ctx context.Context, name string) (*environment.SnapshotInfo, error) {
    return nil, environment.ErrCapabilityNotSupported
}

func (e *E2BSandboxEnvironment) Rollback(ctx context.Context, snapshotID string) error {
    return environment.ErrCapabilityNotSupported
}
```

### 5.4 DaytonaSandboxEnvironment

Daytona provides workspace-based development environments. The fit is medium -- no real pause, template-based snapshots are too slow for per-turn use.

```go
// Package: internal/sandbox/environment/daytona
// File: daytona.go

// DaytonaSandboxEnvironment implements ExecutionEnvironment using the Daytona API.
//
// Key mapping:
//   Create      → daytona.workspace.create(target, source)
//   ExecuteTool → workspace.code_run() or workspace.filesystem operations
//   Pause       → ErrCapabilityNotSupported (auto-stop is lossy)
//   Resume      → ErrCapabilityNotSupported
//   Destroy     → workspace.delete()
//
// Limitations:
//   - No explicit pause/resume — auto-stop triggers after idle, recreate is lossy
//   - Template-based snapshots exist but are slow (minutes, not milliseconds)
//   - Best for stateless or checkpoint-tolerant workloads
type DaytonaSandboxEnvironment struct {
    apiKey     string
    apiURL     string
    httpClient *http.Client
    logger     *slog.Logger

    // Session state (set after Create). Protected by mu per §3.4 concurrency contract.
    mu          sync.RWMutex
    workspaceID string
    state       SessionState
    created     time.Time
    labels      map[string]string
}

// DaytonaOptions carries Daytona-specific environment creation parameters.
type DaytonaOptions struct {
    Target  string // Daytona target (e.g., "local", "aws")
    Image   string // container image
    GitURL  string // repository to clone
    EnvVars map[string]string
}

func (e *DaytonaSandboxEnvironment) Capabilities() environment.Capabilities {
    return environment.DaytonaCapabilities
}

func (e *DaytonaSandboxEnvironment) Pause(ctx context.Context) error {
    return environment.ErrCapabilityNotSupported
}

func (e *DaytonaSandboxEnvironment) Resume(ctx context.Context) error {
    return environment.ErrCapabilityNotSupported
}

func (e *DaytonaSandboxEnvironment) CreateSnapshot(ctx context.Context, name string) (*environment.SnapshotInfo, error) {
    return nil, environment.ErrCapabilityNotSupported
}

func (e *DaytonaSandboxEnvironment) Rollback(ctx context.Context, snapshotID string) error {
    return environment.ErrCapabilityNotSupported
}

// Create, ExecuteTool, Destroy follow the same pattern as E2B but targeting
// the Daytona REST API. code_run() is the primary execution path for all
// tool types.
```

### 5.5 FlySandboxEnvironment

Fly.io is the hardest fit -- no direct exec API means we need SSH or an in-sandbox agent for command execution.

```go
// Package: internal/sandbox/environment/fly
// File: fly.go

// FlySandboxEnvironment implements ExecutionEnvironment using the Fly.io Machines API.
//
// Key mapping:
//   Create      → fly.machine.create(image, volume)
//   ExecuteTool → SSH into machine + exec (requires sshd or agent in image)
//   Pause       → machine.suspend()
//   Resume      → machine.start()
//   Destroy     → machine.destroy() + volume.destroy()
//
// Limitations:
//   - No direct exec API — requires SSH or in-sandbox agent for command execution
//   - Volume snapshots are coarse-grained (not suitable for per-turn)
//   - suspend() saves memory to disk; resume has ~1-3s latency
//   - Custom image required (must include sshd or agent binary)
type FlySandboxEnvironment struct {
    apiToken   string
    appName    string
    httpClient *http.Client
    logger     *slog.Logger

    // Session state (set after Create). Protected by mu per §3.4 concurrency contract.
    mu        sync.RWMutex
    machineID string
    volumeID  string
    ipAddr    string // private IPv6 within Fly network
    state     SessionState
    created   time.Time
    labels    map[string]string

    // SSH config for connecting to machines for tool execution
    sshKey     []byte
    sshTimeout time.Duration
}

// FlyOptions carries Fly.io-specific environment creation parameters.
type FlyOptions struct {
    Image    string            // Docker image (must include sshd/agent)
    Region   string            // Fly region (e.g., "iad", "lhr")
    CPUs     int               // number of shared CPUs
    MemoryMB int               // memory in MB
    VolumeGB int               // persistent volume size
    EnvVars  map[string]string // environment variables
}

func (e *FlySandboxEnvironment) Capabilities() environment.Capabilities {
    return environment.FlyCapabilities
}

func (e *FlySandboxEnvironment) ExecuteTool(ctx context.Context, req environment.ToolRequest, onProgress func(environment.ToolProgress)) (*environment.ToolResponse, error) {
    if e.state != StateActive {
        return nil, environment.ErrNotActive
    }

    // All tool execution goes through SSH.
    // For file ops: use SSH to read/write files on the machine.
    // For process ops: use SSH to execute commands.
    // The machine image must include an agent binary or sshd.
    switch {
    case isFileOp(req.ToolName):
        return e.executeFileOpSSH(ctx, req)
    default:
        return e.executeCommandSSH(ctx, req, onProgress)
    }
}

func (e *FlySandboxEnvironment) Pause(ctx context.Context) error {
    if e.state != StateActive {
        return environment.ErrNotActive
    }
    // PUT /apps/{app}/machines/{machine_id}/suspend
    if err := e.apiSuspendMachine(ctx, e.machineID); err != nil {
        return fmt.Errorf("fly: suspend: %w", err)
    }
    e.state = StatePaused
    return nil
}

func (e *FlySandboxEnvironment) CreateSnapshot(ctx context.Context, name string) (*environment.SnapshotInfo, error) {
    return nil, environment.ErrCapabilityNotSupported
}

func (e *FlySandboxEnvironment) Rollback(ctx context.Context, snapshotID string) error {
    return environment.ErrCapabilityNotSupported
}

// Create, Resume, Destroy follow the same Machine API patterns.
```

**SSH Host Key Verification Strategy:** The custom machine image (which must include sshd) pins a known SSH host key pair baked into the image at build time. The public host key is stored in the application's configuration alongside the SSH private key used for client authentication. When connecting, `FlySandboxEnvironment` uses a `knownhosts.FixedHostKey(pinnedPublicKey)` callback — no `InsecureIgnoreHostKey`. If the image is rebuilt with new host keys, the configuration must be updated in lockstep. This approach works because all machines use the same image and thus the same host key.

---

## 6. Provider Research Summary

### 6.1 E2B (Best Fit)

| Aspect | Details |
|--------|---------|
| **Model** | Cloud sandboxes from templates, REST API |
| **Create** | `POST /sandboxes` with template_id, timeout, metadata |
| **Execute** | `sandbox.commands.run(cmd)` for process ops, `sandbox.filesystem` for file ops |
| **Pause/Resume** | `sandbox.pause()` / `Sandbox.resume(sandbox_id)` — full state preserved |
| **Snapshots** | None — no snapshot/rollback API |
| **Max lifetime** | 24 hours |
| **Streaming** | Yes, `commands.run()` supports streaming output |
| **Pricing** | Per-second compute billing |

### 6.2 Daytona (Medium Fit)

| Aspect | Details |
|--------|---------|
| **Model** | Workspace-based development environments |
| **Create** | `workspace.create(target, source)` |
| **Execute** | `code_run()` for process ops, filesystem API for file ops |
| **Pause/Resume** | Auto-stop only (lossy — disk preserved, processes lost) |
| **Snapshots** | Template-based — too slow for per-turn use (minutes, not ms) |
| **Max lifetime** | Unlimited (but auto-stop after idle) |
| **Streaming** | Yes, `code_run()` supports streaming |
| **Pricing** | Self-hosted or SaaS, varies |

### 6.3 Fly.io (Hardest Fit)

| Aspect | Details |
|--------|---------|
| **Model** | Lightweight VMs (Machines) with persistent volumes |
| **Create** | `machine.create(image, volume)` — custom image required |
| **Execute** | SSH into machine (no direct exec API) — requires sshd or agent in image |
| **Pause/Resume** | `machine.suspend()` / `machine.start()` — memory saved to disk, ~1-3s resume |
| **Snapshots** | Volume snapshots — coarse-grained, not per-turn |
| **Max lifetime** | Unlimited |
| **Streaming** | No native streaming — must be implemented over SSH |
| **Pricing** | Per-second compute + volume storage |

---

## 7. Migration Path

### 7.1 Current Call Chain

```
Agent Loop
  → ToolBackend.ExecuteTool(ctx, ToolRequest, onProgress)
    → LocalBackend.ExecuteTool (All Local, Agent in Sandbox)
      → direct filesystem/process execution
    → SandboxBackend.ExecuteTool (Agent outside Sandbox)
      → SandboxToolClient.ExecuteTool(ctx, sessionID, ToolRequest, onProgress)
        → SandboxClient (internal/rpc/client) speaks ConnectRPC
          → SandboxHostService (internal/sandbox)
```

### 7.2 New Call Chain

```
Agent Loop
  → ExecutionEnvironment.ExecuteTool(ctx, ToolRequest, onProgress)
    → LocalEnvironment (All Local, Agent in Sandbox internal)
      → direct filesystem/process execution
    → NativeSandboxEnvironment (Agent outside Sandbox with our stack)
      → SandboxClient → SandboxHostService
    → E2BSandboxEnvironment (Agent outside Sandbox with E2B)
      → E2B REST API
    → DaytonaSandboxEnvironment (Agent outside Sandbox with Daytona)
      → Daytona REST API
    → FlySandboxEnvironment (Agent outside Sandbox with Fly)
      → Fly Machines API + SSH
```

### 7.3 What Gets Removed

**`ToolBackend` interface** -- Replaced entirely by `ExecutionEnvironment`. Agent loop code that calls `ToolBackend.ExecuteTool()` now calls `ExecutionEnvironment.ExecuteTool()`.

**`LocalBackend`** -- Replaced by `LocalEnvironment`. Same direct-execution logic, now implementing `ExecutionEnvironment` with no-op lifecycle methods.

**`SandboxBackend`** -- Eliminated. There is no wrapper layer. The sandbox environments (NativeSandboxEnvironment, etc.) implement `ExecutionEnvironment` directly. The agent loop calls them directly.

**`SandboxToolClient` interface** -- Eliminated. `NativeSandboxEnvironment` uses `api.SandboxService` directly.

**`ToolRequest.SessionID` field** -- Vestigial after migration. No `ExecutionEnvironment` implementation reads it — session identity is internal to each environment (set during `Create()`). Remove the field from `ToolRequest` in a follow-up cleanup after all callers are migrated off `ToolBackend`.

### 7.4 RuntimeController Updates

The RuntimeController selects the right `ExecutionEnvironment` at startup based on configuration:

```go
// Example: RuntimeController creates environment
func createEnvironment(cfg Config) (environment.ExecutionEnvironment, error) {
    switch cfg.EnvironmentType {
    case "local":
        return local.NewLocalEnvironment(cfg.WorkDir, logger), nil
    case "native":
        svc := connectrpc.NewSandboxServiceClient(cfg.SandboxHostURL)
        return native.NewNativeSandboxEnvironment(svc, logger), nil
    case "e2b":
        return e2b.NewE2BSandboxEnvironment(cfg.E2BAPIKey), nil
    case "daytona":
        return daytona.NewDaytonaSandboxEnvironment(cfg.DaytonaAPIKey, cfg.DaytonaURL), nil
    case "fly":
        return fly.NewFlySandboxEnvironment(cfg.FlyAPIToken, cfg.FlyAppName, cfg.FlySSHKey), nil
    default:
        return nil, fmt.Errorf("unknown environment type: %s", cfg.EnvironmentType)
    }
}
```

### 7.5 Tier 1/2 Distinction

For `NativeSandboxEnvironment`, Tier 1/2 routing is handled server-side by `SandboxHostService` -- the environment just forwards the request. For remote environments, there is no tier distinction. However, remote environments still need to know how to execute different tool types:

- **File ops** (read, write, edit, grep, glob): Use the environment's filesystem API
- **Process ops** (bash, git commands): Use the environment's command execution API

This is an internal concern of each environment implementation, not an interface-level distinction. The `isFileOp()` helper (shared utility in `internal/sandbox/environment`) helps environments route internally:

```go
// Package: internal/sandbox/environment
// File: classify.go

// isFileOp returns true for tool names that operate on the filesystem
// (read, write, edit, grep, glob) and should be dispatched via the
// environment's filesystem API. All other tools are treated as process
// operations and dispatched via the command execution API.
// This is independent of the Tier 1/2 classifier in internal/tools,
// which is a NativeSandbox-specific concept.
func IsFileOp(toolName string) bool {
    switch toolName {
    case "read", "write", "edit", "grep", "glob":
        return true
    default:
        return false
    }
}
```

---

## 8. Snapshot Handling by Environment

| Operation | LocalEnvironment | NativeSandbox | E2BSandbox | DaytonaSandbox | FlySandbox |
|-----------|-----------------|---------------|------------|----------------|------------|
| `CreateSnapshot()` | `ErrCapabilityNotSupported` | ZFS snapshot (instant, COW) | `ErrCapabilityNotSupported` | `ErrCapabilityNotSupported` | `ErrCapabilityNotSupported` |
| `Rollback()` | `ErrCapabilityNotSupported` | ZFS rollback (<100ms) | `ErrCapabilityNotSupported` | `ErrCapabilityNotSupported` | `ErrCapabilityNotSupported` |

The orchestrator MUST check `env.Capabilities()` before relying on snapshot operations:

```go
if env.Capabilities().Rollback {
    err := env.Rollback(ctx, snapshotID)
    // handle rollback
} else {
    // environment doesn't support rollback — use alternative recovery strategy
    // (e.g., destroy + recreate environment)
}
```

---

## 9. Connected Components / Seam Impacts

### 9.1 New Seams

| Component A | Component B | Interface | Notes |
|-------------|-------------|-----------|-------|
| `internal/agent` | `internal/sandbox/environment` | `ExecutionEnvironment` | Replaces `ToolBackend` |
| `internal/sandbox/environment/native` | `internal/rpc/client` | `api.SandboxService` | Native environment delegates to existing RPC client. **Plan 13 gap:** `CreateSnapshot` RPC is needed but not yet defined in plan 13's `SandboxService` interface — see §9.5. |
| `internal/sandbox/environment/native` | `internal/sandbox` | (indirect, via RPC) | Native environment -> RPC -> SandboxHostService |
| `internal/sandbox/environment/e2b` | E2B REST API | HTTP | External API dependency |
| `internal/sandbox/environment/daytona` | Daytona REST API | HTTP | External API dependency |
| `internal/sandbox/environment/fly` | Fly Machines API + SSH | HTTP + SSH | External API + SSH dependency |
| RuntimeController | `internal/sandbox/environment` | `ExecutionEnvironment` | Lifecycle management (Create/Pause/Resume/Destroy) |

### 9.2 Modified Seams

| Original Seam | Change |
|---------------|--------|
| `internal/agent` -> `internal/tools (ToolBackend)` | Replaced by `internal/agent` -> `ExecutionEnvironment` |
| `internal/tools (SandboxBackend)` -> `SandboxToolClient` | Eliminated; NativeSandboxEnvironment uses `api.SandboxService` directly |
| Orchestrator -> `api.SandboxService` (direct RPC) | Orchestrator -> `ExecutionEnvironment` (environment-abstracted) |

### 9.3 Unchanged Seams

| Seam | Why Unchanged |
|------|---------------|
| `internal/sandbox (SandboxHostService)` -> `ZFSManager` / `GVisorManager` | Internal to native environment path |
| `internal/rpc` -> `internal/sandbox` | RPC server still calls SandboxHostService directly |
| `internal/tools (ClassifyTool)` | Tier classification is still shared; remote environments may use it internally |

### 9.4 Implementation Guide Updates

The following sections of `00-implementation-guide.md` need updates **(apply during Phase 4: Agent Loop Migration, not deferred)**:

- **Section 1.6** (ToolBackend): Replace with ExecutionEnvironment; document that ToolBackend is removed
- **Section 2.5** (RPC SandboxBackend Lifecycle): Update to reflect environment abstraction; SandboxBackend is eliminated
- **Section 5** (Seam Reference Table): Replace ToolBackend seam entries with ExecutionEnvironment entries from section 9.1 above
- **Section 5.1** (Import Flow): Add `internal/sandbox/environment` and sub-packages
- **Architecture doc** (`00-architecture.md`): Update Key Terminology (§lines 67-71) to replace `ToolBackend`, `LocalBackend`, `SandboxBackend` with `ExecutionEnvironment` and its implementations

### 9.5 Plan 13 RPC Gap: CreateSnapshot

**Blocker:** `NativeSandboxEnvironment.CreateSnapshot()` calls `e.service.CreateSnapshot(ctx, req)`, but plan 13's `SandboxService` interface does not define a `CreateSnapshot` RPC method. Plan 11's `SandboxHostService` has the server-side implementation (`CreateSnapshot(ctx, sessionID, name)` — §3.1 line 228), but the RPC transport layer is missing.

**Required addition to plan 13's `SandboxService` interface (§4.1):**

```go
CreateSnapshot(ctx context.Context, req *CreateSnapshotRequest) (*CreateSnapshotResponse, error)
```

**Request/Response types:**

```go
type CreateSnapshotRequest struct {
    SessionID string
    Name      string
}

type CreateSnapshotResponse struct {
    SnapshotID string
    SpaceUsed  int64 // bytes
}
```

This maps directly to plan 11's `SandboxHostService.CreateSnapshot(ctx, sessionID, name)`. The orchestrator synthesizes turn-boundary behavior by calling `CreateSnapshot("turn-N")` — no separate `TurnComplete` RPC is needed.

### 9.6 Native Environment Configuration Gap: Quota and TurnComplete

Plan 11's `CreateSessionRequest` includes a `Quota` field (§3.2 line 247) for per-session ZFS dataset quotas. The addendum's `NativeSandboxEnvironment.Create` does not pass Quota.

**Resolution:** Quota is controlled server-side via `ServiceConfig.DefaultSessionQuota`. If per-session overrides are needed, define a `NativeOptions` struct:

```go
type NativeOptions struct {
    Quota int64 // per-session ZFS dataset quota in bytes; 0 = use server default
}
```

Pass via `SessionConfig.Options`. This is not blocking for V1 — the server default is sufficient.

**TurnComplete mapping:** Plan 11's `TurnComplete(ctx, sessionID)` creates an auto-named snapshot at turn boundaries. The addendum's `CreateSnapshot(ctx, name)` subsumes this — the orchestrator calls `env.CreateSnapshot(ctx, fmt.Sprintf("turn-%d", turnNum))`. No separate interface method needed.

---

## 10. Acceptance Criteria

These scenarios prove the ExecutionEnvironment abstraction works end-to-end across component boundaries, not just in isolation.

**AC1: Agent executes tools through E2B environment**
1. RuntimeController creates an `E2BSandboxEnvironment` with a valid template ID.
2. Agent loop receives a user prompt requiring file read + bash execution.
3. Agent calls `env.ExecuteTool()` for `read_file` → receives file content via E2B filesystem API.
4. Agent calls `env.ExecuteTool()` for `bash` → receives streamed output via `onProgress` callback.
5. **Expected:** Both tools complete successfully; agent produces a coherent response incorporating tool output.

**AC2: RuntimeController pauses and resumes a native sandbox session**
1. RuntimeController creates a `NativeSandboxEnvironment`, agent writes a file.
2. RuntimeController calls `env.Pause()` (session state transitions to paused via RPC).
3. RuntimeController calls `env.Resume()` (session state transitions back to active).
4. Agent reads the file written before pause.
5. **Expected:** File content is preserved across pause/resume; agent continues without re-creation.

**AC3: Capability-gated fallback for unsupported operations**
1. RuntimeController creates a `DaytonaSandboxEnvironment` (Pause=false, Snapshots=false).
2. Orchestrator calls `env.CreateSnapshot()` → receives `ErrCapabilityNotSupported`.
3. Orchestrator calls `env.Pause()` → receives `ErrCapabilityNotSupported`.
4. Orchestrator falls back to destroy+recreate recovery strategy.
5. **Expected:** No panics, no errors propagated to user; orchestrator adapts gracefully.

**AC4: Environment swap transparency**
1. Run the same 5-tool agent workflow (read, write, bash, edit, grep) through `LocalEnvironment` and `NativeSandboxEnvironment`.
2. Compare `ToolResponse` structures (content block count, exit code presence).
3. **Expected:** Structurally equivalent responses — the agent loop does not need environment-specific handling.

**AC5: Post-Destroy error enforcement**
1. Create any environment, execute a tool successfully, then call `Destroy()`.
2. Attempt `ExecuteTool()` on the destroyed environment.
3. **Expected:** Returns `ErrNotActive` for all five environment types.

---

## 11. Testing Strategy

### 11.1 Unit Tests (per environment)

**T1: LocalEnvironment behavior**
- Verify Create/Pause/Resume return nil (no-ops)
- Verify Destroy sets destroyed state; subsequent ExecuteTool returns `ErrNotActive`
- Verify ExecuteTool dispatches to local tool execution
- Verify Capabilities returns `LocalCapabilities` (including `Pause: true`)
- Verify CreateSnapshot/Rollback return `ErrCapabilityNotSupported`

**T2: NativeSandboxEnvironment adapter correctness**
- Mock `api.SandboxService`, verify all methods delegate with correct type conversion
- Verify `ToolRequest` -> `api.ExecuteToolRequest` field mapping
- Verify streaming progress callbacks are propagated
- Verify Create stores sessionID, subsequent calls use it

**T3: E2BSandboxEnvironment API mapping**
- Use HTTP test server (`httptest.Server`) to mock E2B API
- Verify Create sends correct POST /sandboxes payload
- Verify Create with wrong Options type (e.g., `DaytonaOptions` instead of `E2BOptions`) returns a clear error
- Verify ExecuteTool routes file ops to filesystem API, commands to commands API
- Verify Pause calls POST /sandboxes/{id}/pause
- Verify CreateSnapshot returns `ErrCapabilityNotSupported`

**T4: DaytonaSandboxEnvironment API mapping**
- Same pattern as T3 against Daytona API mock
- Verify Pause returns `ErrCapabilityNotSupported`
- Verify all snapshot operations return `ErrCapabilityNotSupported`

**T5: FlySandboxEnvironment API mapping**
- Mock Fly Machines API + SSH server for command execution
- Verify Create creates machine + volume
- Verify ExecuteTool connects via SSH
- Verify Pause calls machine suspend

**T6: Capabilities correctness**
- Each environment's `Capabilities()` returns the expected static values
- Verify capability constants match documented environment limitations

### 11.2 Integration Tests

**T7: Agent loop + ExecutionEnvironment integration**
- Wire agent loop to each environment (mocked APIs)
- Verify the full `ExecuteTool()` path works end-to-end
- Verify `onProgress` callbacks flow through the full chain

**T8: Environment selection / factory**
- Verify environment factory returns correct type for each config value
- Verify unknown environment type returns error

**T9: Capability-gated behavior**
- Wire orchestrator-level code to each environment
- Verify snapshot calls skipped when `Snapshots` is false
- Verify Rollback skipped when `Rollback` is false
- Verify Pause returns `ErrCapabilityNotSupported` for Daytona

### 11.3 Backward Compatibility Tests

**T10: NativeSandboxEnvironment parity with direct SandboxClient**
- Run the same tool execution sequence through:
  1. Direct `SandboxClient` (old path)
  2. `NativeSandboxEnvironment` wrapping `SandboxClient` (new path)
- Verify identical `ToolResponse` values (content, snapshot ID, exit code)
- Use `MemorySandboxService` from plan 14 as the backend

### 11.4 Contract Tests

**T11: ExecutionEnvironment interface compliance**
- Write a shared test suite that any `ExecutionEnvironment` must pass
- Tests exercise full lifecycle: Create -> ExecuteTool -> CreateSnapshot -> Pause -> Resume -> Destroy
- For environments that don't support certain operations, verify correct `ErrCapabilityNotSupported`
- Run the shared suite against all five environments (with mocked backends)

---

## 12. Implementation Sequence

1. **Phase 1: Interface + Types** -- Create `internal/sandbox/environment` package with interface, types, capabilities, errors. No implementations yet.

2. **Phase 2: LocalEnvironment** -- Implement the local environment with no-op lifecycle and direct tool execution. Write unit tests. This validates the interface design with the simplest case.

3. **Phase 3: NativeSandboxEnvironment** -- Implement the native adapter wrapping `api.SandboxService`. Write unit tests. Verify parity with direct `SandboxClient` path.

4. **Phase 4: Agent Loop Migration** -- Update agent loop to use `ExecutionEnvironment` instead of `ToolBackend`. Remove `ToolBackend`, `LocalBackend`, `SandboxBackend`, and `SandboxToolClient`. Verify all existing tests pass.

5. **Phase 5: E2BSandboxEnvironment** -- Implement E2B with full API integration. Write unit tests with HTTP mock. This is the highest-value remote environment.

6. **Phase 6: Daytona + Fly Environments** -- Implement remaining environments. These can be done in parallel since they share no code dependencies.

7. **Phase 7: Integration Tests** -- Contract test suite, capability-gated behavior tests, backward compatibility verification.

---

## 13. Orphaned Environment Cleanup

When the RuntimeController process crashes after creating a remote environment but before destroying it, the environment becomes orphaned. Each provider handles this differently:

| Provider | Mechanism | Recovery |
|----------|-----------|----------|
| **E2B** | Built-in 24h TTL (configurable via `timeout` parameter) | Orphaned sandboxes auto-destroy at timeout. Set a conservative default (e.g., 1h). |
| **Daytona** | Auto-stop after idle period | Workspace stops but disk persists. Requires manual cleanup or label-based GC sweep. |
| **Fly** | Auto-stop configured on machine creation | Machine stops after idle; volume persists. Label-based GC sweep for full cleanup. |
| **Native** | Server-side session TTL in SandboxHostService | Handled by plan 11's existing session reaping logic. |

**Label-based GC sweep:** All remote environments attach `Labels` from `SessionConfig` during creation (including a `managed-by: h2-runtime` label and a `created-at` timestamp). A background goroutine in the RuntimeController periodically lists environments via provider APIs, finds those with `managed-by: h2-runtime` labels that exceed a maximum age (configurable, default 2h), and destroys them. This sweep runs every 15 minutes and is safe to run from multiple controllers (destroy is idempotent).

**Manual recovery:** If the GC sweep is disabled or the controller is down, operators can list and destroy orphaned environments via provider CLIs/dashboards using the `managed-by` label filter.

---

## 14. Open Questions

All open questions have been resolved or deferred to implementation:

1. **Environment-specific tool implementations:** Resolved — translation lives in each environment implementation (§7.5 defines IsFileOp() helper shared across remote envs). If common patterns emerge during implementation, a `RemoteToolExecutor` helper can be extracted as a refactor.

2. **State recovery:** Deferred — not needed for initial implementation. Remote environments can be reconstructed from their APIs if needed. A `Recover()` method can be added to the interface in a future addendum if experience shows it is necessary.

3. **Health checks:** Deferred — not part of this addendum's interface. The native environment delegates to `SandboxHostService.HealthCheck()`. Remote environment health checks will be designed when operational monitoring requirements are defined.

4. **Multi-environment sessions:** Resolved as out of scope — the design supports this naturally. Orchestrator-level routing is a separate concern to be addressed in a future plan.

---

## Round 1 Review Disposition

| # | Reviewer | Severity | Summary | Disposition | Notes |
|---|----------|----------|---------|-------------|-------|
| 1 | coder-1-sea | P1 | LocalEnvironment Pause capability contract conflict | Incorporated | LocalCapabilities.Pause set to true; capability semantics clarified in §4.2 and Pause doc comment |
| 2 | coder-1-sea | P1 | Destroy semantics conflict with compliance suite | Incorporated | LocalEnvironment tracks destroyed state; ExecuteTool returns ErrNotActive post-Destroy |
| 3 | coder-1-sea | P2 | Concurrency safety underspecified | Incorporated | §3.4 concurrency contract added; sync.RWMutex added to E2B/Daytona/Fly structs |
| 4 | coder-1-sea | P2 | Session identity ownership ambiguous | Incorporated | SessionConfig.SessionID now required; auto-generation removed |
| 5 | reviewer-sea | P1 | Concurrent access to mutable state unprotected | Incorporated | §3.4 concurrency contract; sync.RWMutex on remote env structs (overlaps coder-1-sea #3) |
| 6 | reviewer-sea | P1 | LocalEnvironment.Pause violates test harness P1 | Incorporated | LocalCapabilities.Pause=true (overlaps coder-1-sea #1) |
| 7 | reviewer-sea | P2 | No state query method on ExecutionEnvironment | Incorporated | State() SessionState added to interface §3.1 |
| 8 | reviewer-sea | P2 | SSH host key verification undefined for Fly | Incorporated | Pinned host key strategy documented in §5.5 |
| 9 | reviewer-sea | P2 | isFileOp() referenced but never defined | Incorporated | IsFileOp() defined in §7.5 with tool name list |
| 10 | reviewer-sea | P2 | Missing acceptance criteria section | Incorporated | §10 added with 5 cross-boundary scenarios |
| 11 | reviewer-sea | P3 | Import flow claim inaccurate | Incorporated | §2.4 updated to acknowledge internal/tools import |
| 12 | reviewer-sea | P3 | SessionConfig.Options typed as any | Incorporated | Wrong-type Options test added to T3 in §11.1 |
| 13 | reviewer-sea | P3 | Dead code in NativeSandboxEnvironment stream loop | Incorporated | Unreachable if-final-nil branch removed in §5.2 |
| 14 | reviewer-sea | P3 | Orphaned sandbox recovery unspecified | Incorporated | §13 added with label-based GC sweep + provider TTL strategy |

## Round 2 Review Disposition

| # | Reviewer | Severity | Summary | Disposition | Notes |
|---|----------|----------|---------|-------------|-------|
| 1 | reviewer-sea | P3 | Pause comment lists Daytona alongside Pause:true envs | Incorporated | Daytona moved to separate note in §4.1 Pause comment |
| 2 | reviewer-sea | P3 | Testing Strategy subsection numbering mismatch | Incorporated | Renumbered §10.x to §11.x |
| 3 | reviewer-sea | P3 | SEC2 inconsistent with required SessionID | Incorporated | Test harness SEC2 updated to require error on empty SessionID |
| 4 | reviewer-sea | P3 | LocalEnvironment destroyed field type contradicts §3.4 | Incorporated | Changed to atomic.Bool with Store/Load in §5.1 |

## Seam Review Disposition (Complete: 5 findings — 1 P1, 2 P2, 2 P3 — all incorporated)

| # | Reviewer | Severity | Summary | Disposition | Notes |
|---|----------|----------|---------|-------------|-------|
| 1 | reviewer-sea | P1 | CreateSnapshot RPC missing from plan 13 | Incorporated | §9.5 added with exact RPC signature needed; plan 13 update tracked as cross-plan dependency |
| 2 | reviewer-sea | P2 | SessionState type undefined, constants differ from plan 11 | Incorporated | SessionState type + 6 constants defined in §3.2; string values match plan 11; StateDestroyed documented as client-side only |
| 3 | reviewer-sea | P2 | ToolRequest.SessionID becomes dead field | Incorporated | Documented as vestigial in §3.2 and §7.3; cleanup deferred to post-migration |
| 4 | reviewer-sea | P3 | Architecture doc terminology drift | Incorporated | §9.4 updated: apply updates during Phase 4, not deferred |
| 5 | reviewer-sea | P3 | Quota and TurnComplete not exposed | Incorporated | §9.6 added: Quota via NativeOptions or server default; TurnComplete via CreateSnapshot("turn-N") |

---

## Plan Review Signoff

| Field | Value |
|-------|-------|
| **Status** | Approved |
| **Date** | 2026-03-14 |
| **Branch** | main |
| **Commit** | 1179700a45a895eda92c921c1baddd8e0d6ec7d4 |
| **Review rounds** | 2 |

### Finding Summary

| Round | Source | Total | P0 | P1 | P2 | P3 |
|-------|--------|-------|-----|-----|-----|-----|
| R1 | coder-1-sea | 4 | 0 | 2 | 2 | 0 |
| R1 | reviewer-sea | 10 | 0 | 2 | 4 | 4 |
| **R1 Total** | | **14** | **0** | **4** | **6** | **4** |
| R2 | reviewer-sea | 4 | 0 | 0 | 0 | 4 |
| **R2 Total** | | **4** | **0** | **0** | **0** | **4** |
| Seam | reviewer-sea | 5 | 0 | 1 | 2 | 2 |
| **Grand Total** | | **23** | **0** | **5** | **8** | **10** |

### Incorporation Rate

- **R1:** 14/14 incorporated (100%)
- **R2:** 4/4 incorporated (100%)
- **Seam Review:** 5/5 incorporated (100%)
- **Overall:** 23/23 incorporated (100%)

### Not Incorporated Items

None. All findings across both review rounds and seam review were incorporated.

### Open Questions Status

All 4 open questions resolved:
1. Environment-specific tool implementations — resolved (translation in each env, shared helper via §7.5 IsFileOp())
2. State recovery — deferred to future addendum (not needed for initial implementation)
3. Health checks — deferred (separate operational concern)
4. Multi-environment sessions — resolved as out of scope (design supports naturally)

### Seam Review Status

Seam review completed with 5 findings (1 P1, 2 P2, 2 P3). All 5 incorporated into the plan. Cross-plan dependency on plan 13 (CreateSnapshot RPC) tracked in §9.5.

### Reviewers

- coder-1-sea
- reviewer-sea
