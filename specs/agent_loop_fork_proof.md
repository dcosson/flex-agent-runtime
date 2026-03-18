# Agent Loop Fork Independence Structural Proof

## Scope

This note provides the structural proof sketch requested by plan 21:

- plan section 2.5 (`docs/plans/21-agent-tlaplus-spec.md:105-114`)
- safety property 6 (`docs/plans/21-agent-tlaplus-spec.md:189`)

Claim:

For two sessions created via `ResumeSession` from identical conversation-log content, mutable runtime state is not shared between the sessions. Updates in fork A do not mutate fork B's session state, conversation log container, or metrics.

## Construction Trace

### 1. `ResumeSession` creates each fork independently

For each call, `AgentLoopService.ResumeSession`:

1. decodes the request log (`validateAndDecodeResumeLog`) into a new `[]AgentMessage` (`internal/agent/service.go:399`, `:639-689`);
2. calls `newManagedSessionLocked(cfg, conversation)` (`internal/agent/service.go:404`);
3. stores the returned `managedSession` under a unique session ID (`internal/agent/service.go:409`).

So each fork call gets its own decode pass and `managedSession` allocation.

### 2. `newManagedSessionLocked` allocates fresh runtime objects

`newManagedSessionLocked` builds a new runtime object graph per call:

- new driver instance via `s.driverFactory(...)` (`internal/agent/service.go:531-534`);
- new agent via `a := New(driver)` (`internal/agent/service.go:536`);
- new base session struct literal (`internal/agent/service.go:537-542`);
- new managed-session struct + fresh streams map + fresh context (`internal/agent/service.go:545-555`).

`New(driver)` also creates a new per-agent `eventBus` and `ControlQueue` (`internal/agent/agent.go:49-55`).

Therefore, the top-level mutable owners (`managedSession`, `Agent`, `driver`, `eventBus`, `ControlQueue`, stream registry, context cancel func) are newly allocated for each fork.

### 3. Conversation log container is copied per fork

`newManagedSessionLocked` copies the decoded conversation slice into the new base session:

- `ConversationLog: append([]AgentMessage(nil), conversation...)` (`internal/agent/service.go:540`).

This allocates a new backing array for each fork's `ConversationLog` container.

Then `a.SetSession(baseSession)` clones again (`internal/agent/service.go:543`), and `SetSession` calls `session.Clone()` (`internal/agent/agent.go:95-107`).

`Session.Clone()` copies:

- `ConversationLog` slice (`internal/agent/types.go:54-57`);
- `StateHistory` slice (`internal/agent/types.go:58-61`);
- scalar fields and metrics by value (`internal/agent/types.go:53`).

So even within one fork there are copy boundaries; across forks, containers are disjoint.

### 4. Read/write access uses clone boundaries

- External reads via `Agent.Session()` return `a.session.Clone()` (`internal/agent/agent.go:75-82`).
- Driver start path runs with `session.Clone()` (`internal/agent/loop.go:69`).
- Resume path passes `sess.Clone()` (`internal/agent/agent.go:210`).

This prevents callers/drivers from receiving aliases to the agent's in-place mutable `Session`.

## Alias Analysis by Mutable Component

1. `managedSession` object: unique allocation per `ResumeSession` call (`internal/agent/service.go:546-555`).
2. `Agent` object: unique allocation per call (`internal/agent/service.go:536`).
3. `eventBus` and `ControlQueue`: unique per `Agent` (`internal/agent/agent.go:49-55`).
4. `Session` struct: unique per call (literal in `newManagedSessionLocked`, then clone in `SetSession`).
5. `ConversationLog` slice backing array: freshly allocated per call and per clone (`internal/agent/service.go:540`, `internal/agent/types.go:55-57`).
6. `StateHistory` slice backing array: freshly allocated per clone (`internal/agent/types.go:59-61`).
7. `SessionMetrics`: embedded value type; no pointer sharing (`internal/agent/types.go:29-46`).
8. `streams` registry map: fresh `make(map[*serviceEventReceiver]struct{})` per managed session (`internal/agent/service.go:552`).

No mutable owner above is shared between fork A and fork B.

## Conversation Entry Payloads

`AgentMessage` contains `Message ai.Message` (interface). `Session.Clone()` copies the `[]AgentMessage` container, not deep-cloning each `ai.Message` object (`internal/agent/types.go:54-57`).

For the `ResumeSession` fork case, this is still safe for independence because each fork's `conversation` is decoded separately in `validateAndDecodeResumeLog` (`internal/agent/service.go:399`, `:661-684`), and `RecordToAgentMessage` constructs fresh message objects during decode (`internal/agent/api/codec.go:141-189`).

After creation, agent code appends new conversation entries (`internal/agent/agent.go:283`) and does not mutate historical entries in-place. Thus fork A's turn progression does not mutate fork B's prior decoded entries.

## Conclusion

Given two `ResumeSession` calls with identical log content:

- each call allocates a distinct mutable runtime graph (`managedSession`, `Agent`, `driver`, bus, queue, maps, context);
- each call owns distinct session/container slices and metrics storage;
- subsequent reads/writes cross clone boundaries.

Therefore, session forks are structurally independent with respect to mutable state, satisfying plan 21 safety property 6.
