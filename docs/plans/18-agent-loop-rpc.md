# 18: Agent Loop RPC Service

**Status:** Draft (R1 + R2 + seam reviews incorporated)
**Depends on:** 05-agent, 13-rpc-layer, 11-sandbox-host-service.add01
**Depended on by:** Orchestrator application (future)
**Scope:** Wrap the existing agent loop with a ConnectRPC interface so it can run as a standalone service, deployable anywhere. Adds a SandboxControl abstraction for unified sandbox lifecycle management across native and cloud providers.
**Incorporated reviews:** 18-agent-loop-rpc-review-r1-a.md, 18-agent-loop-rpc-review-r1-b.md, 18-agent-loop-rpc-review-r2-a.md, 18-agent-loop-rpc-review-r2-b.md, 18-agent-loop-rpc-seam-review.md

---

## 1. Overview

The agent loop (`internal/agent`) can only run in-process today. This plan adds an RPC interface around it so that an orchestrator (or any client) can create agent sessions, send messages, subscribe to events, and issue steering commands — all over the network.

This is the missing seam between the orchestrator layer and the agent layer in the three-layer architecture. It enables:

- **Agent loop as a standalone process** on any machine
- **Agent loop inside a sandbox** launched by a sandbox-host or cloud provider
- **Agent loop in-process** with the orchestrator (zero-cost — same Go interface, no RPC)

Additionally, this plan introduces the **SandboxControl** interface, which abstracts sandbox lifecycle (create, destroy, pause, resume, launch process) at the **orchestrator level**. SandboxControl is the control-plane counterpart to ExecutionEnvironment: SandboxControl provisions and manages sandbox instances from the orchestrator, while ExecutionEnvironment executes tools within an already-provisioned sandbox from the agent loop. They are complementary, not competing -- see section 6 for detailed boundary clarification.

### Key Design Principle

The Agent Loop RPC follows the same pattern as the existing Sandbox RPC: a Go interface (`AgentService`) that can be called in-process or over ConnectRPC. The transport is transparent to the caller.

### Relationship to RuntimeController

The architecture doc defines a `RuntimeController` interface at the orchestrator level. `AgentService` is **not** a replacement for `RuntimeController` -- they operate at different layers:

- **AgentService** is the agent-loop-facing API. It is what an agent loop server exposes (or what the in-process `AgentLoopService` implements). It manages individual agent sessions: create, message, steer, subscribe to events, destroy.
- **RuntimeController** is the orchestrator-facing API. It is what application code calls. A `RuntimeController` implementation delegates to one or more `AgentService` instances (in-process or remote) and composes them with `SandboxControl` for sandbox lifecycle, session persistence to SQLite, and cross-agent coordination.

```mermaid
graph TD
    APP[Application Code] --> RC[RuntimeController]
    RC --> AS1[AgentService - in-process]
    RC --> AS2[AgentService - remote via RPC]
    RC --> SC[SandboxControl]
    RC --> DB[(SQLite)]
```

---

## 2. Architecture

### 2.1 Component Diagram

```mermaid
graph TD
    subgraph "Orchestrator Process"
        ORC[Orchestrator]
        ASC[AgentService Client]
        SCC[SandboxControl Client]
    end

    subgraph "Agent Loop Process"
        ALS[AgentLoopServer]
        AGT[Agent]
        DRV[NativeDriver]
        EE[ExecutionEnvironment]
    end

    subgraph "Sandbox Host"
        SHS[SandboxHostService]
        ZFS[ZFS Manager]
        GV[gVisor Manager]
    end

    subgraph "Cloud Provider"
        CP[E2B / Daytona / Fly API]
    end

    ORC --> ASC
    ORC --> SCC
    ASC -->|ConnectRPC| ALS
    ALS --> AGT
    AGT --> DRV
    DRV --> EE
    EE -->|ConnectRPC| SHS
    SCC -->|ConnectRPC| SHS
    SCC -->|HTTP| CP
    SHS --> ZFS
    SHS --> GV
```

### 2.2 In-Process vs Remote

When agent loop runs in-process with the orchestrator, no RPC is involved -- the orchestrator calls `AgentService` methods directly on the in-process implementation. When remote, the orchestrator uses `AgentServiceClient` which wraps ConnectRPC calls.

```mermaid
graph LR
    subgraph "In-Process (All Local)"
        O1[Orchestrator] --> AS1[AgentLoopService]
        AS1 --> A1[Agent]
    end

    subgraph "Remote (Agent in Sandbox / standalone)"
        O2[Orchestrator] --> AC2[AgentServiceClient]
        AC2 -->|ConnectRPC| ALS2[AgentLoopServer]
        ALS2 --> AS2[AgentLoopService]
        AS2 --> A2[Agent]
    end
```

---

## 3. AgentService Interface

This is the core Go interface. Both the in-process implementation and the RPC server implement it.

**Package location:** `internal/agent/api/agent.go` (NOT `internal/rpc/api`). The interface lives in a new `internal/agent/api` package to avoid circular imports -- `internal/rpc/api` imports `internal/agent` types, so `internal/agent` cannot import `internal/rpc/api`. This follows the same pattern as `internal/sandbox/environment` (transport-neutral interface) vs `internal/rpc/api` (RPC-specific types). The RPC layer wraps AgentService; it does not define it.

```go
// internal/agent/api/agent.go

// AgentService manages agent loop sessions. Implemented by AgentLoopService
// (in-process) and AgentServiceClient (remote via ConnectRPC).
type AgentService interface {
    // CreateSession initializes a new agent session with the given config.
    // The agent loop is ready to receive prompts after this returns.
    // If req.SessionID is non-empty and already exists, returns an error
    // (not idempotent -- callers should use GetSession to check first).
    CreateSession(ctx context.Context, req *CreateAgentSessionRequest) (*CreateAgentSessionResponse, error)

    // GetSession returns the current state of a session.
    GetSession(ctx context.Context, req *GetAgentSessionRequest) (*GetAgentSessionResponse, error)

    // ListSessions returns all active sessions managed by this service.
    ListSessions(ctx context.Context, req *ListAgentSessionsRequest) (*ListAgentSessionsResponse, error)

    // SendMessage sends a user message to the agent and begins a new turn.
    // Returns a stream of events for this turn. The stream terminates when
    // the turn completes (EventTurnCompleted received). See section 3.2
    // for turn-scoping details. Returns BusyError if a turn is already active.
    SendMessage(ctx context.Context, req *SendMessageRequest) (EventReceiver, error)

    // Continue resumes the agent without a new user message (e.g., after tool use).
    // Returns a stream of events for this turn (same scoping as SendMessage).
    Continue(ctx context.Context, req *ContinueRequest) (EventReceiver, error)

    // Steer injects a steering instruction into the current turn.
    // Returns FailedPrecondition error if no turn is active (agent in StateIdle).
    Steer(ctx context.Context, req *SteerRequest) (*SteerResponse, error)

    // FollowUp queues a follow-up prompt for after the current turn completes.
    // Unlike Steer, this is valid in any state -- it queues for the next turn.
    FollowUp(ctx context.Context, req *FollowUpRequest) (*FollowUpResponse, error)

    // Abort requests the agent to stop the current turn.
    Abort(ctx context.Context, req *AbortRequest) (*AbortResponse, error)

    // SubscribeEvents opens a persistent event stream for a session.
    // Unlike SendMessage/Continue (which stream events for one turn),
    // this streams ALL events for the session lifecycle.
    // Internally, this delegates to the existing AgentEventService infrastructure.
    // Events are delivered to both per-turn streams and session-wide subscribers
    // simultaneously (dual delivery). Consuming the per-turn stream is NOT
    // required for the turn to proceed -- the turn runs regardless.
    SubscribeEvents(ctx context.Context, req *SubscribeEventsRequest) (EventReceiver, error)

    // ResumeSession creates a new session initialized with an existing conversation log.
    // The agent picks up from the last message in the log.
    // This enables:
    //   - Crash recovery (orchestrator replays persisted conversation)
    //   - Session forking (same conversation log -> multiple new sessions)
    //   - Cross-agent migration (conversation from one agent type resumed on another)
    ResumeSession(ctx context.Context, req *ResumeSessionRequest) (*ResumeSessionResponse, error)

    // DestroySession tears down a session and releases all resources.
    // If a turn is active, it is forcefully cancelled (context cancelled).
    // Open event streams for the session are closed with EOF.
    // DestroySession does NOT wait for the active turn to finish.
    DestroySession(ctx context.Context, req *DestroyAgentSessionRequest) (*DestroyAgentSessionResponse, error)

    // Close gracefully shuts down the service, stopping all sessions and
    // releasing all resources. Called on server shutdown.
    Close() error
}

// EventReceiver is the transport-neutral interface for receiving agent events.
// This is defined in internal/agent/api (NOT internal/rpc/api) so that
// AgentService remains free of RPC-layer imports. The RPC layer adapts
// between this interface and the wire-format AgentEventReceiver (which wraps
// events in AgentEventEnvelope with a SessionID field for multiplexed streams).
//
// Error contract:
//   - Recv() returns (*event, nil) for each event.
//   - Recv() returns (nil, io.EOF) for normal stream end (turn completed,
//     session destroyed, or Close() called by the consumer).
//   - Recv() returns (nil, err) for abnormal termination (agent crash,
//     internal error). The error is never io.EOF in this case.
//   - After Recv() returns io.EOF or an error, subsequent calls return
//     the same result (the stream is terminal).
//   - Close() is idempotent. Calling Close() causes any blocked Recv()
//     to return (nil, io.EOF).
//
// The RPC adapter (AgentRPCServer) maps these errors as follows:
//   - io.EOF -> clean ConnectRPC stream closure (no error frame)
//   - other errors -> toConnectError() mapping, then stream closure
type EventReceiver interface {
    // Recv returns the next event. See error contract above.
    Recv() (*agent.AgentEvent, error)
    // Close releases resources associated with this receiver.
    // Idempotent. Causes any blocked Recv() to return (nil, io.EOF).
    Close() error
}
```

The existing `AgentEventReceiver` in `internal/rpc/api/types.go` (which returns `*AgentEventEnvelope` containing a session ID and event) remains for the RPC transport layer. The `AgentRPCServer` adapts between `EventReceiver` (returned by `AgentLoopService`) and `AgentEventReceiver` (used by the ConnectRPC stream handler) by wrapping each `*agent.AgentEvent` in an `AgentEventEnvelope` with the session ID from the request.

### 3.1 Request/Response Types

```go
// internal/agent/api/agent_types.go

// ToolEnvironmentType is a typed constant for ToolEnvironmentConfig.Type.
type ToolEnvironmentType string

const (
    ToolEnvLocal   ToolEnvironmentType = "local"
    ToolEnvSandbox ToolEnvironmentType = "sandbox"
)

// SessionConfig holds shared configuration for creating or resuming a session.
// Both CreateAgentSessionRequest and ResumeSessionRequest embed this.
type SessionConfig struct {
    SessionID    string            // Optional; generated (UUID) if empty
    Driver       string            // Driver name (e.g., "native")
    Model        string            // Model ID (e.g., "claude-sonnet-4-6")
    Provider     string            // Provider API name (e.g., "anthropic")
    SystemPrompt string            // System prompt for the agent
    Tools        []string          // Tool names to enable (e.g., ["bash", "read_file", "edit_file"])
    Metadata     map[string]any    // Arbitrary session metadata

    // Tool execution config
    ToolEnvironment ToolEnvironmentConfig
}

type ToolEnvironmentConfig struct {
    Type ToolEnvironmentType

    // For Type=ToolEnvSandbox: address of sandbox-host
    SandboxHostAddr string

    // For Type=ToolEnvSandbox: sandbox session ID (pre-created by orchestrator via SandboxControl)
    SandboxSessionID string

    // For Type=ToolEnvLocal: root directory for file operations
    LocalRootDir string
}

type CreateAgentSessionRequest struct {
    SessionConfig
}

type CreateAgentSessionResponse struct {
    SessionID string
    State     string // AgentState as string
}

type GetAgentSessionRequest struct {
    SessionID string
}

type GetAgentSessionResponse struct {
    SessionID       string
    State           string
    Metrics         agent.SessionMetrics // Reuse existing agent.SessionMetrics directly
    ConversationLen int                  // Use Agent.ConversationLen() (lightweight, no clone)
}

// Note: GetSession should use a lightweight Agent.ConversationLen() method
// that acquires the lock and returns just the count, rather than calling
// Agent.Session() which clones the entire conversation log. For large
// conversations (thousands of messages), cloning is wasteful for a count.

type ListAgentSessionsRequest struct {
    // Filter by labels (optional)
    Labels map[string]string
}

type ListAgentSessionsResponse struct {
    Sessions []AgentSessionSummary
}

type AgentSessionSummary struct {
    SessionID string
    State     string
    CreatedAt time.Time
}

type SendMessageRequest struct {
    SessionID string
    Message   string
}

type ContinueRequest struct {
    SessionID string
}

type SteerRequest struct {
    SessionID string
    Message   string
}

type SteerResponse struct{}

type FollowUpRequest struct {
    SessionID string
    Message   string
}

type FollowUpResponse struct{}

type AbortRequest struct {
    SessionID string
    Reason string
}

type AbortResponse struct{}

type ResumeSessionRequest struct {
    SessionConfig

    // Schema version for the conversation log format. Used to detect
    // incompatible log formats from older versions. Current version: 1.
    SchemaVersion int

    // The conversation log to resume from. The agent starts in a state
    // as if it had this conversation, ready for the next prompt.
    ConversationLog []AgentMessageRecord
}

type AgentMessageRecord struct {
    Turn      int
    Role      string          // "user", "assistant", "tool_result"
    Content   json.RawMessage // Serialized message content (see section 3.3)
    CreatedAt time.Time
}

type ResumeSessionResponse struct {
    SessionID       string
    State           string
    ConversationLen int
    // Warnings produced during validation (e.g., unknown tool references in
    // tool_result records, non-contiguous turn numbers).
    Warnings []string
}

type SubscribeEventsRequest struct {
    SessionID string
}

type DestroyAgentSessionRequest struct {
    SessionID string
}

type DestroyAgentSessionResponse struct{}
```

**Note on SessionMetrics:** The response types use `agent.SessionMetrics` directly from `internal/agent/types.go` rather than defining a duplicate. The RPC codec layer (see section 5.4) handles serialization/deserialization for the wire format, following the same pattern as the existing sandbox codec in `internal/rpc/codec/sandbox_map.go`.

**Note on API key handling:** The `APIKey` field has been removed from `SessionConfig`. API keys must NOT be transmitted over the AgentService RPC. Instead, API keys are injected into the agent process via environment variables (through `SandboxControl.LaunchProcess.Env` for sandbox-launched mode, or standard env vars for standalone mode). The `flexagent serve agent` process reads `ANTHROPIC_API_KEY` (or provider-specific equivalents) from its environment. This eliminates the risk of key exposure in RPC logs, in-memory retention, and compromised sandbox processes. See resolved Open Question 3 in section 17.

### 3.2 Turn-Scoped Event Streams

`SendMessage` and `Continue` return an `EventReceiver` that is scoped to a single turn. Implementation:

1. The `AgentLoopService` subscribes to the session-wide event stream (via `Agent.Subscribe()`) **before** calling `agent.Start()`/`agent.Prompt()`. This ordering is critical to avoid losing early events (e.g., `EventTurnStarted`) that the driver goroutine emits immediately after launch.
2. It wraps the subscription in a filtering `EventReceiver` that:
   - Forwards all events for the current turn
   - Closes the stream (returns `io.EOF`) when `EventTurnCompleted` is received for that turn
3. If `agent.Start()`/`agent.Prompt()` fails after subscription is established, the subscription is closed and discarded.
4. Multiple concurrent `SendMessage` calls on the same session return `BusyError` -- only one turn can be active at a time (enforced by the existing `Agent.Start()`/`Agent.Prompt()` API).

The per-turn stream and any active `SubscribeEvents` stream deliver the **same events simultaneously** (dual delivery). This is not a problem -- the per-turn stream is a convenience for callers who only care about one turn. The orchestrator typically uses `SubscribeEvents` for persistence and the per-turn stream is used by direct API callers.

**Important:** Consumers MUST NOT count or persist events from both streams. The session-wide `SubscribeEvents` stream is the canonical source for persistence. The per-turn stream is a convenience for simple callers that do not use `SubscribeEvents`. If both streams are consumed, the consumer must deduplicate or choose one stream per purpose.

### 3.3 AgentMessageRecord Serialization Format

The `AgentMessageRecord.Content` field uses `json.RawMessage` with a defined JSON schema for each role:

**Role "user":**
```json
{
    "text": "the user message text"
}
```

**Role "assistant":**
```json
{
    "content_blocks": [
        {"type": "text", "text": "..."},
        {"type": "thinking", "thinking": "...", "signature": "..."},
        {"type": "tool_use", "id": "...", "name": "...", "input": {...}}
    ],
    "model": "claude-sonnet-4-6",
    "stop_reason": "toolUse",
    "error_message": "",
    "usage": {"input_tokens": 100, "output_tokens": 50}
}
```

**Role "tool_result":**
```json
{
    "tool_call_id": "...",
    "tool_name": "...",
    "content": [
        {"type": "text", "text": "..."}
    ],
    "is_error": false
}
```

**Bidirectional mapping with `agent.AgentMessage`:**

The conversion between `AgentMessageRecord` and `agent.AgentMessage` (which wraps `ai.Message`) is implemented in a dedicated codec package at `internal/agent/api/codec.go`:

```go
// internal/agent/api/codec.go

// AgentMessageToRecord converts an in-memory AgentMessage to the serializable record format.
func AgentMessageToRecord(msg agent.AgentMessage) (AgentMessageRecord, error)

// RecordToAgentMessage converts a serialized record back to an in-memory AgentMessage.
// Returns an error if the role is unrecognized or the content fails to parse.
func RecordToAgentMessage(rec AgentMessageRecord) (agent.AgentMessage, error)
```

**Note on `stop_reason` and `error_message`:** The `stop_reason` field is critical for resume semantics -- if the last assistant message stopped with `"toolUse"`, the agent knows it needs to continue with tool execution; if `"length"`, the context window was exhausted. The `error_message` field captures any provider-level error. The `API` and `Provider` fields from `ai.AssistantMessage` are intentionally omitted from the record schema since they are metadata about which provider generated the response, not semantic conversation content.

**Known lossy conversions:** Image content blocks (`ai.ImageBlock`) are serialized to base64 in the record but this is lossless. Multi-block assistant messages preserve all blocks. Tool argument schemas are preserved as `json.RawMessage`. Cross-agent resume to Claude Code via termmux `ConversationEntry` IS lossy -- multi-block messages are flattened to single strings and image content is dropped. This is acceptable because cross-agent resume is a best-effort operation and the agent will see the text content of the conversation even if structured formatting is lost. Tool name mapping for cross-agent resume requires both agents to share the same tool definitions; cross-tool-schema resume is not supported in this plan.

### 3.4 ResumeSession Validation

`ResumeSession` performs the following validation on the conversation log before creating the session:

1. **Schema version check:** If `SchemaVersion` does not match the current version (1), return an error with a descriptive message.
2. **Record parsing:** All records must successfully deserialize. Malformed records produce an error (not a warning).
3. **Role validation:** Each record's `Role` must be one of "user", "assistant", "tool_result". Unknown roles produce an error.
4. **Turn monotonicity:** Turn numbers must be monotonically non-decreasing. Gaps are allowed (crash recovery may produce gaps) but produce a warning in the response.
5. **Tool compatibility:** If a `tool_result` record references a tool name not in the session's `Tools` list, this produces a warning (not an error). The agent will see the historical tool result but cannot re-invoke that tool.
6. **Role alternation:** If consecutive records have the same role (e.g., two `user` messages in a row without an intervening `assistant` message), a warning is produced. This can occur legitimately after a crash (e.g., a log ending with `user` without a matching `assistant` response) but may also indicate log corruption. This is a warning rather than an error because some providers (notably Anthropic's API) require strict role alternation and will reject malformed sequences -- catching it here with a descriptive warning is preferable to surfacing as a cryptic provider error on the next `SendMessage`.

**Session forking caveat:** When the same conversation log is replayed into multiple sessions, tool results in the log reflect the state at the time they were originally executed. Forked agents see these historical results as-is -- tool results are never re-executed during replay. The orchestrator is responsible for ensuring the sandbox filesystem state is consistent with the conversation log (e.g., by using ZFS snapshots taken at the fork point).

---

## 4. AgentLoopService (In-Process Implementation)

```go
// internal/agent/service.go

// AgentLoopService implements api.AgentService by managing Agent instances
// directly in-process. This is the real implementation -- the RPC server
// and client are just transport wrappers around this interface.
type AgentLoopService struct {
    mu         sync.Mutex
    sessions   map[string]*managedSession
    publisher  EventPublisher // Injected event publish callback (see below)
    maxSess    int            // Maximum concurrent sessions (0 = unlimited)
    closed     bool
    wg         sync.WaitGroup // Tracks active session goroutines for Close() drain
}

// EventPublisher is the minimal interface for publishing agent events.
// AgentEventServer (in internal/rpc/server) implements this.
// This preserves the import direction: internal/agent does NOT import
// internal/rpc/server.
type EventPublisher interface {
    Publish(sessionID string, event agent.AgentEvent)
}

type managedSession struct {
    agent       *Agent
    config      api.SessionConfig
    ctx         context.Context    // Session-scoped context (parent for all turn contexts)
    cancel      context.CancelFunc // Cancels the session-scoped context
    createdAt   time.Time
    started     bool               // tracks whether Start() has been called (vs Prompt())
    unsubscribe func()             // Returned by Agent.Subscribe(); called on DestroySession
}
```

**Constructor:**
```go
func NewAgentLoopService(publisher EventPublisher, opts ...ServiceOption) *AgentLoopService

type ServiceOption func(*AgentLoopService)

func WithMaxSessions(n int) ServiceOption // Defaults to 0 (unlimited)
```

**CreateSession flow:**
1. Check `closed` flag; return error if service is shutting down
2. Check session count against `maxSess`; return `CodeResourceExhausted` if at limit
3. Generate session ID (UUID v4) if not provided
4. If session ID already exists, return `CodeAlreadyExists` error
5. Resolve model from registry (`ai.GetModel(provider, modelID)`)
6. Resolve driver from registry (`agent.NewDriver(driverName, driverConfig)`)
7. Build ExecutionEnvironment based on ToolEnvironmentConfig:
   - `ToolEnvLocal` -> `environment.NewLocalEnvironment(rootDir)`
   - `ToolEnvSandbox` -> `native.NewNativeSandboxEnvironment(sandboxClient, config)`
8. Build AgentTool list from tool names + environment
9. Create `Agent` with driver, store in sessions map
10. Return session ID and initial state

**SendMessage flow:**
1. Look up session by ID
2. Subscribe to session events and wrap in turn-scoped `EventReceiver` (see section 3.2). **This MUST happen before step 3** to avoid losing early events (`EventTurnStarted`, initial deltas) that the driver goroutine emits immediately after launch.
3. If `!session.started`, call `agent.Start(session.ctx, session, message)`. If `Start()` succeeds, set `started = true`. Otherwise, call `agent.Prompt(session.ctx, message)`. Note: turns use the **session-scoped context** (`session.ctx`), not the RPC request context. This ensures the turn survives RPC connection drops -- the orchestrator can still observe the turn via `SubscribeEvents`.
4. If `Start()`/`Prompt()` fails, close and discard the subscription from step 2, return the error. **Importantly, if `Start()` returns an error, `started` remains `false`.** The next `SendMessage` call will retry `Start()`, which is correct because `Agent.Start()` always accepts a session parameter and sets `a.session` from it. A failed `Start()` leaves `Agent.running = false` and a retry will work correctly.
5. Increment `wg.Add(1)` for the active turn goroutine. The deferred cleanup in `NativeDriver.run()` calls `wg.Done()`.
6. Return the turn-scoped event stream

**Continue flow:**
1. Look up session
2. Call `agent.Continue(ctx)`
3. Return turn-scoped event stream

**Steer/FollowUp/Abort:**
1. Look up session
2. For `Steer`: check agent state; return `CodeFailedPrecondition` if `StateIdle`
3. Call corresponding `agent.Steer(msg)` / `agent.FollowUp(msg)` / `agent.Abort(reason)`
4. Return immediately (these are enqueue operations)

**Note on Steer TOCTOU:** There is an inherent race between checking the agent state (step 2) and enqueuing the steer command (step 3). A turn could complete between the check and the enqueue, making the steer meaningless, or a turn could start, making a previously-rejected steer valid. This is acceptable for best-effort steering semantics -- the steer is silently discarded by the control queue if the turn ends between check and enqueue. Adding a state-checked `SteerIfActive()` to `Agent` itself would add complexity for marginal benefit.

**DestroySession:**
1. Look up session
2. Call `session.unsubscribe()` to detach the event forwarding subscription
3. Call `agent.Stop(ctx)` (cancels context, sets `running = false`)
4. Cancel session context -- in-flight turn goroutine detects `ctx.Err()` and exits
5. Close all open event streams for this session with EOF
6. Remove from sessions map

**Close (graceful shutdown):**
1. Set `closed = true` to reject new `CreateSession` calls
2. For each active session, call `DestroySession`
3. Wait for all session goroutines to exit by calling `wg.Wait()` with a configurable deadline (default 30s). The `sync.WaitGroup` is incremented in `SendMessage`/`Continue` when a turn starts and decremented in the deferred cleanup of `NativeDriver.run()` when the turn goroutine exits.
4. If deadline exceeded, force-cancel all remaining session contexts (the `wg.Wait()` will then complete as the cancelled goroutines exit)

---

## 5. RPC Transport

### 5.1 ConnectRPC Procedures

Following the existing pattern in `internal/rpc/transport/server.go`:

```go
// New procedures added to transport server
ProcedureAgentCreateSession   = "/rpc.v1.AgentService/CreateSession"
ProcedureAgentGetSession      = "/rpc.v1.AgentService/GetSession"
ProcedureAgentListSessions    = "/rpc.v1.AgentService/ListSessions"       // unary
ProcedureAgentResumeSession   = "/rpc.v1.AgentService/ResumeSession"      // unary
ProcedureAgentSendMessage     = "/rpc.v1.AgentService/SendMessage"        // server stream
ProcedureAgentContinue        = "/rpc.v1.AgentService/Continue"           // server stream
ProcedureAgentSteer           = "/rpc.v1.AgentService/Steer"              // unary
ProcedureAgentFollowUp        = "/rpc.v1.AgentService/FollowUp"          // unary
ProcedureAgentAbort           = "/rpc.v1.AgentService/Abort"              // unary
ProcedureAgentSubscribeEvents = "/rpc.v1.AgentService/SubscribeEvents"    // server stream
ProcedureAgentDestroySession  = "/rpc.v1.AgentService/DestroySession"     // unary
```

**Handler types:**
- CreateSession, GetSession, ListSessions, ResumeSession, Steer, FollowUp, Abort, DestroySession -> **unary** (`connect.NewUnaryHandlerSimple`)
- SendMessage, Continue, SubscribeEvents -> **server stream** (`connect.NewServerStreamHandler`)

**Prerequisite: transport.Server refactoring (change to plan 13).** The current `transport.Server` constructor takes `SandboxService`, `AgentEventService`, and `SessionManager` upfront, and `Handler()` unconditionally registers all sandbox procedures. This does not work for the `flexagent serve agent` mode, which needs to register `AgentService` procedures WITHOUT providing `SandboxService`. The `transport.Server` must be refactored to support optional service registration using functional options:

```go
// Refactored constructor (change to plan 13 / internal/rpc/transport/server.go):
func NewServer(cfg ServerConfig, opts ...ServerOption) *Server

type ServerOption func(*Server)

func WithSandboxService(s api.SandboxService) ServerOption
func WithAgentEventService(e api.AgentEventService) ServerOption
func WithSessionManager(t *termmux.SessionManager) ServerOption
func WithAgentService(a agentapi.AgentService) ServerOption
```

`Handler()` conditionally registers only the procedures for services that were provided. This refactoring is a prerequisite for step 8 in the implementation order and should be done as part of that step. Existing callers (`flexagent serve sandbox-host`) must be updated to use the new option-based constructor.

### 5.2 AgentRPCServer

```go
// internal/rpc/server/agent_server.go

type AgentRPCServer struct {
    service agentapi.AgentService  // Delegates to AgentLoopService
}

func NewAgentRPCServer(service agentapi.AgentService) *AgentRPCServer
```

Follows same delegation pattern as SandboxServer: validate -> delegate -> map errors -> return.

### 5.3 AgentServiceClient

```go
// internal/rpc/client/agent_client.go

// AgentServiceClient implements agentapi.AgentService over ConnectRPC.
type AgentServiceClient struct {
    baseURL    string
    httpClient connect.HTTPClient
    opts       []connect.ClientOption
}

func NewAgentServiceClient(baseURL string, opts ...connect.ClientOption) *AgentServiceClient
```

Each method creates a ConnectRPC client for the corresponding procedure and makes the call. Server stream methods return an `AgentEventReceiver` that wraps the ConnectRPC stream.

### 5.4 Error Mapping

Agent-specific errors are mapped to ConnectRPC codes. This extends the existing `rpc.MapError()` function in `internal/rpc/errors.go`:

| Agent Error | ConnectRPC Code |
|------------|-----------------|
| `agent.BusyError` | `CodeFailedPrecondition` |
| `agent.StoppedError` | `CodeFailedPrecondition` |
| `agent.InvalidStateError` | `CodeFailedPrecondition` |
| `agent.ErrQueueFull` | `CodeResourceExhausted` |
| Session not found | `CodeNotFound` |
| Session ID already exists | `CodeAlreadyExists` |
| Max sessions exceeded | `CodeResourceExhausted` |
| Service closed/shutting down | `CodeUnavailable` |

### 5.5 Codec Layer

There are two codec locations with distinct responsibilities:

**`internal/agent/api/codec.go`** -- Domain-level conversion between Go types. Converts `agent.AgentMessage` (wrapping `ai.Message` interface types) to/from `AgentMessageRecord` (the serializable JSON format). This is transport-independent and used by both in-process and RPC callers. Functions: `AgentMessageToRecord()`, `RecordToAgentMessage()`.

**`internal/rpc/codec/agent_map.go`** -- Wire-format conversion for the RPC transport layer, following the pattern of `internal/rpc/codec/sandbox_map.go`. This handles conversion between Go types and the ConnectRPC wire format (JSON serialization for the HTTP transport). Responsibilities:
- `agent.SessionMetrics` -> ConnectRPC JSON wire format and back
- `EventReceiver` -> `AgentEventReceiver` adaptation (wrapping `*agent.AgentEvent` in `AgentEventEnvelope` with session ID)
- `control.ResourceSpec` <-> `api.ResourceSpec` mapping
- Request/response type serialization for ConnectRPC handlers

The boundary is: `codec.go` handles domain model conversion (Go struct <-> Go struct), `agent_map.go` handles transport serialization (Go struct <-> wire format). If the ConnectRPC transport uses JSON and the Go types are already JSON-tagged, some wire-format codec functions may be trivial pass-throughs.

Unit tests for round-trip fidelity at `internal/rpc/codec/agent_map_test.go`.

---

## 6. SandboxControl Interface

This is the orchestrator-level abstraction for sandbox lifecycle. It is separate from and complementary to `ExecutionEnvironment`:

| Aspect | SandboxControl | ExecutionEnvironment |
|--------|---------------|---------------------|
| **Layer** | Orchestrator | Agent loop |
| **Purpose** | Provision and manage sandbox instances | Execute tools within a sandbox |
| **Who calls it** | Orchestrator / RuntimeController | Agent's NativeDriver |
| **Lifecycle** | CreateSandbox, DestroySandbox, PauseSandbox, ResumeSandbox | Create, Destroy, Pause, Resume (within a session) |
| **Unique capability** | LaunchProcess (deploy `flexagent serve agent` into sandbox) | ExecuteTool, TurnComplete, Snapshots |

**How they coordinate:** `SandboxControl.CreateSandbox()` provisions a new sandbox and returns its ID and address. The orchestrator then passes this information (sandbox ID, sandbox-host address) to `CreateAgentSessionRequest.ToolEnvironment` so the agent loop can construct an `ExecutionEnvironment` pointing at the already-provisioned sandbox. They never both manage the same sandbox simultaneously -- SandboxControl hands off to ExecutionEnvironment.

**Capabilities alignment:** `SandboxControl.SandboxCapabilities` defines its own fields with sandbox-appropriate names rather than embedding `environment.Capabilities`. This avoids semantic confusion where identical field names mean different things at different layers (e.g., `ConcurrentSessions` means concurrent tool executions at the environment level but concurrent sandbox instances at the control level; `MaxSessionDuration` means tool execution session lifetime vs. sandbox lifetime). The orchestrator is responsible for mapping between the two when needed:

```go
// internal/sandbox/control/control.go

// SandboxControl manages sandbox lifecycle. The orchestrator uses this
// to create/destroy sandboxes and launch processes inside them.
type SandboxControl interface {
    // CreateSandbox provisions a new sandbox environment.
    // Returns the sandbox ID, connectivity info, and capabilities.
    CreateSandbox(ctx context.Context, req CreateSandboxRequest) (*CreateSandboxResponse, error)

    // DestroySandbox tears down a sandbox and releases all resources.
    DestroySandbox(ctx context.Context, sandboxID string) error

    // LaunchProcess starts a long-running process inside an existing sandbox.
    // Used to deploy the agent loop server inside a sandbox.
    // Returns connectivity info (address, port) for the launched process.
    //
    // Lifecycle: The launched process runs until it exits, the sandbox is
    // destroyed, or KillProcess is called. The sandbox-host monitors the
    // process and can report its status via GetProcessStatus.
    //
    // Port management: The ExposePort in the request is the port the process
    // will listen on inside the sandbox. The sandbox-host allocates a proxy
    // port on its own address and returns it in LaunchProcessResponse.Address.
    // The orchestrator connects to the proxy port, not the container port.
    //
    // Process monitoring: The sandbox-host tracks PID and exit status.
    // If the process exits unexpectedly, the sandbox remains alive (not
    // automatically destroyed). The orchestrator detects the failure via
    // connection loss to the agent process and recovers via ResumeSession.
    LaunchProcess(ctx context.Context, req LaunchProcessRequest) (*LaunchProcessResponse, error)

    // KillProcess terminates a previously launched process.
    KillProcess(ctx context.Context, req KillProcessRequest) error

    // GetProcessStatus returns the current status of a launched process.
    GetProcessStatus(ctx context.Context, req GetProcessStatusRequest) (*GetProcessStatusResponse, error)

    // PauseSandbox pauses a sandbox (provider-specific semantics).
    PauseSandbox(ctx context.Context, sandboxID string) error

    // ResumeSandbox resumes a paused sandbox.
    ResumeSandbox(ctx context.Context, sandboxID string) error

    // Capabilities returns what this provider supports.
    Capabilities() SandboxCapabilities
}

// ResourceSpec specifies resource limits for a sandbox. Defined locally in
// internal/sandbox/control to avoid importing internal/rpc/api (which would
// create an unwanted cross-layer dependency). The two fields mirror
// api.ResourceSpec from internal/rpc/api; the RPC codec maps between them.
type ResourceSpec struct {
    CPUs  float64
    MemMB int
}

type CreateSandboxRequest struct {
    Labels    map[string]string
    Template  string            // Provider-specific template/base image
    Resources ResourceSpec      // CPU, memory limits
}

type CreateSandboxResponse struct {
    SandboxID    string
    Address      string                    // How to reach this sandbox ("host:port" string)
    Capabilities SandboxCapabilities
}

type LaunchProcessRequest struct {
    SandboxID  string
    Binary     string            // Path to binary (or command name)
    Args       []string
    Env        map[string]string // Environment variables (including API keys)
    ExposePort int               // Port the process will listen on inside sandbox
}

type LaunchProcessResponse struct {
    ProcessID string
    Address   string   // How to reach the launched process ("host:port" string)
    Status    ProcessStatus
}

// AddressToURL converts a "host:port" address to an HTTP URL suitable for
// ConnectRPC client construction. Example: "sandbox-host:9100" -> "http://sandbox-host:9100".
// The orchestrator calls this when constructing an AgentServiceClient from
// a LaunchProcessResponse.Address or CreateSandboxResponse.Address.
func AddressToURL(addr string) string

type KillProcessRequest struct {
    SandboxID string
    ProcessID string
    Signal    int // Unix signal (default SIGTERM if 0)
}

type GetProcessStatusRequest struct {
    SandboxID string
    ProcessID string
}

type GetProcessStatusResponse struct {
    Status   ProcessStatus
    ExitCode *int   // Only set if Status == ProcessExited
}

type ProcessStatus string

const (
    ProcessRunning  ProcessStatus = "running"
    ProcessExited   ProcessStatus = "exited"
    ProcessStarting ProcessStatus = "starting"
)

// SandboxCapabilities defines orchestrator-level capability flags for a sandbox provider.
// Unlike environment.Capabilities (which describes tool-execution-level capabilities),
// these fields use sandbox-appropriate names and semantics. No embedding of
// environment.Capabilities -- the field names differ intentionally to prevent
// semantic confusion (e.g., "ConcurrentSandboxes" vs "ConcurrentSessions",
// "MaxSandboxDuration" vs "MaxSessionDuration"). The orchestrator maps between the
// two when constructing ExecutionEnvironment configs from SandboxControl responses.
type SandboxCapabilities struct {
    Snapshots          bool          // Whether this provider supports filesystem snapshots
    Rollback           bool          // Whether this provider supports rollback to snapshots
    Pause              bool          // Whether this provider supports pause/resume
    LaunchProcess      bool          // Whether this provider supports LaunchProcess
    DeepPause          bool          // ZFS-to-S3 style cold storage (future)
    ConcurrentSandboxes int          // Max concurrent sandbox instances (0 = unlimited)
    MaxSandboxDuration  time.Duration // Max sandbox lifetime (0 = unlimited)
}
```

### 6.1 Native Implementation

```go
// internal/sandbox/control/native/native.go

// NativeSandboxControl wraps our sandbox-host RPC.
type NativeSandboxControl struct {
    sandboxClient api.SandboxService
    logger        *slog.Logger
}
```

- `CreateSandbox` -> calls `sandboxClient.CreateSession()`. Field mapping:
  - `CreateSandboxRequest.Template` -> `CreateSessionRequest.BaseSnapshot`
  - `CreateSandboxRequest.Labels` -> `CreateSessionRequest.Labels`
  - `CreateSandboxRequest.Resources` -> not present in `CreateSessionRequest` (logged as debug info, not transmitted; resource limits are set at the sandbox-host level via host config, not per-session)
  - `CreateSessionRequest.Quota` -> uses a default value (configurable via `NativeSandboxControl` constructor option `WithDefaultQuota(bytes int64)`)
  - `CreateSessionRequest.SessionID` -> generated by the sandbox-host (not pre-assigned by SandboxControl)
- `DestroySandbox` -> calls `sandboxClient.DestroySession()`
- `LaunchProcess` -> requires **new LaunchProcess RPC endpoint on sandbox-host**. The LaunchProcess specification (RPC types, lifecycle, port proxying, process monitoring) is defined in section 6 of this plan. A standalone addendum (`11-sandbox-host-service.add03`) should be created as a follow-up before implementation of NativeSandboxControl to give sandbox-host implementors a self-contained reference (see Follow-up Work section). Implementation: sandbox-host runs the process in gVisor (or directly if no gVisor), bind-mounting the sandbox's ZFS dataset, and proxies the exposed port via a TCP reverse proxy on a dynamically allocated port.
- `KillProcess` -> sends signal to tracked PID
- `GetProcessStatus` -> checks PID status
- `PauseSandbox` -> calls `sandboxClient.PauseSession()`
- `ResumeSandbox` -> calls `sandboxClient.ResumeSession()`

### 6.2 Cloud Provider Implementations (Future)

Stub interfaces for Batch 6. Not implemented in this plan:

- `E2BSandboxControl` -- wraps E2B REST API
- `DaytonaSandboxControl` -- wraps Daytona REST API
- `FlySandboxControl` -- wraps Fly Machines API

---

## 7. Agent Loop Server Binary

All server functionality is provided by the unified `flexagent` binary at `cmd/flexagent/main.go`, which uses subcommands under `flexagent serve` to run different server roles:

- `flexagent serve agent` -- runs the agent loop server
- `flexagent serve orchestrator` -- runs the orchestrator (future)
- `flexagent serve sandbox-host` -- runs the sandbox-host service (migrated from `cmd/sandbox-host`)
- `flexagent serve all` -- runs all services in a single process (convenience for local dev)

### 7.1 Standalone Mode

```go
// cmd/flexagent/main.go -- unified binary with subcommands

// The "flexagent serve agent" subcommand:
//   Parse flags: --addr, --sandbox-host, --root-dir, --max-sessions, etc.
//   Read API keys from environment variables (ANTHROPIC_API_KEY, etc.)
//   Create AgentLoopService with WithMaxSessions
//   Create AgentRPCServer wrapping the service
//   Register procedures on transport.Server
//   Set up graceful shutdown handler (see 7.4)
//   ListenAndServe
```

### 7.2 Embedded Mode

For in-process use (All Local), no binary needed. The orchestrator creates `AgentLoopService` directly:

```go
// In orchestrator code:
agentService := agent.NewAgentLoopService(publisher, agent.WithMaxSessions(10))
// Use agentService directly -- same interface as the RPC client
```

### 7.3 Sandbox-Launched Mode

When the sandbox-host launches the agent loop inside a sandbox via `LaunchProcess`:

1. Sandbox-host starts `flexagent serve agent` inside the sandbox (gVisor container or directly)
2. API keys are injected via `LaunchProcessRequest.Env` -- never transmitted over AgentService RPC
3. The process listens on a configured port
4. Sandbox-host proxies that port (or returns the container's network address)
5. Orchestrator connects to the returned address via `AgentServiceClient`

The `flexagent serve agent` process runs tools locally within the sandbox (using `LocalEnvironment`). From its perspective, it's just Mode 1 -- all local. The sandbox boundary is invisible to it.

### 7.4 Graceful Shutdown

When the `flexagent serve agent` process receives SIGTERM or SIGINT:

1. **Stop accepting new sessions:** Set service to draining mode (new `CreateSession`/`ResumeSession` calls return `CodeUnavailable`)
2. **Notify subscribers:** Emit `EventSessionEnded` with reason "server_shutdown" to all active event streams
3. **Drain period:** Wait up to 30 seconds (configurable via `--shutdown-timeout`) for in-flight turns to complete naturally
4. **Force shutdown:** After the drain deadline, cancel all remaining session contexts, close all event streams with EOF, and exit

For sandbox-launched mode, the sandbox-host may kill the process at any time (SIGKILL). The orchestrator handles this via crash recovery (section 10.1) -- it detects connection loss and calls `ResumeSession` on a new `flexagent serve agent` instance.

---

## 8. Connectivity: How Orchestrator Finds Agent Loop

### 8.1 In-Process

No discovery needed. Direct Go function calls.

### 8.2 Standalone Process

Operator configures the address. The orchestrator is told where `flexagent serve agent` is running (flag, config file, service discovery).

### 8.3 Launched Inside Sandbox

The `SandboxControl.LaunchProcess()` call returns the address in `LaunchProcessResponse.Address` as a `"host:port"` string. The orchestrator converts this to a URL via `control.AddressToURL()` (which prepends `http://`) before creating an `AgentServiceClient`.

For native sandbox-host, the proxy approach:
- Agent-server listens on a port inside the sandbox/container
- Sandbox-host proxies this port on its own address (e.g., `sandbox-host:9100` -> container port `8080`)
- `LaunchProcessResponse.Address` = `sandbox-host-addr:9100`

This means the orchestrator only needs to reach the sandbox-host, not the container directly. The sandbox-host acts as a reverse proxy for launched processes.

---

## 9. Sequence Diagrams

### 9.1 Create Agent Session (Remote, Tools in Sandbox)

```mermaid
sequenceDiagram
    participant O as Orchestrator
    participant SC as SandboxControl
    participant SH as SandboxHost
    participant AC as AgentServiceClient
    participant AS as AgentLoopServer
    participant A as Agent

    O->>SC: CreateSandbox(config)
    SC->>SH: CreateSession(req)
    SH-->>SC: sessionID, capabilities
    SC-->>O: sandboxID, address, capabilities

    O->>AC: CreateSession(model, tools, sandbox config)
    AC->>AS: RPC CreateSession
    AS->>A: Create Agent + NativeDriver
    A-->>AS: ready
    AS-->>AC: sessionID, state=idle
    AC-->>O: sessionID

    O->>AC: SendMessage(sessionID, "Fix the bug in main.go")
    AC->>AS: RPC SendMessage (server stream)
    AS->>A: agent.Start(ctx, session, prompt)
    loop Turn Events
        A-->>AS: AgentEvent (delta, tool_started, tool_completed, ...)
        AS-->>AC: event stream
        AC-->>O: events
    end
```

### 9.2 Agent in Sandbox (Launched by Orchestrator)

```mermaid
sequenceDiagram
    participant O as Orchestrator
    participant SC as SandboxControl
    participant SH as SandboxHost
    participant ALS as AgentLoopServer (in sandbox)
    participant A as Agent

    O->>SC: CreateSandbox(config)
    SC->>SH: CreateSession(req)
    SH-->>SC: sandboxID, capabilities
    SC-->>O: sandboxID, address

    O->>SC: LaunchProcess(sandboxID, "flexagent", ["serve","agent"], env={API_KEY=...})
    SC->>SH: LaunchProcess(sandboxID, ...)
    Note over SH: Starts flexagent serve agent in sandbox,<br/>proxies port, injects env vars
    SH-->>SC: processID, address
    SC-->>O: address (e.g. sandbox-host:9100)

    Note over O: Connect to launched flexagent serve agent
    O->>ALS: RPC CreateSession(tools=local)
    ALS->>A: Create Agent (tools run locally in sandbox)
    A-->>ALS: ready
    ALS-->>O: sessionID

    O->>ALS: RPC SendMessage(sessionID, prompt)
    loop Turn Events
        A-->>ALS: events
        ALS-->>O: event stream
    end
```

---

## 10. Session Recovery, Forking, and Cross-Agent Resume

### 10.1 Design Principle: Orchestrator Owns Conversation State

The agent loop server is **ephemeral** (holds in-memory session state but does not persist to disk). The **orchestrator** is the durable state owner, persisting the conversation log in SQLite. This clean separation enables powerful capabilities:

- **Crash recovery:** Agent process crashes -> orchestrator sends conversation log to a new `flexagent serve agent` instance via `ResumeSession`
- **Session forking:** Orchestrator copies conversation log at turn N -> creates N new sessions with the same log but different follow-up prompts
- **Cross-agent migration:** Conversation from one agent type (e.g., our native loop) resumed on a different agent type (e.g., Claude Code via termmux)

The agent loop server never persists conversation state to disk. If it crashes, the session is gone from its perspective. The orchestrator is the recovery mechanism.

**Mid-turn crash behavior:** If the agent process crashes while a tool is executing, the tool's side effects (file writes, bash commands) may have partially completed. `ResumeSession` replays the conversation log but does NOT replay or undo partial tool effects. The orchestrator handles this at the application level:
- For sandbox-backed agents: ZFS snapshots taken at turn boundaries provide the rollback mechanism. The orchestrator can roll back to the last snapshot before resuming.
- For local agents: Partial tool effects are not rolled back. The resumed agent sees the filesystem as-is and must handle any inconsistency (this is the same behavior as a human interrupting a coding agent mid-edit).

### 10.2 ResumeSession Flow

```mermaid
sequenceDiagram
    participant O as Orchestrator
    participant DB as SQLite
    participant AS as AgentLoopServer
    participant A as Agent

    Note over O: Agent-server crashed, or forking
    O->>DB: Read conversation log for session X
    DB-->>O: []AgentMessageRecord

    O->>AS: ResumeSession(schemaVersion=1, conversation log, config)
    AS->>AS: Validate log (section 3.4)
    AS->>A: Create Agent + Driver
    Note over AS,A: Convert each AgentMessageRecord to<br/>agent.AgentMessage via codec.RecordToAgentMessage(),<br/>build Session with resulting ConversationLog,<br/>call agent.SetSession(session)
    AS->>A: agent.SetSession(session) with populated ConversationLog
    A-->>AS: ready (state=idle, conversation loaded)
    AS-->>O: sessionID, state=idle, conversationLen=N, warnings=[...]

    O->>AS: SendMessage(sessionID, "Continue from where you left off")
    AS->>A: agent.Prompt(ctx, message)
    Note over A: Agent sees full conversation history,<br/>continues naturally
```

### 10.3 Session Forking

```mermaid
sequenceDiagram
    participant O as Orchestrator
    participant DB as SQLite
    participant AS1 as AgentServer 1
    participant AS2 as AgentServer 2
    participant AS3 as AgentServer 3

    O->>DB: Read conversation log (10 turns of shared context)

    par Fork into 3 agents
        O->>AS1: ResumeSession(log, config)
        O->>AS2: ResumeSession(log, config)
        O->>AS3: ResumeSession(log, config)
    end

    par Different instructions to each fork
        O->>AS1: SendMessage("Work on the frontend")
        O->>AS2: SendMessage("Work on the backend")
        O->>AS3: SendMessage("Write the test suite")
    end
```

### 10.4 Cross-Agent Resume (Our Loop -> Claude Code)

When the orchestrator wants to resume a conversation that was running on our native agent loop using Claude Code (or vice versa), it uses the existing bidirectional session log conversion in `internal/termmux/driver/claudecode/session_log.go`:

1. Orchestrator reads conversation log from SQLite (`[]AgentMessageRecord`)
2. Converts to `[]agent.AgentMessage` via `codec.RecordToAgentMessage()`
3. Converts to canonical `[]ConversationEntry` format
4. Calls `claudecode.WriteSessionLog(entries, writer)` -> produces Claude Code's `session.jsonl`
5. Writes the file into the sandbox filesystem at Claude Code's expected path
6. Launches Claude Code via termmux -> it picks up from the conversation

**Conversion chain:**
```
AgentMessageRecord (orchestrator/SQLite)
    -> agent.AgentMessage (via codec.RecordToAgentMessage)
    -> ConversationEntry (canonical termmux format)
    -> Claude Code session.jsonl (native format)
```

#### AgentMessage -> ConversationEntry Conversion

The `agent.AgentMessage -> ConversationEntry` conversion function lives in a new file `internal/agent/api/termmux_codec.go` (or in the termmux driver package if import constraints require it). This is the most complex step in the chain because `agent.AgentMessage` wraps the polymorphic `ai.Message` interface while `ConversationEntry` has a flat structure.

```go
// internal/agent/api/termmux_codec.go

// AgentMessageToConversationEntries converts an agent.AgentMessage to one or
// more canonical ConversationEntry records for cross-agent resume.
// A single AgentMessage with an AssistantMessage containing text + tool_use
// blocks produces MULTIPLE ConversationEntry records (one per content block),
// matching the pattern used by claudecode.convertClaudeToCanonical().
func AgentMessageToConversationEntries(msg agent.AgentMessage) []driver.ConversationEntry
```

**Field mapping by message type:**

| ai.Message type | ConversationEntry.Role | Content mapping |
|---|---|---|
| `*ai.UserMessage` | `"user"` | Single entry. `Content` = text content of the message. |
| `*ai.AssistantMessage` (text block) | `"assistant"` | One entry per text block. `Content` = block text. `Thinking` populated from thinking blocks. `Usage` populated from message-level usage. |
| `*ai.AssistantMessage` (tool_use block) | `"tool_use"` | One entry per tool_use block. `ToolCall` = `&ToolCallRecord{ID: block.ID, Name: block.Name, Args: json.Marshal(block.Arguments)}`. |
| `*ai.ToolResultMessage` | `"tool_result"` | Single entry. `Content` = flattened text of result content blocks (image blocks dropped). `ToolCall` = `&ToolCallRecord{ID: msg.ToolCallID, Name: msg.ToolName, Result: flattened content}`. `ToolCall.IsError` = `msg.IsError`. |

**Known lossy steps:** Multi-block assistant messages are split into separate entries. Image content blocks are dropped (text description retained if present). Structured tool argument formatting is flattened to JSON string. This is acceptable for cross-agent resume where the target agent only needs the semantic content of the conversation. Tool name mapping assumes shared tool definitions; cross-tool-schema resume is not supported.

The reverse direction (Claude Code -> our loop) uses `claudecode.ParseSessionLog()` to read the session.jsonl, convert to canonical, then to AgentMessageRecord for replay via ResumeSession.

### 10.5 3rd Party Agent Restart

For termmux-attached 3rd party agents (Claude Code, etc.) that crash or get stuck:

1. The sandbox filesystem (ZFS) preserves all work -- the agent's code changes, files, etc. are on disk
2. Orchestrator reads the agent's native session log from the sandbox filesystem (e.g., Claude Code's session.jsonl)
3. Parses it to canonical format, persists to SQLite
4. Restarts the agent process in the sandbox (via `SandboxControl.LaunchProcess` or termmux restart)
5. Optionally writes the conversation back to the agent's native session format so it can resume

For Claude Code specifically, the agent itself supports resuming from its session.jsonl -- so step 5 may not even be needed. Just restart the process in the same directory.

---

## 11. Event Streaming to Orchestrator

The orchestrator persists conversation state by subscribing to the agent's event stream. Key events that update the conversation log:

- `EventAgentMessageCompleted` -- assistant message finalized -> orchestrator persists it
- `EventToolCompleted` -- tool result -> orchestrator persists it
- `EventTurnCompleted` -- turn boundary marker

The orchestrator subscribes via `SubscribeEvents` (persistent stream) and writes each completed message to SQLite as it arrives. This means the orchestrator's copy of the conversation is always up-to-date, even if the agent process crashes mid-turn (the orchestrator has everything up to the last completed message).

**Relationship to existing AgentEventService:** `AgentService.SubscribeEvents` delegates internally to the existing `AgentEventService` infrastructure (`internal/rpc/server/event_server.go`). It is not a separate event system -- it is the per-session entry point to the same fan-out event bus. The existing `AgentEventService.StreamAgentEvents` RPC remains available as a lower-level entry point for direct subscribers who are not going through `AgentService`.

---

## 12. Dual-View Streaming for Termmux Agents

For 3rd party agents running via termmux (Claude Code, Codex, etc.), the orchestrator can expose two complementary views to UI clients:

### 12.1 Structured Event View

The orchestrator subscribes to the agent's event stream (via the session log tailer in `internal/termmux/eventsrc/sessionlog/`). The tailer watches the agent's native session log file (e.g., Claude Code's session.jsonl), parses it using the driver's `ParseSessionLog()`, converts to canonical `ConversationEntry` format, and emits structured `AgentEvent`s.

This view powers:
- Orchestrator's own persistence (conversation log to SQLite)
- Structured UI rendering (message bubbles, tool call cards, thinking blocks)
- Cross-agent interoperability (same event format regardless of agent type)

### 12.2 Native Terminal View

The orchestrator connects to the raw terminal output via the existing `TerminalService.StreamTerminal` RPC (`internal/rpc/api/terminal.go`). This streams the raw ANSI/VT100 bytes from the agent's PTY session using the existing `TerminalStreamHandle` protocol (which supports input, output, resize, attach, and detach semantics).

This view powers:
- "See exactly what the agent sees" debugging view
- Native Claude Code terminal rendering (xterm.js in web UI, or passthrough in TUI)
- Interactive terminal access (send keystrokes, Ctrl+C, etc.)

**Terminal access routing:** The `AgentService` interface does NOT include a `StreamTerminal` method. Terminal streaming for termmux-backed sessions uses the existing `TerminalService` interface directly. The orchestrator maintains the mapping between agent session IDs and terminal session IDs, and connects to `TerminalService` on the appropriate sandbox-host or agent server for terminal access. If the agent server needs to expose terminal streaming, it registers the existing `TerminalService` procedures on its HTTP handler alongside the AgentService procedures -- no new interface needed.

### 12.3 Orchestrator Relay

The orchestrator relays both streams to its own clients. For each termmux agent session, it maintains:

1. **Event subscription** -> structured events forwarded to UI clients via the orchestrator's own event streaming API
2. **Terminal subscription** -> raw terminal bytes forwarded to UI clients via a terminal relay endpoint (using existing `TerminalService`)

```mermaid
graph LR
    subgraph "Sandbox"
        CC[Claude Code]
        SL[session.jsonl]
        PTY[PTY Session]
    end

    subgraph "Termmux Layer"
        T[Session Log Tailer]
        TS[TerminalService]
    end

    subgraph "Orchestrator"
        EP[Event Processor]
        TR[Terminal Relay]
        DB[(SQLite)]
    end

    subgraph "UI Client"
        SV[Structured View]
        TV[Terminal View]
    end

    CC -->|writes| SL
    CC -->|output| PTY
    SL -->|tail + parse| T
    PTY -->|TerminalService RPC| TS
    T -->|AgentEvents| EP
    TS -->|TerminalStreamHandle| TR
    EP -->|persist| DB
    EP -->|stream| SV
    TR -->|stream| TV
```

### 12.4 Backpressure, Timing, and Failure Handling

**Backpressure policy:** The orchestrator relay inherits the existing `AgentEventServer.Publish()` behavior: slow consumers have events dropped (non-blocking channel send with `default` case). For the orchestrator's own persistence subscriber, events are processed synchronously (blocking write to SQLite) so the orchestrator buffer must be sized appropriately. If the UI client is slow, events are dropped at the orchestrator relay, not at the tailer. The UI client is expected to handle gaps by re-fetching state from the orchestrator's REST API.

**Failure correlation:** The event stream and terminal stream have **independent lifecycles**. If one fails, the other continues. The orchestrator logs the failure and attempts reconnection for the failed stream. Both streams are torn down only when the session itself is destroyed. This independence is important because the structured event view (tailer-based) and terminal view (PTY-based) come from different sources with different failure modes.

**Timing skew:** The structured event view has inherent latency relative to the terminal view because the session log tailer polls at ~500ms intervals (configurable) while the terminal stream is real-time. This means the terminal view shows output before the structured view reflects it. This skew is acknowledged and acceptable -- the two views serve different purposes (debugging vs. structured rendering). UI clients that display both views should present them as independent panels, not as synchronized. Future: consider adding monotonic sequence numbers to `AgentEvent` to enable cross-view correlation if needed.

---

## 13. Package Structure

```
internal/
  agent/
    api/
      agent.go          # AgentService interface (transport-neutral)
      agent_types.go    # Request/response types
      codec.go          # AgentMessageRecord <-> AgentMessage conversion
      codec_test.go
      termmux_codec.go  # AgentMessage -> ConversationEntry conversion (cross-agent resume)
      termmux_codec_test.go
    service.go          # AgentLoopService (in-process implementation of AgentService)
    service_test.go
  rpc/
    codec/
      agent_map.go      # Wire-format codec for agent types
      agent_map_test.go
    server/
      agent_server.go   # AgentRPCServer (ConnectRPC handler)
      agent_server_test.go
    client/
      agent_client.go   # AgentServiceClient (ConnectRPC client)
      agent_client_test.go
    transport/
      server.go         # Updated: register agent procedures
    errors.go           # Updated: add agent error mappings
  sandbox/
    control/
      control.go        # SandboxControl interface + types
      native/
        native.go       # NativeSandboxControl (wraps sandbox-host RPC)
        native_test.go

cmd/
  flexagent/
    main.go             # Unified binary with subcommands (serve agent, serve orchestrator, serve sandbox-host, serve all)
```

---

## 14. Testing Strategy

### 14.1 Unit Tests

**AgentLoopService:**
- CreateSession with valid config -> session created, state=idle
- CreateSession with invalid model -> error
- CreateSession with duplicate session ID -> `CodeAlreadyExists`
- CreateSession exceeding MaxSessions -> `CodeResourceExhausted`
- ListSessions returns all active sessions
- SendMessage -> events stream, turn completes with `EventTurnCompleted`
- SendMessage while turn active -> `BusyError`
- Steer while idle -> `CodeFailedPrecondition`
- Steer/FollowUp/Abort -> control commands enqueued
- DestroySession -> agent stopped, resources released, event streams closed with EOF
- DestroySession during active turn -> forceful cancel, events indicate abort
- Concurrent operations on same session -> serialized correctly
- Multiple sessions -> independent lifecycles
- EventPublisher receives correct session ID and event types (using mock EventPublisher that records published events)
- DestroySession stops publishing events to EventPublisher for that session
- ResumeSession with conversation log -> session created with history, state=idle
- ResumeSession with empty log -> equivalent to CreateSession
- ResumeSession with invalid schema version -> error
- ResumeSession with malformed records -> error
- ResumeSession with non-contiguous turns -> warning in response
- ResumeSession with unknown tool references -> warning in response
- ResumeSession -> SendMessage sees full prior conversation context
- Close -> all sessions destroyed, new creates rejected

**AgentRPCServer:**
- Delegation to service (mirrors sandbox server test pattern)
- Error mapping (all agent errors -> correct ConnectRPC codes)
- Request validation

**AgentServiceClient:**
- Round-trip through in-memory ConnectRPC server
- Event stream consumption
- Connection failure handling

**Codec:**
- `AgentMessageToRecord` round-trip for user, assistant, tool_result roles
- `RecordToAgentMessage` with malformed content -> error
- `AgentMessageToRecord` with multi-block assistant messages -> preserves all blocks
- Wire-format codec round-trip for `SessionMetrics`

### 14.2 Integration Tests

**In-process agent loop:**
- Full conversation: create -> send message -> receive events -> tool calls -> response -> destroy
- Multi-turn with follow-up
- Steering during active turn
- Abort during streaming
- Resume from conversation log -> continue where left off
- Fork: resume same log into 3 sessions -> each gets different prompt -> independent execution
- Cross-agent: conversation from native loop -> WriteSessionLog -> Claude Code session.jsonl -> verify format

**Remote agent loop (via loopback RPC):**
- Same scenarios as in-process but over ConnectRPC
- Verify event stream fidelity (no dropped events)
- Connection drop during streaming -> graceful recovery

**SandboxControl + AgentService combined:**
- Create sandbox -> create agent session with sandbox tools -> send message -> tools execute in sandbox -> verify results

### 14.3 Harness Tests

- Property test: any sequence of valid operations produces valid state transitions
- Stress test: N concurrent sessions, M concurrent messages
- Benchmark: RPC overhead vs in-process (quantify the cost of the network hop)

**Note:** A dedicated test harness document (`18-agent-loop-rpc-test-harness.md`) should be created before or during implementation step 10 (integration tests), as it will inform the test infrastructure design. It should cover mock agent driver setup, in-memory ConnectRPC test server infrastructure, session forking stress tests, cross-agent resume round-trip property tests, and event stream fidelity verification.

---

## 15. Implementation Order

1. **AgentService interface + request/response types** (`internal/agent/api/agent.go`, `agent_types.go`)
2. **AgentMessageRecord codec** (`internal/agent/api/codec.go`) + unit tests
3. **AgentLoopService** (`internal/agent/service.go`) -- in-process implementation with `Close()`, `MaxSessions`, `EventPublisher`
4. **Unit tests for AgentLoopService** with mock driver
5. **Agent error mapping** (extend `internal/rpc/errors.go`)
6. **Wire-format codec** (`internal/rpc/codec/agent_map.go`) + unit tests
7. **AgentRPCServer** (`internal/rpc/server/agent_server.go`) -- ConnectRPC handlers
8. **Refactor transport.Server + register agent procedures** (`internal/rpc/transport/server.go`) -- Refactor `NewServer` to use functional options (see section 5.1 prerequisite). Update existing sandbox-host callers. Register agent procedures conditionally. This is a change to the plan 13 (RPC layer) codebase.
9. **AgentServiceClient** (`internal/rpc/client/agent_client.go`) -- ConnectRPC client
10. **Integration tests** -- in-process and remote round-trip
11. **`flexagent serve agent` subcommand** (`cmd/flexagent/`) with graceful shutdown. The existing `cmd/sandbox-host` migrates to `flexagent serve sandbox-host`.
12. **SandboxControl interface** (`internal/sandbox/control/control.go`)
13. **NativeSandboxControl** with mock sandbox-host for testing. **Note:** Steps 12-13 are blocked for LaunchProcess/KillProcess/GetProcessStatus implementation until the `11-sandbox-host-service.add03` addendum is created and the corresponding sandbox-host RPC endpoint is implemented. CreateSandbox, DestroySandbox, PauseSandbox, and ResumeSandbox can proceed immediately as they wrap existing sandbox-host RPCs. Use a mock sandbox-host for LaunchProcess testing.
14. **End-to-end test** -- orchestrator -> `flexagent serve agent` -> sandbox-host

---

## 16. Connected Components

### 16.1 Modified Seams

| Seam | Change | Impact |
|------|--------|--------|
| `internal/rpc/transport/server.go` | Refactor to functional options (change to plan 13) + add agent service procedures | Constructor signature change, conditional handler registration |
| `internal/rpc/errors.go` | Add agent error -> ConnectRPC code mappings | Extended existing `MapError` function |
| `flexagent serve sandbox-host` (migrated from `cmd/sandbox-host`) | Add LaunchProcess RPC endpoint (specified in section 6; standalone plan `11-sandbox-host-service.add03` to be extracted) | New capability on existing subcommand |

### 16.2 New Seams

| Seam | Description |
|------|-------------|
| `internal/agent/api` (new package) | `AgentService` interface + types, transport-neutral |
| `internal/agent/api/codec.go` | `AgentMessageRecord` <-> `agent.AgentMessage` conversion |
| `internal/rpc/codec/agent_map.go` | Wire-format codec for agent RPC types |
| `internal/sandbox/control` (new package) | `SandboxControl` interface + types |
| `LaunchProcess` on sandbox-host | New RPC for starting long-running processes in sandboxes |

### 16.3 Consumed Seams

| Seam | How Used |
|------|----------|
| `internal/agent/agent.go` | `AgentLoopService` creates and manages `Agent` instances via `Agent.Start()`, `Agent.Prompt()`, `Agent.Continue()`, `Agent.Steer()`, `Agent.FollowUp()`, `Agent.Abort()`, `Agent.Stop()`, `Agent.Subscribe()` |
| `internal/agent/types.go` | `AgentMessage`, `SessionMetrics`, `AgentEvent`, `AgentEventType` used directly |
| `internal/sandbox/environment` | `ExecutionEnvironment` consumed by `AgentLoopService` for tool execution setup. `Capabilities` no longer embedded by `SandboxCapabilities` (replaced with explicit fields); orchestrator maps between the two when needed. |
| `internal/rpc/api/types.go` | `AgentEventReceiver`, `AgentEventEnvelope` used by RPC transport layer (adapted from `EventReceiver` by `AgentRPCServer`) |
| `internal/rpc/api/terminal.go` | `TerminalService` used by orchestrator for terminal access to termmux sessions (not added to AgentService) |
| `internal/termmux/driver/driver.go` | `ConversationEntry` used in cross-agent resume conversion chain |

### 16.4 Import Flow

```
cmd/flexagent (serve agent) -> internal/agent (AgentLoopService)
                            -> internal/agent/api (AgentService interface, types)
                            -> internal/rpc/transport (Server)
                            -> internal/rpc/server (AgentRPCServer)

internal/agent/api          -> internal/agent (AgentMessage, AgentEvent, SessionMetrics -- types only)
                            -> internal/termmux/driver (ConversationEntry -- for termmux_codec.go only)
internal/agent/service      -> internal/agent/api (AgentService interface, types, EventReceiver)
                            -> internal/agent (Agent, Driver)
                            -> internal/sandbox/environment (ExecutionEnvironment)

internal/rpc/server/agent_server -> internal/agent/api (AgentService, types, EventReceiver)
                                  -> internal/rpc/api (AgentEventReceiver, AgentEventEnvelope)
                                  -> internal/rpc/codec (wire format mapping)
internal/rpc/client/agent_client -> internal/agent/api (AgentService, types, EventReceiver)
                                  -> internal/rpc/api (AgentEventReceiver)
                                  -> internal/rpc/codec (wire format mapping)
internal/rpc/codec/agent_map     -> internal/agent/api (types)
                                  -> internal/agent (AgentMessage, SessionMetrics)
                                  -> internal/sandbox/control (ResourceSpec)

internal/sandbox/control         -> (no imports from internal/sandbox/environment -- SandboxCapabilities uses its own fields)
internal/sandbox/control/native  -> internal/rpc/api (SandboxService)
                                 -> internal/sandbox/control (SandboxControl)
```

**Key invariants:**
- `internal/agent` does NOT import `internal/rpc/api` or `internal/rpc/server`. The `EventPublisher` interface in `internal/agent` is implemented by `AgentEventServer` (in `internal/rpc/server`), preserving the import direction.
- `internal/agent/api` does NOT import `internal/rpc/api`. The `EventReceiver` interface returns `*agent.AgentEvent` directly. The RPC layer (`internal/rpc/server/agent_server.go`) adapts between `EventReceiver` and `AgentEventReceiver`.
- The `internal/agent/api -> internal/agent` import MUST remain types-only. If codec or interface logic ever needs to call `Agent` methods, those methods should be expressed as function parameters or interfaces defined in `internal/agent/api`, not direct imports of `internal/agent` functions. This constraint is enforced by convention today; a CI linting tool (e.g., `go-import-lint` or a custom `go vet` analyzer) could enforce it automatically in the future.

**Context propagation:** Turns use the session-scoped context (from `managedSession.ctx`), NOT the RPC request context. This is critical for RPC mode: when the `SendMessage` RPC connection drops, the turn continues running because it is attached to the session context, not the request context. The orchestrator can still observe the turn via `SubscribeEvents` (which has its own connection). The session context is cancelled only on `DestroySession` or `Close`.

---

## 17. Resolved Open Questions

1. **Agent-server binary vs subcommand**: Unified `flexagent` binary in `cmd/flexagent/` with subcommands (`flexagent serve agent`, `flexagent serve orchestrator`, `flexagent serve sandbox-host`, `flexagent serve all`). Single binary simplifies deployment and distribution -- one artifact to build and ship into sandboxes. The existing `cmd/sandbox-host` migrates into `flexagent serve sandbox-host`.

2. **Port proxying on sandbox-host**: Sandbox-host allocates a dynamic port on its own address and sets up a TCP reverse proxy to the container's exposed port. For non-gVisor mode, the process runs directly on the host and listens on a port -- no proxying needed. The LaunchProcess specification lives in section 6 of this plan; a standalone addendum (`11-sandbox-host-service.add03`) should be extracted before NativeSandboxControl implementation (see Follow-up Work section).

3. **~~API key handling~~**: Resolved. API keys are injected via environment variables, never transmitted over the AgentService RPC. For sandbox-launched mode, keys are passed through `LaunchProcessRequest.Env`. For standalone mode, keys come from standard environment variables. The `APIKey` field has been removed from `SessionConfig`.

4. **~~Session recovery after agent process restart~~**: Resolved -- the orchestrator owns conversation state (section 10). The agent process is ephemeral. On crash, orchestrator calls `ResumeSession` with the persisted conversation log on a new `flexagent serve agent` instance.

---

## Round 1 Review Disposition

Reviews incorporated: `18-agent-loop-rpc-review-r1-a.md` (Reviewer A), `18-agent-loop-rpc-review-r1-b.md` (Reviewer B).

| # | Finding | Reviewer | Priority | Disposition | Notes |
|---|---------|----------|----------|-------------|-------|
| F1 | Circular import: AgentService in `internal/rpc/api` | A | P0 | **Incorporated** | Moved AgentService to new `internal/agent/api` package. Added `EventPublisher` interface to avoid reverse import. See sections 3, 4, 13, 16.4. |
| P0 | SandboxControl conflicts with ExecutionEnvironment | B | P0 | **Incorporated** | Clarified complementary roles (orchestrator vs agent level). SandboxCapabilities now embeds `environment.Capabilities`. Added coordination table. See section 6. |
| F2 | Missing Close/shutdown on AgentLoopService | A | P0 | **Incorporated** | Added `Close()` method with drain logic. Added graceful shutdown for `flexagent serve agent`. See sections 3, 4, 7.4. |
| F3 | APIKey transmitted in plain struct over RPC | A | P0 | **Incorporated** | Removed `APIKey` from `SessionConfig`. Keys injected via env vars. See sections 3.1, 7.3, 17. |
| F4 | Duplicate SessionMetrics type | A | P1 | **Incorporated** | Use `agent.SessionMetrics` directly. Wire-format codec handles serialization. See section 3.1 note. |
| F5 | AgentMessageRecord to ai.Message conversion unspecified | A | P1 | **Incorporated** | Added section 3.3 with JSON schema per role, codec functions, and lossy conversion documentation. |
| P1-resume | Cross-agent resume conversion chain lossy/unspecified | B | P1 | **Incorporated** | Documented lossy steps, added codec.go for bidirectional mapping, noted tool name compatibility requirement. See sections 3.3, 10.4. |
| F6 | SubscribeEvents overlaps existing AgentEventService | A | P1 | **Incorporated** | Clarified: SubscribeEvents delegates to existing AgentEventService internally. See sections 3, 11. |
| F7 | SendMessage/Continue return per-turn streams but Agent only has session-wide subscriptions | A | P1 | **Incorporated** | Added section 3.2 specifying turn-scoping via EventTurnCompleted filtering. Documented dual delivery model. |
| F8 | LaunchProcess insufficient specification | A | P1 | **Incorporated** | Added lifecycle, port management, process monitoring details. Added KillProcess, GetProcessStatus. See section 6. |
| F9 | No session limits | A | P1 | **Incorporated** | Added `MaxSessions` config via `WithMaxSessions` option. See section 4. |
| F10 | Steer when idle undefined | A | P1 | **Incorporated** | Steer returns `CodeFailedPrecondition` when no active turn. FollowUp remains valid in any state. See sections 3, 4. |
| P1-RuntimeController | AgentService vs RuntimeController relationship | B | P1 | **Incorporated** | Added explicit layering explanation and diagram. See section 1. |
| P1-validation | ResumeSession validation | B | P1 | **Incorporated** | Added section 3.4 with schema versioning, validation rules, and warnings. |
| P1-stateless | Stateless label misleading | B | P1 | **Incorporated** | Changed "stateless" to "ephemeral". Documented mid-turn crash behavior and ZFS rollback. See section 10.1. |
| P1-backpressure | Dual-view streaming backpressure | B | P1 | **Incorporated** | Added section 12.4 covering backpressure policy, failure correlation, and timing skew. |
| P2-StreamTerminal | StreamTerminal duplicates existing TerminalService | A (F13), B | P2 | **Incorporated** | Removed `StreamTerminal` from AgentService. Orchestrator uses existing `TerminalService` directly. See section 12.2. |
| F11 | Duplicate section numbering | A | P2 | **Incorporated** | Renumbered all sections consistently (13-17). Fixed subsection numbering. |
| F12 | ToolEnvironmentConfig.Type should be typed constant | A | P2 | **Incorporated** | Added `ToolEnvironmentType` with constants `ToolEnvLocal`, `ToolEnvSandbox`. See section 3.1. |
| P2-seams | Connected components table incomplete | B | P2 | **Incorporated** | Added "Consumed Seams" table (section 16.3) listing all consumed interfaces and types. |
| P2-impl-order | Implementation ordering has dependency violation | B | P2 | **Incorporated** | Updated dependency header to include 11.add02. NativeSandboxControl uses mock for testing. Reordered steps. See section 15. |
| P2-APIKey | APIKey security concern with no mitigation | B | P2 | **Incorporated** | Resolved via env var injection (see F3 above). |
| P2-event-overlap | SubscribeEvents and SendMessage overlap underspecified | B | P2 | **Incorporated** | Documented dual delivery model in sections 3, 3.2. |
| P2-shutdown | No graceful shutdown protocol for agent server | B | P2 | **Incorporated** | Added section 7.4 with drain period, event delivery, and deadline behavior. |
| F14 | ResourceSpec undefined in SandboxControl | A | P2 | **Incorporated (R2)** | Originally acknowledged. R2 review identified the cross-layer import issue. Now defines local `ResourceSpec` in `internal/sandbox/control`. See section 6 and R2 disposition. |
| F15 | No idempotency for CreateSession/ResumeSession | A | P2 | **Incorporated** | Specified: duplicate session ID returns error. See sections 3, 4. |
| F16 | Missing ListSessions method | A | P2 | **Incorporated** | Added `ListSessions` to AgentService interface. See section 3. |
| F17 | Error mapping for agent errors not defined | A | P2 | **Incorporated** | Added error mapping table. See section 5.4. |
| F19 | AgentEventServer import direction violation | A | P3 | **Incorporated** | Replaced with `EventPublisher` interface in `internal/agent`. See section 4. |
| F20 | Testing section missing codec tests | A | P3 | **Incorporated** | Added codec round-trip tests to section 14.1. |
| F21 | Continue semantic gap (Start vs Prompt) | A | P3 | **Incorporated** | Added `started` flag to `managedSession`. See section 4 SendMessage flow. |
| F22 | DestroySession vs active streaming | A | P3 | **Incorporated** | Specified: forceful cancel, streams closed with EOF. See sections 3, 4. |
| F23 | Implementation order missing MapError extension | A | P3 | **Incorporated** | Added as step 5 in implementation order. See section 15. |
| P3-test-harness | No test harness document | B | P3 | **Acknowledged** | Noted as TODO in section 14.3. Will create as follow-up. |
| P3-event-import | AgentEventServer shared from RPC layer | B | P3 | **Incorporated** | See F19 resolution above. |
| P3-ResumeRequest-duplication | ResumeSessionRequest duplicates CreateAgentSessionRequest | B | P3 | **Incorporated** | Extracted shared `SessionConfig` type. See section 3.1. |
| F18 | Verify termmux session_log.go path exists | A | P3 | **Acknowledged** | Path references are from existing plan docs. Verification deferred to implementation. |

---

## Follow-up Work

Items identified during R2 review that should be completed before or during implementation:

1. **`11-sandbox-host-service.add03` addendum (blocking for steps 12-13 LaunchProcess):** Extract the LaunchProcess sandbox-host specification (RPC types, lifecycle, port proxying, PID tracking, gVisor integration for launched processes) from section 6 of this plan into a standalone addendum document. This gives sandbox-host implementors a self-contained reference and enables independent review. Must be created before NativeSandboxControl.LaunchProcess/KillProcess/GetProcessStatus can be implemented. Without this, steps 12-13 can only implement CreateSandbox, DestroySandbox, PauseSandbox, and ResumeSandbox (which wrap existing sandbox-host RPCs).

2. **`18-agent-loop-rpc-test-harness.md`:** Create the dedicated test harness document before or during implementation step 10 (integration tests). See section 14.3.

---

## Round 2 Review Disposition

Reviews incorporated: `18-agent-loop-rpc-review-r2-a.md` (Reviewer A), `18-agent-loop-rpc-review-r2-b.md` (Reviewer B).

| # | Finding | Reviewer | Priority | Disposition | Notes |
|---|---------|----------|----------|-------------|-------|
| B-F1 | Subscribe-before-start race: SendMessage subscribes to events AFTER calling agent.Start(), losing early events | B | P1 | **Incorporated** | Reordered SendMessage flow: subscribe FIRST, then call Start()/Prompt(). If Start() fails, subscription is closed. See section 3.2 and section 4 SendMessage flow steps 2-4. |
| B-F2 | Close() drain has no sync primitive to wait on goroutine exit | B | P1 | **Incorporated** | Added `sync.WaitGroup` to `AgentLoopService`. Incremented on turn start (SendMessage step 5), decremented in NativeDriver.run() deferred cleanup. Close() calls `wg.Wait()` with deadline. See section 4 Close flow and `AgentLoopService` struct. |
| A-F2 | `AgentEventReceiver` from `internal/rpc/api` used in transport-neutral `AgentService` interface | A | P1 | **Incorporated** | Defined new `EventReceiver` interface in `internal/agent/api` returning `*agent.AgentEvent` directly (no envelope). `AgentService` methods now return `EventReceiver`. RPC layer adapts between `EventReceiver` and wire-format `AgentEventReceiver`. See section 3 interface, new `EventReceiver` type, and updated import flow in section 16.4. |
| A-F3 | Dependency `11-sandbox-host-service.add02` referenced but plan document does not exist | A | P1 | **Incorporated** | Removed phantom `.add02` dependency from header. LaunchProcess specification lives in section 6 of this plan. Added Follow-up Work section noting that `11-sandbox-host-service.add03` should be extracted as a standalone addendum before implementation step 13. |
| A-F1 | `EventPublisher` placement in `internal/agent` vs `internal/agent/api` | A | P2 | **Acknowledged (no change)** | Left `EventPublisher` in `internal/agent/service.go`. Both placements work; current location keeps the interface next to its consumer (`AgentLoopService`). Implementor may move it to `internal/agent/api` during step 3 if preferred. |
| A-F5 / B-F8 | `ResumeSession` validation does not check role alternation | A+B | P2 | **Incorporated** | Added role alternation check as rule 6 in section 3.4. Produces a warning (not error) since crash recovery can legitimately produce non-alternating sequences. Warning is preferable to surfacing cryptic provider API errors. |
| B-F3 | Steer TOCTOU race between checking StateIdle and enqueueing the steer | B | P2 | **Incorporated** | Added explicit acknowledgment of the TOCTOU gap in section 4 Steer flow. The race is inherent and acceptable for best-effort steering semantics -- steers are silently discarded by the control queue if the turn ends between check and enqueue. |
| B-F4 | `SandboxCapabilities` embedding `environment.Capabilities` leaks agent-level fields | B | P2 | **Incorporated** | Originally added documentation. Seam review (F6) escalated this to P1: replaced embedding with explicit fields (`Snapshots`, `Rollback`, `Pause`, `ConcurrentSandboxes`, `MaxSandboxDuration`). See section 6. |
| A-F6 | Two codec locations (`codec.go` and `agent_map.go`) with unclear boundary | A | P2 | **Incorporated** | Rewrote section 5.5 to clearly explain the boundary: `internal/agent/api/codec.go` handles domain model conversion (AgentMessage <-> AgentMessageRecord), `internal/rpc/codec/agent_map.go` handles wire-format serialization for RPC transport. |
| B-F5 | Missing `stop_reason` in assistant JSON schema | B | P3 | **Incorporated** | Added `stop_reason` and `error_message` fields to the assistant role JSON schema in section 3.3. Added note explaining why these are important for resume and why `API`/`Provider` are intentionally omitted. |
| A-F8 / B-F6 | `ResourceSpec` import ambiguity in `CreateSandboxRequest` | A+B | P3 | **Incorporated** | Defined local `ResourceSpec` in `internal/sandbox/control/control.go` (two fields: `CPUs`, `MemMB`) to avoid cross-layer import of `internal/rpc/api`. RPC codec maps between `control.ResourceSpec` and `api.ResourceSpec`. See section 6 and updated import flow in section 16.4. |
| A-F10 / B context | Context propagation: RPC request context vs session-scoped context | A | P3 | **Incorporated** | Documented in section 16.4: turns use session-scoped context (from `managedSession.ctx`), not the RPC request context. This ensures turns survive RPC connection drops. Added `ctx` field to `managedSession` struct. See section 4 SendMessage flow step 3 and section 16.4 Context propagation note. |
| B replay | ResumeSession replay mechanism detail missing | B | P3 | **Incorporated** | Added explicit mechanism to section 10.2 sequence diagram: convert each `AgentMessageRecord` to `agent.AgentMessage` via `codec.RecordToAgentMessage()`, build a `Session` with the resulting `ConversationLog`, call `agent.SetSession(session)`. |
| B test | Test harness document TODO should specify timing | B | P3 | **Incorporated** | Updated section 14.3 TODO to specify: create before or during implementation step 10 (integration tests), as it informs test infrastructure design. |
| A-F4 | `ai.GetModel(provider, modelID)` may not exist | A | P2 | **Acknowledged** | This is a minor detail -- the implementor will resolve the actual model registry API during step 3. The intent (resolve model + provider from string identifiers) is clear. |
| A-F7 | Wiring between `Agent.Subscribe()` and `EventPublisher` unspecified | A | P2 | **Acknowledged** | Straightforward wiring: `AgentLoopService` calls `agent.Subscribe(fn)` during CreateSession, the callback calls `publisher.Publish(sessionID, event)`. The `unsubscribe` function is stored in `managedSession.unsubscribe` and called during DestroySession. `Agent.Subscribe()` callbacks run synchronously in the event bus goroutine; `EventPublisher.Publish()` uses non-blocking channel sends so it does not slow the agent loop. |
| A-F9 | Error mapping extends `MapError` adding `internal/agent` import | A | P3 | **Acknowledged** | Adding `internal/agent` as a dependency of `internal/rpc/errors.go` is acceptable -- the `rpc` package is already a cross-cutting concern that imports `internal/sandbox` and `internal/sandbox/zfs`. |
| B-F4 context | `internal/agent/api` -> `internal/agent` import fragility | B | P2 | **Incorporated** | Added invariant note in section 16.4: the import MUST remain types-only. If codec logic needs Agent methods, use function parameters or interfaces in `internal/agent/api`. |

---

## Seam Review Disposition

Review incorporated: `18-agent-loop-rpc-seam-review.md` (automated seam review).

| # | Finding | Seam | Severity | Disposition | Notes |
|---|---------|------|----------|-------------|-------|
| F13 | transport.Server constructor requires all services upfront; Handler() unconditionally registers all sandbox procedures. `flexagent serve agent` mode needs AgentService without SandboxService. | `flexagent serve agent` <-> transport.Server | P0 | **Incorporated** | Documented transport.Server refactoring to functional options as a prerequisite in section 5.1. Updated implementation step 8 to include the refactoring (change to plan 13). Updated Modified Seams table. See sections 5.1, 15, 16.1. |
| F1 | Agent.Start vs Agent.Prompt asymmetry creates fragile `started` flag branching; partial Start() failure could desync the flag. | AgentService <-> Agent | P1 | **Incorporated** | Documented that if `Start()` returns an error, `started` stays false and the next `SendMessage` retries `Start()`. Made this explicit in section 4 SendMessage flow step 4. |
| F4 | EventReceiver error contract underspecified -- adapter must handle io.EOF vs ErrStreamClosed vs other errors correctly. | AgentService <-> RPC | P1 | **Incorporated** | Defined full error contract in the `EventReceiver` interface comment: (event, nil) for events, (nil, io.EOF) for normal end, (nil, err) for abnormal termination. Specified RPC adapter mapping. See section 3. |
| F6 | SandboxCapabilities embedding environment.Capabilities creates semantic confusion -- same field names mean different things at different layers. | SandboxControl <-> ExecutionEnvironment | P1 | **Incorporated** | Replaced embedding with explicit fields. `SandboxCapabilities` now has its own fields (`Snapshots`, `Rollback`, `Pause`, `LaunchProcess`, `DeepPause`, `ConcurrentSandboxes`, `MaxSandboxDuration`). No embedding of `environment.Capabilities`. Orchestrator maps between the two when needed. See section 6. |
| F10 | CreateSandbox -> CreateSession field mapping lossy -- Resources/Quota mismatch. | SandboxControl <-> Sandbox-Host RPC | P1 | **Incorporated** | Documented field mapping in section 6.1: Template -> BaseSnapshot, Labels -> Labels, Resources -> logged but not transmitted (host-level config), Quota -> default value via `WithDefaultQuota()` constructor option. |
| F11 | LaunchProcess depends on nonexistent sandbox-host RPC endpoint. | SandboxControl <-> Sandbox-Host RPC | P1 | **Incorporated** | Already addressed in Follow-up Work section. Added explicit note that implementation steps 12-13 are blocked for LaunchProcess/KillProcess/GetProcessStatus until `11-sandbox-host-service.add03` addendum is created. See sections 15, Follow-up Work. |
| F12 | agent.AgentMessage -> ConversationEntry conversion function unspecified -- gap in cross-agent resume chain. | Cross-agent resume chain | P1 | **Incorporated** | Added subsection to 10.4 specifying `AgentMessageToConversationEntries()` function in `internal/agent/api/termmux_codec.go`. Specified field mapping table by message type. Added file to package structure and import flow. See sections 10.4, 13, 16.4. |
| F3 | GetSession clones full conversation just to compute ConversationLen -- wasteful for large conversations. | AgentService <-> Agent | P2 | **Incorporated** | Added note to `GetAgentSessionResponse` that `GetSession` should use a lightweight `Agent.ConversationLen()` method, not clone the full conversation. See section 3.1. |
| F5 | Dual delivery double-counting risk -- consumers could persist events from both per-turn and session-wide streams. | AgentService <-> RPC | P2 | **Incorporated** | Added explicit warning in section 3.2 that consumers MUST NOT count/persist events from both streams. Session-wide stream is canonical for persistence. |
| F7 | Address format ambiguity (host:port vs URL) between SandboxControl responses and AgentServiceClient constructor. | SandboxControl <-> ExecutionEnvironment | P2 | **Incorporated** | Standardized address format to "host:port" string. Added `AddressToURL()` helper function in `internal/sandbox/control`. Updated address field comments. See sections 6, 8.3. |
| F8 | EventPublisher testing gap -- unit tests for AgentLoopService do not mention mocking EventPublisher. | AgentLoopService <-> EventPublisher | P2 | **Incorporated** | Added mock EventPublisher test cases to section 14.1: verify session ID propagation, event type correctness, and that destroyed sessions stop publishing. |
| F2 | Agent.Abort() signature -- cleared, no issue found. | AgentService <-> Agent | P2 | **Acknowledged (no change)** | Signatures align. No action needed. |
| F9 | EventPublisher.Publish is fire-and-forget -- persistence-critical events could be silently dropped under backpressure. | AgentLoopService <-> EventPublisher | P3 | **Acknowledged (no change)** | Consistent with existing `AgentEventServer` behavior. Already discussed in section 12.4 (backpressure policy). The orchestrator's persistence subscriber uses blocking writes; if buffer sizing is adequate, drops do not occur. |
| F14 | Import constraint between internal/agent/api -> internal/agent enforced by convention only. | Import cycles | P3 | **Acknowledged** | Import flow is safe (no cycle). Added note in section 16.4 that CI linting (e.g., `go-import-lint`) could enforce the types-only constraint automatically in the future. |
