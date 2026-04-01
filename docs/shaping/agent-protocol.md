---
shaping: true
---

# Agent Protocol & Runtime Wrapping — Shaping

## Source

> I want to rewrite the agent runner part of flex agent runtime, I don't feel like
> it's quite right. I want it to look more like an ACP-style wrapper. BUT key
> difference, I want it to be able to run raw agent TUI apps not have to write a new
> harness using the anthropic agent sdk for instance, I want it to properly wrap a
> true claude code instance (or codex, or other TUI agents).
>
> I want it to be something more like ACP, a clearly documented, more pluggable
> protocol. Maybe we can even use ACP itself? but we might need some extensions such
> as ability for 2-way streaming of the PTY interface and stuff.
>
> Should we use tmux, the h2 midterm solution, or libghostty as the TTY virtual
> terminal for running these in?
>
> To get the PTY extension layer, we have to rewrite all the ACP clients ourselves
> right? To wrap the underlying TUI apps.
>
> The whole point here is unifying the interface. So we need to wrap the runtime of
> the PTY agent, our wrapper collects the hook, OTEL, and log data and translates it
> to an ACP client API. That's the whole point of this exercise.

---

## Problem

The flex-agent-runtime's agent runner has two drivers today:

1. **NativeDriver** — a built-in LLM-to-tools loop we control entirely
2. **TermmuxDriverAdapter** — wraps 3rd-party CLI agents (Claude Code, Codex) via PTY

The protocol between the orchestrator and agents is an internal `AgentService` RPC
interface that evolved organically. It works, but it's not a standard, not
documented as a spec, and the two driver types surface very different levels of
information — NativeDriver gives full structured tool calls and messages, while
TermmuxDriverAdapter gives terminal bytes and inferred state.

Meanwhile, ACP (Agent Communication Protocol) has emerged as a structured JSON-RPC
protocol for agent communication. Existing ACP adapters (`claude-agent-acp`,
`codex-acp`) wrap agent SDKs, not the real TUI binaries. They give you structured
data but lose the terminal UI entirely.

**Core insight #1:** The runtime itself should BE the ACP adapter for TUI agents.
It wraps the real binary in a PTY, collects structured data from hooks/OTEL/logs,
and presents a unified ACP-compatible interface to all consumers — regardless of
whether the underlying agent speaks ACP natively or is a raw TUI app. The PTY
stream is available as a sideband for clients that want terminal rendering.

**Core insight #2:** ACP is designed for text editors — the client **observes** an
autonomous agent. We need the inverse: the client (runtime) is the **driver**. It
owns the system prompt, tools, MCP servers, skills, CLAUDE.md instructions, and
permission policies. The agent is the execution engine, not the authority. ACP
doesn't have a concept of the client configuring the agent — we need side-channels
for this, just like we need a side-channel for PTY streaming.

## Outcome

A runtime that presents a single, ACP-based interface to consumers where:

- The runtime is the **driver** of agent sessions — it owns configuration (system
  prompt, tools, skills, MCP servers, permissions) and injects it into agents
- Agents that speak ACP natively are passed through with minimal glue
- TUI agents are wrapped in a PTY, with the runtime collecting hooks/OTEL/logs and
  translating them into ACP events — the runtime is the adapter
- PTY terminal I/O is available as a sideband stream for rendering/interaction
- Configuration injection, PTY streaming, and other capabilities beyond ACP's
  scope are handled via well-defined side-channels
- One protocol, one interface, regardless of what's underneath

---

## Requirements (R)

| ID   | Requirement | Status |
|------|-------------|--------|
| R0   | Unified ACP interface: all consumers see ACP events (messages, tool calls, session lifecycle) regardless of whether the underlying agent is ACP-native or PTY-wrapped | Core goal |
| R1   | Wrap real TUI binaries: run actual `claude`, `codex`, etc. in a PTY — not SDK wrappers that behave differently | Core goal |
| R2   | PTY sideband: bidirectional terminal I/O stream available alongside structured ACP data, for rendering agent TUI and interactive input | Core goal |
| R3   | Driver-side configuration: the runtime (ACP "client") owns and injects system prompts, tools, MCP servers, skills, CLAUDE.md instructions, and permission policies into agents — both at launch and mid-session | Core goal |
| R4   | Pluggable: adding a new agent type = config (binary, args, env) + declaring which event sources are available (hooks, OTEL, logs, ACP) — not writing a custom Go driver | Must-have |
| R5   | Multi-client attach: multiple clients can observe the PTY sideband and/or consume ACP events concurrently | Must-have |
| R6   | Best-effort structured data for PTY agents: the runtime extracts what it can from hooks/OTEL/logs and emits it as ACP events. Consumers accept that PTY agents have lower-fidelity data than ACP-native agents | Must-have |
| R7   | Session lifecycle: create, pause, resume, destroy — works for both ACP-native and PTY-wrapped agents. Runtime is source of truth for session state, not the agent | Must-have |
| R8   | Works in sandbox: protocol works when agent runs in gVisor container or remote VM | Must-have |
| R9   | Protocol is documented as a spec that external consumers can implement against | Nice-to-have |

---

## Shape A: Pure ACP (Third-Party Adapters)

Use ACP as-is. For TUI agents, rely on third-party ACP adapters
(`claude-agent-acp`, `codex-acp`) that wrap agent SDKs.

| Part | Mechanism | Flag |
|------|-----------|:----:|
| **A1** | Agent protocol is ACP v0.3 JSON-RPC over NDJSON stdio | |
| **A2** | NativeDriver rewritten as an ACP agent binary | |
| **A3** | Claude Code via `claude-agent-acp` (wraps Agent SDK, not real CLI) | |
| **A4** | Codex via `codex-acp` (wraps Codex internals, not real CLI) | |
| **A5** | No PTY streaming — clients get structured `session/update` only | |
| **A6** | For new agents without adapters: write a new third-party-style adapter per agent | ⚠️ |

### Weaknesses
- **Not real binaries** — SDK wrappers behave differently than the actual CLI (different
  permissions, tools, hooks, no TUI)
- **No PTY** — terminal UI is lost entirely, no interactive mode
- **Adapter tax stays** — still need per-agent adapter work, just done by third parties
  who may lag behind or abandon the project

---

## Shape B: Own Protocol (h2-Style, Cleaned Up)

Keep the current PTY wrapping approach. Clean up and document the internal
`AgentService` RPC as the protocol. No ACP alignment.

| Part | Mechanism | Flag |
|------|-----------|:----:|
| **B1** | Protocol is our ConnectRPC `AgentService` (cleaned up, documented) | |
| **B2** | NativeDriver: built-in Go agent loop, full structured events | |
| **B3** | TUI agents: PTY + termmux, state from hooks/OTEL/logs, own event types | |
| **B4** | PTY streaming via bidirectional `StreamTerminal` RPC | |
| **B5** | Multi-client attach via termmux multiplexing | |
| **B6** | New agents: harness config + optional monitor adapter | ⚠️ |

### Weaknesses
- **Two event vocabularies** — NativeDriver emits rich structured data, PTY driver
  emits inferred state. Consumers must handle both.
- **Not a standard** — our own RPC, nobody else implements it
- **Per-harness work** — still need custom Go code per agent for monitoring

---

## Shape C: ACP + PTY Sideband (Three Modes)

Use ACP as structured protocol. Add PTY as a sideband capability. Three modes
based on agent capabilities: ACP-only, PTY-only, ACP+PTY.

| Part | Mechanism | Flag |
|------|-----------|:----:|
| **C1** | ACP v0.3 for structured events | |
| **C2** | PTY sideband negotiated via `pty` capability at `initialize` | |
| **C3** | ACP-native agents: structured data, no PTY | |
| **C4** | PTY-only agents: runtime wraps binary, infers events, emits as ACP | |
| **C5** | Hybrid agents: ACP on stdio + PTY on sideband simultaneously | |
| **C6** | Multi-client attach on PTY sideband | |
| **C7** | Observability hierarchy: prefer ACP events, fall back to inference | |

### Key Problem (surfaced in discussion)

**For modes (b) PTY-only and (c) ACP+PTY, we have to write our own adapters
anyway.** The existing third-party adapters (`claude-agent-acp`, `codex-acp`) wrap
SDKs, not real binaries, and have no PTY support. To get a real Claude Code binary
running in a PTY AND emitting ACP events, someone has to:

1. Spawn the binary in a PTY
2. Collect hooks/OTEL/logs
3. Translate them into ACP events on stdio
4. Stream the PTY on a sideband

That's the same per-agent work as Shape B's harness system, just repackaged as ACP
adapters. The "three modes" framing obscures that modes (b) and (c) require us to
do all the hard work ourselves.

### Weaknesses
- **We write all the hard adapters anyway** — the third-party ACP adapters don't help
  for real binary wrapping
- **Three modes = three code paths** — complexity without proportional benefit
- **Sideband coordination** — syncing ACP events with PTY state is a new problem
  that doesn't exist in the simpler shapes

---

## Shape D: ACP Internal, Own External API

Use ACP between runtime and ACP-native agents. PTY agents use current wrapping.
Expose our own higher-level API to clients.

| Part | Mechanism | Flag |
|------|-----------|:----:|
| **D1** | Runtime → ACP agent: speaks ACP over stdio | |
| **D2** | Runtime → PTY agent: wraps in PTY, infers events | |
| **D3** | Runtime → Clients: our own `AgentService` API (superset of ACP + PTY) | |
| **D4** | Translation layer: ACP events and inferred events both map to our API | |

### Weaknesses
- **Two protocols** — ACP internal + own API external
- **Translation layer** — mapping between event models
- **Still two code paths** — ACP agents and PTY agents handled differently internally

---

## Shape E: Runtime as Universal ACP Driver

**The runtime IS the ACP adapter for every agent, and it's the driver — not the
observer.** The runtime owns the session configuration (system prompt, tools, MCP
servers, skills, instructions, permissions) and injects it into agents. ACP flows
structured events outward to consumers. Side-channels handle everything ACP
doesn't cover: PTY streaming, configuration injection, and mid-session control.

```
Consumer (h2, web UI, API client, other orchestrators)
    │
    │  ACP protocol (structured events)
    │  + Side-channels:
    │      • PTY sideband (terminal I/O)
    │      • Driver control (config, tools, resume)
    │
    ▼
┌──────────────────────────────────────────────────────┐
│              flex-agent-runtime (THE DRIVER)          │
│                                                      │
│  Owns: system prompt, tools, MCP servers, skills,    │
│        CLAUDE.md, permissions, session state          │
│                                                      │
│  ┌─ Agent Launch ─────────────────────────────────┐  │
│  │ 1. Write config files (settings.json, skills/) │  │
│  │ 2. Set env vars (OTEL endpoint, hook socket)   │  │
│  │ 3. Build CLI args (--system-prompt, --model)   │  │
│  │ 4. Spawn agent in PTY                          │  │
│  └────────────────────────────────────────────────┘  │
│                                                      │
│  ┌─ ACP-native agent ─┐  ┌─ PTY-wrapped agent ────┐ │
│  │                     │  │                        │ │
│  │ stdio ◄──►          │  │ PTY ──► VT buffer      │ │
│  │ ACP JSON-RPC        │  │ hooks ─┐               │ │
│  │                     │  │ OTEL ──┼► Event         │ │
│  │ + driver sideband   │  │ logs ──┘  Synthesis     │ │
│  │   for mid-session   │  │          ──► ACP msgs   │ │
│  │   config changes    │  │                        │ │
│  └─────────────────────┘  │ + driver sideband      │ │
│                           │   for mid-session       │ │
│                           │   config changes        │ │
│                           └────────────────────────┘ │
│                                                      │
│  Outward: ACP events + PTY stream + driver control   │
│  Session state: runtime is source of truth           │
└──────────────────────────────────────────────────────┘
```

| Part | Mechanism | Flag |
|------|-----------|:----:|
| **E1** | **Unified outward interface is ACP** — all consumers see the same ACP protocol (session lifecycle, messages, tool calls, events) regardless of agent type | |
| **E2** | **Runtime is the driver** — it owns session configuration and is the source of truth for session state. The agent is the execution engine. This inverts ACP's model where the agent is autonomous | |
| **E3** | **Configuration injection at launch** — runtime writes config files (settings.json, skills/, CLAUDE.md), sets env vars, and builds CLI args before spawning the agent. Covers: system prompt, model, tools, MCP servers, skills, permissions, hooks, working directory | |
| **E4** | **Driver side-channel** — for mid-session configuration changes that can't wait for a restart. Examples: injecting a new MCP server, updating allowed tools, changing system prompt, adding a skill. Mechanism is agent-type-specific (see side-channel section below) | |
| **E5** | **PTY sideband** — bidirectional terminal I/O on a separate channel. Available for any PTY-backed agent. Used for rendering agent TUI and interactive input (passthrough mode) | |
| **E6** | **ACP-native passthrough** — agents that speak ACP natively are connected via stdio. Runtime adds driver side-channel + PTY sideband as needed | |
| **E7** | **PTY wrapping with event synthesis** — for TUI agents, the runtime spawns the binary in a PTY and synthesizes ACP events from available signals | |
| **E7.1** | Event sources, in priority order: (1) hooks — highest fidelity, gives tool lifecycle and permission events; (2) OTEL — medium fidelity, gives token/cost metrics and turn boundaries; (3) session logs — agent-specific JSONL logs parsed for structured data; (4) PTY output heuristics — lowest fidelity, pattern matching on terminal output | |
| **E7.2** | Per-agent config declares which event sources are available and how to access them (hook socket path, OTEL endpoint, log file path, output patterns). Adding a new agent = writing this config + optionally a log parser | |
| **E7.3** | Synthesized events are best-effort and tagged with fidelity level | |
| **E8** | **Virtual terminal backend** — PTY output processed through an embedded VT emulator for buffer management, scroll capture, and ANSI state tracking | |
| **E9** | **Multi-client multiplexing** — multiple clients can subscribe to ACP events and/or the PTY sideband concurrently. One client at a time can hold passthrough (direct PTY input) | |
| **E10** | **Session lifecycle with runtime as source of truth** — runtime stores: launch config + ACP event stream + driver side-channel state. Resume = reconstruct context and relaunch, using `session/load` if agent supports it, or feeding stored conversation as initial context if not | |

### How It Differs From Shape C

Shape C described three "modes" as if they were equal peers. Shape E recognizes
the reality: **the runtime does the wrapping work for PTY agents — that's its
job.** The modes aren't a protocol feature to negotiate; they're an implementation
detail of how the runtime handles different agent backends. Externally, it's always
ACP (+ side-channels).

More importantly, Shape C still used ACP's client-as-observer model. Shape E
**inverts the authority**: the runtime is the driver. It doesn't just watch the
agent — it configures, controls, and owns the session.

### What We Own vs What's External

| Component | Who builds it |
|-----------|--------------|
| ACP protocol spec | External (ACP community) |
| Side-channel specs (PTY, driver control) | Us (proposed back to ACP community where applicable) |
| Runtime / driver (ACP outward + side-channels) | Us |
| Configuration injection (launch + mid-session) | Us |
| Event synthesis from hooks/OTEL/logs | Us (per-agent work) |
| Virtual terminal backend | Us (or embedded library) |
| ACP-native agent binaries | Agent vendors or us for NativeDriver |
| Per-agent event source configs | Us (declarative, not custom code) |

### Key Design Question: Event Synthesis Fidelity

The hard part of this shape is E4 — synthesizing ACP events from non-ACP sources.
This is what h2 does today with its three-source event handler. The question is:
how good is "good enough"?

**What hooks give us (Claude Code):**
- `UserPromptSubmit` — user sent a message (turn boundary)
- `PreToolUse` / `PostToolUse` — tool lifecycle with tool name
- `PermissionRequest` — agent waiting for permission
- `SessionStart` / `SessionEnd` — session lifecycle

**What hooks DON'T give us:**
- Tool call parameters (what command was run, what file was read)
- Tool call results (output of bash, contents of file)
- Full message content (what the agent said)
- Token usage per turn
- Thinking/reasoning content

**What OTEL gives us (Claude Code):**
- Token counts (input, output, cached)
- Cost per turn
- Turn boundaries
- Connected/disconnected state

**What session logs give us (Claude Code):**
- Full message content (user + assistant)
- Full tool call parameters and results
- Token usage
- Thinking content (if enabled)
- This is the richest source — but it's a Claude Code-specific JSONL format

**Bottom line:** For Claude Code, session logs give us almost everything we need
to synthesize full ACP events. For Codex, similar logs exist. For truly generic
agents, we fall back to hooks (if supported) or pure PTY heuristics (minimal
fidelity). This gradient is acceptable — the protocol is uniform, the fidelity
varies by agent.

---

## Fit Check: R × Shapes

| Req | Requirement | Status | A | B | C | E |
|-----|-------------|--------|---|---|---|---|
| R0 | Unified ACP interface regardless of agent type | Core goal | ❌ | ❌ | ✅ | ✅ |
| R1 | Wrap real TUI binaries, not SDK wrappers | Core goal | ❌ | ✅ | ✅ | ✅ |
| R2 | PTY sideband for terminal I/O | Core goal | ❌ | ✅ | ✅ | ✅ |
| R3 | Driver-side config injection (system prompt, tools, MCP, skills) | Core goal | ❌ | ✅ | ❌ | ✅ |
| R4 | Pluggable: config + capabilities, not custom driver | Must-have | ✅ | ❌ | ❌ | ✅ |
| R5 | Multi-client attach | Must-have | ❌ | ✅ | ✅ | ✅ |
| R6 | Best-effort structured data for PTY agents | Must-have | ❌ | ❌ | ✅ | ✅ |
| R7 | Session lifecycle, runtime is source of truth | Must-have | ❌ | ✅ | ⚠️ | ✅ |
| R8 | Works in sandbox (gVisor/remote VM) | Must-have | ✅ | ✅ | ✅ | ✅ |
| R9 | Documented spec for external consumers | Nice-to-have | ✅ | ❌ | ✅ | ✅ |

**Notes:**
- A fails R0 (SDK adapters give different behavior than real binary), R1 (wraps
  SDK not CLI), R2/R5 (no PTY), R3 (ACP has no config injection — agent is
  autonomous), R7 (agent owns session state, not client)
- B fails R0 (two different event vocabularies), R4 (requires Go code per agent),
  R6 (inferred events aren't in a structured protocol)
- C fails R3 (still ACP's model where agent owns config), R4 (same per-agent
  wrapping work as B)
- E passes all: runtime is the driver and adapter. Owns config (R3), controls
  session state (R7), config-driven event sources (R4), ACP outward (R0, R9),
  real binaries in PTY (R1, R2), synthesized events (R6)

**Shape D dropped** — it's a less coherent version of E (two protocols instead of one).

---

## Layered Architecture: Hooks All the Way Down

The system has three layers, each communicating via the same pattern: the lower
layer emits structured events, and the upper layer acts on them.

```
┌─────────────────────────────────────────────────────────────┐
│  Environment Manager                                        │
│  (sandbox, container, VM, local dev)                        │
│                                                             │
│  Subscribes to driver lifecycle hooks:                      │
│    prepareForLaunch → provision resources                   │
│    agentLaunched    → start monitoring                      │
│    prepareForPause  → snapshot filesystem                   │
│    prepareForResume → restore snapshot, start container     │
│    agentExited      → cleanup resources                     │
│                                                             │
│  Different implementations for different backends:          │
│  ZFS+gVisor, E2B, Daytona, Fly, local (no-op)             │
└──────────────────────────┬──────────────────────────────────┘
                           │ driver lifecycle hooks
                           │
┌──────────────────────────▼──────────────────────────────────┐
│  Driver (this protocol layer)                               │
│                                                             │
│  Owns: agent config, session state, ACP interface           │
│  Emits: lifecycle hooks upward to environment manager       │
│  Consumes: ACP events from agent (+ synthesizes for PTY)    │
│  Exposes: ACP + PTY sideband to consumers                   │
│                                                             │
│  Agent lifecycle: launch, configure, interact, pause,       │
│                   resume, destroy                           │
│  NOT in scope: provisioning, snapshots, resource sizing     │
│  (those are environment manager's job, triggered by hooks)  │
└──────────────────────────┬──────────────────────────────────┘
                           │ ACP (stdio) + PTY sideband
                           │
┌──────────────────────────▼──────────────────────────────────┐
│  Agent (claude, codex, generic binary)                      │
│                                                             │
│  ACP-native: structured events on stdio                     │
│  PTY-wrapped: hooks/OTEL/logs → driver synthesizes ACP      │
│                                                             │
│  Runs in: the environment that was provisioned above        │
└─────────────────────────────────────────────────────────────┘
```

### Why Hooks at Both Layers

Agents are "hook-driven" in the sense that they report their actions and the
driver observes them (via ACP events, or via hooks/OTEL/logs synthesized into
ACP). The driver is also "hook-driven" in the sense that it emits lifecycle
events and the environment manager observes them.

Same pattern, different concerns:

| Layer | Emitter | Events | Consumer | What consumer does |
|-------|---------|--------|----------|-------------------|
| Agent → Driver | Agent | ACP: tool_call, message, usage, permission | Driver | Records session, exposes to consumers, manages session state |
| Driver → Environment | Driver | Lifecycle: prepareForPause, agentExited, etc. | Environment Manager | Snapshots filesystem, provisions/destroys resources |

This is clean because neither layer knows about the other's implementation:
- The driver doesn't know if the environment is ZFS+gVisor or E2B or local
- The environment manager doesn't know if the agent is Claude Code or Codex
- Each layer has a single interface to implement

### Driver Lifecycle Hooks

These are the events the driver emits for the environment manager:

| Hook | When | Environment manager might... |
|------|------|----------------------------|
| `prepareForLaunch` | Driver is about to spawn agent. Environment must be ready. | Provision container, mount filesystem, set up networking |
| `agentLaunched` | Agent process is running, ACP connected | Start resource monitoring, begin cost tracking |
| `turnCompleted` | Agent finished a turn (idle between prompts) | Take incremental snapshot (optional, for per-turn snapshots) |
| `prepareForPause` | Agent session saved, process about to be killed | Take full filesystem snapshot, prepare for hibernation |
| `paused` | Agent process is dead, session state saved by driver | Suspend container, detach volumes (optional, for cost savings) |
| `prepareForResume` | Driver wants to relaunch agent. Environment must be ready. | Restore snapshot, start container, remount filesystem |
| `resumed` | Agent relaunched, ACP reconnected | Resume monitoring |
| `prepareForShutdown` | Session ending, driver will destroy agent | Take final snapshot (if archiving), prepare for cleanup |
| `agentExited` | Agent process exited (normal or crash) | Clean up resources, destroy container (or keep for debugging) |
| `configChanged` | Driver updated agent config mid-session | (Usually no-op, but could trigger resource resizing) |

The environment manager responds to hooks but doesn't control the driver's
decisions. The driver decides *when* to pause/resume/destroy — the environment
manager handles the infrastructure consequences.

### Pause/Resume Sequence (Cross-Layer)

```
Consumer requests pause
    │
    ▼
Driver: save session state (ACP event log + driver config)
Driver: stop agent process (graceful shutdown)
Driver: emit prepareForPause hook ──► Environment: snapshot filesystem
                                      Environment: ack
Driver: emit paused hook           ──► Environment: suspend container
                                      Environment: detach volume (optional)
    │
    │  ... time passes ...
    │
Consumer requests resume
    │
    ▼
Driver: emit prepareForResume hook ──► Environment: restore snapshot
                                       Environment: start container
                                       Environment: ack
Driver: write agent config files (Phase 1 — may have changed)
Driver: spawn agent binary
Driver: resume ACP session (session/load or context reconstruction)
Driver: emit resumed hook          ──► Environment: resume monitoring
```

The driver orchestrates the sequence. The environment manager reacts. Neither
needs to understand the other's internals.

## Side-Channels

ACP covers structured agent events. Two side-channels extend it for capabilities
ACP doesn't address:

### Side-Channel 1: PTY Sideband

**Purpose:** Stream the agent's terminal UI to clients and accept interactive
input. This is what makes TUI agent wrapping work.

**What flows:**
- **Output:** Raw terminal bytes (ANSI escape sequences, screen content, cursor
  movement) from agent's PTY → driver VT buffer → clients
- **Input:** Keystrokes, mouse events, resize signals from client → agent's PTY
  (passthrough mode)

**Not in scope for ACP.** ACP's `terminal/*` methods are for the agent to spawn
child processes on the client's side — completely different from streaming the
agent's own TUI output.

**Transport options:** See OQ2 in Open Questions.

### Side-Channel 2: Driver Control

**Purpose:** The driver manages the full agent lifecycle and injects
configuration. This is the key inversion from ACP's model where the agent is
autonomous.

**What it covers:**

**Agent lifecycle (not in ACP at all):**
- **Launch** — spawn agent binary with config (args, env, files). ACP only starts
  after the process exists.
- **Pause** — save session state, stop agent, emit hooks for environment manager
- **Resume** — restore config, relaunch agent, reconnect ACP session
- **Destroy** — tear down agent process, emit hooks for cleanup

**Configuration injection at launch (before ACP `initialize`):**

| What | How (Claude Code) | How (Codex) | How (Generic) |
|------|-------------------|-------------|---------------|
| System prompt | `--system-prompt` flag | `--system-prompt` flag | `--system-prompt` or env var |
| CLAUDE.md / instructions | `--append-system-prompt` flag, or write to CLAUDE.md in cwd | N/A | N/A |
| Model | `--model` flag | `--model` flag | Flag or env var |
| MCP servers | Write to `settings.json` before launch | Write to config file | N/A |
| Skills | Write to skills directory before launch | N/A | N/A |
| Custom tools | Write to `settings.json` (allowedTools) | Config file | N/A |
| Permissions | `--permission-mode` flag, or config | `--approval-mode` flag | N/A |
| Hooks | Write to `settings.json` before launch | N/A | N/A |
| Working directory | `cwd` argument | `cwd` argument | `cwd` argument |
| Additional directories | `--add-dir` flags | N/A | N/A |
| Environment | Env vars (API keys, OTEL endpoint, etc.) | Env vars | Env vars |

**Mid-session configuration (while agent is running):**

| Change | Claude Code mechanism | Difficulty |
|--------|---------------------|:----------:|
| Update system prompt | Not supported mid-session | ❌ |
| Add/remove MCP server | Update settings.json → agent hot-reloads (if supported) | ⚠️ |
| Add/remove skill | Write to skills dir → agent hot-reloads (if supported) | ⚠️ |
| Change model | `session/set_config_option` via ACP (if in ACP mode) | ✅ |
| Change mode | `session/set_mode` via ACP | ✅ |
| Update allowed tools | Update settings.json → agent hot-reloads | ⚠️ |
| Inject context | Send as a message via `session/prompt` or PTY input | ✅ |
| Change permissions | Not supported mid-session | ❌ |

**Key insight:** Most configuration changes that ACP supports (`set_mode`,
`set_config_option`) are lightweight. The heavy ones (system prompt, tools,
MCP servers, skills) must be done at launch or via agent-specific file
manipulation. Mid-session changes to these are best handled by:

1. **Hot-reload** — update config files and signal the agent (agent-specific)
2. **Session restart** — store state, kill agent, relaunch with new config, resume
3. **Message injection** — send configuration as a user message ("from now on,
   also use this tool: ...")

### Event Ingestion (Internal to Driver, NOT a Side-Channel)

Event ingestion is how the driver collects signals from PTY-wrapped agents to
synthesize ACP events. It is **not** a side-channel — it's entirely internal to
the driver. Consumers never see it; they just see ACP events coming out.

**Sources (in fidelity order):**

| Source | Transport | What it provides | Agent support |
|--------|-----------|-----------------|---------------|
| **Hooks** | Agent calls hook binary → Unix socket to driver | Tool lifecycle (pre/post), permission requests, session start/end, user prompt submit | Claude Code: full. Codex: partial. Generic: none |
| **OTEL** | HTTP POST to driver's OTEL collector | Token counts, costs, turn boundaries, connected state | Claude Code: full. Codex: none. Generic: none |
| **Session logs** | Driver tails agent's JSONL log file | Full messages, tool calls with params & results, thinking, tokens | Claude Code: full (richest source). Codex: similar. Generic: none |
| **PTY heuristics** | Driver's VT buffer pattern matching | Activity/idle state, rough turn boundaries | Any agent with terminal output |

**For ACP-native agents**, event ingestion is unnecessary — structured events
flow through ACP's stdio directly.

**For PTY-wrapped agents**, all sources may be active simultaneously. The driver
merges them, preferring higher-fidelity sources when they conflict:

```
Hooks says "tool started: Bash" (no params)
Session log says "tool: Bash, command: 'go test ./...', output: 'PASS'"
→ Driver emits ACP tool_call with full params from session log,
  using hook timing for the event timestamp
```

### What This Means for the Protocol Spec

The protocol we document is:
1. **ACP v0.3** (as-is) for structured agent events — this is the core
2. **PTY sideband extension** — spec for bidirectional terminal I/O alongside ACP
3. **Driver control** — agent lifecycle (launch/pause/resume/destroy) +
   configuration injection. Includes the agent-type manifest format.
4. **Driver lifecycle hooks** — events the driver emits for the environment
   manager to act on (prepareForPause, agentExited, etc.)
5. **Agent-type manifest** — declarative config declaring what each agent type
   supports: configuration injection methods, event sources, capabilities

Items 3-5 are **not ACP protocol messages** — they're the driver layer's own
interface. ACP handles agent ↔ driver communication. The driver control and
lifecycle hooks handle everything above and below that.

---

## ACP as Source of Truth: Data Fidelity, Resume, and Customization

A critical question for any shape that uses ACP: can the ACP client become the
**sole source of truth** for the entire agent session? This matters because if the
runtime stores the full session, it controls resume, replay, audit, and
orchestration without depending on agent-side persistence.

### What ACP Captures

#### Conversation & Messages

| Data | Captured in ACP? | Notes |
|------|:-:|-------|
| User prompt text | ✅ | Full content blocks in `session/prompt` |
| Agent response text | ✅ | Streamed via `agent_message_chunk` updates |
| Thinking/reasoning | ✅ | Via `agent_thought_chunk` updates |
| Tool call name + full input params | ✅ | Via `tool_call` update — includes `toolName`, `toolUseId`, and full `input` object |
| Tool call results/output | ✅ | Via `tool_call_update` with status + content |
| Token usage per turn | ✅ | Via `usage_update` — input, output, cache creation, cache read tokens |
| Stop reason | ✅ | In `session/prompt` response |
| Permission requests | ✅ | Full `requestPermission` flow with tool kind and options |

This is good — for the conversational content, ACP is quite complete. You get
full tool call parameters (not just names), full results, thinking, and token
counts.

#### What ACP Does NOT Capture

| Data | Captured? | Impact |
|------|:-:|--------|
| System prompt | ❌ | **Critical gap.** The client never sees what instructions the agent is operating under. Cannot reconstruct agent behavior from ACP alone. |
| Model parameters (temperature, stop sequences, thinking budget) | ❌ | Cannot reproduce exact inference conditions |
| Full context window at any point | ❌ | You see messages individually but not the exact assembled context sent to the LLM (which includes trimming, caching, system prompt) |
| Initial agent configuration (env vars, flags, plugins loaded) | ❌ | Agent startup state is opaque |
| MCP server configurations | ❌ | Client can pass `mcpServers` at connect time but this isn't part of ACP spec |
| Available tools list (declarative) | ❌ | Agent may advertise `available_commands_update` but this is display-level, not a full tool schema |
| File contents read/written by agent | Partial | Only if agent uses ACP's `readTextFile`/`writeTextFile` (client-side FS). If agent reads files internally (most do), client never sees it |
| Terminal command output | Partial | Only via ACP `terminalOutput` polling — not streaming, may be truncated at 64KB |

#### The System Prompt Gap

This is the most important missing piece. The system prompt defines the agent's
personality, capabilities, constraints, and instructions. Without it:
- You cannot replay a session and get the same behavior
- You cannot audit what the agent was told to do
- You cannot resume a session on a different agent instance

In h2, the system prompt is set by the runtime (via `--system-prompt` and
`--append-system-prompt` flags). The runtime knows it because it controls launch.
In ACP, the agent owns its system prompt and never shares it.

**For Shape E**, the runtime controls agent launch, so it knows the system prompt
regardless of whether ACP transmits it. This is an advantage of the "runtime as
adapter" approach — the runtime has context that pure ACP clients lack.

### Session Persistence & Resume

#### How acpx Does It Today

acpx stores sessions in two tiers:

1. **Raw ACP event stream** — NDJSON files containing every JSON-RPC message
   (requests, responses, notifications). This is the high-fidelity record.
   Stored in `~/.acpx/events/<sessionId>/` with segment rotation.

2. **Session record** — Summary JSON with trimmed conversation (200 messages max,
   text capped at 8K chars, thinking at 4K), cumulative token usage, metadata.
   Stored in `~/.acpx/sessions/<sessionId>.json`.

#### Resume Flow

```
Client wants to resume session "abc-123"
    │
    ├─ Read stored SessionRecord from disk
    ├─ Spawn agent process
    ├─ Send initialize
    │
    ├─ Does agent support loadSession capability?
    │   ├─ YES → send session/load with stored agentSessionId
    │   │         Agent restores its own internal state
    │   │         ✅ Resumed (agent has full context)
    │   │
    │   └─ NO → Create new session
    │            Client has conversation history but agent starts fresh
    │            ❌ Agent has no memory of prior turns
    │
    └─ Agent process died?
        ├─ Spawn new agent
        └─ Same flow as above — depends on agent's own persistence
```

#### Key Limitation: Agent Owns Its State

**ACP's resume model depends on the agent persisting its own state.** The client
stores the conversation record, but resuming requires the agent to have its own
session storage that `session/load` can restore from. If the agent doesn't
persist (or its storage is lost), the client's record is just a log — it can't
be replayed into a fresh agent to reconstruct state.

This means:
- **ACP client is NOT the sole source of truth** — it's a secondary record
- **The agent's internal storage is authoritative** for resume
- **If agent storage is lost, resume fails** even if client has full event log

#### What We Need for Runtime-as-Source-of-Truth

For the runtime to be the authoritative session store (enabling resume across
agent restarts, migrations between hosts, etc.), we need to either:

1. **Extend ACP with a replay mechanism** — send the stored conversation back to
   a fresh agent as context (like how Claude Code's `--resume` replays from its
   session log)
2. **Store enough state to reconstruct the agent's context window** — system
   prompt + full message history + tool results, then feed it back on resume
3. **Accept that resume requires agent cooperation** — the runtime stores what it
   can, but true resume depends on the agent's own `session/load` support

Option (2) is most aligned with Shape E: the runtime controls launch (knows
system prompt), captures all ACP events (knows full conversation), and can
reconstruct the context window. On resume, it could either use `session/load`
if available, or synthesize the conversation as initial context for a new session.

### Customization: Tools, System Prompts, Skills, Plugins

#### ACP's Model vs Our Model

ACP is designed for **text editors observing an autonomous agent**. The agent
decides its own system prompt, tools, and capabilities. The client can suggest
modes and config options, but these are hints the agent can ignore.

**Our model is inverted.** The runtime is the **driver**. It decides what system
prompt the agent uses, what tools are available, what MCP servers are connected,
what skills are loaded, what permissions are granted. The agent is the execution
engine.

#### What ACP Provides for Customization

| Customization | ACP Support | Sufficient for driver model? |
|---------------|:-:|:-:|
| Set system prompt | ❌ | ❌ — must use driver control side-channel |
| CLAUDE.md / global instructions | ❌ | ❌ — must inject via config files at launch |
| Define custom tools | ❌ | ❌ — must inject via MCP/config at launch |
| Inject MCP servers | ❌ (out of spec) | ❌ — must inject via config files at launch |
| Define skills | ❌ | ❌ — must inject via skills directory at launch |
| Set permissions | ❌ | ❌ — must inject via flags/config at launch |
| Set agent mode | ✅ `session/set_mode` | ✅ — works mid-session |
| Set model | ✅ `session/set_config_option` | ✅ — works mid-session |
| Set config options | ✅ `session/set_config_option` | ⚠️ — agent-defined keys only |
| Restrict available tools | ❌ (out of spec) | ❌ — must inject at launch |
| Set working directory | ✅ `cwd` in `session/new` | ✅ |

**8 out of 11 customization needs are not covered by ACP.** All 8 require the
driver control side-channel (see Side-Channel Architecture above).

#### How Shape E Handles This (Full Picture)

The driver control side-channel operates in three phases:

**Phase 1: Pre-launch configuration (runtime writes, agent reads at startup)**
```
Runtime prepares agent workspace:
  1. Write settings.json     → MCP servers, hooks, allowed tools, permissions
  2. Write CLAUDE.md         → global instructions, role-specific context
  3. Write skills/ directory → skill definitions
  4. Set env vars            → API keys, OTEL endpoint, hook socket path
  5. Build CLI args          → --system-prompt, --model, --permission-mode,
                               --add-dir, --append-system-prompt

Runtime spawns agent:
  claude --system-prompt "..." --model opus --permission-mode approve-reads ...
```

**Phase 2: ACP-mediated control (standard ACP methods, mid-session)**
```
Runtime can change via ACP:
  session/set_mode           → switch to plan mode, accept-edits, etc.
  session/set_config_option  → change model, reasoning effort
  session/prompt             → inject context as messages
```

**Phase 3: Hot-reload control (runtime writes files, agent detects changes)**
```
Runtime updates config mid-session:
  1. Write updated settings.json  → add/remove MCP server, update tools
  2. Write updated skills/        → add/remove skills
  3. Signal agent to reload       → agent-specific (USR1, file watch, etc.)

Caveats:
  - Not all agents support hot-reload (❌ for most today)
  - System prompt cannot be changed mid-session for most agents
  - Permissions cannot be changed mid-session for most agents
  - If hot-reload fails → fall back to session restart with resume
```

**Phase 4: Session restart with resume (nuclear option for config changes)**
```
Runtime needs to change something that can't be hot-reloaded:
  1. Store current session state (ACP event log + driver config)
  2. Kill agent process
  3. Write new configuration (Phase 1)
  4. Relaunch agent with new config
  5. Resume session:
     a. session/load if agent supports it
     b. Or reconstruct context from stored events + new system prompt
```

#### Per-Agent Configuration Manifest

Each agent type declares what configuration it accepts and how, via a
declarative manifest. This is part of what makes the runtime pluggable (R4):

```yaml
# Example: Claude Code agent manifest
agent_type: claude_code
binary: claude
config:
  system_prompt:
    launch: --system-prompt
    mid_session: not_supported
  append_instructions:
    launch: --append-system-prompt
    mid_session: not_supported
  model:
    launch: --model
    mid_session: acp:session/set_config_option(configId=model)
  permission_mode:
    launch: --permission-mode
    mid_session: not_supported
  mcp_servers:
    launch: config_file:settings.json:.mcpServers
    mid_session: config_file:settings.json:.mcpServers  # hot-reload
  skills:
    launch: directory:skills/
    mid_session: directory:skills/  # hot-reload
  hooks:
    launch: config_file:settings.json:.hooks
    mid_session: not_supported
  working_directory:
    launch: cwd
    mid_session: not_supported
  additional_dirs:
    launch: --add-dir (repeated)
    mid_session: not_supported
  global_instructions:
    launch: file:CLAUDE.md
    mid_session: not_supported
event_sources:
  hooks:
    transport: unix_socket
    events: [session_start, session_end, user_prompt_submit,
             pre_tool_use, post_tool_use, permission_request]
  otel:
    transport: http
    endpoint: env:OTEL_EXPORTER_OTLP_ENDPOINT
    events: [token_usage, turn_boundary, connected_state]
  session_log:
    transport: file_tail
    path: "{harness_config_path}/logs/{session_id}.jsonl"
    parser: claude_code_jsonl
    events: [full_messages, tool_calls_with_params, thinking, tokens]
```

A Codex manifest would look similar but with different flag names, different
config file paths, and different event source support. A generic agent might
only have `binary`, `cwd`, and PTY heuristics.

### Summary: ACP Fitness for Source-of-Truth Role

| Requirement | ACP Fitness | Shape E Mitigation |
|-------------|:-:|-----|
| Full conversation capture | ✅ | ACP events provide full messages, tool calls, results, thinking |
| System prompt capture | ❌ | Runtime knows it (controls launch) — stored in driver config alongside ACP events |
| Tool/MCP/skill configuration | ❌ | Runtime owns and injects via driver control side-channel. Stored in driver config |
| Session resume | ⚠️ | Depends on agent's `session/load`. Runtime can reconstruct context window as fallback from stored ACP events + driver config |
| Full inference params | ❌ | Runtime knows model + config at launch. Mid-session changes via `session/set_config_option`. All stored in driver config |
| Agent state reconstruction | ⚠️ | ACP event stream + driver config = ~95% of state. Remaining 5% is agent-internal caches (prompt cache, context trimming decisions) |

**Bottom line:** ACP alone is not sufficient as the sole source of truth. But
**ACP events + driver config** (system prompt, model, tools, MCP, skills,
permissions, env) gives us everything we need. The runtime stores both:

```
Session record = {
  driver_config: {            ← what the runtime injected
    system_prompt, model, mcp_servers, skills,
    permissions, hooks, env_vars, cli_args, ...
  },
  acp_event_stream: [...],   ← what happened during the session
  driver_config_changes: [    ← mid-session config mutations
    { timestamp, change: "added mcp server X" },
    { timestamp, change: "model changed to sonnet" },
  ],
  pty_metadata: {             ← optional, for PTY-backed agents
    vt_state_snapshot, scrollback_hash, ...
  }
}
```

This makes the runtime the authoritative source of truth. Resume works by
replaying driver config + ACP events, regardless of whether the agent has its
own persistence.

---

## Virtual Terminal Backend

Orthogonal to the protocol choice, we need a virtual terminal for PTY agent
rendering. Three options:

### VT-A: tmux

The standard terminal multiplexer. Well-known, battle-tested.

- **Pros:** Extremely stable, universally available, rich feature set, scriptable
- **Cons:** External process dependency, IPC overhead for programmatic control,
  not embeddable as a library, session management is tmux's model not ours,
  escape sequence handling is tmux's interpretation (may filter/modify agent output)

### VT-B: midterm (h2's current solution)

Pure Go virtual terminal emulator (`mterm`/`midterm`). h2 uses this today for
its VT buffer, ANSI parsing, and scroll capture.

- **Pros:** Embeddable as Go library, full control over parsing and buffer
  management, already integrated with h2's session model, no external dependency,
  scroll capture and ANSI scanning built-in
- **Cons:** Less mature than tmux/ghostty, may have edge cases with complex escape
  sequences (sixel, kitty graphics, etc.), maintenance burden is on us, performance
  for very high-throughput terminal output is unproven at scale

### VT-C: libghostty

Ghostty's terminal emulation core as a C library. State-of-the-art terminal
emulation (used by the Ghostty terminal).

- **Pros:** Extremely correct and complete escape sequence handling (passes all
  vttest suites), high performance (SIMD-optimized parsing), handles modern
  terminal features (sixel, kitty graphics protocol, synchronized output),
  embeddable via C FFI
- **Cons:** C dependency (CGO required — conflicts with pure-Go cross-compilation
  preference), large dependency to vendor, Zig build system adds complexity,
  API is oriented toward rendering not headless buffer management, may need
  significant adaptation for headless use (we want buffer state, not GPU rendering)

### VT Comparison

| Requirement | tmux | midterm | libghostty |
|-------------|------|---------|------------|
| Embeddable as Go library | ❌ (external process) | ✅ | ⚠️ (CGO) |
| Pure Go (no CGO) | ✅ (separate process) | ✅ | ❌ |
| Escape sequence correctness | ✅ (very good) | ⚠️ (good, not exhaustive) | ✅ (best-in-class) |
| Modern terminal features | ⚠️ (lags behind) | ❌ (basic) | ✅ (full) |
| Headless buffer management | ⚠️ (capture-pane) | ✅ (native) | ⚠️ (rendering-oriented) |
| Already integrated | ❌ | ✅ (in h2) | ❌ |
| Maintenance burden | ❌ (external) | ⚠️ (on us) | ❌ (external) |
| Cross-compilation | ✅ | ✅ | ❌ |

---

## Appendix: ACP v0.3 Protocol Reference

Complete reference for ACP v0.3 as implemented in the reference clients/agents.
Transport is JSON-RPC 2.0 over NDJSON (newline-delimited JSON) on stdio.

### Message Format

```
// Request (client → agent or agent → client)
{"jsonrpc":"2.0","id":"req-1","method":"session/prompt","params":{...}}

// Response
{"jsonrpc":"2.0","id":"req-1","result":{...}}

// Error response
{"jsonrpc":"2.0","id":"req-1","error":{"code":-32002,"message":"Session not found","data":{}}}

// Notification (no id, no response expected)
{"jsonrpc":"2.0","method":"session/update","params":{...}}
```

### 1. Handshake

#### `initialize` — Client → Agent (Request/Response)

Capability negotiation. Must be the first message.

```
Request params:
  protocolVersion  (number)  — protocol version
  clientCapabilities:
    fs:
      readTextFile   (boolean)  — client can serve file reads
      writeTextFile  (boolean)  — client can serve file writes
    terminal         (boolean)  — client can create/manage terminals
  clientInfo:
    name     (string)  — e.g., "acpx", "flex-runtime"
    version  (string)

Response result:
  protocolVersion      (number)
  agentCapabilities:
    loadSession        (boolean, optional)  — supports session/load
  authMethods          (AuthMethod[], optional):
    - id  (string)     — auth method identifier
  serverInfo:
    name     (string)  — e.g., "claude-agent-acp"
    version  (string)
```

### 2. Authentication

#### `authenticate` — Client → Agent (Request/Response)

Called if `initialize` response includes `authMethods`.

```
Request params:
  methodId    (string)  — matches an id from authMethods
  credential  (string, optional)  — credential value

Response result: {} (empty on success)
```

### 3. Session Management

#### `session/new` — Client → Agent (Request/Response)

Create a new conversation session.

```
Request params:
  cwd              (string, optional)  — working directory
  sessionMetadata  (object, optional)  — custom metadata

Response result:
  sessionId          (string)  — unique session ID
  sessionCapabilities (object, optional)
  agentSessionId     (string, optional)  — agent's internal ID
```

#### `session/load` — Client → Agent (Request/Response)

Resume a previously persisted session. Requires `loadSession` capability.

```
Request params:
  sessionId  (string)  — session to resume
  cwd        (string, optional)

Response result:
  agentSessionId  (string, optional)
  models          (SessionModelState, optional)
```

#### `session/list` — Client → Agent (Request/Response)

List available sessions.

```
Request params: (none required)

Response result:
  sessions  (SessionInfo[]):
    - sessionId   (string)
    - name        (string, optional)
    - createdAt   (string, optional)
    - lastUsedAt  (string, optional)
```

#### `session/close` — Client → Agent (Request/Response) [UNSTABLE]

Close and clean up a session.

```
Request params:
  sessionId  (string)

Response result: {}
```

#### `session/fork` — Client → Agent (Request/Response) [UNSTABLE]

Branch a session into a new independent conversation.

```
Request params:
  sessionId  (string)  — parent session
  cwd        (string, optional)

Response result:
  sessionId       (string)  — new forked session ID
  agentSessionId  (string, optional)
```

#### `session/resume` — Client → Agent (Request/Response) [UNSTABLE]

Resume a paused session (distinct from `session/load`).

```
Request params:
  sessionId  (string)

Response result:
  agentSessionId  (string, optional)
```

### 4. Prompt & Conversation

#### `session/prompt` — Client → Agent (Request/Response + Streaming)

Send a prompt and receive streaming updates followed by a final response.

```
Request params:
  sessionId  (string)
  prompt     (ContentBlock[]):
    - { type: "text", text: string }
    - { type: "image", mimeType: string, data: string }  // base64
    - { type: "resource_link", uri: string, title?: string, name?: string }
    - { type: "resource", resource: { uri: string, text?: string } }

Response result:
  stopReason  (StopReason):
    "end_turn" | "completed" | "done" | "max_tokens" |
    "stop_sequence" | "tool_use"
  models      (SessionModelState, optional)
```

During execution, the agent emits `session/update` notifications (see section 5).

#### `session/cancel` — Client → Agent (Request/Response or Notification)

Cooperatively cancel an in-flight prompt.

```
Request params:
  sessionId  (string)

Response result: {}
```

### 5. Session Updates (Streaming Notifications)

#### `session/update` — Agent → Client (Notification)

Emitted during `session/prompt` execution. Carries a `sessionUpdate` discriminator.

```
Notification params:
  sessionId  (string)
  update     (SessionNotification)  — one of the types below
```

**Update types:**

```
agent_message_chunk:
  sessionUpdate  "agent_message_chunk"
  content        (ContentBlock)  — text, tool_use, etc.

agent_thought_chunk:
  sessionUpdate  "agent_thought_chunk"
  content:
    type  "thinking"
    text  (string)

tool_call:
  sessionUpdate  "tool_call"
  toolName       (string)
  toolUseId      (string)
  input          (object)  — tool parameters

tool_call_update:
  sessionUpdate  "tool_call_update"
  toolUseId      (string)
  status         "in_progress" | "completed" | "error"
  content        (ContentBlock, optional)

usage_update:
  sessionUpdate  "usage_update"
  inputTokens                (number)
  outputTokens               (number)
  cacheCreationInputTokens   (number, optional)
  cacheReadInputTokens       (number, optional)

current_mode_update:
  sessionUpdate  "current_mode_update"
  currentModeId  (string)

config_option_update:
  sessionUpdate    "config_option_update"
  configOption     (SessionConfigOption):
    id             (string)
    name           (string)
    description    (string, optional)
    currentValue   (string)
    options        ({ value, name, description }[], optional)

available_commands_update:
  sessionUpdate      "available_commands_update"
  availableCommands  (AvailableCommand[]):
    - id           (string)
    - title        (string)
    - description  (string, optional)

plan:
  sessionUpdate  "plan"
  plan           (object)  — plan structure with steps/actions
```

### 6. Session Control

#### `session/set_mode` — Client → Agent (Request/Response)

Change the agent's operating mode.

```
Request params:
  sessionId  (string)
  modeId     (string)  — e.g., "default", "acceptEdits", "plan",
                          "bypassPermissions", "dontAsk"

Response result: {}
```

#### `session/set_config_option` — Client → Agent (Request/Response)

Set a session configuration option (e.g., model, reasoning effort).

```
Request params:
  sessionId  (string)
  configId   (string)  — e.g., "model", "reasoning_effort"
  value      (string)

Response result:
  configOptions  (SessionConfigOption[])  — updated options list
```

#### `session/set_model` — Client → Agent (Request/Response) [UNSTABLE]

Change the model mid-session.

```
Request params:
  sessionId  (string)
  modelId    (string)

Response result: {}
```

### 7. Permission System (Agent → Client)

#### `requestPermission` — Agent → Client (Request/Response)

Agent requests permission to execute a tool. Client evaluates against its
permission policy and responds.

```
Request params:
  sessionId  (string)
  toolCall:
    title  (string)  — human-readable tool description
    kind   (ToolKind, optional):
      "read" | "edit" | "delete" | "move" | "execute" |
      "search" | "fetch" | "think" | "other"
  options  (PermissionOption[]):
    - optionId     (string)
    - kind         (PermissionOptionKind):
        "allow_once" | "allow_always" | "reject_once" | "reject_always"
    - description  (string, optional)

Response result:
  outcome:
    outcome   "selected" | "cancelled"
    optionId  (string, conditional)  — present if "selected"
```

**Client-side permission modes:**
- `approve-all` — auto-select first `allow_*` option
- `approve-reads` — auto-approve `kind: "read"`, prompt for others
- `deny-all` — reject all by default

### 8. Filesystem Operations (Agent → Client)

#### `readTextFile` — Agent → Client (Request/Response)

Agent requests file contents from the client's filesystem.

```
Request params:
  sessionId  (string)
  path       (string)

Response result:
  contents  (string)
  encoding  (string, optional)
```

#### `writeTextFile` — Agent → Client (Request/Response)

Agent requests to write a file on the client's filesystem.

```
Request params:
  sessionId  (string)
  path       (string)
  contents   (string)
  encoding   (string, optional)

Response result: {}
```

### 9. Terminal Operations (Agent → Client)

These allow the agent to spawn and manage processes on the client side.

#### `createTerminal` — Agent → Client (Request/Response)

```
Request params:
  sessionId        (string)
  command          (string)
  args             (string[], optional)
  cwd              (string, optional)
  env              (EnvVar[], optional):
    - name   (string)
    - value  (string)
  outputByteLimit  (number, optional)  — default 64KB

Response result:
  terminalId  (string)
  pid         (number, optional)
```

#### `terminalOutput` — Agent → Client (Request/Response)

Read new output from a terminal since last call.

```
Request params:
  terminalId  (string)

Response result:
  output     (string)
  truncated  (boolean)
```

#### `waitForTerminalExit` — Agent → Client (Request/Response)

Block until terminal process exits.

```
Request params:
  terminalId  (string)

Response result:
  exitCode  (number, optional)
  signal    (string, optional)
```

#### `killTerminal` — Agent → Client (Request/Response)

Send a signal to a terminal process.

```
Request params:
  terminalId  (string)
  signal      (string, optional)  — default "SIGTERM"

Response result: {}
```

#### `releaseTerminal` — Agent → Client (Request/Response)

Release terminal resources (cleanup without killing).

```
Request params:
  terminalId  (string)

Response result: {}
```

### 10. Error Codes

| Code | Name | Meaning |
|------|------|---------|
| -32700 | Parse error | Malformed JSON |
| -32600 | Invalid request | Not valid JSON-RPC |
| -32601 | Method not found | Unknown method |
| -32602 | Invalid params | Bad parameters |
| -32603 | Internal error | Unhandled agent error |
| -32000 | Auth required | Authentication needed |
| -32001 | Resource not found | File/resource missing |
| -32002 | Session not found | Unknown session ID |
| -32070 | Timeout | Operation timed out |
| -32071 | Permission denied | Permission rejected |
| -32072 | Permission unavailable | No permission UI available |

### 11. Content Block Types (Shared)

Used in prompts, updates, and responses:

```
{ type: "text", text: string }
{ type: "image", mimeType: string, data: string }
{ type: "resource_link", uri: string, title?: string, name?: string }
{ type: "resource", resource: { uri: string, text?: string } }
{ type: "tool_use", id: string, name: string, input: object }
{ type: "tool_result", toolUseId: string, content: ContentBlock[], isError: boolean }
```

### 12. What ACP Does NOT Cover

These are explicitly out of ACP's scope — things our PTY sideband extension
and runtime need to handle:

- **Terminal rendering** — ACP's `terminal/*` methods are for the agent to spawn
  processes, not for streaming the agent's own TUI output
- **Bidirectional PTY I/O** — no concept of seeing/interacting with the agent's
  terminal
- **Multi-client attach** — single client assumed
- **Agent state inference** — no idle/active/thinking state machine
- **Inter-agent messaging** — no agent-to-agent communication
- **Orchestration** — no credential injection, task assignment, or coordination
- **Filesystem snapshots** — no snapshot/rollback concepts
- **Cost/token aggregation** — `usage_update` is per-turn, no session totals

---

## Open Questions

### OQ1: Is ACP Stable Enough to Build On?

ACP is at v0.3 (alpha). The spec may change. How much churn risk are we taking?
Mitigation: we could implement our outward interface as "ACP-compatible" with a
thin translation layer, so if ACP changes we update the layer not the whole
runtime. The internal event model can be our own.

### OQ2: PTY Sideband Channel Design

How does the PTY sideband work alongside ACP's stdio NDJSON? Options:
- **Additional fd pair (fd 3/4)** — clean separation, but requires process launch
  control and doesn't work over network without multiplexing
- **Unix domain socket** — works locally and remotely (via forwarding), session-scoped
- **Separate RPC stream** — like current `StreamTerminal` ConnectRPC procedure, works
  over network natively
- **Multiplexed on stdio with framing** — like h2's wire protocol, but fights with
  ACP's assumption of clean NDJSON on stdio

### OQ3: Session Log Parsing — How Fragile Is It?

Session logs (Claude Code's JSONL, Codex's equivalent) are the richest event
source for synthesizing ACP events from PTY agents. But they're not a stable API:
- Format changes across agent versions
- Not documented as a contract
- May be deprecated if agents adopt ACP natively

Is it worth building rich parsers for these, or should we treat them as
"nice to have" and focus on hooks + OTEL as the primary sources?

### OQ4: Virtual Terminal Backend

See VT comparison above. Initial leaning: stick with midterm (pure Go, embeddable,
proven in h2). The main risk is escape sequence edge cases. Could we use midterm
as primary and optionally integrate libghostty later if correctness gaps emerge?

### OQ5: NativeDriver — Rewrite as ACP Binary or Keep Internal?

Shape E's outward interface is ACP. Should our built-in NativeDriver (the Go
LLM-to-tools loop) be rewritten as a standalone ACP agent binary that the runtime
spawns? Or should it remain an internal Go implementation that directly emits
ACP-shaped events without the stdio serialization overhead?

Tradeoff: external binary is more dogfooding/uniform, but adds serialization cost
and process management overhead for something we fully control.
