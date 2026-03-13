# Code Review: aiag-qkh.5 (R1, reviewer-sea)

- Bead: aiag-qkh.5
- Commit range: 73f7805..a935251
- Plan doc: docs/plans/01-ai-core-test-harness.md §2 (O1, O2, O3)
- Reviewer: reviewer-sea
- Review commit: a935251

## Findings

### P3 - Dead variable `isErr` in goMsgToWire AssistantMessage case

**Location:** `internal/ai/oracle_test.go:102,129`

**Problem**
The `goMsgToWire` function declares `isErr := false` for the AssistantMessage case, then immediately blanks it with `_ = isErr`. This is dead code left over from development — assistant messages use `ErrorMessage` (string) rather than `IsError` (bool).

**Suggested fix**
Delete lines 102 and 129 (`isErr := false` and `_ = isErr`).

---

### P3 - Misleading init() comment

**Location:** `internal/ai/oracle_test.go:1297-1301`

**Problem**
```go
func init() {
    // Suppress slog debug output during tests.
    _ = strings.Contains
    _ = os.Stderr
}
```
The comment says "Suppress slog debug output" but the function only blanks imported symbols to avoid "imported but not used" compiler errors. Either the suppression logic was removed without updating the comment, or the comment is simply wrong.

**Suggested fix**
If `strings` and `os` are only used in the init() block, remove the init function and the unused imports. If they're used elsewhere in the file (they are — `strings` in `isSyntheticToolResult` comparison and `os` for `Stderr` piping to harness), then remove just the init function since the imports are legitimately used by other code.

---

### P3 - Pre-generated corpus file lacks staleness guard

**Location:** `testdata/transform_corpus.json` (9383 lines)

**Problem**
The pre-generated corpus file is committed alongside the programmatic corpus builder `buildTransformCorpus()`. If the builder is modified (new test categories, different parameters), the file becomes stale with no mechanism to detect this. The O1/O2/O3 tests use `buildTransformCorpus()` directly, so test correctness is not affected — the stale file only impacts external consumers using `loadTestCorpus()`.

**Suggested fix**
Add a `TestCorpusFileUpToDate` test that regenerates the corpus and compares it against the committed file. This would catch staleness during regular test runs. Alternatively, add a comment in the file noting it must be manually regenerated via `go test -run TestWriteCorpusFile`.

---

## Plan Compliance Check

| Deliverable | Status |
|---|---|
| O1: TransformMessages comparison oracle (100+ corpus) | ✅ (110 cases) |
| O1: Node.js harness importing @mariozechner/pi-ai | ✅ |
| O1: Corpus diversity (text, tool calls, multi-tool, cross-model, thinking, error/aborted, orphaned, mixed) | ✅ |
| O2: CalculateCost comparison oracle | ✅ (21 cases, 3 models × 7 usage patterns) |
| O3: IsContextOverflow comparison oracle | ✅ (25 cases: 15 overflow, 4 empty body, 1 silent, 5 non-overflow) |
| Wire format types for JSON serialization | ✅ |
| Go↔Wire conversion functions (all message and content types) | ✅ |
| TS subprocess harness (stdin/stdout JSONL protocol) | ✅ |
| Pre-generated corpus file for external consumers | ✅ |
| transform.go fix (empty content assistant messages) | ✅ |

## Test Results

| Test | Status | Notes |
|---|---|---|
| TestO1_TransformMessagesVsTypeScript | ✅ | 110 subtests, all pass |
| TestO2_CalculateCostVsTypeScript | ✅ | 21 subtests, all pass |
| TestO3_IsContextOverflowVsTypeScript | ✅ | 25 subtests, all pass |
| TestCorpusSize | ✅ | 110 transform, 21 cost, 25 overflow |
| TestWriteCorpusFile | ✅ | Generates and verifies loadability |
| All tests pass with `-race` | ✅ | 0 race warnings |

## Implementation Quality Notes

- **Oracle-discovered bug**: The comparison testing found that `transformAssistantMessage` was dropping assistant messages with empty content (after filtering). The TS reference preserves these. Fix correctly removes the `len(newContent) == 0` nil return guard while keeping the error/aborted nil return.
- **Wire format design**: Clean flat JSON structure using role discrimination. All mutable fields properly converted in both directions. `omitempty` tags correctly applied.
- **Harness process management**: `newTSHarness` correctly skips tests if node_modules aren't installed, uses `t.Cleanup` for process lifecycle, and pipes stderr for debugging.
- **Comparison methodology**: O1 uses JSON serialization comparison with timestamp zeroing for synthetic results. O2 uses floating-point epsilon comparison (1e-12). O3 uses direct boolean equality. All appropriate for their domains.
- **Corpus generation**: Programmatic generation in Go ensures reproducibility and easy extension. 110 cases cover all categories specified in the plan.
- **Bead process**: Bead correctly left open (status: in_progress) for review — process improvement from prior beads.

## Summary

3 findings: 0 P0, 0 P1, 0 P2, 3 P3

**Verdict**: Approved

Excellent implementation of the comparison oracle test suite. All three oracle specs (O1, O2, O3) are fully implemented with comprehensive corpus coverage exceeding the 100+ minimum. The oracle testing proved its value by discovering a real transform bug (empty content assistant messages). The TS harness design is clean and maintainable. The three P3 findings are minor code cleanliness issues.
