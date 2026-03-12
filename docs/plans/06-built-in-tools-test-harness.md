# 06: Built-in Tools and Backend Dispatch — Test Harness

**Companion to:** [06-built-in-tools.md](./06-built-in-tools.md)
**Scope:** High-assurance verification for tool correctness, backend parity, safety boundaries, and performance.

---

## 1. Property-Based Tests

### P1. Path Safety Invariant

Invariant:
- Any accepted path resolves within workspace root.
- Any escaping path (including symlink-assisted) is rejected.

Generator:
- Random path fragments with `..`, symlinks, unicode, absolute/relative mixes.

### P2. Edit Correctness Invariant

Invariant:
- Exact-match edit changes only intended spans.
- If `old_string` absent, file bytes remain unchanged.

Generator:
- Random source strings and replacement parameters.

### P3. Backend Parity Invariant

Invariant:
- For tier-agnostic calls, LocalBackend and SandboxBackend produce equivalent normalized outputs.

Generator:
- Randomized tool inputs over deterministic workspace fixtures.

### P4. Grep Determinism

Invariant:
- Same query + workspace yields stable sorted results independent of file walk order.

### P5. Tier Classifier Stability

Invariant:
- Tool name + operation flags map to exactly one expected tier.

---

## 2. Fault Injection / Chaos Tests

### F1. Mid-write crash simulation

- Inject failure between temp-file write and rename in `write_file`.
- Expect no partial/corrupt target file.

### F2. Bash process kill races

- Kill or timeout bash subprocess during heavy output.
- Ensure clean termination and bounded goroutines.

### F3. Sandbox RPC intermittent failures

- Inject timeout/reset on ExecuteTool RPC.
- Verify surfaced error typing and no stale local state mutation.

### F4. Massive-output truncation path

- Commands and grep returning huge output.
- Validate truncation metadata and memory ceilings.

### F5. Concurrent tool calls in same workspace

- High-concurrency mixed reads/writes/edits.
- Ensure expected serialization/atomicity guarantees hold.

---

## 3. Comparison / Oracle Tests

### O1. Tool Golden Output Corpus

- Golden fixtures for read/write/edit/grep/glob/git output shapes.
- Verify stable wire-compatible result formatting.

### O2. Differential grep oracle

- Compare pure-Go grep results against reference `rg` on fixture repos for supported feature subset.
- Any mismatch must be classified (unsupported feature vs bug).

### O3. Edit operation oracle

- Compare edit tool outcomes against independent string-transform oracle implementation.

---

## 4. Deterministic Simulation Tests

### S1. Tier Routing Simulation

- Simulate mixed tool workloads and assert expected Tier 1/Tier 2 dispatch decisions.

### S2. Callback Event Ordering

- Simulate long-running bash and ensure `onUpdate` sequence monotonicity before terminal result.

### S3. Snapshot Metadata Simulation

- Feed sandbox responses with snapshot IDs and verify propagation through tool results without loss.

---

## 5. Benchmarks and Performance Targets

### B1. Read throughput

Target:
- `read_file` sustains >= 500 MB/s for large sequential reads on local SSD baseline.

### B2. Edit latency

Target:
- exact-match edit on 1MB file p95 < 5ms.

### B3. Grep throughput

Target:
- pure-Go grep scans >= 100 MB/s on literal mode, >= 40 MB/s on regex mode baseline.

### B4. Glob scalability

Target:
- glob over 100k-path tree p95 < 200ms with bounded allocations.

### B5. Bash update overhead

Target:
- callback forwarding overhead < 3% wall-time on high-output command benchmark.

---

## 6. Stress and Soak Tests

### ST1. 12-hour mixed-tool soak

- Continuous mixed calls across 50 concurrent agents in shared host simulation.
- Assertions: no leaks, no deadlocks, stable latency distribution.

### ST2. High-fanout grep stress

- Repeat grep workloads over large synthetic repository trees.
- Assertions: no OOM, deterministic truncation behavior.

### ST3. Git command stress

- Rapid status/diff/log cycles with changing repo state.
- Assertions: consistent exit/error handling and no lockfile-related corruption.

---

## 7. Security Tests

### SEC1. Path traversal + symlink escape

- Adversarial file paths and symlink chains.
- Ensure strict containment enforcement.

### SEC2. Command injection boundary

- Validate bash tool argument handling and logging do not execute unintended shell expansions outside explicit command contract.

### SEC3. Secret redaction

- Ensure tool outputs/logging paths redact configured secret patterns.

### SEC4. Resource exhaustion guards

- Oversized file reads/writes and extreme regex patterns.
- Ensure bounded memory/CPU behavior and explicit failures.

---

## 8. Manual QA Plan

1. Run an agent coding task end-to-end using LocalBackend and inspect file diffs for correctness.
2. Repeat the same task with SandboxBackend and compare outputs + snapshot metadata.
3. Trigger edit mismatch scenarios and verify clear user-facing diagnostics.
4. Run long `bash` tool command and confirm progressive updates in UI/logs.
5. Validate grep/glob usability on a real medium-size repository.

---

## 9. CI Tier Mapping

| Tier | Runs | Contents |
|------|------|----------|
| PR-fast | every PR | unit tests for schemas, path guards, tier classifier, edit semantics |
| PR-standard | every PR | component tests + bounded property tests + golden fixtures |
| PR-race | every PR | concurrent tool-call race suite |
| Nightly | nightly | differential oracles + chaos/fault injection + benchmark trend tracking |
| Weekly | weekly | soak tests, large-repo stress, sandbox integration suite |

Gated infra tests require sandbox environment and secrets.

---

## 10. Exit Criteria

1. Property tests P1-P5 pass across repeated randomized seeds.
2. Fault injection tests F1-F5 pass with no corruption/leaks.
3. Oracle tests O1-O3 pass or have documented accepted compatibility exceptions.
4. Deterministic simulations S1-S3 pass.
5. Benchmark targets B1-B5 met or regressions explicitly accepted.
6. Stress/soak tests ST1-ST3 pass in scheduled CI.
7. Security tests SEC1-SEC4 pass.
8. Manual QA checklist completed for release-candidate commit.
9. CI tier matrix implemented and green.
