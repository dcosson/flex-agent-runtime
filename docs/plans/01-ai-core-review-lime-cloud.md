# Review: 01-ai-core (lime-cloud)

- Source doc: `docs/plans/01-ai-core.md`
- Reviewed commit: fa46d05
- Reviewer: lime-cloud

## Findings

### P1 - EventStream can deadlock on close-without-terminal-event paths

**Problem**
`EventStream.Result()` blocks on `s.result` forever unless a `done`/`error` event writes to `result` (`Result()` at [docs/plans/01-ai-core.md:420](docs/plans/01-ai-core.md:420), writes only in `Send()` for `EventDone`/`EventError` at [docs/plans/01-ai-core.md:394](docs/plans/01-ai-core.md:394)). `Drain()` always calls `Result()` after channel close ([docs/plans/01-ai-core.md:427](docs/plans/01-ai-core.md:427)). The provider pattern explicitly says channel close can happen on panic via `defer es.Close()` ([docs/plans/01-ai-core.md:456](docs/plans/01-ai-core.md:456), [docs/plans/01-ai-core.md:470](docs/plans/01-ai-core.md:470)); in that path no terminal event is guaranteed, so `Complete()`/`Drain()` can hang permanently.

**Required fix**
Define a terminal-state contract enforced by `EventStream` itself: `Close()` must guarantee `Result()` unblocks exactly once (e.g., inject an internal terminal error like `ErrStreamClosedWithoutTerminalEvent` if no terminal event arrived), and document single-writer/single-terminal semantics. Add tests for panic/early-return/no-terminal-event and for double-terminal-event behavior.

---

### P1 - Model registry leaks mutable internal state via shallow copies

**Problem**
`Model` contains mutable reference fields (`Headers map[string]string`, `Compat` pointer with map members) ([docs/plans/01-ai-core.md:633](docs/plans/01-ai-core.md:633), [docs/plans/01-ai-core.md:644](docs/plans/01-ai-core.md:644), [docs/plans/01-ai-core.md:657](docs/plans/01-ai-core.md:657), [docs/plans/01-ai-core.md:662](docs/plans/01-ai-core.md:662)). `GetModel` and `GetModels` return shallow copies from registry maps ([docs/plans/01-ai-core.md:581](docs/plans/01-ai-core.md:581), [docs/plans/01-ai-core.md:596](docs/plans/01-ai-core.md:596)). Callers can mutate returned nested maps/pointers and inadvertently mutate registry state across goroutines, breaking thread safety guarantees and causing data races.

**Required fix**
Specify and enforce immutability at registry boundaries: deep-copy mutable fields on register and on read, or make model internals immutable (no exposed maps/pointers). Add explicit tests proving caller mutation of returned models does not affect subsequent `GetModel`/`GetModels` results and passes under `-race`.

---

### P2 - Deterministic transform ordering claim conflicts with specified algorithm

**Problem**
The plan claims deterministic ordering by sorting pending tool call IDs before synthetic result insertion ([docs/plans/01-ai-core.md:1712](docs/plans/01-ai-core.md:1712)), but the provided algorithm iterates `pendingToolCalls` directly as a map in both flush paths ([docs/plans/01-ai-core.md:1176](docs/plans/01-ai-core.md:1176), [docs/plans/01-ai-core.md:1208](docs/plans/01-ai-core.md:1208)), which is nondeterministic in Go.

**Required fix**
Make the transform algorithm and tests explicitly deterministic: collect map keys, sort, then append synthetic tool results in sorted order. Add a reproducibility test that runs the same input many times and asserts byte-for-byte stable output.

---

## Summary

3 findings: 0 P0, 2 P1, 1 P2, 0 P3

**Verdict**: Approved with revisions
