# Architecture Overview

flex-agent-runtime is an ACP-based agent framework. It wraps any agent —
real TUI binaries, ACP-native agents, or a built-in LLM loop — behind a
uniform ACP interface, with a PTY sideband for terminal I/O.

## System Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│  Controller / Driver                                            │
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
└───────┬──────────────────────────────────┬──────────────────────┘
        │                                  │
   ACP v0.3                           PTY Sideband
   (JSON-RPC / NDJSON stdio)          (HTTP streaming)
        │                                  │
┌───────▼──────────────────────────────────▼──────────────────────┐
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

## Protocols

### ACP v0.3 (Agent Communication Protocol)

Standard ACP as defined by the ACP community. JSON-RPC 2.0 over NDJSON
stdio. This is the structured event stream between client and agent.

**We use ACP as-is with a few extensions** (see 01-ACP-protocol-extensions.md).

| Direction | Category | Methods |
|-----------|----------|---------|
| Client → Agent | Handshake | `initialize`, `authenticate` |
| Client → Agent | Session lifecycle | `session/new`, `session/load`, `session/list`, `session/close` [UNSTABLE], `session/fork` [UNSTABLE], `session/resume` [UNSTABLE] |
| Client → Agent | Interaction | `session/prompt`, `session/cancel` |
| Client → Agent | Configuration | `session/set_mode`, `session/set_config_option` |
| Agent → Client | Streaming | `session/update` (9 update types: message chunks, thought chunks, tool calls, tool updates, usage, mode, config, commands, plan) |
| Agent → Client | Reverse calls | `requestPermission`, `readTextFile`, `writeTextFile`, `createTerminal`, `terminalOutput`, `waitForTerminalExit`, `killTerminal`, `releaseTerminal` |

### PTY Sideband

Binary stream for bidirectional terminal I/O, separate from ACP. Served
over HTTP by the agent wrapper. Supports multiple read-only viewers with
exclusive write lock.

See 02-agent-runtime-and-lifecycle.md for transport details.

### Supervisor API

Out-of-band process management. The supervisor spawns wrapper processes
and reports connection info. Not part of the ACP protocol contract.

See 02-agent-runtime-and-lifecycle.md for the supervisor API.

## Extensions Beyond Standard ACP

We extend ACP in a few targeted ways, designed to be proposed back upstream:

1. **Rich `sessionMetadata`** — structured schema for driver configuration
   injection via `session/new` and `session/set_config_option`
2. **Sub-agent reverse calls** — `requestSubAgent`, `subAgentStatus`,
   `subAgentResult`, `cancelSubAgent` — following ACP's existing reverse-call
   pattern (like `createTerminal`)

See 01-ACP-protocol-extensions.md for full details.

## What the Framework Provides

- **Agent wrappers** — implementations that wrap real agent binaries (Claude
  Code, Codex, etc.) and present ACP + PTY sideband. For PTY-wrapped agents,
  the wrapper synthesizes ACP events from hooks/OTEL/session logs.
- **NativeDriver** — a built-in Go agent loop that emits ACP directly
- **Supervisor** — process that runs in target environments and spawns
  wrappers on demand (out of band, not part of protocol)
- **VT backend** — embedded virtual terminal (midterm, pure Go) for PTY
  buffer management
- **Agent-type manifests** — declarative config for each agent type

## What Consumers Build

- **Their own controller/driver** — ACP client + PTY sideband client +
  whatever orchestration logic they need
- **Lifecycle hooks** (client-side) — the driver observes ACP events and
  fires hooks to an environment manager
- **Environment manager** (optional) — handles lifecycle hooks for
  infrastructure (snapshots, containers, resource provisioning)

## Design Principles

1. **The driver controls cross-cutting capabilities; the agent owns its own
   logic.** The driver injects configuration common across agent types:
   skills, MCP servers, tools, system prompt additions, permissions, model
   selection. The agent retains its own internal state, harness logic, and
   personality.

2. **ACP is the protocol.** One protocol for everything between client and
   agent. No separate "driver extension" protocol.

3. **Hooks all the way down.** Agents emit ACP events. The client observes
   them and fires its own lifecycle hooks to the environment manager. Same
   pattern, different layers.

4. **Same interface regardless of agent type.** Whether the agent is
   NativeDriver, a real Claude Code binary in a PTY, or a future ACP-native
   agent — consumers see ACP + optional PTY sideband.

5. **Client owns session state and resume.** The client stores ACP events +
   config. Resume = create a new session with prior context in
   `sessionMetadata`. The agent doesn't need its own persistence.

## Agent Type Mapping

| Agent Type | ACP Events | PTY Sideband | Config Mechanism | Event Fidelity |
|-----------|------------|-------------|-----------------|----------------|
| NativeDriver | Emits directly | N/A | Direct (in-process) | Full |
| Claude Code wrapper | Synthesized from hooks/OTEL/logs | Real TUI | CLI flags + config files | High |
| Codex wrapper | Synthesized from hooks/logs | Real TUI | Wrapper-specific | Medium |
| Generic wrapper | Synthesized from PTY heuristics | Raw terminal | env vars + cwd | Low |
| ACP-native (future) | Passthrough | If agent has TUI | Agent-native | Full |

## Open Questions

### OQ1: NativeDriver as Subprocess vs In-Process

Should NativeDriver run as a separate binary (true subprocess, ACP on real
stdio) or stay in-process (emits ACP events directly via Go channels)?

Leaning: In-process, but with a test mode that runs it as a subprocess to
validate protocol conformance.

### OQ2: ACP Stability

ACP is v0.3 alpha. The spec may change. Mitigation: our wrappers emit ACP,
so if the spec changes we update the wrappers. Since we're not forking ACP,
spec changes should be manageable.

### OQ3: Per-Session vs Per-Wrapper Config

ACP supports concurrent sessions, and `sessionMetadata` is per-session.
But some config (MCP servers, hooks, skills) is injected at the
wrapper/process level. Can different sessions within the same wrapper have
different MCP servers?

For NativeDriver: yes. For PTY wrappers: probably not — config files are
shared by the process.
