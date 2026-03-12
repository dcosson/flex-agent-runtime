# 07: Code Interpreter Meta-Tool — Test Harness

**Companion to:** [07-code-interpreter.md](./07-code-interpreter.md)
**Scope:** Deep validation for sandbox safety, determinism, limits enforcement, RLM budget tracking, DataStore correctness, tier classification, and backend-neutral script behavior.

---

## 1. Property-Based Tests

### P1. Deterministic Execution

Invariant:
- Same script + same tool responses + fixed RLM responses + same config => identical result and trace.

Generator:
- Random scripts from bounded grammar using discover/describe/invoke/log/llm_call/store_*.

### P2. Step Budget Correctness

Invariant:
- Reported `steps` equals number of builtin invocations. `llm_batch` counts as N steps (one per call in batch).
- Exceeding limit always yields `budget_exceeded`.

### P3. Conversion Round-trip

Invariant:
- JSON-compatible values round-trip between Go <-> Starlark <-> Go without semantic drift.
- DataStore values round-trip through write/read without corruption.

### P4. Sandbox Capability Closure

Invariant:
- No script can access non-whitelisted globals or load external modules.
- RLM sub-calls cannot access parent agent conversation context.
- DataStore access is scoped — no cross-session key access.

### P5. Backend Neutrality

Invariant:
- With equivalent tool behavior, LocalBackend and SandboxBackend produce equivalent script-level results.

### P6. RLM Token Budget Monotonicity

Invariant:
- Running token total is monotonically non-decreasing.
- Total tokens used never exceeds configured budget + one call's worth (last call may partially exceed).
- Budget enforcement fires before any call that would push total above limit.

### P7. DataStore Key Isolation

Invariant:
- Keys written by one script execution are not visible from a concurrent execution on a different session.
- Key validation rejects all path traversal attempts (`../`, `..\\`, absolute paths, null bytes).

### P8. Tier Classification Consistency

Invariant:
- Any script containing call expressions to `llm_call` or `llm_batch` is classified as TierFull.
- Explicit tier override in request is always respected.
- TierLightweight scripts run with lightweight limits; TierFull with full limits.

---

## 2. Fault Injection / Chaos Tests

### F1. Tool Error Storms

- Randomly fail `invoke` calls mid-script.
- Verify deterministic abort behavior and typed error surfaces.

### F2. Timeout Races

- Trigger context deadline exactly during builtin execution.
- Ensure single terminal outcome and no goroutine leak.
- Test timeout during active RLM sub-call — verify clean cancellation.

### F3. Oversized Trace/Result

- Generate huge log/output payloads.
- Verify truncation and bounded memory behavior.
- Generate large RLM responses and verify trace size limits apply.

### F4. VM Interrupt Chaos

- Inject forced interrupts during heavy loops.
- Ensure clean teardown and reusable runtime state for next execution.

### F5. Concurrent Script Execution Pressure

- Run many scripts simultaneously against shared tool catalog.
- Validate thread safety and no cross-script state contamination.
- Include mix of lightweight and full tier scripts.

### F6. RLM Provider Failures

- Provider returns 429 (rate limit) during `llm_call`.
- Provider returns 500 (internal error) during `llm_batch` for subset of calls.
- Provider hangs indefinitely — verify timeout propagation.
- Provider returns malformed response — verify clean error handling.

### F7. DataStore Failures

- FSDataStore: disk full during `store_write`.
- FSDataStore: permission denied on read.
- MemoryDataStore: memory limit exceeded during write.
- BlobDataStore: network error during read.
- SQLDataStore: connection lost during query.
- Verify each failure surfaces as typed Starlark error with clean trace entry.

### F8. Budget Exhaustion Races

- Start RLM call that would exactly exhaust token budget, then immediately start another.
- Verify exactly one call succeeds and the other gets `token_budget_exceeded`.
- No double-spend under concurrent `llm_batch` execution.

---

## 3. Comparison / Oracle Tests

### O1. Golden Trace Corpus

- Maintain canonical scripts and expected trace/result fixtures.
- Extended with RLM and DataStore operation traces.
- Detect regression in builtin semantics and ordering.

### O2. Capability Oracle

- Automated script set attempting known escape patterns (`load`, hidden globals, type abuse).
- Extended with attempts to access RLM internals and cross-session DataStore keys.
- Must always fail with expected sandbox errors.

### O3. Tool-call Parity Oracle

- Compare scripted multi-step workflow against equivalent explicit agent tool-call sequence.
- Outputs should match semantically.

### O4. DataStore Implementation Oracle

- Run identical store operation sequences against MemoryDataStore, FSDataStore, and SQLDataStore.
- Verify identical results for all operations (excluding performance characteristics).
- Search results should match for same patterns (where supported).

### O5. RLM Cost Oracle

- Run scripts with known provider pricing against model catalog.
- Verify reported `llm_cost_usd` matches independent calculation from token counts * catalog prices.
- Verify budget enforcement fires at correct thresholds.

---

## 4. Deterministic Simulation Tests

### S1. Progressive Discovery Simulation

- Simulate realistic discovery narrowing (`discover("file")`, `describe`, `invoke`).
- Verify context-size discipline and expected decision path.

### S2. Limit Boundary Simulation

- Execute scripts that end exactly at step/time limits and one step beyond.
- Verify boundary behavior is precise and reproducible.
- Extended: scripts that end exactly at token budget boundary.

### S3. Failure Recovery Simulation

- Run failing script followed by succeeding script in same process.
- Verify no lingering state from prior failure (including RLM state and DataStore state).

### S4. RLM Map-Reduce Simulation

- Simulate a full map-reduce pattern: read N files, `llm_batch` to summarize each, aggregate with `llm_call`.
- Verify correct ordering, parallel execution, budget tracking across all phases.
- Test with N=1, N=5 (within concurrency), N=20 (exceeding concurrency, requiring batching).

### S5. Tier Escalation Simulation

- Run a series of scripts with increasing complexity.
- Verify tier classification matches expected tier for each.
- Verify config selection (limits, DataStore) matches classified tier.

### S6. DataStore Lifecycle Simulation

- Write keys, read them, search them, list them, delete some, verify remaining state.
- Run across all DataStore implementations.
- Verify cleanup on execution completion.

---

## 5. Benchmarks and Performance Targets

### B1. Script Startup Latency

Target:
- VM init + simple script execution p95 < 5ms (lightweight tier).
- VM init + full tier setup p95 < 20ms (including DataStore init).

### B2. Builtin Dispatch Overhead

Target:
- discover/describe/invoke host-call overhead < 200us median (excluding underlying tool latency).
- store_read/store_write host-call overhead < 100us median for MemoryDataStore.
- store_read/store_write host-call overhead < 1ms median for FSDataStore.

### B3. Trace Serialization Cost

Target:
- Trace marshaling < 1ms for 100-step script with compact records.
- Trace marshaling < 5ms for 500-step full-tier script with RLM entries.

### B4. Compiled-program Cache Benefit

Target:
- Repeated identical script runs show >= 30% latency improvement with compile cache enabled.

### B5. RLM Dispatch Overhead

Target:
- `llm_call` dispatch overhead (excluding provider latency) < 500us median.
- `llm_batch` dispatch overhead per call < 200us median (amortized).

### B6. DataStore Throughput

Target:
- MemoryDataStore: >= 1M ops/sec for 1KB values (single-goroutine sequential).
- MemoryDataStore concurrent read-heavy workload: >= 2M read ops/sec aggregate baseline on 4-core CI runners.
- FSDataStore: 10K ops/sec for 1KB values (sequential write).
- MemoryDataStore memory overhead: < 2x stored data size.

## Review Disposition

| # | Reviewer | Severity | Summary | Disposition | Notes |
|---|----------|----------|---------|-------------|-------|
| 1 | coder-2-sea | P1 | Determinism wording should explicitly account for provider non-determinism | Incorporated | P1 wording now requires fixed RLM responses for deterministic replay. |
| 2 | coder-2-sea | P2 | Tier classification should validate actual call expressions, not identifier matches | Incorporated | P8 now targets call-expression semantics. |
| 3 | coder-2-sea | P3 | MemoryDataStore throughput target too low | Incorporated | B6 raised to 1M+ with a concurrent read baseline. |

---

## 6. Stress and Soak Tests

### ST1. Long-run Script Churn

- 12-hour loop of mixed scripts (lightweight and full tier), failures, and limit-triggering runs.
- Include RLM calls with mock provider.
- Assertions: no leaks, stable latency, deterministic outcomes, DataStore cleanup.

### ST2. High-concurrency Scripting

- 500 concurrent script executions against mock tools and mock provider.
- Mix of lightweight and full tier.
- Assertions: no races and bounded resource usage.

### ST3. Adversarial Loop Stress

- Scripts engineered for maximal step consumption and RLM token consumption.
- Assertions: budget limits engage reliably without process instability.

### ST4. DataStore Capacity Stress

- Write large volumes of data through MemoryDataStore until limit hit.
- Write many concurrent keys through FSDataStore.
- Verify bounded memory, no file descriptor leaks, clean error on limit breach.

### ST5. RLM Burst Stress

- 100 concurrent scripts each running `llm_batch` with 10 calls against mock provider.
- Verify goroutine count stays bounded (no goroutine leak).
- Verify aggregate token tracking accuracy under high concurrency.

---

## 7. Security Tests

### SEC1. Sandbox Breakout Attempts

- Direct probes for filesystem, network, process APIs from script.
- Must fail due to absent capabilities.

### SEC2. Import/Load Denial

- Verify `load` and module import mechanisms are disabled.

### SEC3. Resource Exhaustion Controls

- Pathological scripts for CPU/time/memory pressure.
- Ensure hard limits and explicit errors.

### SEC4. Sensitive Data Handling

- Ensure traces/logs redact configured secrets from tool outputs.
- Ensure RLM sub-call prompts do not leak parent conversation secrets.

### SEC5. RLM Prompt Injection

- Craft RLM sub-call prompts attempting to override system instructions.
- Verify sub-calls use isolated context (no parent system prompt leakage).
- Verify sub-call responses are treated as data, not instructions.

### SEC6. DataStore Path Traversal

- Attempt keys with `../`, `..\\`, absolute paths, null bytes, Unicode normalization attacks.
- Verify all rejected at validation layer before reaching implementation.
- Test against all DataStore implementations.

### SEC7. Cross-session DataStore Isolation

- Run two concurrent scripts with different session scopes.
- Verify neither can read/write the other's keys.
- Test with MemoryDataStore (separate instances) and FSDataStore (separate root directories).

---

## 8. Manual QA Plan

1. Run a real coding task using `execute_script` with tool discovery and inspect trace readability.
2. Run an RLM workflow: read code files, summarize with `llm_call`, write summary with `store_write`. Verify trace shows complete RLM interaction with usage stats.
3. Run a map-reduce RLM workflow: batch-summarize multiple files, then aggregate. Verify parallel execution and correct aggregation.
4. Validate progressive discovery behavior in a prompt-constrained scenario.
5. Trigger step/time/token limit failures and verify user-facing diagnostics are clear and actionable.
6. Execute same script with LocalBackend and SandboxBackend to confirm parity.
7. Attempt known sandbox-escape snippets and confirm deterministic rejection.
8. Inspect DataStore contents after script execution to verify expected state.
9. Verify tier auto-classification for various script patterns matches expectations.

---

## 9. CI Tier Mapping

| Tier | Runs | Contents |
|------|------|----------|
| PR-fast | every PR | unit tests for builtins (all 10+), conversion, sandbox restrictions, tier classification, key validation |
| PR-standard | every PR | component tests + deterministic simulations + bounded properties + DataStore oracle |
| PR-race | every PR | concurrent execution race suite (scripts + RLM + DataStore) |
| Nightly | nightly | capability oracle, chaos tests (F1-F8), benchmark trends, RLM cost oracle |
| Weekly | weekly | soak tests (ST1-ST5), backend-parity integration runs, security suite (SEC1-SEC7) |

---

## 10. Exit Criteria

1. Property tests P1-P8 pass across repeated seeds.
2. Fault injection tests F1-F8 pass with no leaks/races.
3. Oracle suites O1-O5 pass and fixtures are stable.
4. Deterministic simulations S1-S6 pass.
5. Benchmark targets B1-B6 met or regressions explicitly accepted.
6. Stress/soak suites ST1-ST5 pass in scheduled CI.
7. Security tests SEC1-SEC7 pass.
8. Manual QA checklist completed for release-candidate commit.
