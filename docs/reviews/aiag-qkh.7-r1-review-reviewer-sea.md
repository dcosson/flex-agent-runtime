# Code Review: aiag-qkh.7 (R1, reviewer-sea)

- Bead: aiag-qkh.7
- Commit range: de62aca..da853c0
- Plan doc: docs/plans/01-ai-core.md §16.2, §17.1, §17.3
- Reviewer: reviewer-sea
- Review commit: b3314ab

## Findings

### P3 - No UnsafeEvent benchmark variant for realistic SSE data

**Location:** `internal/ai/sse/sse_test.go:179-197`

**Problem**
`BenchmarkScanner100EventsRealistic` uses `Event()` (safe path, 204 allocs/op) but has no corresponding `BenchmarkScanner100EventsRealisticUnsafe` using `UnsafeEvent()`. The basic benchmark already shows the improvement (104→4 allocs), but the realistic benchmark with Anthropic-style JSON payloads would be the most meaningful measurement for the actual production use case.

**Suggested fix**
Add `BenchmarkScanner100EventsRealisticUnsafe` mirroring the realistic benchmark but calling `UnsafeEvent()`.

---

### P3 - readLine lineBuf accumulates full line before size check

**Location:** `internal/ai/sse/sse.go:74-86, 119-122`

**Problem**
When a line spans the bufio buffer boundary, `readLine()` accumulates the entire line in `lineBuf` before returning. The `maxLineBytes` check in `Next()` happens after the full line is in memory. For pathologically long lines (e.g., 100MB with no newline), `lineBuf` will grow to the full size before the limit rejects it. This doesn't affect correctness or the default limit (1MB is reasonable), but the behavior differs from what `SetLimits` might imply — limits prevent *processing*, not *allocation*.

**Suggested fix**
Add a brief comment in `readLine()` or `SetLimits()` noting that line length is checked after accumulation. No code change needed — this is defense-in-depth, not a hard memory guarantee.

---

## Plan Compliance Check

| Deliverable | Status |
|---|---|
| §16.2: EventStream leak detection via runtime.SetFinalizer | ✅ |
| §16.2: Only active in test builds (testing.Testing() guard) | ✅ |
| §16.2: Logs creation stack trace on unclosed GC | ✅ |
| §17.1: SSE zero-copy line reading (ReadSlice) | ✅ |
| §17.1: Reusable dataBuf across events | ✅ |
| §17.1: UnsafeEvent with unsafe.String for zero-copy views | ✅ |
| §17.1: B2 SSE benchmark improvement verified | ✅ (96% fewer allocs, 22% faster) |
| §17.3: UnmarshalArgumentsFromReader with json.NewDecoder | ✅ |
| §17.3: LargeArgumentThreshold (64KB) constant | ✅ |
| §17.3: Benchmark for large argument payloads | ✅ (128KB and 512KB benchmarks) |
| Public API re-exports for new functions/constants | ✅ |

## Test Results

| Test | Status | Notes |
|---|---|---|
| TestEventStreamLeakDetection | ✅ | Verifies closed tracking |
| TestEventStreamLeakDetectionFires | ✅ | Verifies finalizer runs without panic |
| TestUnmarshalArguments (small, large, invalid) | ✅ | Both paths covered |
| TestUnmarshalArgumentsFromReader (small, large, invalid) | ✅ | Both paths covered |
| BenchmarkScanner100Events | ✅ | 104 allocs/op (safe), 4 allocs/op (unsafe) |
| BenchmarkScanner100EventsRealistic | ✅ | 204 allocs/op with Anthropic JSON |
| BenchmarkUnmarshalArguments | ✅ | 6 variants: small/large × bytes/reader |
| All existing tests pass with `-race` | ✅ | 0 race warnings |
| FuzzScanner | ✅ | Updated to exercise new code paths |

## Implementation Quality Notes

- **ReadSlice optimization**: Correct use of `bufio.ReadSlice` to avoid per-line string allocation. Falls back to `lineBuf` accumulation only when lines span the bufio internal buffer — rare in practice for SSE.
- **Go compiler optimization**: `switch string(field)` with constant cases is documented as a compiler-recognized pattern that avoids heap allocation. Good comment explaining this.
- **Deferred string conversion**: `dataBuf` accumulates raw bytes across multi-line data fields. `Event()` converts once; `UnsafeEvent()` avoids conversion entirely. Clean two-tier API.
- **Leak detection correctness**: Uses `atomic.Bool` for `closed` field, correctly checked by the finalizer which runs on a separate goroutine. `testing.Testing()` guard ensures zero overhead in production.
- **Honest benchmarks**: The argument parsing benchmarks correctly show that `FromReader` is slower for already-in-memory data. The code comment explains that the benefit is avoiding materialization when reading from HTTP response bodies — the right level of documentation.

## Summary

2 findings: 0 P0, 0 P1, 0 P2, 2 P3

**Verdict**: Approved

Clean implementation of all three plan items. The SSE zero-copy optimization delivers measurable improvement (96% fewer allocations, 22% faster) with a well-designed safe/unsafe API split. Leak detection is correctly guarded to test builds only. The large JSON streaming helpers are honest about their tradeoffs with thorough benchmarks. Both P3 findings are minor documentation/benchmark completeness nits.
