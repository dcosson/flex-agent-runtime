# R2 Re-review: aiag-8o6.1 & aiag-8o6.2 — ZFS Core + Snapshot/Pool/Transfer Ops

**Reviewer:** reviewer-sea
**Round:** R2 (re-review of P2 fixes from R1)
**Branch:** batch4/zfs-ops @ f8fb01d
**Prior reviews:** aiag-8o6.1-r1-review-reviewer-sea.md, aiag-8o6.2-r1-review-reviewer-sea.md
**Date:** 2026-03-13

## Verdict: Approved

All P2 items from both R1 reviews have been addressed correctly.

## Test Results

- `go test -race ./internal/sandbox/zfs/... -count=1` — all PASS (unit, property, fault injection, deterministic, stress)
- No race conditions detected

## P2 Fix Verification

### R1-8o6.1 M1 (Sentinel errors too coarse) — RESOLVED

Fine-grained sentinels are now defined in `errors.go:12-19`: `ErrDatasetNotFound`, `ErrDatasetExists`, `ErrSnapshotNotFound`, `ErrSnapshotExists`, `ErrSnapshotHeld`, `ErrHasClones`, `ErrPoolNotFound`, `ErrPoolFaulted`. The coarse sentinels (`ErrNotFound`, `ErrAlreadyExists`) are retained at lines 28-29 for backward compatibility, and the `wrapNotFound`/`wrapExists` helpers (lines 59-65) use `errors.Join` so that `errors.Is` matches both fine-grained and coarse sentinels. This is a clean design.

### R1-8o6.1 M2 (classifyError missing op param and patterns) — RESOLVED

`classifyError` signature is now `classifyError(op, stderr string, exitCode int) error` (line 67), matching the plan. The default case uses `op` in the fallback message (line 104). All missing stderr patterns from the R1 review are now covered:

- `"could not find any snapshots"` (line 76) -> `ErrSnapshotNotFound`
- `"snapshot already exists"` (line 83) -> `ErrSnapshotExists`
- `"dataset has dependent clones"` (line 87) -> `ErrHasClones`
- `"tag already exists on this dataset"` (line 70) -> returns nil (idempotent)
- `"no such tag on this dataset"` (line 77) -> `ErrSnapshotNotFound`
- `"no such pool"` (line 79) -> `ErrPoolNotFound`
- `"snapshot has holds"` (line 89) -> `ErrSnapshotHeld`
- `"pool is faulted"` / `"state is faulted"` (line 95) -> `ErrPoolFaulted`

Tests cover the idempotent hold tag case (`TestClassifyErrorHoldIdempotent`) and dataset-not-found fine-grained matching.

### R1-8o6.2 P2-2 (parseRatio 1% bug) — RESOLVED

`parseRatio` in `cli.go:295-303` now unconditionally divides by 100 with a clear comment: `// zpool list -p emits integer percentages (0..100)`. The conditional `if v > 1` has been removed. The new `TestParseRatioPercentValues` test explicitly covers the `"1"` -> `0.01` case alongside `"0"` -> `0.0`, `"20"` -> `0.2`, and `"100"` -> `1.0`.

### R1-8o6.2 P2-3 (Missing compile-time interface assertions) — RESOLVED

`assertions.go` contains:
```go
var _ ZFSManager = (*CLIManager)(nil)
var _ ZFSManager = (*MockManager)(nil)
```
Both assertions in a dedicated file, which is clean.

### R1-8o6.2 P2-4 (Undocumented -F flag on Receive) — RESOLVED

`transfer.go:53-54` no longer uses `-F`. The command is `[]string{"receive", dataset}` with an explicit comment: `// Intentionally do not force rollback (-F); caller controls destructive behavior.`

### R1-8o6.2 P3-2 (MockManager.Send missing existence check) — RESOLVED

`mock.go:487-492`: `Send` now checks `m.snapshots[snapshot]` under `RLock` and returns `errors.Join(ErrNotFound, ErrSnapshotNotFound)` if missing. `EstimateSendSize` at lines 466-471 has the same check.

### R1-8o6.2 P3-3 (MockManager.Receive does not create dataset) — RESOLVED

`mock.go:514-522`: `Receive` now creates a dataset entry in `m.datasets` if one doesn't already exist, with default mountpoint and creation time.

## Remaining Notes

- The P3 items from R1 (test harness depth, deterministic sim coverage, benchmark style) were not blocking and remain as-is. These are acceptable for this bead scope.
- The S-level items from the 8o6.1 R1 (property value validation, mock `DependentClones` handling, test coverage gaps) remain as future improvements but are not blockers.
- MockManager errors consistently use `errors.Join(ErrNotFound, ErrDatasetNotFound)` pattern, matching the `wrapNotFound`/`wrapExists` approach in `classifyError`. This is consistent.

## Files Reviewed

- `internal/sandbox/zfs/errors.go` — Fine-grained sentinels, `classifyError` with op param
- `internal/sandbox/zfs/cli.go` — `parseRatio` unconditional divide-by-100
- `internal/sandbox/zfs/assertions.go` — Compile-time interface assertions
- `internal/sandbox/zfs/transfer.go` — Receive without `-F`, explicit comment
- `internal/sandbox/zfs/mock.go` — Send/EstimateSendSize existence checks, Receive dataset creation
- `internal/sandbox/zfs/errors_test.go` — Tests for fine-grained classifyError and idempotent hold
- `internal/sandbox/zfs/cli_parse_test.go` — parseRatio boundary tests
- `internal/sandbox/zfs/mock_test.go` — Send/Receive lifecycle test
