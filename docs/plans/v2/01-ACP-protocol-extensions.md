# ACP Protocol & Extensions

This document covers the full ACP v0.3 protocol as used by flex-agent-runtime,
followed by our extensions that we intend to propose upstream.

---

## Part 1: Standard ACP v0.3

Standard ACP as defined by the ACP community. JSON-RPC 2.0 over NDJSON stdio.

### Transport

JSON-RPC 2.0 messages, one per line (NDJSON), over the wrapper's stdio when
running as a subprocess — or over a streaming RPC connection when running
remotely.

### Methods: Client → Agent

#### `initialize` — Handshake & Capability Negotiation

Connects to a single agent process. The agent type is NOT selected here — it
was already determined by which binary the supervisor spawned.

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
  sessionMetadata       (object, optional)  — see Extensions section

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
  configId              (string)  — "model", "reasoning_effort", "sessionMetadata", etc.
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

## Part 2: Our Extensions

These are extensions beyond standard ACP that we implement in the wrapper
layer. They follow ACP's existing patterns and are designed to be proposed
back upstream.

### Extension 1: Rich `sessionMetadata` Schema

When creating a session, the driver passes semantically rich, agent-agnostic
configuration in the `sessionMetadata` field of `session/new`. The wrapper
translates this to agent-specific mechanisms (CLI flags, config files, env
vars, etc.).

The client does NOT need to know how Claude Code vs Codex vs NativeDriver
consumes each config — it sends the same semantic schema regardless.

#### `sessionMetadata` Schema

All fields are optional. The wrapper ignores fields it doesn't support.

```
sessionMetadata:

  # --- Identity & Instructions ---
  systemPrompt        (string)          Agent's primary system prompt
  appendInstructions  (string)          Additional text appended to the system
                                        prompt (maps to --append-system-prompt
                                        CLI flag where supported)
  globalInstructions  (InstructionFile[]) Instruction files to inject
                                        (e.g., CLAUDE.md, AGENTS.md):
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

#### Mid-Session Configuration via `sessionMetadata`

The client sends the full updated `sessionMetadata` as a single config option
via `session/set_config_option`. One `configId: "sessionMetadata"`, full
replacement. The wrapper diffs against the previous metadata and applies
what changed.

```
session/set_config_option:
  configId: "sessionMetadata"
  value: '{ "model": "sonnet", "mcpServers": [...], "skills": [...], ... }'
```

No per-field config IDs, no patch semantics. The client always sends the
complete desired state. The response tells the client what took effect.

The wrapper compares to the previous metadata, identifies what changed, and:
- Applies changes it can (model → ACP native, MCP servers → config file hot-reload)
- Ignores fields that haven't changed
- Reports what was applied vs rejected in the response

**Mid-session support by field:**

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
context.MCPServers = metadata.MCPServers
context.Skills = metadata.Skills
context.PermissionMode = metadata.PermissionMode
```

### Extension 2: Sub-Agent Reverse Calls

Agents can request sub-agents from the driver using the same pattern as
ACP's `createTerminal`. The driver does the actual launch, applies its
policies, and gives the agent a handle to interact with the result.

Key insight: **agents don't spawn sub-agents directly — they request them
from the driver.** This gives the driver full visibility, policy enforcement,
cost tracking, and lifecycle management over all sub-agents.

#### How Agents Initiate Sub-Agent Requests

The mechanism differs by agent type:

**NativeDriver:** Sub-agent request is a built-in tool in the tool registry.
When the LLM calls it, the NativeDriver makes the ACP reverse call to the
driver directly. Straightforward — we control the loop.

**PTY-wrapped agents (Claude Code, Codex, etc.):** The agent binary doesn't
speak ACP, so the wrapper exposes sub-agent capabilities via an **MCP
server**. The wrapper injects this MCP server into the agent's config
(via `sessionMetadata.mcpServers` → settings.json). The MCP server exposes
tools: `request_sub_agent`, `sub_agent_status`, `sub_agent_result`,
`cancel_sub_agent`. When the agent calls one of these MCP tools, the wrapper
intercepts it and translates to the ACP reverse call upstream to the driver.

```
PTY-wrapped agent flow:

  Agent calls MCP tool "request_sub_agent"
    → Wrapper's MCP server receives the tool call
      → Wrapper sends ACP reverse call (requestSubAgent) to driver
        → Driver launches sub-agent, returns handle
          → Wrapper returns sub-agent ID via MCP tool result
            → Agent polls via "sub_agent_status" / reads via "sub_agent_result"
```

```
NativeDriver flow:

  LLM calls built-in "request_sub_agent" tool
    → NativeDriver sends ACP reverse call (requestSubAgent) to driver
      → Driver launches sub-agent, returns handle
        → NativeDriver returns result to LLM as tool output
```

#### Protocol Methods

The ACP reverse calls below are the wire protocol between wrapper and
driver. For PTY-wrapped agents, the wrapper translates between MCP tool
calls and these reverse calls transparently.

#### `requestSubAgent` — Agent Requests a Sub-Agent

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
  subAgentId            (string)
```

The driver can modify or override any config before launching — e.g.,
enforce a cheaper model for sub-agents, restrict tool access, inject
additional instructions.

#### `subAgentStatus` — Agent Checks Sub-Agent Progress

```
Request:
  subAgentId            (string)

Response:
  state                 "running" | "completed" | "error" | "cancelled"
  turnsCompleted        (number)
  usage:
    inputTokens         (number)
    outputTokens        (number)
```

#### `subAgentResult` — Agent Reads Sub-Agent Result

```
Request:
  subAgentId            (string)

Response:
  result                (ContentBlock[])
  stopReason            (StopReason)
  usage:
    inputTokens         (number)
    outputTokens        (number)
```

#### `cancelSubAgent` — Agent Cancels Sub-Agent

```
Request:
  subAgentId            (string)

Response: {}
```

#### Coexistence with Agent-Internal Sub-Agents

Agents like Claude Code have their own built-in Agent tool that spawns
sub-agents internally. Both paths coexist:

- **Driver-managed** (`requestSubAgent`): Full visibility, session state
  capture, lifecycle hooks, cost tracking. Preferred for substantial work.
- **Agent-internal** (built-in Agent tool): The driver can't control these,
  but can **observe** them via log synthesis (see below).

Both paths are valid. The driver should not assume it controls all sub-agent
spawning.

#### Observing Agent-Internal Sub-Agents

The wrapper already synthesizes ACP events from agent logs for the main
session. It can do the same for internal sub-agents. Claude Code writes
separate JSONL logs per sub-agent; Codex emits `CollabAgent*` events. The
wrapper parses these and emits dedicated `session/update` types.

**New update types for internal sub-agent observability:**

```
internal_sub_agent_started:
  toolUseId             (string)  — the parent Agent tool call
  internalSubAgentId    (string)  — opaque ID from the agent's logs
  model                 (string, optional)
  prompt                (string, optional) — truncated/summarized

internal_sub_agent_update:
  toolUseId             (string)
  internalSubAgentId    (string)
  update                (SessionNotification)  — reuses the same update types
                                                 as top-level (message chunks,
                                                 tool calls, usage, etc.)

internal_sub_agent_completed:
  toolUseId             (string)
  internalSubAgentId    (string)
  stopReason            (StopReason)
  usage:
    inputTokens         (number)
    outputTokens        (number)
```

Properties:

- **Best-effort, not guaranteed.** Depends on what the agent exposes in
  logs. Claude Code's JSONL gives full sub-agent events. Codex's collab
  events give less. Generic agents give nothing. Fidelity is declared in
  the agent-type manifest's `eventSources` section.
- **Scoped under `toolUseId`.** The driver knows which parent tool call
  spawned this sub-agent. It's observability into an existing tool call,
  not a new session.
- **Same update vocabulary.** `internal_sub_agent_update` reuses the same
  `SessionNotification` types (message chunks, tool calls, usage) so the
  driver doesn't need separate rendering logic.
- **Read-only.** The driver can observe but can't control these — no
  cancel, no policy injection. For controllable sub-agents, use the
  driver-managed `requestSubAgent` path instead.

### Extension 3: Agent-Type Manifest

Each agent type declares its capabilities in a declarative manifest. This
makes the framework pluggable — you describe an agent type, you don't write
custom Go code.

```yaml
agentType: claude_code
binary: claude

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
    midSession: config_file:settings.json:.mcpServers
  skills:
    launch: directory:skills/
    midSession: directory:skills/
  hooks:
    launch: config_file:settings.json:.hooks
    midSession: not_supported
  additionalDirs:
    launch: flag_repeated:--add-dir
    midSession: not_supported
  workingDirectory:
    launch: cwd
    midSession: not_supported

eventSources:
  hooks:
    transport: unix_socket
    events: [session_start, session_end, user_prompt_submit,
             pre_tool_use, post_tool_use, permission_request]
  otel:
    transport: http
    env: OTEL_EXPORTER_OTLP_ENDPOINT
    events: [token_usage, turn_boundary, connected_state]
  sessionLog:
    transport: file_tail
    path: "{configPath}/logs/{sessionId}.jsonl"
    parser: claude_code_jsonl
    events: [full_messages, tool_calls_with_params, thinking, tokens]

capabilities:
  pty: true
  hotReload: true
  nativeAcp: false
  resume: true
```

```yaml
agentType: native
binary: null  # in-process

config:
  systemPrompt:   { launch: direct, midSession: direct }
  model:          { launch: direct, midSession: acp:session/set_config_option(configId=model) }
  mcpServers:     { launch: direct, midSession: direct }
  tools:          { launch: direct, midSession: direct }
  permissionMode: { launch: direct, midSession: direct }

eventSources: {}  # emits ACP directly

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
  workingDirectory: { launch: cwd, midSession: not_supported }
  env:              { launch: env_vars, midSession: not_supported }

eventSources:
  ptyHeuristics:
    transport: vt_buffer
    events: [activity_idle]

capabilities:
  pty: true
  hotReload: false
  nativeAcp: false
  resume: false
```

The manifest declares which `sessionMetadata` fields each wrapper supports,
so the client can know what will be applied vs ignored.
