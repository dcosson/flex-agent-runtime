# flex-agent-runtime

Building blocks for an AI agent runtime in Go. Work in progress.

## What's here

This project provides composable components for building agent systems. Some examples of what it includes today:

- **LLM & Embedding client library** — unified streaming interface across Anthropic, OpenAI, Google, and Cohere with provider registry, model catalog, cost tracking, extended thinking support, and context overflow detection
- **A Pi-inspired agent loop** — LLM-to-tools-to-LLM turn loop with a state machine, event bus, control queue for steering (pause, follow-up, exit), and session persistence
- **Sandbox execution** — run tools in isolated environments with ZFS copy-on-write snapshots, gVisor containers, per-session resource quotas, and automatic snapshot rollback
- **Built-in tools** — bash, file read/write/edit, grep, glob, git operations, and a code interpreter, split into tiers (in-process vs. isolated)
- **Terminal multiplexer** — PTY session management with ANSI/VT100 emulation, multi-client streaming, and real-time activity monitoring
- **RPC layer** — ConnectRPC-based API for sandbox lifecycle, agent events, and terminal streaming with auth, versioning, and WebSocket relay
- **Stubserver** — test double that serves canned SSE and JSON responses with fault injection (TCP reset, malformed responses, backpressure) for testing provider integrations without real API calls

## Supported providers

| Provider | Chat/LLM | Embeddings |
|----------|----------|------------|
| Anthropic | Yes | - |
| OpenAI | Yes | Yes |
| Google | Yes | Yes |
| Cohere | - | Yes |

All providers support configurable base URLs for compatible third-party endpoints.

## Quick start

```bash
# Build everything
make build

# Run the interactive LLM demo
export ANTHROPIC_API_KEY=your-key-here
./bin/llm-demo chat

# Run the embedding similarity demo
export OPENAI_API_KEY=your-key-here
./bin/embedding-demo rank --query "how to cook pasta" \
  --text "boil water and add spaghetti" \
  --text "the weather is sunny today" \
  --text "Italian recipes for beginners"
```

## Using as a library

```go
import (
    "github.com/dcosson/flex-agent-runtime/ai"
    "github.com/dcosson/flex-agent-runtime/ai/provider/anthropic"
)

// Register a provider
anthropic.Register(anthropic.Config{
    APIKey: os.Getenv("ANTHROPIC_API_KEY"),
}, "my-app")

// Stream a response
model, _ := ai.GetModel("anthropic", "claude-sonnet-4-20250514")
es := ai.StreamSimple(ctx, model, ai.Context{
    Messages: messages,
    Tools:    tools,
}, ai.SimpleStreamOptions{})

for event := range es.C {
    // handle streaming events
}
msg, err := es.Result()
```

```go
import (
    "github.com/dcosson/flex-agent-runtime/ai"
    "github.com/dcosson/flex-agent-runtime/ai/provider/openai"
)

// Embeddings
openai.RegisterEmbedding(openai.Config{
    APIKey: os.Getenv("OPENAI_API_KEY"),
}, "my-app")

resp, err := ai.Embed(ctx, "text-embedding-3-small", ai.EmbeddingRequest{
    Texts: []string{"hello world", "goodbye world"},
})
```

## Deployment modes

The orchestrator supports flexible agent deployment across four independent dimensions:

### Dimensions

1. **Agent Loop Placement** — where the agent loop process runs
   - **Orchestrator** — in-process on the orchestrator host
   - **Remote** — on a separate host

2. **Tools Placement** — where tool execution happens
   - **Co-located** — tools run wherever the agent loop is
   - **Sandbox** — tools dispatch to a sandbox-host via RPC

3. **Agent Type** — what drives the agent loop
   - **Native** — built-in AgentLoopService (flex-agent-runtime's own loop)
   - **Terminal coding agent** — 3rd party agent driven via terminal multiplexer (e.g. Claude Code, Codex)

4. **Execution Environment** — isolation level for the host
   - **Bare instance** — EC2/VPS with VM-level isolation only (static-host or fleet)
   - **gVisor** — sandbox-host with gVisor containers + ZFS snapshots (static-host or fleet)
   - *(Future: E2B, Daytona, Fly)*

### Valid combinations

| Agent Type + Environment | Orch + Co-located | Orch + Sandbox | Remote + Co-located | Remote + Sandbox |
|---|:---:|:---:|:---:|:---:|
| Native + Bare | dev/local | — | — | — |
| Native + gVisor | — | tools-sandbox | — | — |
| Native + E2B | — | *(future)* | — | — |
| Native + Daytona | — | *(future)* | — | — |
| Native + Fly | — | *(future)* | — | — |
| Terminal Agent + Bare | — | — | agent-direct | *(future)* |
| Terminal Agent + gVisor | — | — | agent-sandbox | *(future)* |
| Terminal Agent + E2B | — | — | *(future)* | *(future)* |
| Terminal Agent + Daytona | — | — | *(future)* | *(future)* |
| Terminal Agent + Fly | — | — | *(future)* | *(future)* |

A single orchestrator can serve multiple modes concurrently. Each agent session specifies its placement mode at creation time.

## Build & test

```bash
make help          # list all targets
make build         # build all binaries into ./bin/
make check         # gofmt + vet + staticcheck
make test          # quick tests
make test-race     # race-enabled tests
make test-harness  # property/determinism/concurrency tests
make test-bench    # benchmarks
```

## License

MIT
