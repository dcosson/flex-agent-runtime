---
shaping: true
---

# Agent Orchestrator — Shaping

## Source

> I want a new demo entry point, which will be Agent Demo, and this should be honestly more than just a demo. It should be the full shebang, but I want to be able to run it in the different sandbox modes.
>
> I'm actually now thinking I want to run an orchestrator server and then be able to spin up agents by calling to the server.
>
> The orchestrator is running, it's listening to event streams from the agents, using those event streams to render its own UI. It's then exposing that UI over a surface somehow, which could be either a terminal UI app or a web app.
>
> I should be able to see all the concurrent agent sessions; also the agent should be able to spawn new sessions.
>
> The orchestrator might as well be persistent but just use SQLite; use the native Go, non-CGO version of SQLite.
>
> The orchestrator is just running on your local machine; it's not running on sandbox host but that's an interesting idea if you already have the host available. Let's allow the orchestrator to run either locally or on the sandbox host.
>
> It's not really three individual modes; there are all different variations because even in local mode you should be able to run orchestrator and the agent separately but on the same machine. In tools in sandbox, you should also be able to run orchestrator, agent, and tools all on different machines.
>
> The agent loop for agent in sandbox should be able to just run on another server or another computer somewhere without actually generating the sandbox architecture. It could run in a sandbox generated on that other computer, or it could run just directly on that other computer.
>
> The orchestrator process that's writing to SQLite and stuff — that's the part that I'm considering to be just like a demo app because really the point of this library is you could build your own applications. But maybe it's gonna be useful enough that it shouldn't just be called a demo.

---

## Problem

The flex-agent-runtime has all the building blocks (agent loop, tools, sandbox environments, RPC layer, event bus) but two critical gaps:

1. **No Agent Loop RPC** — The agent loop can only run in-process with its caller. There's no way to run it as a standalone service that an orchestrator connects to remotely. This is the missing seam between the orchestrator layer and the agent layer.

2. **No orchestrator** — No unified entry point that wires everything together into a runnable system. No way to manage multiple concurrent agents, observe them, steer them, or let agents spawn other agents.

### Key Architectural Insight

The "modes" (All Local, Agent in Sandbox, Tools in Sandbox) are not discrete configurations — they're points on a continuous placement spectrum. The three layers (orchestrator, agent loop, tools) are independently deployable:

| Layer | Placement Options |
|-------|------------------|
| **Orchestrator** | Local process, remote server |
| **Agent Loop** | In-process with orchestrator, standalone process (any machine), inside a sandbox |
| **Tools** | In-process with agent (e.g. Starlark interpreter), same machine/sandbox as agent (local filesystem), remote sandbox-host via RPC (our ZFS/gVisor infra), remote cloud provider via RPC (E2B/Daytona/Fly — designed, not yet implemented) |

The named modes are just common deployment patterns, not hard boundaries. The library should support any valid combination.

### What Exists Today

| Capability | Status |
|-----------|--------|
| Agent loop (internal/agent) — in-process only | ✅ Implemented |
| Local tools (internal/tools) — in-process with agent | ✅ Implemented |
| Sandbox host (cmd/sandbox-host) — remote tool execution via RPC | ✅ Implemented |
| ExecutionEnvironment interface — unified local/remote tool dispatch | ✅ Implemented |
| RPC layer (internal/rpc) — ConnectRPC for sandbox host | ✅ Implemented |
| Agent Loop RPC — run agent loop as a service | ❌ Not implemented |
| Orchestrator — multi-session management, persistence, UI | ❌ Not implemented |

---

## Requirements (R)

| ID | Requirement | Status |
|----|-------------|--------|
| R0 | Agent loop can run as a standalone RPC service, deployable anywhere (bare metal, VM, inside sandbox, or in-process) | Core goal |
| R1 | Orchestrator server manages concurrent agent sessions with independent placement of agent loop and tools | Core goal |
| R2 | Agents can spawn new agent sessions through the orchestrator | Core goal |
| R3 | UI clients (TUI or web app) can connect to observe and interact with all sessions | Core goal |
| R4 | Persistent state survives orchestrator restart (SQLite, pure Go / no CGO) | Must-have |
| R5 | Event streaming from agent sessions to UI clients in real-time | Must-have |
| R6 | User can send messages / follow-ups / steering commands to running agents | Must-have |
| R7 | Session lifecycle management: create, pause, resume, destroy | Must-have |
| R8 | Agent loop server connects to sandbox-host for remote tool execution OR runs tools locally — independent config | Must-have |
| R9 | SandboxControl can launch agent loop server inside a sandbox (native via sandbox-host, and external via cloud provider APIs) | Must-have |

---

## Scoping Decision: Two Deliverables

This shaping is really about two things that should likely be separate plans:

### Plan A: Agent Loop RPC Service
The foundational library piece. Wraps the existing agent loop with an RPC interface:
- CreateSession, SendMessage, SubscribeEvents, Steering, DestroySession
- Deployable as standalone binary or in-process
- Connects to sandbox-host for tools, or runs tools locally
- This is pure library infrastructure — anyone building on flex-agent-runtime benefits

### Plan B: Orchestrator Application
The application layer built on top of Plan A:
- Multi-session management, SQLite persistence, UI surface (web/TUI)
- Agent-spawning-agents
- Lives in cmd/orchestrator (not demos/ — it's a real application)
- Demonstrates how to build on the library, but is also genuinely useful standalone

Plan A must come first. Plan B depends on it.

---

## Key Design Decisions

### Sandbox Control Abstraction

The orchestrator needs a **SandboxControl** interface for creating/destroying sandboxes, independent of provider:

- **NodeSandboxControl** — RPC client to our sandbox-host (which manages ZFS + gVisor internally)
- **E2BSandboxControl** — HTTP client to E2B API
- **DaytonaSandboxControl** — HTTP client to Daytona API
- **FlySandboxControl** — HTTP client to Fly Machines API

This is distinct from **ExecutionEnvironment** (which is about executing tools within an already-created sandbox). SandboxControl is about lifecycle; ExecutionEnvironment is about usage.

For our native stack, the sandbox-host IS the control plane. For 3rd parties, their API is the control plane. The orchestrator doesn't care — it calls SandboxControl.CreateSandbox() either way.

### Per-Tool-Call vs Session-Level Lifecycle

- **Per-tool-call isolation** is internal to the ExecutionEnvironment implementation (e.g. native creates/destroys a gVisor container per call; E2B runs commands in a persistent VM)
- **Session-level lifecycle** (create, pause, resume, destroy) is the orchestrator's job via SandboxControl
- The ExecutionEnvironment never decides about pausing — it just executes tools

---

## Future Considerations

### Deep Pause via ZFS Send to S3

For cost optimization at scale with our native sandbox: instead of keeping ZFS datasets on local disk during pause, use `zfs send` to stream the dataset to S3, then destroy the local copy. Resume uses `zfs receive` from S3. This makes EBS "dumb storage" with no important state — ZFS is always recreatable from S3. Not needed for v1 but the SandboxControl.Pause/Resume interface should be designed to allow this.

---
