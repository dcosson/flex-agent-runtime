# 07: Tool Scripting Meta-Tool — Test Harness

**Companion to:** [07-tool-scripting.md](./07-tool-scripting.md)
**Scope:** Deep validation for sandbox safety, determinism, limits enforcement, and backend-neutral script behavior.

---

## 1. Property-Based Tests

### P1. Deterministic Execution

Invariant:
- Same script + same tool responses + same config => identical result and trace.

Generator:
- Random scripts from bounded grammar using discover/describe/invoke/log.

### P2. Step Budget Correctness

Invariant:
- Reported `steps` equals number of builtin invocations (plus configured instruction hook increments when enabled).
- Exceeding limit always yields `budget_exceeded`.

### P3. Conversion Round-trip

Invariant:
- JSON-compatible values round-trip between Go <-> Starlark <-> Go without semantic drift.

### P4. Sandbox Capability Closure

Invariant:
- No script can access non-whitelisted globals or load external modules.

### P5. Backend Neutrality

Invariant:
- With equivalent tool behavior, LocalBackend and SandboxBackend produce equivalent script-level results.

---

## 2. Fault Injection / Chaos Tests

### F1. Tool error storms

- Randomly fail `invoke` calls mid-script.
- Verify deterministic abort behavior and typed error surfaces.

### F2. Timeout races

- Trigger context deadline exactly during builtin execution.
- Ensure single terminal outcome and no goroutine leak.

### F3. Oversized trace/result

- Generate huge log/output payloads.
- Verify truncation and bounded memory behavior.

### F4. VM interrupt chaos

- Inject forced interrupts during heavy loops.
- Ensure clean teardown and reusable runtime state for next execution.

### F5. Concurrent script execution pressure

- Run many scripts simultaneously against shared tool catalog.
- Validate thread safety and no cross-script state contamination.

---

## 3. Comparison / Oracle Tests

### O1. Golden trace corpus

- Maintain canonical scripts and expected trace/result fixtures.
- Detect regression in builtin semantics and ordering.

### O2. Capability oracle

- Automated script set attempting known escape patterns (`load`, hidden globals, type abuse).
- Must always fail with expected sandbox errors.

### O3. Tool-call parity oracle

- Compare scripted multi-step workflow against equivalent explicit agent tool-call sequence.
- Outputs should match semantically.

---

## 4. Deterministic Simulation Tests

### S1. Progressive discovery simulation

- Simulate realistic discovery narrowing (`discover("file")`, `describe`, `invoke`).
- Verify context-size discipline and expected decision path.

### S2. Limit boundary simulation

- Execute scripts that end exactly at step/time limits and one step beyond.
- Verify boundary behavior is precise and reproducible.

### S3. Failure recovery simulation

- Run failing script followed by succeeding script in same process.
- Verify no lingering state from prior failure.

---

## 5. Benchmarks and Performance Targets

### B1. Script startup latency

Target:
- VM init + simple script execution p95 < 5ms.

### B2. Builtin dispatch overhead

Target:
- discover/describe/invoke host-call overhead < 200us median (excluding underlying tool latency).

### B3. Trace serialization cost

Target:
- trace marshaling < 1ms for 100-step script with compact records.

### B4. Compiled-program cache benefit

Target:
- repeated identical script runs show >= 30% latency improvement with compile cache enabled.

---

## 6. Stress and Soak Tests

### ST1. Long-run script churn

- 12-hour loop of mixed scripts, failures, and limit-triggering runs.
- Assertions: no leaks, stable latency, deterministic outcomes.

### ST2. High-concurrency scripting

- 500 concurrent script executions against mock tools.
- Assertions: no races and bounded resource usage.

### ST3. Adversarial loop stress

- Scripts engineered for maximal step consumption.
- Assertions: budget limits engage reliably without process instability.

---

## 7. Security Tests

### SEC1. Sandbox breakout attempts

- Direct probes for filesystem, network, process APIs from script.
- Must fail due to absent capabilities.

### SEC2. Import/load denial

- Verify `load` and module import mechanisms are disabled.

### SEC3. Resource exhaustion controls

- Pathological scripts for CPU/time/memory pressure.
- Ensure hard limits and explicit errors.

### SEC4. Sensitive data handling

- Ensure traces/logs redact configured secrets from tool outputs.

---

## 8. Manual QA Plan

1. Run a real coding task using `execute_script` and inspect trace readability.
2. Validate progressive discovery behavior in a prompt-constrained scenario.
3. Trigger step/time limit failures and verify user-facing diagnostics.
4. Execute same script with LocalBackend and SandboxBackend to confirm parity.
5. Attempt known sandbox-escape snippets and confirm deterministic rejection.

---

## 9. CI Tier Mapping

| Tier | Runs | Contents |
|------|------|----------|
| PR-fast | every PR | unit tests for builtins, conversion, sandbox restrictions |
| PR-standard | every PR | component tests + deterministic simulations + bounded properties |
| PR-race | every PR | concurrent execution race suite |
| Nightly | nightly | capability oracle, chaos tests, benchmark trends |
| Weekly | weekly | soak tests and backend-parity integration runs |

---

## 10. Exit Criteria

1. Property tests P1-P5 pass across repeated seeds.
2. Fault injection tests F1-F5 pass with no leaks/races.
3. Oracle suites O1-O3 pass and fixtures are stable.
4. Deterministic simulations S1-S3 pass.
5. Benchmark targets B1-B4 met or regressions explicitly accepted.
6. Stress/soak suites ST1-ST3 pass in scheduled CI.
7. Security tests SEC1-SEC4 pass.
8. Manual QA checklist completed for release-candidate commit.
