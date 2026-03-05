# Workflow Engine Integration Notes

Discussion notes on how `ai-agent-go` packages integrate with everything-db's Workflow Engine.

## Context

everything-db's Workflow Engine (plan `04b`) has two LLM-related activity types deferred to V2:
- `ActivityLLMCall` — single LLM inference call
- `ActivityAgentLoop` — iterative LLM agent loop (call → tools → call → ... → done)

Our `ai` package replaces the workflow engine's `LLMProvider` interface. Our `agent` package implements `ActivityAgentLoop`. The workflow engine doesn't need its own LLM abstractions — it uses ours.

## Agent Loop: Who Owns What

The agent loop is fundamentally different from a workflow DAG. A DAG has predefined steps with known edges. An agent loop has the LLM dynamically deciding what to do next — which tools to call, how many turns to take, when to stop.

**The workflow engine doesn't orchestrate the agent's decisions.** It provides a durable envelope:
- Checkpoint after each LLM turn and tool execution
- On crash, replay event log → reconstruct conversation history → resume agent from last checkpoint
- Budget enforcement (token/iteration limits) at the infrastructure level
- Tool dispatch mapping (agent tools → engine activities via ActivityManager with retry, auth propagation, gRPC routing)

**The agent package doesn't know about durability.** It:
- Accepts pre-existing conversation history (for state reconstruction after crash)
- Emits events via `Subscribe()` (which the workflow engine persists)
- Runs its loop (LLM → tools → LLM → ... → done)

Integration sketch:

```go
func (we *WorkflowExecutor) executeAgentLoop(ctx context.Context, config AgentLoopConfig) error {
    // 1. On crash recovery: replay events → reconstruct conversation
    history := we.reconstructFromEventLog(workflowID)

    // 2. Create agent with tools mapped to workflow activities
    tools := we.mapToolDefsToAgentTools(config.ToolDefs)
    a := agent.New(agent.Config{
        Model:   config.LLMConfig.Model,
        Tools:   tools,
        System:  config.LLMConfig.SystemPrompt,
        History: history,
    })

    // 3. Subscribe → checkpoint every event to EventStore
    a.Subscribe(func(event agent.AgentEvent) {
        we.eventStore.Append(ctx, toWorkflowEvent(event))
    })

    // 4. Run — agent handles the loop internally
    if len(history) == 0 {
        a.Prompt(ctx, config.InitialPrompt)
    } else {
        a.FollowUp(ctx)
    }
    return nil
}
```

## Terminal Tool Pattern

When the agent loop is embedded in a larger workflow, downstream steps need structured output from the agent. The cleanest pattern is a **"done" tool** — a tool the agent calls with structured, schema-validated output when it's finished:

```go
{
    Name:        "submit_result",
    Description: "Call when you've finished. Provide structured findings.",
    InputSchema: `{
        "type": "object",
        "properties": {
            "status":     {"type": "string", "enum": ["resolved", "escalated", "needs_info"]},
            "category":   {"type": "string"},
            "resolution": {"type": "string"}
        },
        "required": ["status", "category", "resolution"]
    }`,
}
```

When the agent calls `submit_result`, the loop terminates and the validated JSON arguments become the step's output for downstream workflow steps.

Why this beats alternatives:
- **Parse last text message**: fragile, LLMs ramble or format inconsistently
- **System prompt "format as JSON"**: works ~90% of the time, but tool calling is the reliable structured output channel
- **Agent calls a notify tool directly**: less reliable (might forget, call too early, or keep going), couples agent to downstream infrastructure

Our `agent` package should support both termination modes:
- Default (pi-mono style): loop ends when LLM responds with no tool calls
- Terminal tools: loop ends when a designated tool is called, its return value is the loop output

## Workflow Engine vs. Simple SQL Persistence

For a standalone agent loop (no surrounding workflow steps), the workflow engine's durability reduces to: append event → replay on crash. That's a SQL INSERT and SELECT. Any database works.

**When the workflow engine earns its keep:**
- Multi-step orchestration around the agent (fetch context → agent → store results → notify)
- CDC triggers, timers, cron scheduling
- Tool calls dispatched cross-engine via ActivityManager with retry/idempotency
- Saga compensation if agent's actions need rollback
- Unified observability across many workflow types

**When direct SQL persistence is sufficient:**
- Agent loop is the main thing, not embedded in a workflow
- Domain-specific schemas with proper foreign keys and indexes are important
- Rich querying over agent activity tied to domain objects
- The persistence listener is trivial (just insert rows on `Subscribe()` events)

## Domain Context in Workflow Events

Challenge: workflow engine events are generic (`workflow_id`, `event_type`, `payload`). If you want `ticket_id` on every agent event row for domain queries, you're fighting the abstraction.

Solution: use a workflow step to create a lightweight mapping before the agent starts. The workflow becomes:

```
1. CDC trigger: new support ticket inserted
2. Starlark step: INSERT INTO ticket_agent_sessions (ticket_id, workflow_id)
3. Agent loop: triage and resolve (all events in workflow event store)
4. Notify step: publish result
```

The mapping table is trivial:

```sql
CREATE TABLE ticket_agent_sessions (
    ticket_id   BIGINT PRIMARY KEY REFERENCES support_tickets(id),
    workflow_id UUID NOT NULL,
    created_at  TIMESTAMPTZ DEFAULT now()
);
```

One row per ticket. Query agent activity for a ticket: look up workflow_id → query workflow events. The SQL layer stays minimal, the workflow engine handles all durability/recovery/dispatch, and domain linkage is preserved through a simple join.

Cross-cutting analytics ("what % of tickets did the agent escalate?") requires joining the mapping table with the workflow event store — but that's what CDC-triggered analytics workflows are for, and the workflow engine already has that infrastructure.

## Implications for Our V1 Design

Our `agent` package is already correctly shaped for both integration paths:

| Capability | How V1 Supports It |
|---|---|
| State reconstruction | Accept pre-existing conversation history |
| Event persistence | `Subscribe()` emits all state changes |
| Resumability | `FollowUp()` continues from reconstructed state |
| Tool abstraction | `AgentTool` interface — implementations can dispatch anywhere |
| Terminal tools | Config option for designated tools that end the loop |
| Persistence-agnostic | Package has zero I/O — consumer decides where events go |

No changes needed to the current architecture. The `ai` package replaces the workflow engine's LLM layer, the `agent` package implements `ActivityAgentLoop`, and the workflow engine adds orchestration on top.
