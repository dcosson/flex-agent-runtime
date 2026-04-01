# Agent Runtime & Lifecycle

How the supervisor spawns and manages agent wrapper processes, how wrappers
expose ACP and PTY interfaces, and how clients connect to them.

---

## Supervisor

The supervisor runs in the target environment (local machine, remote host,
container). It spawns agent wrapper processes on demand and reports
connection info back to the controller. It is NOT part of the ACP protocol
— it's out-of-band process management.

### Supervisor API

Minimal REST API (or CLI for local dev):

#### `POST /agents` — Launch Agent

Tells the supervisor to spawn a wrapper process for a given agent type.

```
Request:
  agentType             (string)  — "claude_code", "codex", "native", "generic"
  binary                (string, optional) — path to binary (for generic)
  cwd                   (string)  — working directory
  sessionMetadata       (object, optional) — passed through to session/new

Response:
  agentId               (string)  — unique identifier for this agent instance
  acpTransport:
    type                "stdio" | "tcp" | "unix"
    address             (string, optional) — for tcp/unix
    pid                 (number, optional) — for stdio (subprocess)
  ptyEndpoint:
    url                 (string)  — HTTP endpoint for PTY streaming
                                    e.g., "http://localhost:PORT/agents/{agentId}/pty"
  status                "starting" | "ready"
```

#### `GET /agents` — List Running Agents

```
Response:
  agents:
    - agentId           (string)
      agentType         (string)
      status            "starting" | "ready" | "running" | "stopped" | "error"
      createdAt         (string)
      lastActivityAt    (string, optional)
      sessions          (number) — active session count
```

#### `GET /agents/{agentId}` — Agent Status

```
Response:
  agentId               (string)
  agentType             (string)
  status                (string)
  acpTransport          (object)  — same as launch response
  ptyEndpoint           (object)  — same as launch response
  metrics:
    turnsCompleted      (number)
    totalInputTokens    (number)
    totalOutputTokens   (number)
```

#### `DELETE /agents/{agentId}` — Stop Agent

```
Request:
  force                 (boolean, optional) — SIGKILL vs graceful

Response: {}
```

### Supervisor Deployment Modes

| Mode | How controller talks to supervisor |
|------|-----------------------------------|
| **Local dev** | Controller spawns wrapper directly as subprocess. No separate supervisor process needed. |
| **Remote/sandbox** | Supervisor is a service on the host; controller talks via REST API |
| **In-process** | NativeDriver runs in the controller's process directly — no supervisor needed |

---

## Agent Wrapper Lifecycle

### Startup Sequence

```
1. Supervisor receives launch request
2. Supervisor resolves agent-type manifest
3. Supervisor writes config files (settings.json, skills/, etc.)
   based on sessionMetadata
4. Supervisor spawns wrapper binary
5. Wrapper starts:
   a. Opens ACP channel (stdio or TCP listener)
   b. Starts HTTP server for PTY sideband
   c. Spawns the actual agent binary in a PTY (for PTY-wrapped agents)
   d. Reports ready to supervisor
6. Supervisor returns connection info to controller
7. Controller connects ACP channel
8. Controller sends `initialize` → handshake
9. Controller sends `session/new` with sessionMetadata → session created
10. Controller optionally connects to PTY endpoint
```

### Wrapper Process Architecture

Each wrapper process manages:
- **ACP channel** — JSON-RPC 2.0 on stdio (subprocess) or TCP
- **PTY sideband HTTP server** — serves PTY stream to connected clients
- **Agent binary** — the real agent running in a PTY (or in-process for NativeDriver)
- **Event synthesis** — converts hooks/OTEL/logs into ACP `session/update` notifications
- **VT backend** — embedded midterm terminal emulator for buffer management

```
┌─── Wrapper Process ──────────────────────────────────────┐
│                                                          │
│  ACP Channel ◄──────► JSON-RPC handler                   │
│  (stdio/TCP)           │                                 │
│                        ├── session management            │
│                        ├── config translation            │
│                        └── event synthesis               │
│                             ▲         ▲        ▲         │
│                             │         │        │         │
│                          hooks     OTEL    session logs   │
│                             │         │        │         │
│  PTY ◄──────────────► Agent Binary (claude, codex, etc.) │
│   │                                                      │
│   ▼                                                      │
│  VT Backend (midterm)                                    │
│   │                                                      │
│   ▼                                                      │
│  HTTP Server ────────► PTY Sideband (streaming)          │
│  :PORT/agents/{id}/pty                                   │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

---

## PTY Sideband

### Transport: HTTP Streaming

The PTY sideband is served over HTTP by the wrapper process. This works for
both local and remote scenarios, can be multiplexed for multiple agents on
the same host (different ports or paths), and is firewall/proxy-friendly.

**Endpoint:** `http://{host}:{port}/agents/{agentId}/pty`

The connection upgrades to a bidirectional binary stream (WebSocket or
HTTP/2 bidirectional streaming). The stream carries framed data and control
messages.

### Wire Format

```
Data frame:
  [0x00] [4-byte big-endian length] [terminal bytes]

Control frame:
  [0x01] [4-byte big-endian length] [JSON payload]
```

### Data Frames

**Output (wrapper → client):** Raw terminal bytes from the agent's PTY.
Includes ANSI escape sequences, cursor movement, screen updates —
everything needed to render the terminal.

**Input (client → wrapper):** Raw bytes to write to the agent's PTY.
Keystrokes, mouse events, paste content. Only accepted from the client
holding the write lock.

### Control Frames

#### `resize` — Client → Wrapper

```json
{ "type": "resize", "cols": 120, "rows": 40 }
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

---

## PTY Write Lock & ACP Prompt Interlock

### The Problem

An agent process has a single input stream. If both a PTY client (typing
keystrokes) and an ACP `session/prompt` (injecting structured input) send
data simultaneously, the agent receives garbled, interleaved input. This
must be prevented.

### Write Lock Model

At any given time, the agent's input is controlled by exactly one source:

- **ACP mode** (default): The ACP channel drives the agent via
  `session/prompt`. PTY clients can read/observe but not write.
- **PTY passthrough mode**: One PTY client has exclusive write access.
  ACP `session/prompt` calls are queued until passthrough is released.

```
                    ┌──────────────┐
                    │  Agent Input │
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
         ACP prompt   PTY write    PTY write
         (default)    (locked)     (locked)
              │            │            │
           Client A    Client B     Client C
                       (holder)    (blocked)
```

### Lock Lifecycle

The write lock is managed over the PTY sideband connection itself. No
separate sidechannel is needed — the PTY stream already carries control
frames alongside data frames.

#### Acquiring the Lock

A PTY client sends a control frame to request write access:

```json
{ "type": "write_lock_request" }
```

The wrapper responds:

```json
{ "type": "write_lock_granted", "lockId": "uuid" }
```

Or if another client holds it:

```json
{ "type": "write_lock_denied", "reason": "held_by_other_client", "holder": "client-id" }
```

Or if ACP prompt is in-flight:

```json
{ "type": "write_lock_denied", "reason": "acp_prompt_active" }
```

#### Releasing the Lock

The holder sends:

```json
{ "type": "write_lock_release" }
```

The wrapper confirms:

```json
{ "type": "write_lock_released" }
```

The lock is also auto-released if the holding client disconnects.

#### ACP Interlock

When a PTY client holds the write lock:

1. ACP `session/prompt` calls are **queued**, not rejected. The prompt
   will execute once the lock is released.
2. ACP `session/cancel` still works immediately (cancels the queued prompt
   or interrupts the agent).
3. The wrapper emits a `session/update` notification to inform the ACP
   client that the prompt is queued:

```json
{
  "type": "prompt_queued",
  "reason": "pty_write_lock_held",
  "holder": "client-id"
}
```

When the lock is released, queued prompts execute in order.

When an ACP prompt is actively executing (agent is processing):

1. Write lock requests are **denied** with reason `acp_prompt_active`.
2. The PTY client can retry after the prompt completes (watch for the
   `session/prompt` response or `usage_update`).

This prevents the interleaving problem: at any moment, exactly one input
source is active.

### PTY Client Connection Flow

```
1. Client discovers PTY endpoint from supervisor
   (GET /agents/{agentId} → ptyEndpoint.url)

2. Client connects to PTY endpoint
   GET /agents/{agentId}/pty → Upgrade to WebSocket

3. Wrapper sends screen_state control frame + full screen data frame
   (client can immediately render current terminal state)

4. Client receives output data frames (read-only by default)

5. Client wants interactive control:
   a. Sends write_lock_request control frame
   b. If granted: can send input data frames
   c. If denied: remains read-only, can retry later

6. When done with interactive control:
   a. Sends write_lock_release control frame
   b. Reverts to read-only

7. Client disconnects:
   a. If holding write lock: auto-released
   b. Other clients unaffected
```

### Multi-Client Behavior

Multiple clients can connect to the same PTY endpoint simultaneously:

| Capability | All clients | Write lock holder only |
|-----------|:-----------:|:---------------------:|
| Receive output data frames | ✅ | ✅ |
| Receive control frames | ✅ | ✅ |
| Send resize | ✅ | ✅ |
| Send input data frames | ❌ | ✅ |
| Send write_lock_request | ✅ | N/A |

Resize from any client is accepted (last writer wins for terminal
dimensions). This is pragmatic — if multiple clients are viewing, the
resize should reflect the active viewer.

---

## Multi-Agent on One Host

The supervisor can manage multiple agent wrapper processes on the same host.
Each wrapper gets its own:

- ACP channel (separate stdio pipes or TCP ports)
- PTY HTTP endpoint (separate port or path under the supervisor's HTTP server)

### Multiplexing via Supervisor HTTP

The supervisor's HTTP server can serve as the multiplexer:

```
Supervisor HTTP Server
  /agents/{agentId-1}/pty  →  wrapper-1 PTY
  /agents/{agentId-2}/pty  →  wrapper-2 PTY
  /agents/{agentId-3}/pty  →  wrapper-3 PTY
```

The controller discovers endpoints from the supervisor's launch response
or agent status API. No port management needed by the controller.

---

## Lifecycle Hooks (Client-Side)

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

| Hook | When client fires it | Environment manager might... |
|------|---------------------|----------------------------|
| `prepareForLaunch` | Before asking supervisor to spawn | Provision container, mount FS |
| `agentLaunched` | After ACP initialize succeeds | Start monitoring |
| `turnCompleted` | After seeing usage_update / end_turn | Incremental snapshot |
| `prepareForPause` | Client decides to pause session | Full snapshot |
| `prepareForResume` | Client wants to restart | Restore snapshot |
| `agentExited` | Wrapper process died | Cleanup resources |

These are entirely up to the client. A simple local-dev client might not
fire any hooks. An h2 orchestrator with ZFS+gVisor fires all of them.

---

## Session State & Resume

### Client as Source of Truth

The client stores the session state it cares about: ACP event log +
configuration. Resume does NOT depend on agent-side persistence.

```
SessionState = {
  sessionMetadata       — config passed to session/new
  configChanges         — mid-session set_config_option mutations
  acpEventLog           — complete ACP event log (NDJSON)
  vtState (optional)    — VT buffer snapshot for PTY agents
  metadata              — agentId, agentType, timestamps, metrics
}
```

### Resume Flow

```
1. Client has stored: sessionMetadata + ACP event log
2. Client calls supervisor to launch a new wrapper (same agent type)
3. Client sends initialize → ACP handshake
4. Client sends session/new with:
   - Same (or updated) sessionMetadata
   - priorContext field containing conversation history
5. Wrapper reconstructs agent context from priorContext:
   - Claude Code: --resume flag, or replay from session log
   - NativeDriver: preload conversation directly
   - Generic: best-effort (may start fresh)
6. Agent is ready for new prompts with prior context
```

### Sub-Agent Session Hierarchy

The driver tracks parent-child relationships for sub-agents:

```
SessionHierarchy:
  parentAgentId         (string, optional)  — null for top-level
  childAgentIds         (string[])
  depth                 (number)            — 0 for top-level
```

This enables:
- Tree visualization of agent work
- Cost rollup (parent + all sub-agents)
- Cascade operations (stop parent → stop all children)
- Depth limits (prevent runaway delegation)

---

## Virtual Terminal Backend

For PTY-wrapped agents, the wrapper needs an embedded VT emulator for
buffer management, scroll capture, and ANSI state tracking.

**Choice: midterm (pure Go)**

- Embeddable as Go library, full control over parsing and buffer management
- No external dependency, no CGO
- Already integrated with h2's session model
- Scroll capture and ANSI scanning built-in
- Risk: edge cases with complex escape sequences (sixel, kitty graphics)

If correctness gaps emerge with specific agents, evaluate libghostty as a
targeted replacement for the VT parsing layer (requires CGO).
