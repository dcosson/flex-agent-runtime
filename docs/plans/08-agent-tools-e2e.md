# 08: Agent + Tools End-to-End Test Suite

**Status:** Draft
**Depends on:** 05-agent, 06-built-in-tools, 07-tool-scripting
**Depended on by:** 14-mode3-e2e
**Implements:** `e2etests/` suite validating full agent-loop behavior with built-in tools and scripting workflows across local execution modes.

---

## 1. Overview

This plan defines end-to-end verification for the integrated Batch-3 stack: agent loop, built-in tools, and tool scripting. The focus is user-visible behavior across component boundaries rather than isolated unit correctness.

Primary goals:
- Prove `agent.Agent` can complete realistic multi-turn coding workflows with local tools.
- Validate steering, follow-up, and terminal-tool behavior in full flows.
- Validate scripting meta-tool workflows (`discover -> describe -> invoke`) in real conversations.
- Establish reusable E2E harness patterns for later Mode-3/Mode-2 distributed E2E plans.

Non-goals:
- Remote sandbox host lifecycle coverage (plan 14/15).
- gVisor/ZFS performance characterization (plan 16).

---

## 2. Architecture

### 2.1 Test Harness Components

```mermaid
graph TB
    subgraph "e2etests"
        H[harness/runner.go\nscenario executor]
        FX[fixtures/\nrepos, prompts, expected outputs]
        OBS[observer.go\nAgentEvent capture + assertions]
        ORA[oracles.go\nfilesystem + conversation validators]
        SCN[scenarios/*.go\nworkflow definitions]

        H --> SCN
        H --> OBS
        H --> ORA
        SCN --> FX
    end

    subgraph "Runtime under test"
        AG[internal/agent]
        TOOLS[internal/tools]\n        SCRIPT[internal/tools/scripting]
        AI[internal/ai/provider/*]
    end

    H --> AG
    AG --> TOOLS
    AG --> SCRIPT
    AG --> AI
```

### 2.2 Core E2E Flow

```mermaid
sequenceDiagram
    participant Test as E2E runner
    participant Agent as agent.Agent
    participant Provider as ai provider
    participant Tools as built-in tools
    participant FS as workspace

    Test->>Agent: Prompt(task)
    Agent->>Provider: Stream call
    Provider-->>Agent: tool call(s)
    Agent->>Tools: Execute read/edit/write/bash/grep/glob/git
    Tools->>FS: mutate/read workspace
    Tools-->>Agent: tool results
    Agent->>Provider: continue
    Provider-->>Agent: final assistant response
    Agent-->>Test: events + final state
    Test->>FS: assert expected files and outputs
```

### 2.3 Scenario Matrix

| Scenario family | Purpose |
|----------------|---------|
| File-edit workflows | validate read/write/edit/grep/glob integration |
| Bash workflows | validate long-running command handling and exit semantics |
| Steering/follow-up workflows | validate control-plane behavior under active turns |
| Scripting workflows | validate meta-tool orchestration over built-in tools |
| Terminal-tool workflows | validate short-circuit turn completion |

---

## 3. Test Suite Design

### 3.1 File Layout

```text
e2etests/
├── harness/
│   ├── runner.go              # scenario execution and lifecycle
│   ├── env.go                 # temp workspace + provider setup
│   ├── observer.go            # event collection and sequencing asserts
│   └── assertions.go          # common end-state assertions
├── fixtures/
│   ├── repos/                 # seeded mini-repositories
│   ├── prompts/               # canonical task prompts
│   └── expected/              # expected file/event outcomes
├── scenarios/
│   ├── local_file_flow_test.go
│   ├── local_bash_flow_test.go
│   ├── steering_followup_test.go
│   ├── scripting_workflow_test.go
│   └── terminal_tool_test.go
└── testutil/
    ├── provider_fake.go       # deterministic provider scripts
    └── fsdiff.go              # workspace diff helpers
```

### 3.2 Provider Modes for E2E

- Default PR mode uses deterministic fake provider traces for reproducibility.
- Optional gated mode uses real Anthropic provider for smoke realism.
- Same scenario contracts should pass in both modes (allowing textual variance where needed).

### 3.3 Assertion Layers

Per scenario, assert at three layers:
1. Event-level: canonical event ordering and required milestones.
2. Conversation-level: expected message/tool-call shape and control actions.
3. Filesystem/process-level: expected file diffs, command outputs, and exit codes.

---

## 4. Key Scenarios

### 4.1 Multi-turn Local File Refactor

- Prompt asks agent to inspect and modify multiple files.
- Agent should perform read/grep/edit/write sequence over >1 turn.
- Final assertions verify concrete file contents and clean event timeline.

### 4.2 Bash Build-and-Fix Loop

- Prompt triggers `bash` test/build command, then file edits, then re-run.
- Assertions verify command exit transitions (fail -> pass) and incremental updates.

### 4.3 Steering Mid-turn

- Inject steering during active streaming.
- Assertions verify steering applied at boundary and altered subsequent behavior.

### 4.4 Follow-up Queue Chain

- Queue multiple follow-up directives during first turn.
- Assertions verify FIFO follow-up execution and eventual idle state.

### 4.5 Tool Scripting Workflow

- Prompt uses `execute_script` meta-tool for multi-step workflow.
- Assertions verify progressive discovery usage and scripted tool-call trace.

### 4.6 Terminal Tool Completion

- Scenario uses terminal tool to produce structured final artifact.
- Assertions verify no extra LLM continuation after terminal tool completion.

---

## 5. Connected Components (Seams)

| Component | Seam | Contract |
|-----------|------|----------|
| `internal/agent` | Public runtime API | prompt/continue/steer/follow-up/subscribe behavior under integrated load |
| `internal/tools` | Tool execution semantics | correct results and side effects for built-in tool catalog |
| `internal/tools/scripting` | Meta-tool behavior | discover/describe/invoke orchestration and limit handling |
| `internal/ai` providers | Streaming and tool-call translation | provider events drive tool loop correctly |
| `e2etests` infra | Deterministic environment | reproducible fixtures, stable assertions, controlled variance |

---

## 6. Acceptance Criteria

1. **G4 baseline passes**
- Steps: run full local E2E suite in deterministic provider mode.
- Expected: all scenarios green; agent handles multi-turn tool workflows end-to-end.

2. **File operations correctness**
- Steps: run refactor scenario touching multiple files.
- Expected: resulting repository state matches expected fixtures exactly.

3. **Bash loop correctness**
- Steps: run failing-test-to-passing-test scenario.
- Expected: command exit transitions and final pass state are observed.

4. **Steering/follow-up correctness**
- Steps: inject steering and follow-ups during active session.
- Expected: steering applied at valid boundary; follow-ups execute FIFO.

5. **Tool scripting correctness**
- Steps: run scripting scenario that discovers and invokes tools.
- Expected: script trace shows progressive discovery and successful multi-step completion.

6. **Terminal-tool correctness**
- Steps: run scenario using terminal tool result path.
- Expected: structured terminal output returned; loop ends cleanly with no extra turn.

7. **Optional live-provider smoke**
- Steps: run gated real-provider E2E smoke scenario.
- Expected: workflow completes with same semantic outcomes as deterministic mode.

---

## 7. Testing Strategy

### 7.1 Determinism Strategy

- Freeze fixtures and deterministic provider traces for PR runs.
- Normalize volatile fields (timestamps, token counts where provider-dependent).
- Keep expected assertions semantic, not brittle to exact wording.

### 7.2 CI Partitioning

- Fast E2E subset on every PR.
- Full deterministic suite nightly.
- Live-provider smoke behind secrets gate.

### 7.3 Failure Diagnostics

On scenario failure, automatically emit:
- event timeline dump
- conversation transcript dump
- workspace diff artifact
- command stdout/stderr excerpts

---

## 8. URP (Unreasonably Robust Programming)

1. **Scenario fuzz composer:** generate randomized but valid multi-turn tasks over fixture repos to broaden behavioral coverage.
2. **Dual-run differential checker:** run each scenario twice (fresh workspace and resumed workspace) and compare outcomes.
3. **Replay debugger:** export reproducible replay bundles (fixtures + provider trace + control injections).

---

## 9. Extreme Optimization

1. Parallel scenario scheduling with isolated temp workspaces to reduce CI wall time.
2. Snapshot fixture caches to avoid repeated repository setup cost.
3. Event assertion engine with streaming checks to fail fast on first invariant break.

---

## 10. Alien Artifacts

1. **Behavioral invariant mining:** infer cross-scenario invariants from passing runs and enforce them automatically.
2. **Metamorphic E2E tests:** apply semantics-preserving prompt transformations and assert equivalent end-state.
3. **Counterexample minimization:** automated shrinking of failing scenario traces to minimal repro.

---

## 11. Dependencies

| Dependency | Purpose |
|-----------|---------|
| `internal/agent` | runtime under test |
| `internal/tools` | built-in tool behaviors under test |
| `internal/tools/scripting` | meta-tool workflows under test |
| `internal/ai/provider/anthropic` + fake provider harness | deterministic and live-provider modes |

---

## 12. Exit Criteria

1. `e2etests/` contains scenario suite covering file, bash, steering/follow-up, scripting, and terminal-tool workflows.
2. Deterministic provider mode is stable and green in CI.
3. Live-provider smoke mode is available behind secrets gate.
4. Failure artifacts (events, transcript, workspace diff) are generated automatically.
5. G4 gate criteria are demonstrably met by this suite.
