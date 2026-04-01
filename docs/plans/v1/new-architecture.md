# New Architecture — Agent Protocol & Driver Framework

## Overview

flex-agent-runtime uses ACP (Agent Communication Protocol) as the protocol
between clients (drivers) and agents. The framework provides agent wrappers
that make any agent — real TUI binaries, ACP-native agents, or our built-in
LLM loop — speak ACP. The only addition beyond standard ACP is a PTY sideband
for streaming terminal I/O from TUI agents.

Process management (spawning agent binaries, provisioning containers) is handled
by a supervisor that is out of band — not part of the protocol contract. Lifecycle
hooks from the client to an environment manager are a client-side implementation
concern, not a protocol layer.

```
┌─────────────────────────────────────────────────────────────────┐
│  Your Controller / Driver                                       │
│  (h2, web UI, CI runner, custom orchestrator — you build this)  │
│                                                                 │
│  Implements:                                                    │
│    • ACP Client (configure, observe, prompt agents)             │
│    • PTY Sideband Client (render terminal, send input)          │
│    • Lifecycle hooks to environment manager (client-side)       │
│                                                                 │
│  Source of truth for:                                           │
│    • Session state (ACP event log + config)                     │
│    • Session resume (reconstructs context, creates new session) │
└───────┬──────────────────────────────────────┬──────────────────┘
        │                                      │
   ACP v0.3                               PTY Sideband
   (JSON-RPC / NDJSON stdio)              (binary stream)
        │                                      │
┌───────▼──────────────────────────────────────▼──────────────────┐
│  Agent Wrapper (provided by this framework)                     │
│                                                                 │
│  Per-agent-type implementation:                                 │
│    • claude-code wrapper                                        │
│    • codex wrapper                                              │
│    • native-driver (built-in LLM loop)                          │
│    • generic wrapper (any CLI)                                  │
│                                                                 │
│  All wrappers present ACP + optional PTY sideband               │
│  regardless of what's underneath                                │
└─────────────────────────────────────────────────────────────────┘
        ▲
        │ (out of band)
┌───────┴─────────────────────────────────────────────────────────┐
│  Supervisor (provided by this framework)                        │
│                                                                 │
│  Runs in target environment. Spawns wrapper processes.          │
│  Not part of the ACP protocol contract.                         │
└─────────────────────────────────────────────────────────────────┘
```

### What the Framework Provides

- **Agent wrappers** — implementations that wrap real agent binaries (Claude Code,
  Codex, etc.) and present ACP + PTY sideband. For PTY-wrapped agents, the
  wrapper synthesizes ACP events from hooks/OTEL/session logs.
- **NativeDriver** — a built-in Go agent loop that emits ACP directly
- **Supervisor** — process that runs in target environments and spawns wrappers
  on demand (out of band, not part of protocol)
- **VT backend** — embedded virtual terminal for PTY buffer management
- **Agent-type manifests** — declarative config for each agent type

### What Consumers Build

- **Their own controller/driver** — ACP client + PTY sideband client + whatever
  orchestration logic they need (h2's messaging + roles + pods, a CI runner, etc.)
- **Lifecycle hooks** (client-side) — the driver observes ACP events and fires
  hooks to an environment manager (snapshot on turn completion, etc.)
- **Environment manager** (optional) — handles lifecycle hooks for infrastructure
  (snapshots, containers, resource provisioning)

### Design Principles

1. **The driver controls cross-cutting capabilities; the agent owns its own
   logic.** The driver injects configuration that's common across agent types:
   skills, MCP servers, custom tools, system prompt additions (AGENTS.md /
   CLAUDE.md), permissions, model selection. But the agent retains its own
   internal state, harness logic, and personality — that's the whole reason
   you'd choose to run Claude Code vs Codex vs something else. They make
   different decisions about how to approach problems, what tools to use by
   default, how to manage context, etc. The driver may also leave configuration
   blank and let the agent fall back to its built-in defaults.

2. **ACP is the protocol.** One protocol for everything between client and agent:
   configuration via `sessionMetadata` and `set_config_option`, observation via
   `session/update`, interaction via `session/prompt`, permissions via
   `requestPermission`. No separate "driver extension" protocol.

3. **Hooks all the way down.** Agents emit ACP events. The client/driver observes
   them and fires its own lifecycle hooks to the environment manager. Same
   pattern, different layers — but the lifecycle hooks are a client concern, not
   a protocol between client and agent.

4. **Same interface regardless of agent type.** Whether the agent is our built-in
   NativeDriver, a real Claude Code binary in a PTY, or a future agent that
   speaks ACP natively — consumers see ACP + optional PTY sideband.

5. **Client owns session state and resume.** The client stores ACP events + config.
   Resume = create a new session (`session/new`) with prior context in
   `sessionMetadata`. The agent doesn't need its own persistence.

---

## Protocol Layer 1: ACP v0.3 (Agent Communication Protocol)

Standard ACP as defined by the ACP community. JSON-RPC 2.0 over NDJSON stdio.
This is the structured event stream — what the agent is doing.

**We use ACP as-is. No modifications to the spec.**

For PTY-wrapped agents, the wrapper synthesizes ACP events from hooks/OTEL/logs.
For the NativeDriver, it emits ACP events directly. Consumers always see ACP
regardless of what's underneath.

### Transport

JSON-RPC 2.0 messages, one per line (NDJSON), over the wrapper's stdio when
running as a subprocess — or over a streaming RPC connection when running
remotely.

### Methods: Client → Agent

#### `initialize` — Handshake & Capability Negotiation

Note: ACP `initialize` connects to a single agent process. The agent type is
NOT selected here — it was already determined by which binary `agent/launch`
spawned. Each `agent/launch` = one wrapper = one `initialize` = one agent type.

```
Request:
  protocolVersion       (number)
  clientCapabilities:
    fs:
      readTextFile      (boolean)
      writeTextFile     (boolean)
    terminal            (boolean)
    auth:
      terminal          (boolean, optional) — terminal-based auth supported
      _meta:
        gateway         (boolean, optional) — gateway auth supported
        terminal-auth   (boolean, optional)
    _meta:
      terminal_output   (boolean, optional) — client handles terminal output
  clientInfo:
    name                (string)   — e.g., "h2", "flex-ci-runner"
    version             (string)

Response:
  protocolVersion       (number)
  agentCapabilities:
    loadSession         (boolean, optional)
    sessionCapabilities:
      fork              (object, optional)  — supports session/fork [UNSTABLE]
      list              (object, optional)  — supports session/list
      resume            (object, optional)  — supports session/resume [UNSTABLE]
      close             (object, optional)  — supports session/close [UNSTABLE]
    promptCapabilities:
      image             (boolean, optional) — supports image content blocks
      embeddedContext   (boolean, optional)
    mcpCapabilities:
      http              (boolean, optional) — supports HTTP MCP servers
      sse               (boolean, optional) — supports SSE MCP servers
    _meta:
      claudeCode:
        promptQueueing  (boolean, optional)
  agentInfo:
    name                (string)   — e.g., "flex-claude-wrapper"
    title               (string, optional)
    version             (string)
  authMethods           (AuthMethod[], optional)
```

#### `authenticate` — Auth Handshake

```
Request:
  methodId              (string)
  credential            (string, optional)

Response: {}
```

#### `session/new` — Create Session

```
Request:
  cwd                   (string, optional)
  sessionMetadata       (object, optional)

Response:
  sessionId             (string)
  sessionCapabilities   (object, optional)
  agentSessionId        (string, optional)
```

#### `session/load` — Resume Persisted Session

Requires `loadSession` capability.

```
Request:
  sessionId             (string)
  cwd                   (string, optional)

Response:
  agentSessionId        (string, optional)
  models                (SessionModelState, optional)
```

#### `session/list` — List Sessions

```
Request: (none)

Response:
  sessions              (SessionInfo[]):
    - sessionId         (string)
    - name              (string, optional)
    - createdAt         (string, optional)
    - lastUsedAt        (string, optional)
```

#### `session/prompt` — Send Prompt (Streaming)

```
Request:
  sessionId             (string)
  prompt                (ContentBlock[]):
    - { type: "text", text: string }
    - { type: "image", mimeType: string, data: string }
    - { type: "resource_link", uri: string, title?: string, name?: string }
    - { type: "resource", resource: { uri: string, text?: string } }

Response:
  stopReason            (StopReason):
    "end_turn" | "completed" | "done" | "max_tokens" |
    "stop_sequence" | "tool_use"
  models                (SessionModelState, optional)
```

During execution, the agent emits `session/update` notifications (see below).

#### `session/cancel` — Cancel In-Flight Prompt

```
Request:
  sessionId             (string)

Response: {}
```

#### `session/set_mode` — Change Agent Mode

```
Request:
  sessionId             (string)
  modeId                (string)  — "default", "plan", "acceptEdits", etc.

Response: {}
```

#### `session/set_config_option` — Set Config Option

```
Request:
  sessionId             (string)
  configId              (string)  — "model", "reasoning_effort", etc.
  value                 (string)

Response:
  configOptions         (SessionConfigOption[])
```

#### `session/close` [UNSTABLE]

```
Request:
  sessionId             (string)

Response: {}
```

#### `session/fork` [UNSTABLE]

```
Request:
  sessionId             (string)
  cwd                   (string, optional)

Response:
  sessionId             (string)
  agentSessionId        (string, optional)
```

#### `session/resume` [UNSTABLE]

```
Request:
  sessionId             (string)

Response:
  agentSessionId        (string, optional)
```

### Notifications: Agent → Client

#### `session/update` — Streaming Session Updates

Emitted during `session/prompt` execution.

```
Params:
  sessionId             (string)
  update                (SessionNotification)
```

**Update types:**

```
agent_message_chunk:
  content               (ContentBlock)

agent_thought_chunk:
  content:
    type                "thinking"
    text                (string)

tool_call:
  toolName              (string)
  toolUseId             (string)
  input                 (object)

tool_call_update:
  toolUseId             (string)
  status                "in_progress" | "completed" | "error"
  content               (ContentBlock, optional)

usage_update:
  inputTokens           (number)
  outputTokens          (number)
  cacheCreationInputTokens (number, optional)
  cacheReadInputTokens  (number, optional)

current_mode_update:
  currentModeId         (string)

config_option_update:
  configOption          (SessionConfigOption)

available_commands_update:
  availableCommands     (AvailableCommand[])

plan:
  plan                  (object)
```

### Methods: Agent → Client (Reverse Calls)

#### `requestPermission` — Permission Request

```
Request:
  sessionId             (string)
  toolCall:
    title               (string)
    kind                (ToolKind, optional):
      "read" | "edit" | "delete" | "move" | "execute" |
      "search" | "fetch" | "think" | "other"
  options               (PermissionOption[]):
    - optionId          (string)
    - kind              "allow_once" | "allow_always" | "reject_once" | "reject_always"
    - description       (string, optional)

Response:
  outcome:
    outcome             "selected" | "cancelled"
    optionId            (string, conditional)
```

#### `readTextFile` — Read File from Client FS

```
Request:
  sessionId             (string)
  path                  (string)

Response:
  contents              (string)
  encoding              (string, optional)
```

#### `writeTextFile` — Write File to Client FS

```
Request:
  sessionId             (string)
  path                  (string)
  contents              (string)
  encoding              (string, optional)

Response: {}
```

#### `createTerminal` — Spawn Process on Client Side

```
Request:
  sessionId             (string)
  command               (string)
  args                  (string[], optional)
  cwd                   (string, optional)
  env                   (EnvVar[], optional)
  outputByteLimit       (number, optional)

Response:
  terminalId            (string)
  pid                   (number, optional)
```

#### `terminalOutput` — Read Terminal Output

```
Request:
  terminalId            (string)

Response:
  output                (string)
  truncated             (boolean)
```

#### `waitForTerminalExit` — Wait for Process Exit

```
Request:
  terminalId            (string)

Response:
  exitCode              (number, optional)
  signal                (string, optional)
```

#### `killTerminal` — Kill Terminal Process

```
Request:
  terminalId            (string)
  signal                (string, optional)  — default "SIGTERM"

Response: {}
```

#### `releaseTerminal` — Release Terminal Resources

```
Request:
  terminalId            (string)

Response: {}
```

### Error Codes

| Code | Name | Meaning |
|------|------|---------|
| -32700 | Parse error | Malformed JSON |
| -32600 | Invalid request | Not valid JSON-RPC |
| -32601 | Method not found | Unknown method |
| -32602 | Invalid params | Bad parameters |
| -32603 | Internal error | Unhandled error |
| -32000 | Auth required | Authentication needed |
| -32001 | Resource not found | File/resource missing |
| -32002 | Session not found | Unknown session ID |
| -32070 | Timeout | Operation timed out |
| -32071 | Permission denied | Permission rejected |
| -32072 | Permission unavailable | No permission UI available |

### Content Block Types

```
{ type: "text", text: string }
{ type: "image", mimeType: string, data: string }
{ type: "resource_link", uri: string, title?: string, name?: string }
{ type: "resource", resource: { uri: string, text?: string } }
{ type: "tool_use", id: string, name: string, input: object }
{ type: "tool_result", toolUseId: string, content: ContentBlock[], isError: boolean }
```

---

## Driver Concerns Via ACP

Everything the driver needs to control is handled through standard ACP methods
with rich use of `sessionMetadata` and `session/set_config_option`. No separate
driver protocol.

### Configuration Injection via `sessionMetadata`

When creating a session, the driver passes **semantically rich, agent-agnostic**
configuration in the `sessionMetadata` field of `session/new`. The wrapper
translates this to agent-specific mechanisms (CLI flags, config files, env vars,
etc.). The client does NOT need to know how Claude Code vs Codex vs NativeDriver
consumes each config — it sends the same semantic schema regardless.

#### `sessionMetadata` Schema

All fields are optional. The wrapper ignores fields it doesn't support.

```
sessionMetadata:

  # --- Identity & Instructions ---
  systemPrompt        (string)          Agent's primary system prompt
  appendInstructions  (string)          Additional instructions appended to
                                        system prompt (e.g., AGENTS.md content)
  globalInstructions  (InstructionFile[]) Instruction files to inject:
    - name            (string)            e.g., "CLAUDE.md", "AGENTS.md"
      content         (string)            file content

  # --- Model & Mode ---
  model               (string)          e.g., "opus", "sonnet", "gpt-4o"
  mode                (string)          e.g., "default", "plan", "acceptEdits"

  # --- Tools & Capabilities ---
  mcpServers          (MCPServer[])     MCP servers to connect:
    - name            (string)
      transport       (string)          "stdio" | "http" | "sse"
      command         (string, optional) for stdio transport
      args            (string[], optional)
      url             (string, optional) for http/sse transport
      env             (object, optional)
  skills              (Skill[])         Skills to make available:
    - name            (string)
      content         (string)          skill definition content
  allowedTools        (string[])        Restrict tool set (whitelist)
  deniedTools         (string[])        Block specific tools (blacklist)
  hooks               (Hook[])          Event hooks:
    - event           (string)          "PreToolUse", "PostToolUse", etc.
      command         (string)

  # --- Permissions ---
  permissionMode      (string)          "approve-all" | "approve-reads" | "deny-all"

  # --- Environment ---
  additionalDirs      (string[])        Extra directories to expose
  env                 (object)          Additional environment variables

  # --- Resume / Context ---
  priorContext:
    conversationHistory (AcpEvent[])    Prior ACP events to reconstruct from
    previousSessionId   (string)        Hint for agents with own persistence

  # --- Extension point ---
  custom              (object)          Agent-type-specific config that doesn't
                                        fit the standard schema. Wrapper passes
                                        through to the agent as-is.
```

#### Translation Examples

The same `sessionMetadata` gets translated differently per wrapper:

**Claude Code wrapper receives:**
```json
{
  "systemPrompt": "You are a reviewer",
  "model": "opus",
  "mcpServers": [{"name": "gh", "transport": "stdio", "command": "gh-mcp"}],
  "skills": [{"name": "commit", "content": "..."}],
  "permissionMode": "approve-reads"
}
```

**Wrapper translates to:**
```
CLI args:  claude --system-prompt "You are a reviewer" --model opus
                  --permission-mode approve-reads
Files:     settings.json ← { "mcpServers": {...} }
           skills/commit.md ← skill content
```

**NativeDriver receives the same metadata and applies it directly:**
```
context.SystemPrompt = metadata.SystemPrompt
context.Model = metadata.Model
context.MCPServers = metadata.MCPServers  // connects directly
context.Skills = metadata.Skills          // registers directly
context.PermissionMode = metadata.PermissionMode
```

**Generic wrapper receives it and applies what it can:**
```
env vars:  SYSTEM_PROMPT="You are a reviewer"  (if agent reads it)
cwd:       set from session/new cwd field
           (most other fields ignored — generic agents have no config surface)
```

The agent-type manifest declares which fields each wrapper supports, so the
client can know what will be applied vs ignored.

### Mid-Session Configuration via `session/set_config_option`

The client sends the full updated `sessionMetadata` as a single config option.
The wrapper diffs against the previous metadata and applies what it can.

```
session/set_config_option:
  configId: "sessionMetadata"
  value: '{ "model": "sonnet", "mcpServers": [...], "skills": [...], ... }'
```

Full replacement — the client always sends the complete desired state. The
wrapper compares to the previous metadata, identifies what changed, and:
- Applies changes it can (model → ACP native, MCP servers → config file hot-reload)
- Ignores fields that haven't changed
- Reports what was applied vs rejected in the response

The `set_config_option` response includes `configOptions` which the wrapper
uses to report status. Wrappers should return which fields were applied and
which were rejected (with reasons).

**What's typically supported mid-session:**

| Field | Mid-session support | Mechanism |
|-------|:-------------------:|-----------|
| `model` | ✅ | ACP native (`set_config_option`) |
| `mode` | ✅ | ACP native (`set_mode`) |
| `mcpServers` | ⚠️ | Write config file, trigger hot-reload |
| `skills` | ⚠️ | Write skill files, trigger hot-reload |
| `allowedTools` / `deniedTools` | ⚠️ | Write config file, trigger hot-reload |
| `systemPrompt` | ❌ | Most agents don't support mid-session |
| `appendInstructions` | ❌ | Most agents don't support mid-session |
| `permissionMode` | ❌ | Most agents don't support mid-session |
| `env` | ❌ | Process environment is fixed at launch |

For changes that can't be applied mid-session, the client can close the
session and create a new one with updated `sessionMetadata` including
`priorContext` for conversation continuity.

### Session Resume via `session/new` + Context Reconstruction

The client is the source of truth for session state. Resume does NOT use
`session/load` (which depends on agent-side persistence). Instead:

```
Resume flow:
  1. Client has stored: sessionMetadata (config) + ACP event log
  2. Client calls session/new with:
     - Same (or updated) sessionMetadata
     - priorContext field containing conversation history
  3. Wrapper reconstructs agent context from priorContext:
     - Claude Code: --resume flag, or replay from session log
     - NativeDriver: preload conversation directly
     - Generic: best-effort (may start fresh)
  4. Agent is ready for new prompts with prior context
```

The framework can also support `session/load` for agents that have their own
persistence, but the client should not depend on it.

### Sub-Agent Requests via ACP Reverse Calls

Agents can request sub-agents from the driver using the same pattern as
ACP's `createTerminal`:

```
requestSubAgent (Agent → Client reverse call):
  Request:
    sessionId           (string)
    prompt              (ContentBlock[])
    config:
      agentType         (string, optional) — default: same as parent
      model             (string, optional)
      systemPrompt      (string, optional)
      cwd               (string, optional)
      tools             (string[], optional)
    metadata:
      role              (string, optional)
      toolUseId         (string, optional) — links to parent's tool_call

  Response:
    subAgentId          (string)

subAgentStatus (Agent → Client reverse call):
  Request:
    subAgentId          (string)
  Response:
    state               "running" | "completed" | "error" | "cancelled"
    turnsCompleted      (number)
    usage:
      inputTokens       (number)
      outputTokens      (number)

subAgentResult (Agent → Client reverse call):
  Request:
    subAgentId          (string)
  Response:
    result              (ContentBlock[])
    stopReason          (StopReason)

cancelSubAgent (Agent → Client reverse call):
  Request:
    subAgentId          (string)
  Response: {}
```

The client receives `requestSubAgent`, creates a new session (possibly in the
same wrapper or a different one), runs the prompt, and returns the handle.
The client maintains full visibility — the sub-agent is just another session
it manages.

Agents may also spawn sub-agents internally (e.g., Claude Code's Agent tool).
These are opaque to the client — visible only as long-running tool calls.
Both paths coexist.

### Lifecycle Hooks (Client-Side, Not Protocol)

The client observes ACP events and fires its own hooks to the environment
manager. These are NOT part of the ACP protocol — they're a client
implementation concern.

```
Client observes ACP events          Client fires lifecycle hooks
─────────────────────────           ──────────────────────────────
session/update: usage_update    ──► turnCompleted → env snapshots FS
agent exits (process dies)      ──► agentExited → env cleans up
client decides to pause         ──► prepareForPause → env snapshots
client decides to resume        ──► prepareForResume → env restores
```

Example hooks the client might fire:

| Hook | When client fires it | Environment manager might... |
|------|---------------------|----------------------------|
| `prepareForLaunch` | Before asking supervisor to spawn | Provision container, mount FS |
| `agentLaunched` | After ACP initialize succeeds | Start monitoring |
| `turnCompleted` | After seeing usage_update / end_turn | Incremental snapshot |
| `prepareForPause` | Client decides to pause session | Full snapshot |
| `prepareForResume` | Client wants to restart | Restore snapshot |
| `agentExited` | Wrapper process died | Cleanup resources |

These are entirely up to the client. A simple local-dev client might not fire
any hooks. An h2 orchestrator with ZFS+gVisor fires all of them.

### What Happened to the Driver Extension Protocol

It collapsed into ACP:

| Was Driver Extension | Now |
|---------------------|-----|
| `agent/launch` | Out of band (supervisor) |
| `agent/configure` | `session/set_config_option` with rich values |
| `agent/pause` | Client stops prompting + fires own hooks |
| `agent/resume` | `session/new` with `priorContext` in metadata |
| `agent/stop` | Client disconnects + fires own hooks |
| `agent/status` | Derived from ACP event stream |
| `agent/sessionState` | Client's own stored state |
| Lifecycle hooks | Client-side implementation, not protocol |
| Sub-agent requests | ACP reverse calls (like `createTerminal`) |

The only thing beyond standard ACP is the PTY sideband (binary stream that
can't be JSON-RPC) and the sub-agent reverse calls (which follow ACP's
existing reverse-call pattern).

---

## REMOVED: ~~Protocol Layer 2: Driver Extension Protocol~~

_This section has been removed. Driver concerns are handled via ACP's existing
methods (`sessionMetadata`, `set_config_option`, reverse calls) and client-side
lifecycle hooks. See "Driver Concerns Via ACP" above._

The original Driver Extension methods are preserved below for reference only.
They show the thinking that led to the current simpler design.

<details>
<summary>Original Driver Extension Protocol (historical reference)</summary>

JSON-RPC 2.0 over a separate channel (not stdio — that's ACP). Manages the
agent wrapper lifecycle and injects cross-cutting configuration.

ACP assumes the agent is autonomous and already running. The Driver Extension
handles everything ACP doesn't: launching the agent, injecting shared
capabilities (tools, MCP servers, skills, system prompt additions, permissions),
managing lifecycle (pause/resume/stop), and emitting hooks for environment
managers.

The driver does NOT own every piece of agent configuration. Agents retain their
own internal logic, built-in defaults, harness behavior, and state management.
The driver controls the pieces that are common across agent types and that an
orchestration layer needs to set — things like "what tools should be available"
and "what role should this agent play." Agent-specific behavior (how it manages
context, how it decides which tools to use, its built-in prompting strategies)
stays with the agent. Any configuration field in `agent/launch` can be omitted
to let the agent use its built-in defaults.

### Transport

JSON-RPC 2.0 over a dedicated channel, separate from ACP's stdio. Options:
- Unix domain socket (local)
- TCP/HTTP (remote)
- Additional fd pair (fd 3/4) if wrapper is a subprocess

The Driver Extension channel is established BEFORE the ACP channel — it's how
the controller tells the wrapper to launch the agent in the first place.

### Methods: Controller → Wrapper

#### `agent/launch` — Launch Agent Process

Tells the wrapper to spawn the agent binary with the given configuration.
This is process lifecycle — the agent binary isn't running yet. After launch
succeeds, the ACP channel is established (`initialize` handshake), and then
`session/new` creates a conversation within the running agent.

The distinction: `agent/launch` = "start this binary with this config."
`session/new` (ACP) = "start a conversation in the already-running agent."
A single launched agent could potentially host multiple ACP sessions, though
in practice most agents run one conversation at a time.

```
Request:
  agentType             (string)         — "claude_code", "codex", "native", "generic"
  binary                (string, optional) — path to binary (for generic)
  cwd                   (string)         — working directory
  config:
    systemPrompt        (string, optional)
    appendInstructions  (string, optional) — CLAUDE.md content, appended to system prompt
    model               (string, optional) — "opus", "sonnet", "gpt-4o", etc.
    permissionMode      (string, optional) — "approve-all", "approve-reads", "deny-all"
    mcpServers          (MCPServerConfig[], optional):
      - name            (string)
      - command         (string)
      - args            (string[], optional)
      - env             (object, optional)
    skills              (SkillConfig[], optional):
      - name            (string)
      - path            (string)         — path to skill definition
    allowedTools        (string[], optional)
    hooks               (HookConfig[], optional):
      - event           (string)         — "PreToolUse", "PostToolUse", etc.
      - command         (string)
    additionalDirs      (string[], optional)
    env                 (object, optional) — additional env vars
  resumeSessionId       (string, optional) — resume from stored session

Response:
  wrapperId             (string)         — unique wrapper instance ID
  agentPid              (number, optional)
  capabilities:
    pty                 (boolean)        — PTY sideband available
    hotReload           (boolean)        — supports mid-session config changes
    nativeAcp           (boolean)        — agent speaks ACP natively (vs synthesized)
```

After `agent/launch` succeeds, the controller can connect ACP (stdio or stream)
and optionally the PTY sideband.

#### `agent/configure` — Mid-Session Configuration Change

Update configuration while the agent is running. What's supported depends on
the agent type (declared in `capabilities` and the agent manifest).

```
Request:
  wrapperId             (string)
  changes:
    model               (string, optional)
    mode                (string, optional)
    mcpServers          (MCPServerConfig[], optional) — full replacement
    skills              (SkillConfig[], optional)     — full replacement
    allowedTools        (string[], optional)          — full replacement
    injectContext       (string, optional)             — sent as user message

Response:
  applied               (string[])      — which changes were applied
  rejected              (ChangeRejection[], optional):
    - field             (string)
    - reason            (string)        — "not_supported_mid_session", etc.
  requiresRestart       (boolean)       — true if some changes need restart
```

Changes that the wrapper can apply via ACP (`set_mode`, `set_config_option`)
are applied immediately. Changes that require file writes + hot-reload are
applied best-effort. Changes that can't be applied mid-session are rejected
with `requiresRestart: true`.

#### `agent/pause` — Pause Agent Session

Save session state and stop the agent process. Emits `prepareForPause` lifecycle
hook before stopping, giving the environment manager time to snapshot.

```
Request:
  wrapperId             (string)

Response:
  sessionState:
    driverConfig        (object)        — the config used to launch
    acpEventCount       (number)        — events in the ACP log
    configChanges       (object[], optional) — mid-session config mutations
    vtState             (object, optional)   — VT buffer snapshot (PTY agents)
```

#### `agent/resume` — Resume Paused Agent

Relaunch agent with saved state. Emits `prepareForResume` lifecycle hook first.

```
Request:
  wrapperId             (string)
  configOverrides       (object, optional) — override saved config on resume

Response:
  agentPid              (number, optional)
  resumed               (boolean)       — true if agent restored prior session
```

#### `agent/stop` — Stop Agent (No State Save)

Kill the agent process immediately. Emits `prepareForShutdown` hook.

```
Request:
  wrapperId             (string)
  force                 (boolean, optional) — SIGKILL vs graceful

Response: {}
```

#### `agent/status` — Query Wrapper Status

```
Request:
  wrapperId             (string)

Response:
  state                 (string)        — "launching", "running", "paused", "stopped", "error"
  agentPid              (number, optional)
  agentType             (string)
  uptime                (number)        — seconds since launch
  acpConnected          (boolean)
  ptyAvailable          (boolean)
  lastActivity          (string, optional) — ISO timestamp
  metrics:
    turnsCompleted      (number)
    toolCallsCompleted  (number)
    totalInputTokens    (number)
    totalOutputTokens   (number)
    totalCostUsd        (number, optional)
```

#### `agent/sessionState` — Get Full Session State

Returns the complete session state as the driver stores it. This is the
source-of-truth record.

```
Request:
  wrapperId             (string)

Response:
  driverConfig          (object)        — launch config (system prompt, model, tools, etc.)
  configChanges         (ConfigChange[]):
    - timestamp         (string)
    - change            (object)
  acpEventLog           (string)        — path to NDJSON event log, or inline
  vtState               (object, optional)
```

### Notifications: Wrapper → Controller (Lifecycle Hooks)

These are the hooks that environment managers (or the controller itself)
subscribe to. The wrapper emits them at lifecycle boundaries.

#### `lifecycle/prepareForLaunch`

Emitted before the agent binary is spawned. Environment should be ready.

```
Params:
  wrapperId             (string)
  agentType             (string)
  cwd                   (string)
```

#### `lifecycle/agentLaunched`

Agent process is running and ACP is connected.

```
Params:
  wrapperId             (string)
  agentPid              (number)
  acpConnected          (boolean)
  ptyAvailable          (boolean)
```

#### `lifecycle/turnCompleted`

Agent finished a turn (idle between prompts). Good time for incremental snapshot.

```
Params:
  wrapperId             (string)
  turnNumber            (number)
  usage:
    inputTokens         (number)
    outputTokens        (number)
```

#### `lifecycle/prepareForPause`

Agent session saved, process about to be killed. Environment should snapshot.

```
Params:
  wrapperId             (string)
```

#### `lifecycle/paused`

Agent process is dead, session state saved.

```
Params:
  wrapperId             (string)
```

#### `lifecycle/prepareForResume`

Driver wants to relaunch. Environment should restore snapshot.

```
Params:
  wrapperId             (string)
```

#### `lifecycle/resumed`

Agent relaunched and ACP reconnected.

```
Params:
  wrapperId             (string)
  agentPid              (number)
```

#### `lifecycle/prepareForShutdown`

Session ending. Environment should take final snapshot if archiving.

```
Params:
  wrapperId             (string)
```

#### `lifecycle/agentExited`

Agent process exited (normal or crash).

```
Params:
  wrapperId             (string)
  exitCode              (number, optional)
  signal                (string, optional)
  reason                (string)        — "normal", "crash", "killed", "oom"
```

#### `lifecycle/configChanged`

Driver updated agent config mid-session.

```
Params:
  wrapperId             (string)
  applied               (string[])
  rejected              (string[])
```

</details>

---

## PTY Sideband

Binary stream for bidirectional terminal I/O. This is NOT JSON-RPC — it's raw
terminal bytes for rendering the agent's TUI and accepting interactive input.

### When Available

- **PTY-wrapped agents** (Claude Code, Codex, generic CLI): always available
- **NativeDriver**: NOT available (no TUI to stream — unnecessary)
- **ACP-native agents**: available if the agent has a terminal (rare)

The `agent/launch` response declares `capabilities.pty: true/false`.

### Transport

Separate binary channel alongside ACP. Options:

- **Unix domain socket** — one socket per wrapper, bidirectional byte stream
- **Additional fd pair** — fd 3 (output), fd 4 (input) if wrapper is subprocess
- **WebSocket** — for remote/browser clients
- **RPC stream** — bidirectional streaming RPC (like current `StreamTerminal`)

The transport carries two types of frames:

### Wire Format

```
Data frame:
  [0x00] [4-byte big-endian length] [terminal bytes]

Control frame:
  [0x01] [4-byte big-endian length] [JSON payload]
```

This is the same framing h2 uses today. Simple, proven, low overhead.

### Data Frames

**Output (wrapper → client):** Raw terminal bytes from the agent's PTY. Includes
ANSI escape sequences, cursor movement, screen updates — everything needed to
render the terminal.

**Input (client → wrapper):** Raw bytes to write to the agent's PTY. Keystrokes,
mouse events, paste content. In passthrough mode, this goes directly to the
agent's terminal input.

### Control Frames (JSON)

#### `resize` — Client → Wrapper

```json
{ "type": "resize", "cols": 120, "rows": 40 }
```

#### `passthrough_request` — Client → Wrapper

Request exclusive input access (passthrough mode).

```json
{ "type": "passthrough_request" }
```

#### `passthrough_granted` — Wrapper → Client

```json
{ "type": "passthrough_granted" }
```

#### `passthrough_denied` — Wrapper → Client

Another client holds passthrough.

```json
{ "type": "passthrough_denied", "owner": "client-id" }
```

#### `passthrough_release` — Client → Wrapper

Release passthrough mode.

```json
{ "type": "passthrough_release" }
```

#### `screen_state` — Wrapper → Client

Full screen snapshot (sent on connect and periodically).

```json
{
  "type": "screen_state",
  "cols": 120,
  "rows": 40,
  "cursorRow": 15,
  "cursorCol": 32,
  "cursorVisible": true
}
```
Followed immediately by a data frame with the full screen content.

### Multi-Client

Multiple clients can connect to the PTY sideband simultaneously. All clients
receive output data frames. Only one client at a time can hold passthrough
(exclusive input). Other clients can still send control frames.

---

## Topology: Supervisor, initialize, and session/new

### The Nesting

```
Supervisor (out of band) — spawns wrapper process, sets up channels
  └─ initialize          — ACP: handshake, negotiate capabilities
       └─ session/new    — ACP: create a conversation (with sessionMetadata config)
       └─ session/new    — ACP: create another concurrent conversation
```

- **Supervisor** (out of band) spawns the wrapper process for a given agent type.
  This is not part of the ACP protocol — it's an implementation detail of how
  wrapper processes get started.
- **`initialize`** establishes the ACP connection. One per wrapper process.
- **`session/new`** creates a conversation. The `sessionMetadata` carries
  driver configuration (system prompt, tools, MCP servers, etc.). Multiple
  concurrent sessions per wrapper are supported by ACP.

### Multi-Agent Topology

```
Controller
    │
    ├── (supervisor spawns claude-code wrapper)
    │     └─ initialize ─► ACP connection 1
    │          └─ session/new(meta={role:coder, model:opus}) ─► session A
    │          └─ session/new(meta={role:coder, model:opus}) ─► session B
    │          └─ session/new(meta={role:coder, model:opus}) ─► session C
    │
    ├── (supervisor spawns claude-code wrapper)
    │     └─ initialize ─► ACP connection 2
    │          └─ session/new(meta={role:reviewer, model:sonnet}) ─► session D
    │
    └── (supervisor spawns codex wrapper)
          └─ initialize ─► ACP connection 3
               └─ session/new(meta={role:coder}) ─► session E
```

Each wrapper process = one ACP connection = one agent type. Multiple concurrent
sessions within one wrapper share the wrapper process but each has its own
`sessionMetadata` config.

### Multi-Session Within One Agent

ACP explicitly supports concurrent sessions within a single agent process. From
the ACP architecture docs:

> "Each connection can support several concurrent sessions, so you can have
> multiple trains of thought going on at once."

This is a real, intended capability. Multiple `session/prompt` calls with
different `sessionId`s can be in-flight simultaneously.

Note: each `session/new` carries its own `sessionMetadata`, so different
sessions within the same wrapper CAN have different configurations (different
system prompts, different tools, etc.) — it's up to the wrapper to honor
per-session metadata.

### The Supervisor (Out of Band)

The supervisor runs in the target environment and spawns wrapper processes.
It is NOT part of the ACP protocol contract. How the controller talks to
the supervisor is an implementation concern:

- **Local dev:** controller spawns wrapper directly as a subprocess
- **Remote/sandbox:** supervisor is a service on the host; controller talks
  to it via its own API (REST, gRPC, whatever)
- **In-process:** NativeDriver runs in the controller's process directly

The supervisor is part of what the framework provides — it's the entry point
consumers deploy into target environments. But its API is separate from and
simpler than the agent protocol.

---

## How Each Agent Type Maps

All agent types present ACP + optional PTY sideband. The controller sees the
same interface regardless of what's underneath.

### NativeDriver (Built-In Go Agent Loop)

```
Controller
    │
    ├── ACP ──────────────► NativeDriver emits ACP events directly
    │                       (no synthesis needed — it controls the loop)
    │                       Config via sessionMetadata + set_config_option
    │
    └── PTY Sideband ──────► NOT AVAILABLE (no TUI)
```

**ACP events:** Full fidelity — the NativeDriver controls the LLM loop directly:
- `tool_call` with complete input parameters
- `tool_call_update` with full output
- `agent_message_chunk` with streaming response
- `usage_update` with exact token counts
- No synthesis, no inference, no fidelity degradation

**Configuration:** `sessionMetadata` fields are applied directly (system prompt
loaded into context, tools registered, MCP servers connected). `set_config_option`
changes are applied immediately.

**Tool execution:** NativeDriver's tools may call into a sandbox (in split-tools
mode) via a **separate tool execution interface**. This is orthogonal to ACP —
it's an internal concern of how NativeDriver's tools work.

```
NativeDriver
    │
    │  Emits ACP events directly
    │
    │  Internally dispatches tool calls to:
    ├── Local tools (in-process: read, write, grep, glob)
    └── Sandbox tools (via RPC: bash, code interpreter)
            │
            ▼
        Tool Execution Interface (not part of ACP)
```

### Claude Code Wrapper (PTY-Wrapped)

```
Controller
    │
    ├── ACP ──────────────► Wrapper synthesizes from hooks + OTEL + session logs
    │                       Fidelity: high (session logs give full tool params)
    │                       Config via sessionMetadata → CLI flags + config files
    │
    └── PTY Sideband ──────► Real Claude Code TUI streamed to clients
```

**Event synthesis sources:**
- Hooks (highest priority): tool lifecycle, permissions, session boundaries
- OTEL: token counts, costs, turn boundaries
- Session logs (richest): full messages, tool calls with params & results, thinking
- PTY heuristics (fallback): activity/idle detection

**Configuration:** Wrapper translates `sessionMetadata` to Claude Code's
mechanisms: `--system-prompt` flag, `--model` flag, write `settings.json` for
MCP servers/hooks, write skills directory, etc.

### Codex Wrapper (PTY-Wrapped)

```
Controller
    │
    ├── ACP ──────────────► Wrapper synthesizes from hooks + logs
    │                       Fidelity: medium (less hook support than Claude Code)
    │
    └── PTY Sideband ──────► Real Codex TUI streamed to clients
```

### Generic Wrapper (Any CLI)

```
Controller
    │
    ├── ACP ──────────────► Wrapper synthesizes from PTY heuristics only
    │                       Fidelity: low (activity/idle, rough turn boundaries)
    │
    └── PTY Sideband ──────► Binary's terminal output streamed to clients
```

### Future: ACP-Native Agent

When agents support ACP natively, the wrapper becomes a thin passthrough:

```
Controller
    │
    ├── ACP ──────────────► Passthrough to agent's native ACP stdio
    │                       Fidelity: full (no synthesis needed)
    │
    └── PTY Sideband ──────► Available if agent has a TUI mode
```

---

## Agent-Type Manifest

Each agent type declares its capabilities in a declarative manifest. This is
what makes the framework pluggable — you describe an agent type, you don't
write custom Go code.

```yaml
agentType: claude_code
binary: claude

# What configuration the agent accepts and how to inject it
config:
  systemPrompt:
    launch: flag:--system-prompt
    midSession: not_supported
  appendInstructions:
    launch: flag:--append-system-prompt
    midSession: not_supported
  model:
    launch: flag:--model
    midSession: acp:session/set_config_option(configId=model)
  permissionMode:
    launch: flag:--permission-mode
    midSession: not_supported
  mcpServers:
    launch: config_file:settings.json:.mcpServers
    midSession: config_file:settings.json:.mcpServers  # hot-reload
  skills:
    launch: directory:skills/
    midSession: directory:skills/  # hot-reload
  hooks:
    launch: config_file:settings.json:.hooks
    midSession: not_supported
  additionalDirs:
    launch: flag_repeated:--add-dir
    midSession: not_supported
  workingDirectory:
    launch: cwd
    midSession: not_supported

# What event sources are available for ACP synthesis
eventSources:
  hooks:
    transport: unix_socket
    events:
      - session_start
      - session_end
      - user_prompt_submit
      - pre_tool_use
      - post_tool_use
      - permission_request
  otel:
    transport: http
    env: OTEL_EXPORTER_OTLP_ENDPOINT
    events:
      - token_usage
      - turn_boundary
      - connected_state
  sessionLog:
    transport: file_tail
    path: "{configPath}/logs/{sessionId}.jsonl"
    parser: claude_code_jsonl
    events:
      - full_messages
      - tool_calls_with_params
      - thinking
      - tokens

# Capabilities
capabilities:
  pty: true
  hotReload: true          # settings.json changes detected
  nativeAcp: false         # synthesized, not native
  resume: true             # supports --resume flag
```

```yaml
agentType: native
binary: null  # in-process

config:
  systemPrompt:
    launch: direct
    midSession: direct
  model:
    launch: direct
    midSession: acp:session/set_config_option(configId=model)
  mcpServers:
    launch: direct
    midSession: direct
  tools:
    launch: direct
    midSession: direct
  permissionMode:
    launch: direct
    midSession: direct

eventSources: {}  # no synthesis needed — emits ACP directly

capabilities:
  pty: false
  hotReload: true
  nativeAcp: true
  resume: true
```

```yaml
agentType: generic
binary: "{userSpecified}"

config:
  workingDirectory:
    launch: cwd
    midSession: not_supported
  env:
    launch: env_vars
    midSession: not_supported

eventSources:
  ptyHeuristics:
    transport: vt_buffer
    events:
      - activity_idle

capabilities:
  pty: true
  hotReload: false
  nativeAcp: false
  resume: false
```

---

## Session State: Driver as Source of Truth (for What It Controls)

The driver (wrapper) stores the session state it owns: launch config, config
changes, and the ACP event log. This is the authoritative record for resume,
replay, and audit of the driver-controlled aspects of the session.

The agent may also have its own internal state (context management decisions,
prompt cache, internal heuristics) that the driver doesn't see. For resume,
the driver reconstructs what it can — which is the vast majority of meaningful
session state — and the agent rebuilds its internals from there.

```
SessionState = {
  // What the driver injected at launch
  driverConfig: {
    agentType, binary, cwd,
    systemPrompt, appendInstructions, model,
    permissionMode, mcpServers, skills,
    allowedTools, hooks, additionalDirs, env
  },

  // Mid-session config mutations
  configChanges: [
    { timestamp, field, oldValue, newValue, applied: boolean }
  ],

  // Complete ACP event log (NDJSON)
  // Every JSON-RPC message: requests, responses, notifications
  acpEventLog: "path/to/events.ndjson",

  // VT buffer state (PTY agents only)
  vtState: {
    cols, rows,
    screenContent,    // current screen buffer
    scrollbackLines,  // scrollback history
    cursorPosition
  },

  // Wrapper metadata
  metadata: {
    wrapperId, agentType,
    createdAt, lastActivityAt,
    turnsCompleted, totalTokens, totalCostUsd
  }
}
```

### Resume from Stored State

```
Controller calls agent/resume
    │
    ├─ Wrapper reads stored SessionState
    ├─ Emits lifecycle/prepareForResume hook
    ├─ Writes config files (from driverConfig + configChanges)
    ├─ Spawns agent binary
    │
    ├─ Agent supports session/load?
    │   ├─ YES → ACP session/load with stored session ID
    │   │         Agent restores internal state
    │   │         ✅ Full resume
    │   │
    │   └─ NO → ACP session/new
    │            Feed stored conversation as initial context
    │            ⚠️ Approximate resume (agent starts fresh but has history)
    │
    ├─ Emits lifecycle/resumed hook
    └─ Ready for session/prompt
```

---

## What ACP Covers vs What's Outside the Protocol

| Concern | How it's handled |
|---------|-----------------|
| Structured events (messages, tools, tokens) | ACP `session/update` |
| Prompt/cancel | ACP `session/prompt` / `session/cancel` |
| Mode/config changes | ACP `session/set_mode` / `session/set_config_option` |
| Permission requests | ACP `requestPermission` |
| FS/terminal operations (agent→client) | ACP reverse calls |
| Configuration injection | ACP `sessionMetadata` in `session/new` |
| System prompt, tools, MCP, skills | ACP `sessionMetadata` in `session/new` |
| Mid-session config | ACP `session/set_config_option` |
| Session resume | ACP `session/new` with `priorContext` in metadata |
| Sub-agent requests | ACP reverse calls (`requestSubAgent`) |
| Terminal rendering | PTY sideband (binary stream, separate from ACP) |
| Interactive input (passthrough) | PTY sideband |
| Multi-client attach | PTY sideband |
| Agent lifecycle (launch/stop) | Supervisor (out of band) |
| Environment hooks (snapshot, provision) | Client-side implementation |

---

## Virtual Terminal Backend

For PTY-wrapped agents, the wrapper needs an embedded VT emulator for buffer
management, scroll capture, and ANSI state tracking. Three options evaluated:

### VT-B: midterm (recommended)

Pure Go VT emulator. h2 uses this today.

- Embeddable as Go library, full control over parsing and buffer management
- No external dependency, no CGO
- Already integrated with h2's session model
- Scroll capture and ANSI scanning built-in
- Risk: edge cases with complex escape sequences (sixel, kitty graphics)

### VT-A: tmux

External process, not embeddable. Session model fights ours. Not recommended.

### VT-C: libghostty

Best-in-class correctness but requires CGO (breaks pure-Go cross-compilation).
Rendering-oriented, not headless-buffer-oriented. Could be a future upgrade if
midterm's escape sequence handling proves insufficient.

**Recommendation:** Start with midterm. It's proven in h2, pure Go, and
embeddable. If correctness gaps emerge with specific agents, evaluate libghostty
as a targeted replacement for the VT parsing layer.

---

## Sub-Agents and Multi-Agent Visibility

### Current State: ACP Has No Sub-Agent Support

ACP v0.3 has zero sub-agent awareness. When Claude Code spawns a sub-agent
via its Agent tool, the ACP client sees a regular `tool_call` with
`kind: "think"`. There is:

- No indication a sub-agent was spawned
- No sub-agent session ID
- No visibility into sub-agent progress or events
- No session hierarchy (parent/child relationships)
- No sub-agent completion or result events

This is listed as future work in ACP's roadmap: "Multi-agent orchestration —
Agent A prompts Agent B through acpx. Session bridging."

### What Agents Do Internally

Agents already have rich sub-agent support internally — it's just not exposed
via ACP:

**Codex** has internal events:
- `CollabAgentSpawnBegin` / `CollabAgentSpawnEnd`
- `CollabAgentInteractionBegin` / `CollabAgentInteractionEnd`
- `CollabWaitingBegin` / `CollabWaitingEnd`
- Session source tracking: `SubAgent(SubAgentSource::ThreadSpawn { nickname, role })`

**Zed** tracks session hierarchy:
- `parent_id` in thread storage
- `parent_session_id` in thread metadata
- `subagent_context` in thread payload

**Claude Code** spawns sub-agents via the Agent tool, but the ACP adapter maps
this to a generic tool call with no special handling.

### Why This Matters for Us

In h2's model, multi-agent coordination is first-class — the orchestrator
launches agents, routes messages between them, monitors their work. If the
driver can't see that a sub-agent was spawned, it can't:

- **Monitor progress** — sub-agent could be running for 10 minutes inside
  what looks like a single tool call
- **Apply policies** — sub-agent inherits parent's permissions/tools, but the
  driver can't enforce different policies
- **Capture state** — sub-agent session isn't in the driver's ACP event log
- **Route messages** — can't send h2 messages to a sub-agent the driver
  doesn't know about
- **Track costs** — sub-agent token usage may or may not roll up into parent's
  `usage_update`

### Sub-Agent Model: Agent Requests, Driver Launches

Both the controller and agents need to be able to initiate sub-agents:

- **Controller-initiated:** The controller calls `agent/launch` directly to
  spin up agents as part of its orchestration logic (e.g., h2 launching a pod
  of agents with assigned roles).
- **Agent-initiated:** An agent decides it needs to delegate work and requests
  a sub-agent from the driver. The driver does the actual `agent/launch`,
  applies its policies, and gives the agent a handle to interact with the
  result.

The key insight: **agents don't spawn sub-agents directly — they request them
from the driver.** This follows the same pattern as ACP's `createTerminal`:
the agent asks the client to spawn a process, the client does it and returns
a handle. Here the agent asks the driver to spawn a sub-agent, the driver
does it, and returns a handle.

```
Agent needs to delegate work
    │
    │  ACP reverse call: requestSubAgent(prompt, config)
    │
    ▼
Driver receives request
    │
    ├── Applies policies (allowed? what permissions? what model?)
    ├── Calls agent/launch for the sub-agent (full protocol stack)
    ├── Monitors sub-agent via ACP events
    ├── Returns handle to parent agent
    │
    ▼
Parent agent can:
    ├── Poll sub-agent status
    ├── Read sub-agent result when complete
    └── Cancel sub-agent

Driver maintains:
    ├── Full visibility into sub-agent (same ACP events as any agent)
    ├── Full session state capture
    ├── Lifecycle hooks (environment manager snapshots, etc.)
    └── Cost/token tracking
```

Every sub-agent is a full driver-managed agent with its own ACP stream,
Driver Extension lifecycle, and optional PTY sideband. The driver doesn't
distinguish between controller-initiated and agent-initiated agents in terms
of monitoring — they're all equally visible and controllable.

### Sub-Agent Protocol Methods

#### Agent → Driver (ACP Reverse Calls, like `createTerminal`)

**`requestSubAgent`** — Agent requests a sub-agent

```
Request:
  sessionId             (string)        — parent session
  prompt                (ContentBlock[]) — task for the sub-agent
  config                (object, optional):
    agentType           (string, optional) — default: same as parent
    model               (string, optional) — default: same as parent
    systemPrompt        (string, optional) — additional instructions
    cwd                 (string, optional) — default: same as parent
    tools               (string[], optional) — restrict available tools
  metadata              (object, optional):
    role                (string, optional) — "reviewer", "researcher", etc.
    toolUseId           (string, optional) — links to parent's tool_call

Response:
  subAgentId            (string)        — handle for the sub-agent
  wrapperId             (string)        — driver's wrapper ID (for status)
```

The driver can modify or override any config before launching — e.g., enforce
a cheaper model for sub-agents, restrict tool access, inject additional
instructions. The agent doesn't control the sub-agent's full configuration,
just requests one with hints.

**`subAgentStatus`** — Agent checks sub-agent progress

```
Request:
  subAgentId            (string)

Response:
  state                 (string)        — "running", "completed", "error", "cancelled"
  lastUpdate            (SessionNotification, optional) — most recent ACP event
  turnsCompleted        (number)
  usage:
    inputTokens         (number)
    outputTokens        (number)
```

**`subAgentResult`** — Agent reads sub-agent result

```
Request:
  subAgentId            (string)

Response:
  result                (ContentBlock[]) — sub-agent's final output
  stopReason            (StopReason)
  usage:
    inputTokens         (number)
    outputTokens        (number)
  conversationSummary   (string, optional) — condensed conversation log
```

**`cancelSubAgent`** — Agent cancels sub-agent

```
Request:
  subAgentId            (string)

Response: {}
```

#### Driver → Controller (Lifecycle Hooks)

When a sub-agent is launched (whether controller- or agent-initiated), the
driver emits the same lifecycle hooks as any agent. Additionally:

**`lifecycle/subAgentRequested`** — an agent requested a sub-agent

```
Params:
  parentWrapperId       (string)
  parentSessionId       (string)
  subAgentWrapperId     (string)
  prompt                (ContentBlock[])
  config                (object)
  metadata              (object, optional)
```

This lets the controller observe agent-initiated sub-agent creation. The
controller could intercept and modify these requests (e.g., limit sub-agent
depth, override model choices, deny certain delegations).

### How This Coexists With Agents' Built-In Sub-Agents

Agents like Claude Code have their own built-in Agent tool that spawns
sub-agents internally. Both paths coexist:

**Driver-managed path** (`requestSubAgent`): When an agent calls
`requestSubAgent`, the driver launches a full driver-managed sub-agent
with complete visibility, session state capture, lifecycle hooks, etc.
This is the preferred path for substantial work delegation.

**Agent-internal path** (built-in Agent tool): Agents may also spawn
sub-agents internally using their own mechanisms. This is fine — not every
sub-agent needs full driver management. Lightweight delegation (quick
research, small subtasks) can stay internal. The driver sees these as
long-running tool calls. Session log parsing may extract some sub-agent
activity as best-effort visibility.

**Both paths are valid.** The driver can influence which path agents use
(e.g., expose `requestSubAgent` as an MCP tool, or configure allowed tools),
but should not assume it controls all sub-agent spawning. Some agents will
always do some internal delegation, and that's part of their harness logic
that the driver doesn't own.

### Sub-Agent Session Hierarchy

The driver tracks parent-child relationships:

```
SessionHierarchy:
  parentWrapperId       (string, optional)  — null for top-level agents
  childWrapperIds       (string[])
  depth                 (number)            — 0 for top-level

Controller can query the full tree:
  agent/status includes:
    parentWrapperId     (string, optional)
    childWrapperIds     (string[])
    subAgentDepth       (number)
```

This enables:
- Tree visualization of agent work
- Cost rollup (parent + all sub-agents)
- Cascade operations (pause parent → pause all children)
- Depth limits (max sub-agent nesting to prevent runaway delegation)

---

## Open Questions

### OQ1: PTY Sideband Transport

The binary framing works on any bidirectional byte stream. Options:
- **Unix socket** — one per wrapper, natural for local
- **WebSocket** — works for remote/browser clients
- **Additional fd pair** — zero overhead for subprocess, but doesn't work remote
- **Multiplexed on ACP stdio** — possible with framing but fights NDJSON assumption

### OQ2: ACP Stability

ACP is v0.3 alpha. The spec may change. Mitigation: our wrappers emit ACP, so
if the spec changes we update the wrappers. Consumers' ACP client code would
also need updating. Since we're not forking ACP (just using `sessionMetadata`
and `set_config_option` richly), spec changes should be manageable.

### OQ3: NativeDriver as Subprocess vs In-Process

Should NativeDriver run as a separate binary (true subprocess, ACP on real stdio)
or stay in-process (emits ACP events directly via Go channels)?

- **Subprocess:** More uniform (same as all other wrappers), truly dogfoods the
  protocol, process isolation. But adds serialization overhead and process
  management for something we fully control.
- **In-process:** No serialization overhead, simpler. But different code path
  than wrapped agents — could mask protocol bugs.

Leaning: In-process, but with a test mode that runs it as a subprocess to
validate protocol conformance.

### OQ4: Session Log Parsing Fragility

For Claude Code and Codex, session logs are the richest event source for ACP
synthesis. But they're not a stable API — format changes across versions. Worth
building rich parsers, or focus on hooks + OTEL as primary sources?

Leaning: Build the parsers. Session logs give dramatically better fidelity.
Version the parsers and test against known log formats. Accept maintenance cost.

### OQ5: Supervisor API

The supervisor is out of band but still needs an API. How minimal can it be?
Maybe just:
- "Start a wrapper of type X" → returns ACP connection info + PTY sideband info
- "List running wrappers"
- "Stop wrapper"

This could be a simple REST API, a CLI, or even just "run this binary." It's
deliberately not part of the ACP protocol, but it still needs to be specified
for the framework to be usable.

### OQ6: Per-Session vs Per-Wrapper Config

ACP supports concurrent sessions, and `sessionMetadata` is per-session. But
some config (MCP servers, hooks, skills) is injected at the wrapper/process
level (e.g., written to settings.json before the binary starts). Can different
sessions within the same wrapper have different MCP servers?

For NativeDriver: yes, easily — each session is independent in-process state.
For PTY wrappers: probably not — config files are shared by the process. This
might limit when multi-session within one wrapper is practical.
