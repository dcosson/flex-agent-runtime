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

| Layer | Can run... |
|-------|-----------|
| **Orchestrator** | On your laptop, on a cloud server, co-located with sandbox-host |
| **Agent Loop** | In-process with orchestrator, as a standalone process on any machine, inside a sandbox |
| **Tools** | In-process with agent loop, on a remote sandbox-host via RPC |

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
| R9 | Sandbox-host can launch agent loop server inside a sandbox it manages (enables Agent in Sandbox with our loop) | Undecided |

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
