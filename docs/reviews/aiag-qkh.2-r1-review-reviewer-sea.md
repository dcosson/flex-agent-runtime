# Code Review: aiag-qkh.2 (R1, reviewer-sea)

- Bead: aiag-qkh.2
- Commit range: efa83a8..66d0313
- Plan doc: docs/plans/01-ai-core.md §5, §6, §7, §8, §12
- Test harness: docs/plans/01-ai-core-test-harness.md §1 (P3, P7, P9), §3 (F1), §4 (S2), §5 (B2, B6, B7), §7 (Sec2)
- Reviewer: reviewer-sea
- Review commit: 66d0313

## Findings

### P3 - Custom minInt/maxInt helpers instead of Go builtins

**Location:** `internal/ai/options.go:108-121`

**Problem**
The code defines custom `minInt` and `maxInt` helper functions. Since go.mod specifies Go 1.24.3, the builtin `min()` and `max()` functions are available (Go 1.21+). The plan uses `min()` directly. These custom helpers are redundant.

**Suggested fix**
Replace `minInt(a, b)` with `min(a, b)` and `maxInt(a, b)` with `max(a, b)`. Delete the helper functions.

---

### P3 - Minimal model catalog (single model)

**Location:** `internal/ai/models/catalog.json`

**Problem**
The catalog only contains one model (claude-sonnet-4-20250514). While functional for testing, the plan says the catalog should include all V1 models. This limits the usefulness of `GetModel`, `GetModels`, `SupportsXHigh` tests against real catalog data.

**Suggested fix**
Add at least a few representative models across providers (anthropic opus, openai gpt-5, google gemini) to exercise the catalog more thoroughly. Can be done as a follow-up since the infrastructure is correct.

---

### P3 - Bead closed before review (repeat of qkh.1 process issue)

**Location:** `.beads/issues/closed/aiag-qkh.2.json`

**Problem**
Same as qkh.1 — bead moved to closed in the implementation commit before review was completed.

**Suggested fix**
Leave beads open until reviewer approves.

---

## Plan Compliance Check

| Deliverable | Status |
|---|---|
| SSE Scanner (internal/ai/sse) with BOM, comments, multi-line data, event delimiters | ✅ |
| SSE resource limits (ErrLineTooLarge, ErrEventTooLarge) | ✅ (goes beyond plan — Sec2 addition) |
| Provider interface (API, Stream, StreamSimple) | ✅ |
| Provider registry (Register, Get, GetAll, Unregister, Clear, sync.RWMutex, sourceID) | ✅ |
| Model struct with ModelCost, ModelCompat (all fields match plan) | ✅ |
| Model registry with deepCopyModel (Headers, Input, Compat deep cloning) | ✅ |
| Embedded catalog via go:embed with init() loader | ✅ |
| CalculateCost ($/million tokens arithmetic) | ✅ |
| ModelsEqual (nil handling, ID+Provider equality) | ✅ |
| SupportsXHigh (opus-4-6, gpt-5.2/5.3 detection) | ✅ |
| Stream, StreamSimple, Complete, CompleteSimple entry points | ✅ |
| errorStream helper (immediate error event) | ✅ |
| BuildBaseOptions (default MaxTokens=32000, apiKey passthrough) | ✅ |
| ClampReasoning (xhigh → high) | ✅ |
| AdjustMaxTokensForThinking (defaults, custom budgets, minOutputTokens=1024) | ✅ |
| Implementation guide §1.1 Provider contract | ✅ |

## Test Harness Coverage

| Test | Status | Notes |
|---|---|---|
| P3: CalculateCost consistency (rapid) | ✅ | 100 iterations, epsilon 1e-9 |
| P7: Model registry mutation isolation | ✅ | Headers, Input, Compat.ReasoningEffortMap all verified |
| P9: Registry thread-safety (50×1000) | ✅ | Both provider and model registries, -race clean |
| S2: Registry stress contention | ✅ | 16 goroutines × 750ms bounded |
| F1: SSE parser fuzz | ✅ | 5 seeds, `\\n` → `\n` replacement |
| B2: SSE parsing benchmark (100 events) | ✅ | BenchmarkScanner100Events |
| B6: CalculateCost benchmark | ✅ | BenchmarkCalculateCost with ReportAllocs |
| B7: Model lookup benchmark | ✅ | BenchmarkGetModel with ReportAllocs |
| Sec2: SSE resource limits | ✅ | Line and event size limits enforced |
| All tests pass with `-race` | ✅ | 0 race warnings, both packages |

## Implementation Quality Notes

- **SSE Scanner**: Correct EOF handling — dispatches pending event on EOF even without trailing blank line. Resource limits add defense-in-depth beyond the plan. BOM stripping correctly applied only to first line (improvement over plan which would strip from every line).
- **Deep copy correctness**: All mutable fields (Headers map, Input slice, Compat pointer with ReasoningEffortMap) are properly cloned in both RegisterModel and GetModel paths.
- **Registry thread safety**: sync.RWMutex used correctly — RLock for reads, Lock for writes. sourceID-based UnregisterProviders safely iterates and deletes during write lock.
- **Stream entry points**: errorStream correctly uses goroutine + defer Close pattern matching the provider goroutine contract.
- **GetModelProviders sorts output**: Deterministic output for consumers, good practice.

## Summary

3 findings: 0 P0, 0 P1, 0 P2, 3 P3

**Verdict**: Approved

Thorough, well-structured implementation matching the plan closely. All deliverables present, all test harness tests pass with -race. The three P3 findings are minor nits (Go builtin min/max, minimal catalog, and process bead-close timing).
