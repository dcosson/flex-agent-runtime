# 08: Agent + Tools E2E — Test Harness

**Companion to:** [08-agent-tools-e2e.md](./08-agent-tools-e2e.md)
**Scope:** Advanced test harness strategy for validating integrated agent+tools behavior at scale and under adversarial conditions.

---

## 1. Property-Based Tests

### P1. End-state Determinism (Deterministic Mode)

Invariant:
- Given fixed fixture repo + provider trace + control injections, final workspace and normalized transcript are identical across runs.

### P2. Event Milestone Coverage

Invariant:
- Every successful scenario emits required milestones (`turn_started`, `tool_started`, `tool_completed`, `turn_completed`, `state_change:idle`).

### P3. Follow-up FIFO Preservation

Invariant:
- Queued follow-ups execute in insertion order across full E2E workflow.

### P4. Scripting Trace Consistency

Invariant:
- Scripted flows always include discover/describe/invoke trace ordering when those calls are present.

### P5. Terminal-tool Short-circuit

Invariant:
- Once terminal tool completes, no additional provider call occurs in same turn.

---

## 2. Fault Injection / Chaos Tests

### F1. Provider interruption mid-turn

- Inject stream abort or malformed event in middle of tool workflow.
- Verify clean failure artifact generation and no hung sessions.

### F2. Tool timeout in bash loop

- Force timeout during command execution.
- Verify proper error propagation and controlled recovery behavior.

### F3. Concurrent control injection

- Race steering/follow-up/abort with active E2E scenario.
- Validate deterministic boundary semantics.

### F4. Filesystem mutation conflict

- Introduce out-of-band file mutation during scenario.
- Verify diagnostics capture and expected failure mode.

### F5. Script limit exceedance

- Force script step/time budget exceedance inside E2E run.
- Verify typed limit failure and bounded side effects.

---

## 3. Comparison / Oracle Tests

### O1. Deterministic vs Live-provider semantic oracle

- Execute selected scenarios in deterministic and live-provider modes.
- Compare semantic outcomes (file diffs, tool sequence classes, terminal state), ignoring textual phrasing drift.

### O2. LocalBackend vs SandboxBackend oracle (when available)

- Run same scenario across both backends.
- Expect equivalent final repo state and normalized conversation-level milestones.

### O3. Replay bundle oracle

- Re-run archived failure bundles and ensure identical reproduction before fix, green after fix.

---

## 4. Deterministic Simulation Tests

### S1. Scenario state-machine simulation

- Simulate expected event progression for each scenario and compare runtime traces.

### S2. Control-boundary simulation

- Enumerate steering/follow-up injection points in scenario timelines and verify legal outcomes.

### S3. Fixture mutator simulation

- Systematically mutate fixture repos (file names/contents/depth) and ensure scenario assertions remain meaningful.

---

## 5. Benchmarks and Performance Targets

### B1. E2E scenario runtime

Target:
- fast scenario median < 10s in CI baseline.

### B2. Full deterministic suite runtime

Target:
- full deterministic E2E suite < 10 minutes on standard CI runner.

### B3. Artifact generation overhead

Target:
- failure artifact collection adds < 5% overhead to passing scenarios.

### B4. Parallel scaling

Target:
- >= 3x speedup with parallel scenario execution on 4-core runner.

---

## 6. Stress and Soak Tests

### ST1. Repeated scenario churn

- Run full deterministic suite continuously for 12 hours.
- Assertions: no flake growth, no resource leaks.

### ST2. Multi-agent parallel E2E stress

- Run many independent scenarios concurrently.
- Assertions: isolation between workspaces and stable pass rates.

### ST3. Long conversation stress

- Extended multi-turn scenarios with repeated tool/script cycles.
- Assertions: bounded memory and sustained correctness.

---

## 7. Security Tests

### SEC1. Workspace containment in E2E

- Ensure all scenario tool actions stay within test workspace root.

### SEC2. Secret redaction in artifacts

- Validate generated transcripts/logs/diffs redact configured secrets.

### SEC3. Script sandbox enforcement in integrated runs

- Ensure escape attempts still fail when invoked through full agent E2E path.

### SEC4. Command safety checks

- Ensure dangerous command patterns are gated/flagged per tool policy in test env.

---

## 8. Manual QA Plan

1. Run representative deterministic scenarios and inspect event timeline readability.
2. Run one live-provider smoke scenario and manually verify semantic parity with deterministic baseline.
3. Trigger a controlled failure and confirm diagnostics bundle quality (events + transcript + fs diff).
4. Validate steering/follow-up UX expectations in logs and final outcomes.
5. Validate code interpreter workflow trace from `execute_script` (including RLM and DataStore ops) in a real conversation.

---

## 9. CI Tier Mapping

| Tier | Runs | Contents |
|------|------|----------|
| PR-fast | every PR | minimal deterministic scenarios (file flow + bash flow) |
| PR-standard | every PR | broader deterministic matrix incl. steering/follow-up + code interpreter |
| Nightly | nightly | full deterministic suite + chaos injections + benchmarks |
| Weekly | weekly | soak tests + backend-parity oracle + live-provider smoke set |

---

## 10. Exit Criteria

1. Property tests P1-P5 pass reliably.
2. Fault injection tests F1-F5 pass with expected diagnostics.
3. Oracle tests O1-O3 pass (or approved exceptions documented).
4. Deterministic simulations S1-S3 pass.
5. Benchmark targets B1-B4 are met or regressions accepted.
6. Stress/soak tests ST1-ST3 pass in scheduled CI.
7. Security tests SEC1-SEC4 pass.
8. Manual QA checklist completed for release-candidate commit.
