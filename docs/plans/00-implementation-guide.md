# Implementation Guide

**Purpose:** Living reference for every implementing agent. Read this before starting any plan implementation. It synthesizes cross-cutting interface contracts, lifecycle invariants, configuration flow, common pitfalls from review rounds, and the seam reference table.

**Last updated:** 2026-03-12

---

## 1. Interface Contracts

This section lists the tricky cross-component interfaces with exact signatures. Each entry names the defining plan, the package, and the contract that downstream consumers must honor.

### 1.1 Provider (plan 01)

```go
// Package: internal/ai
// Plan: 01-ai-core §5.1

type Provider interface {
    API() string
    Stream(ctx context.Context, model Model, llmCtx Context, opts StreamOptions) *EventStream
    StreamSimple(ctx context.Context, model Model, llmCtx Context, opts SimpleStreamOptions) *EventStream
}
```

**Implementers:** Anthropic (plan 02), OpenAI (plan 03), Google (plan 04).

**Critical contract:**
- Every provider runs its streaming loop in a **goroutine** that begins with `defer es.Close()`. This ensures the `EventStream` terminal-state guarantee holds even on panic.
- The `done` or `error` event must be sent via `es.Send()` before returning. `Close()` without a terminal event injects `ErrStreamClosedWithoutTerminalEvent`.
- Provider implementations never import `internal/agent`. The dependency arrow is strictly `internal/agent → internal/ai`.

### 1.2 EventStream (plan 01)

```go
// Package: internal/ai
// Plan: 01-ai-core §4.2

type EventStream struct {
    C <-chan AssistantMessageEvent  // consumer read side
}
func NewEventStream() *EventStream
func (s *EventStream) Send(event AssistantMessageEvent)  // provider write side
func (s *EventStream) Close()                             // idempotent
func (s *EventStream) Result() (AssistantMessage, error)  // blocks until terminal
func (s *EventStream) Drain() (AssistantMessage, error)   // discard events, return result
```

**Critical contract:**
- **Terminal-state guarantee:** `Result()` is guaranteed to unblock exactly once. An `atomic.Bool` (`terminated`) guards the `result` channel — whichever of `Send(done/error)` or `Close()` wins the CAS publishes, the loser is a no-op.
- Buffer size is 32 events.
- `Drain()` is how `Complete()` / `CompleteSimple()` work — they iterate the channel to completion, then call `Result()`.

### 1.3 StopReason Constants (plan 01)

```go
// Package: internal/ai
// Plan: 01-ai-core §3.4

type StopReason string

const (
    StopReasonStop    StopReason = "stop"
    StopReasonLength  StopReason = "length"
    StopReasonToolUse StopReason = "toolUse"
    StopReasonError   StopReason = "error"
    StopReasonAborted StopReason = "aborted"
)
```

**There is no `StopReasonMaxTokens`.** All providers must map their max-token / length-limit termination to `StopReasonLength`. This was a recurring cross-provider consistency issue caught in R2/R3 reviews.

### 1.4 AgentDriver (plan 05)

```go
// Package: internal/agent
// Plan: 05-agent §3.2

type AgentDriver interface {
    Start(ctx context.Context, session *Session, prompt string) error
    Resume(ctx context.Context, session *Session) error
    Stop(ctx context.Context) error
    Subscribe(fn func(AgentEvent)) (unsubscribe func())
}
```

**Implementers:** `NativeDriver` (plan 05), `ClaudeCodeDriver` / `CodexDriver` (plan 09-termmux via adapter).

**Critical contract:**
- All drivers emit the same canonical `AgentEvent` types. The agent layer never inspects driver-specific internals.
- `Subscribe` callbacks must be isolated: a panic in one subscriber must not crash the agent loop.
- `NativeDriver` is in `internal/agent/loop.go`. 3rd-party driver adapters are in `internal/agent/driver_claudecode.go` and `driver_codex.go`.

### 1.5 AgentTool (plan 05 + 06)

```go
// Package: internal/agent
// Plan: 05-agent §3.3

type AgentTool struct {
    ai.Tool
    Label   string
    Execute func(ctx context.Context, toolCallID string, params map[string]any,
        onUpdate func(AgentToolResult)) (AgentToolResult, error)
}
```

**Critical contract:**
- The agent loop treats tools as pure interface calls. It does not know or care whether a `LocalBackend` or `SandboxBackend` handles execution — that is hidden behind tool construction by the factories.
- Both `NewLocalTools` and `NewSandboxTools` (plan 06 §3.2) must return tools with the same names, schemas, and behavioral semantics. Only execution placement differs.

### 1.6 ToolBackend (plan 06)

```go
// Package: internal/tools
// Plan: 06-built-in-tools §3.1

type ToolBackend interface {
    ExecuteTool(ctx context.Context, req ToolRequest, onProgress func(ToolProgress)) (*ToolResponse, error)
}

type ToolRequest struct {
    SessionID  string
    ToolName   string
    ToolCallID string
    Params     map[string]any
    Resources  *ResourceSpec
}

type ToolResponse struct {
    Content    []ai.ContentBlock
    SnapshotID string  // empty for LocalBackend
    ExitCode   *int
}

type ToolProgress struct {
    Content string
    IsError bool
}
```

**Implementers:** `LocalBackend` (in-process), `SandboxBackend` (RPC client to sandbox host).

**Critical contract:**
- `onProgress` is used for Tier 2 tools (bash) to stream incremental output. Tier 1 tools are fast enough to skip progress callbacks. **`SandboxBackend` must accept and propagate `onProgress`** — it uses server-streaming RPC (`ExecuteToolStream`) to receive progress chunks from the sandbox host and forwards them to the callback. Each streamed message is either a progress chunk or the final response.
- `SandboxBackend` propagates `ToolCallID` in both request and response for end-to-end traceability. The server echoes it back.
- **`SnapshotID` propagation:** The full chain is `SandboxHostService.ExecuteToolResponse.SnapshotID` → RPC stream → `SandboxBackend` → `ToolResponse.SnapshotID`. Per-tool snapshots are opt-in via `config.PerToolSnapshots` on the sandbox host. When enabled, snapshots are named `tool-{toolCallID}-{timestamp}`. `SnapshotID` is empty when per-tool snapshots are disabled (the default) — per-turn snapshots via `TurnComplete` are the primary mechanism.
- Tier classification is centralized via a `ClassifyTool` function shared across all backends. Do not implement independent classifiers.

### 1.7 RuntimeController (architecture doc)

```go
// Package: defined in architecture doc
// Implemented by: DefaultController (library built-in)

type RuntimeController interface {
    CreateSession(ctx context.Context, opts SessionOptions) (*Session, error)
    GetSession(ctx context.Context, id string) (*Session, error)
    ListSessions(ctx context.Context) ([]*Session, error)
    StopSession(ctx context.Context, id string) error
    PauseSession(ctx context.Context, id string) error
    ResumeSession(ctx context.Context, id string) error
    Subscribe(ctx context.Context, sessionID string) (<-chan AgentEvent, error)
    SessionState(ctx context.Context, id string) (SessionState, error)
}
```

**Critical contract:**
- Agent does NOT own session creation. The RuntimeController creates the session and passes it to `Agent.Start()`.
- Pause/Resume are controller-level operations. The agent does not implement pause state — the controller prevents new `Prompt`/`Continue` calls while paused.
- Session IDs are runtime-owned (`Session.ID`), not driver-native IDs. Driver session IDs are stored as `Session.DriverSessionID` for correlation only.

### 1.8 AgentEvent Stream Types (plans 05, 13)

```go
// Package: internal/rpc
// Plan: 13-rpc-layer §4.2

type AgentEventSender interface {
    Send(*AgentEventEnvelope) error
    Close() error
}

type AgentEventReceiver interface {
    Recv() (*AgentEventEnvelope, error)
    Close() error
}
```

**Critical contract:**
- The stream is **unidirectional**: agent process produces (Sender), RuntimeController consumes (Receiver).
- `StreamAgentEvents` returns `AgentEventReceiver` to the consumer side. The `AgentEventSender` is used internally by the agent/server process.
- Do NOT return the producer type to consumer code — this was a P1 finding in R2.

### 1.9 Canonical AgentEvent Types (plan 05)

```
Session lifecycle: session_started, session_ended
Turn lifecycle:    turn_started, turn_completed
Streaming:         agent_message_delta, agent_message_completed, thinking_delta
Tooling:           tool_started, tool_update, tool_completed
Control/state:     state_change, steering_applied, followup_enqueued, aborted
Errors:            driver_error, provider_error, tool_error
```

**Critical contract:**
- All drivers, RPC layers, and E2E tests must use exactly these names. Event taxonomy drift was the dominant R2 finding theme (3 of 10 findings).
- `session_started` is always the first lifecycle event. `session_ended` is always the last.
- `turn_completed` is the primary per-turn snapshot trigger (see §2.1).
- `state_change(idle)` fires only when the follow-up queue is empty.

### 1.10 SandboxHostService (plan 11)

```go
// Package: internal/sandbox
// Plan: 11-sandbox-host-service §3.1

type SandboxHostService struct { /* ... */ }

func NewSandboxHostService(cfg ServiceConfig, z zfs.ZFSManager, g gvisor.GVisorManager, logger *slog.Logger) *SandboxHostService

// Session management
func (s *SandboxHostService) CreateSession(ctx, CreateSessionRequest) (*SessionInfo, error)
func (s *SandboxHostService) DestroySession(ctx, sessionID) error
func (s *SandboxHostService) PauseSession(ctx, sessionID) error
func (s *SandboxHostService) ResumeSession(ctx, sessionID) error

// Tool execution
func (s *SandboxHostService) ExecuteTool(ctx, ExecuteToolRequest) (*ExecuteToolResponse, error)

// Snapshot management
func (s *SandboxHostService) TurnComplete(ctx, sessionID) (*SnapshotResult, error)
func (s *SandboxHostService) CreateSnapshot(ctx, sessionID, name) (*SnapshotResult, error)
func (s *SandboxHostService) RollbackSession(ctx, sessionID, snapshotID) error
func (s *SandboxHostService) ListSnapshots(ctx, sessionID) ([]zfs.SnapshotInfo, error)
```

**Critical contract:**
- `CreateSession` uses a mutex (`sessionsMu`) for atomic capacity-check + store to prevent TOCTOU race. This was a P1 finding.
- `TurnComplete` uses the `prospectiveTurn` pattern: compute turn number before snapshot, commit after success, return in result. Do not use undefined symbols.
- Session state machine: `Creating → Active → Paused → Active → Destroying → [destroyed]`, plus `Failed` from `Creating` or `Active`.
- ZFS rollback is synchronous (<100ms), so there is no `RollingBack` state.

### 1.11 ZFSManager (plan 09-zfs)

```go
// Package: internal/sandbox/zfs
// Plan: 09-sandbox-zfs §3.1

type ZFSManager interface {
    // Dataset ops
    CreateDataset(ctx, name, DatasetOptions) error
    CloneFromSnapshot(ctx, snapshot, newDataset) error
    DestroyDataset(ctx, name, DestroyOptions) error
    GetMountpoint(ctx, dataset) (string, error)
    SetMountpoint(ctx, dataset, mountpoint) error
    GetDatasetInfo(ctx, name) (*DatasetInfo, error)
    ListDatasets(ctx, parent) ([]DatasetInfo, error)
    DatasetExists(ctx, name) (bool, error)

    // Snapshot ops
    CreateSnapshot(ctx, dataset, snapName) (*SnapshotInfo, error)
    Rollback(ctx, dataset, snapName, RollbackOptions) error
    ListSnapshots(ctx, dataset) ([]SnapshotInfo, error)
    DestroySnapshot(ctx, dataset, snapName) error
    HoldSnapshot(ctx, dataset, snapName, tag) error
    ReleaseSnapshot(ctx, dataset, snapName, tag) error
    SnapshotExists(ctx, dataset, snapName) (bool, error)

    // Pool ops
    PoolStatus(ctx, pool) (*PoolStatus, error)
    PoolSpace(ctx, pool) (*PoolSpace, error)
    ImportPool(ctx, pool, device) error
    ExportPool(ctx, pool) error

    // Transfer
    EstimateSendSize(ctx, snapshot, SendOptions) (int64, error)
    Send(ctx, snapshot, SendOptions, w io.Writer) error
    Receive(ctx, dataset, r io.Reader) error
}
```

**Critical contract:**
- This is a pure ZFS abstraction. It does not know about agents, sessions, or tools.
- CLI-based implementation (`CLIManager`) — no `libzfs` / CGo dependency (resolved OQ1).
- All methods are safe for concurrent use.
- Name validation (`ValidateName()`) is enforced on all inputs — defense against injection via dataset/snapshot names.

### 1.12 GVisorManager (plan 10)

```go
// Package: internal/sandbox/gvisor
// Plan: 10-sandbox-gvisor §4.1

type GVisorManager interface {
    Run(ctx context.Context, opts ContainerOptions) (*ContainerResult, error)
    CleanupStale(ctx context.Context) (int, error)
    ActiveContainers() int
    Close() error  // idempotent, cancels in-flight, blocks until cleanup
}
```

**Critical contract:**
- Each `Run()` creates a fresh container, executes, captures output, and destroys. No persistent containers between calls.
- OOM detection: Only set `OOMKilled=true` when `Status == StatusExited && ExitCode == 137`. Timeout/cancel have separate status codes (`StatusTimedOut`, `StatusKilled`) — do NOT check exit code for those cases. This was a P1 finding.
- `Close()` returns `ErrManagerClosed` on subsequent `Run()` calls. Idempotent and safe for concurrent callers.
- `ContainerResult.Duration` is end-to-end wall-clock; `BootDuration` is runsc-specific. Both must be set.

### 1.13 DataStore (plan 07)

```go
// Package: internal/tools/codeinterp
// Plan: 07-code-interpreter §5.4

type DataStore interface {
    Write(key string, data []byte) error
    Read(key string) ([]byte, error)
    ReadRange(key string, offset, limit int64) ([]byte, error)
    List(prefix string) ([]string, error)
    Delete(key string) error
    Close() error
}

type SearchableDataStore interface {
    Search(keyPrefix string, pattern string) ([]Match, error)
}
```

**Critical contract:**
- `SearchableDataStore` is an optional capability. If the backing store does not implement it, `store_search` returns a typed `ErrNotSupported` error.
- `Search` interprets `keyPrefix` as a key prefix, returning matches across all keys beginning with that prefix.
- `Close()` follows explicit session-scoped lifecycle — the caller owns cleanup.

---

## 2. Lifecycle Ordering Invariants

### 2.1 Per-Turn Snapshot Trigger Seam

This is the most important lifecycle invariant in the system. It was refined across multiple review rounds (R1 P1, R2 P1, R3 verification).

```
Agent emits:  turn_completed → (optionally more turns if follow-ups) → state_change(idle)
```

**Rules:**
1. `turn_completed` is emitted at **every** turn boundary, including intermediate turns in follow-up chains.
2. `turn_completed` is the **primary snapshot trigger**. Subscribers (sandbox host, RuntimeController) create snapshots on this event regardless of whether the agent transitions to idle.
3. `state_change(idle)` fires **only** when the follow-up queue is empty and no more turns will execute. It is informational, not a snapshot trigger.
4. Per-tool-call snapshots remain optional backend policy (via `Snapshot` flag on `ExecuteToolRequest`).

**Cardinality invariant for test assertions:**
- `count(turn_completed) == completed_turns`
- `count(state_change(idle)) == completed_turn_sequences` (one per prompt+follow-up chain)

### 2.2 Agent Startup / Shutdown

```
Startup:
1. RuntimeController.CreateSession() → session object
2. Agent.Start(ctx, session, prompt) → delegates to AgentDriver.Start()
3. Driver subscribes to provider stream, emits session_started
4. Turn loop begins

Shutdown:
1. Agent.Stop(ctx) or natural completion
2. Driver emits session_ended with outcome metadata
3. Agent transitions to Exited state
4. RuntimeController DestroySession() cleans up sandbox if applicable
```

### 2.3 Sandbox Host Session Lifecycle

```
CreateSession:
1. Generate/validate session ID
2. Acquire sessionsMu lock (TOCTOU guard)
3. Check capacity (MaxSessions)
4. ZFS CloneFromSnapshot(base, sessionDataset)
5. ZFS GetMountpoint(sessionDataset)
6. Create Session object (state=Active, turnCount=0)
7. Store in sessions map
8. Release lock

TurnComplete:
1. Compute prospectiveTurn = session.turnCount + 1
2. ZFS CreateSnapshot(dataset, snapshotName)
3. Commit: session.turnCount = prospectiveTurn
4. Return SnapshotResult{TurnNumber: prospectiveTurn}

DestroySession:
1. Drain in-flight tool executions (PauseDrainTimeout)
2. Transition state → Destroying
3. ZFS DestroyDataset(dataset, Recursive: true)
4. Remove from session registry
```

### 2.4 gVisor Container Lifecycle

```
Run():
1. Generate container ID
2. Build OCI spec (cgroups, mounts, rootfs)
3. Create bundle directory, write config.json
4. runsc run --bundle <path> <id>
5. Wait for exit (or timeout/cancel)
6. Capture stdout/stderr
7. Check OOM: only if StatusExited && exitCode==137
8. runsc delete <id>
9. Cleanup bundle directory
10. Return ContainerResult
```

### 2.5 RPC SandboxBackend Lifecycle

```
1. CreateSession succeeds → sessionID returned
2. Construct SandboxBackend(client, sessionID)
3. Pass SandboxBackend to agent loop as ToolBackend
4. Each ExecuteTool is an independent unary RPC (no persistent stream)
5. On PauseSession: discard SandboxBackend
6. On ResumeSession: create NEW SandboxBackend with same sessionID
7. On DestroySession: discard SandboxBackend (no cleanup RPC needed)
```

---

## 3. Config Contract

### 3.1 Configuration Flow

Configuration flows top-down from the `cmd/sandbox-host` binary or the consumer's main function:

```
cmd/sandbox-host (or consumer main)
  └─ ServiceConfig (plan 11 §3.3)
       ├─ Pool config: PoolName, BasesDataset, SessionsDataset
       ├─ Session limits: MaxSessions, DefaultSessionQuota
       ├─ Snapshot config: SnapshotPrefix, MaxSnapshotsPerSession
       ├─ Tool execution: DefaultResources (ResourceSpec), ToolTimeout
       ├─ Health: PoolSpaceWarnThreshold, PoolSpaceCritThreshold, HealthCheckInterval
       ├─ Drain: PauseDrainTimeout
       └─ Shutdown: ShutdownTimeout

  └─ ZFSManager (plan 09): receives pool/dataset names from ServiceConfig
  └─ GVisorManager (plan 10): receives ManagerConfig
       ├─ RunscPath, BundleDir, StateDir
       ├─ DefaultResources (ResourceSpec)
       ├─ HealthCheckInterval, StaleContainerTimeout
       └─ DebugLogging (configurable, default off in prod)
```

### 3.2 Shared Keys

- **`SessionID`**: Generated by `CreateSession` or passed in request. Used consistently across `SandboxHostService`, `ToolBackend`, `RPC`, `AgentEvent` keying, and snapshot management. Always the runtime session ID, never the driver-native ID.
- **`ToolCallID`**: Generated by the LLM (via `ToolCall.ID`). Propagated through agent → tool → backend → RPC → sandbox host. Echoed in responses. Used as idempotency key for `ExecuteTool` retries.
- **`ResourceSpec`**: Used by both `ContainerOptions` (plan 10) and `ExecuteToolRequest` (plan 11). Contains CPUs, MemoryMB, MaxPIDs, Timeout, MaxOutputBytes.

### 3.3 Required vs Optional

| Field | Required | Default |
|-------|----------|---------|
| `ServiceConfig.PoolName` | Yes | — |
| `ServiceConfig.MaxSessions` | No | 0 (unlimited) |
| `ServiceConfig.ToolTimeout` | No | 5m |
| `ServiceConfig.ShutdownTimeout` | No | 30s |
| `ResourceSpec.CPUs` | No | 0 (no limit) |
| `ResourceSpec.MemoryMB` | No | 0 (no limit) |
| `ResourceSpec.MaxPIDs` | No | 0 (default 1024) |
| `ResourceSpec.Timeout` | No | 0 (use context deadline) |
| `ResourceSpec.MaxOutputBytes` | No | 0 (default 10 MB) |
| `ManagerConfig.RunscPath` | Yes | — |
| `ManagerConfig.BundleDir` | Yes | — |

---

## 4. Common Pitfalls

These are recurring themes from the three review rounds (112 findings total). Be aware of these when implementing.

### 4.1 API Contract and Interface Specification Gaps (~25 findings)

The most frequent R1 issue. Interfaces were declared but insufficiently specified for independent implementation.

**How to avoid:**
- When implementing an interface from a plan, verify every method signature, return type, and error condition against the plan doc. If something is ambiguous, check the architecture doc and connected plan docs before making assumptions.
- When adding a new interface method, define its full contract including: error conditions, thread safety, idempotency, and what happens at lifecycle boundaries (startup, shutdown, pause).

### 4.2 Correctness Defects in Pseudo-Code (~12 P1+ findings)

Plans contained pseudo-code with real bugs: undefined symbols, wrong type references, incorrect exit-code checks.

**How to avoid:**
- Don't copy pseudo-code from plans verbatim. Use the types and constants defined in the **canonical** source (typically plan 01 for AI types, plan 05 for agent types, plan 11 for sandbox types).
- Verify all constant references compile before committing. If a plan references `ai.StopReasonMaxTokens`, that constant does not exist — use `ai.StopReasonLength`.

### 4.3 Cross-Document Consistency (~10 findings)

Terminology drift ("orchestrator" vs "RuntimeController") and event name divergence across plans.

**How to avoid:**
- Use **exactly** the terminology from the architecture doc and plan 05's canonical event types.
- `RuntimeController`, not "orchestrator".
- `session_started` / `session_ended`, not "run_started" / "run_ended" or similar.
- `AgentEventReceiver` on consumer side, `AgentEventSender` on producer side. Never return the wrong type.

### 4.4 State Machine and Lifecycle Issues (~8 findings)

Race conditions in session creation capacity checks, turn count management, and in-flight tool draining.

**How to avoid:**
- `CreateSession` capacity check MUST be under `sessionsMu` lock to prevent TOCTOU.
- `TurnComplete` uses the `prospectiveTurn` pattern — compute before, commit after.
- `DestroySession` must drain in-flight tools before transitioning to `Destroying`.
- `PauseSession` must also drain in-flight tools (`PauseDrainTimeout`).

### 4.5 OOM Detection

**Correct pattern (plan 10, post-R2 fix):**
```go
// Only classify as OOM when the process exited normally and the
// exit code indicates SIGKILL from the OOM killer.
if result.Status == StatusExited && result.ExitCode == 137 {
    result.OOMKilled = true
    result.Status = StatusOOMKilled
}
```

**DO NOT** check exit code when status is `StatusTimedOut` or `StatusKilled` — those have `ExitCode == -1` but are NOT OOM kills.

### 4.6 Cross-Seam Interface Drift (seam review, 3 findings)

The seam review caught three cases where connected components had interface definitions that had drifted out of sync:

1. **ToolBackend signature drift (P1):** Plan 06 defined `ToolBackend.ExecuteTool` with an `onProgress` callback, but plan 13's `SandboxBackend` implementation omitted it. The `SandboxBackend` must accept `onProgress` and use server-streaming RPC (`ExecuteToolStream`) to forward progress from the sandbox host.

2. **Snapshot metadata gap (P1):** Plan 06's `ToolResponse` includes `SnapshotID`, but plan 11's `ExecuteToolResponse` did not. The `SnapshotID` must flow through the entire chain: `SandboxHostService` → RPC response → `SandboxBackend` → `ToolResponse`. Per-tool snapshots are opt-in (`config.PerToolSnapshots`); when disabled, `SnapshotID` is empty.

3. **RPC idempotency mismatch (P2):** Plan 13 claimed `CreateSession`, `PauseSession`, and `ResumeSession` were "naturally idempotent", but plan 11's state machine returns errors for invalid state transitions (e.g., pausing an already-paused session returns `failed_precondition`). These methods are **state-guarded**, not naturally idempotent. Callers must handle state errors rather than blindly retrying.

**How to avoid:** When implementing one side of a seam, always read the connected plan's interface definition. Verify method signatures, parameter lists, response fields, and error semantics match exactly. The Seam Reference Table (§5) maps every connected pair.

---

## 5. Seam Reference Table

Connected component pairs with the interface at each boundary. Reference these when implementing to ensure your side of the seam matches.

| Component A | Component B | Interface | Plan A § | Plan B § |
|-------------|-------------|-----------|----------|----------|
| `internal/ai` (Provider) | `internal/agent` (NativeDriver) | `ai.StreamSimple/Stream → EventStream` | 01 §5.1 | 05 §5.1 |
| `internal/ai` (types) | `internal/tools` (tool schemas) | `ai.Tool`, `ai.ContentBlock` | 01 §3.5 | 06 §3.1 |
| `internal/agent` (AgentTool) | `internal/tools` (factories) | `[]agent.AgentTool` via `NewLocalTools/NewSandboxTools` | 05 §3.3 | 06 §3.2 |
| `internal/agent` (events) | `internal/rpc` (event stream) | `AgentEvent → AgentEventSender/Receiver` | 05 §3.4 | 13 §4.2 |
| `internal/agent` (Agent) | RuntimeController | `Agent.Start/Stop/Subscribe`, `Session` | 05 §7.1 | arch doc |
| `internal/agent` (turn boundary) | `internal/sandbox` (snapshots) | `turn_completed` event → `TurnComplete()` | 05 §5.5 | 11 §4.5 |
| `internal/tools` (ToolBackend) | `internal/sandbox` (service) | `ToolBackend.ExecuteTool(onProgress) → SandboxHostService.ExecuteTool` + `SnapshotID` propagation | 06 §3.1 | 11 §3.1 |
| `internal/tools` (SandboxBackend) | `internal/rpc` (client) | `SandboxBackend → ExecuteToolStream` (server-streaming RPC for progress + final response) | 06 §5.2 | 13 §6 |
| `internal/rpc` (server) | `internal/sandbox` (service) | `RPC server → SandboxHostService` methods | 13 §5 | 11 §3.1 |
| `internal/sandbox` (service) | `internal/sandbox/zfs` | `ZFSManager` interface | 11 §2.1 | 09-zfs §3.1 |
| `internal/sandbox` (service) | `internal/sandbox/gvisor` | `GVisorManager` interface | 11 §2.1 | 10 §4.1 |
| `internal/sandbox` (tier routing) | `internal/tools` (classifier) | Shared `ClassifyTool` function | 11 §router | 06 §5.3 |
| `internal/tools/codeinterp` | `internal/tools` (tool catalog) | `discover → describe → invoke` via tool factories | 07 §5.2 | 06 §3.2 |
| `internal/tools/codeinterp` | `internal/ai` (Provider) | `llm_call/llm_batch` → `ai.StreamSimple` | 07 §5.3 | 01 §5.1 |
| `internal/termmux` | `internal/agent` (driver adapters) | `TermmuxDriverAdapter → AgentDriver` | 09-tmux §3 | 05 §3.2 |
| `tests/integration/` | `internal/agent` + `internal/tools` | Full agent loop with deterministic provider | 08 §2 | 05, 06 |
| `tests/integration/mode3/` | `internal/rpc` + `internal/sandbox` | Remote dispatch via `MemorySandboxService` or real host | 14 §3 | 13, 11 |

### 5.1 Import Flow (No Circular Dependencies)

```
internal/ai/sse          → stdlib only
internal/ai/models       → embedded JSON
internal/ai              → internal/ai/sse, internal/ai/models
internal/ai/provider/*   → internal/ai, internal/ai/sse
internal/agent           → internal/ai, internal/termmux (adapter files only)
internal/tools           → internal/ai, internal/agent (AgentTool type only)
internal/tools/codeinterp → internal/tools, internal/ai
internal/sandbox/zfs     → stdlib only
internal/sandbox/gvisor  → stdlib, OCI spec libs, otel
internal/sandbox         → internal/sandbox/zfs, internal/sandbox/gvisor, internal/tools, internal/ai
internal/rpc             → internal/sandbox, internal/agent, internal/tools
internal/termmux         → stdlib, otel (does NOT import internal/agent)
cmd/sandbox-host         → internal/sandbox, internal/rpc
tests/integration/                → all internal packages
```

**Hard rule:** `internal/ai` must NEVER import `internal/agent`. `internal/termmux` must NEVER import `internal/agent`. The dependency flows downward.

---

## 6. Two-Tier Tool Execution Reference

| Tier | Tools | Execution | Container? | Snapshot? |
|------|-------|-----------|------------|-----------|
| **1** | read, write, edit, grep, glob, git status/diff/log/show | Go function on ZFS dataset | No | No (by default) |
| **2** | bash, git add/commit | gVisor container with bind-mounted ZFS dataset | Yes (~50-150ms boot) | Auto post-execution |

- ~80% of tool calls are Tier 1, bypassing container overhead entirely.
- Tier 2 containers are spun up per tool call and destroyed after completion — NOT kept alive during LLM thinking.
- Classification is centralized via `ClassifyTool()` (shared between plan 06 and plan 11).

---

## 7. RPC Layer Quick Reference

- **Protocol:** ConnectRPC over HTTP/2 (or HTTP/1.1 fallback).
- **Max message size:** 16 MB (configured on both client and server).
- **Truncation:** Tool output exceeding 16 MB is truncated by sandbox host. `ToolOutputTruncation` metadata included in response.
- **Versioning:** `x-api-version` header on every call. Additive-only protobuf evolution. Minimum version enforcement on server.
- **Idempotency:** `ExecuteTool` uses `tool_call_id` as idempotency key (5-minute dedup window). `GetSession`, `ListSnapshots`, `DestroySession`, `RollbackSession` are naturally idempotent. **`CreateSession`, `PauseSession`, `ResumeSession` are state-guarded** — they return typed errors (`already_exists`, `failed_precondition`) if the session is not in the expected state. Callers must check these errors before retrying blindly.
- **Retry policy:** Retry on `unavailable` and transient transport errors. Never retry `invalid_argument`, `not_found`, `permission_denied`. For state-guarded methods, only retry after transient RPC failure if the session may still be in the expected state.

---

## 8. Testing Quick Reference

- **Unit tests**: Co-located with implementation packages. Run under `-race`. Coverage check per package.
- **E2E tests**: `tests/integration/` directory. Do NOT mix with application code packages.
- **Deterministic provider**: Default for CI. Uses canned provider traces and fixture repos.
- **Live provider**: Behind secrets gate. Optional smoke lane.
- **MemorySandboxService**: In-memory fake for Mode 3 tests. Implements full `SandboxHostService` interface with in-memory filesystem, snapshot-as-copy, and session state machine.
- **Fixture repos**: Defined in plan 08. Reused across Mode 1 and Mode 3 tests.
