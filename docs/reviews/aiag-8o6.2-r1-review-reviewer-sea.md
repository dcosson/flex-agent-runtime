# Code Review: aiag-8o6.2 (R1, reviewer-sea)

- Bead: aiag-8o6.2
- Commit range: cdc1fa1..80a6ba5 (on branch batch4/zfs-ops)
- Plan doc: docs/plans/09-sandbox-zfs.md (sections 4, 5, 6, 7, 8), docs/plans/09-sandbox-zfs-test-harness.md
- Reviewer: reviewer-sea
- Date: 2026-03-13

## Findings

### P2-1: Sentinel errors collapsed — plan specifies fine-grained, implementation uses coarse

**Location:** `internal/sandbox/zfs/errors.go:9-17`

**Problem**
The plan (section 10.1) specifies 13 sentinel errors with fine granularity: `ErrDatasetNotFound`, `ErrSnapshotNotFound`, `ErrPoolNotFound`, `ErrDatasetExists`, `ErrSnapshotExists`, `ErrSnapshotHeld`, `ErrHasClones`, `ErrPoolFaulted`, etc. The implementation collapses these into 8 coarser sentinels: `ErrNotFound` (absorbs dataset/snapshot/pool not-found), `ErrAlreadyExists` (absorbs dataset/snapshot exists), and drops `ErrSnapshotHeld`, `ErrHasClones`, and `ErrPoolFaulted` entirely.

The plan's `classifyError` also handles special cases like `"tag already exists on this dataset"` (idempotent hold, returns nil) and `"dataset has dependent clones"` (returns `ErrHasClones`), none of which are in the implementation's `classifyError`.

While the downstream consumer (plan 11, sandbox host service) does not currently reference these specific fine-grained error sentinels, the plan defines them as the API contract for this package. Future callers may need to distinguish "snapshot not found" from "dataset not found" for proper error handling (e.g., distinguishing a missing session dataset from a missing snapshot in a rollback path).

**Suggested fix**
Either:
(a) Add the fine-grained sentinels from the plan and update `classifyError` to distinguish them, OR
(b) Document in the code that the coarse sentinels are an intentional simplification and callers should use `ZFSError.Op` or `ZFSError.Stderr` if finer discrimination is needed.

Option (a) is preferred for plan compliance.

---

### P2-2: `parseRatio` boundary bug at exactly 1%

**Location:** `internal/sandbox/zfs/cli.go:316-324`

**Problem**
```go
func parseRatio(s string) (float64, error) {
    // ...
    if v > 1 {
        return v / 100, nil
    }
    return v, nil
}
```

ZFS with `-p` (parseable) flag outputs capacity and fragmentation as integer percentages (e.g., `20` for 20%, `5` for 5%). The function correctly divides values greater than 1 by 100. However, for a value of exactly `1` (1% capacity), the condition `v > 1` is false, so 1.0 is returned as-is — meaning a pool at 1% capacity would report capacity=1.0 (100%). The condition should be `v > 1.0` changed to something like always dividing by 100 when the value could be a ZFS percentage, or using `v >= 2` (ZFS never outputs fractional percentages with `-p`). The simplest correct fix:

**Suggested fix**
Since ZFS `-p` always emits whole-number percentages, the safest approach is:
```go
// ZFS -p flag always outputs integer percentages (0-100).
return v / 100, nil
```
Remove the conditional entirely. A value of `0` divided by 100 is still 0.0. If there's concern about non-`-p` output somehow arriving here (already a fractional), add a comment that this parser assumes `-p` flag is always used.

---

### P2-3: Missing compile-time interface compliance assertions

**Location:** (absent from codebase)

**Problem**
Neither `CLIManager` nor `MockManager` has a compile-time assertion that it implements `ZFSManager`. This is standard Go practice for ensuring interface compliance is caught at compile time rather than runtime:

```go
var _ ZFSManager = (*CLIManager)(nil)
var _ ZFSManager = (*MockManager)(nil)
```

If a method signature drifts between the interface and either implementation, this catches it immediately.

**Suggested fix**
Add both assertions, either in `zfs.go` (for CLIManager) and `mock.go` (for MockManager), or both in `zfs.go`.

---

### P2-4: `Receive` adds `-F` flag not in the plan

**Location:** `internal/sandbox/zfs/transfer.go:43`

**Problem**
The implementation uses `[]string{"receive", "-F", dataset}` but the plan specifies `[]string{"receive", dataset}`. The `-F` flag forces a rollback of the receiving dataset to its most recent snapshot before applying the incoming stream. For fresh receives (new dataset) this is harmless, but for incremental receives into an existing dataset it could silently destroy uncommitted changes. This should be an intentional, documented decision, not an unexplained deviation.

**Suggested fix**
Either:
(a) Remove `-F` to match the plan, OR
(b) Add a code comment explaining why `-F` is used (e.g., "Force rollback for idempotent receive — safe because we only receive into fresh or fully-snapshotted datasets") and consider making it configurable via `ReceiveOptions`.

---

### P3-1: `classifyError` signature diverges from plan

**Location:** `internal/sandbox/zfs/errors.go:49`

**Problem**
The plan specifies `classifyError(op, stderr string, exitCode int) error` (3 params), using `op` in the default error message (`"zfs %s failed: %s"`) and potentially for operation-specific classification. The implementation uses `classifyError(stderr string, exitCode int) error` (2 params). The `op` is instead captured in the `ZFSError.Op` field at the call site, so information is not lost — but the default error message becomes generic (`"zfs command failed: %s"`) without the operation name.

**Suggested fix**
Minor. Consider passing `op` through for a more helpful default error message, or leave as-is since `ZFSError.Op` provides the same context at a higher level.

---

### P3-2: `MockManager.Send` does not check snapshot existence

**Location:** `internal/sandbox/zfs/mock.go:402-418`

**Problem**
`MockManager.Send` validates the snapshot name but does not check whether the snapshot actually exists in the mock's internal state. A real ZFS send would fail if the snapshot doesn't exist. The mock should check `m.snapshots[snapshot]` and return `ErrNotFound` if missing, for behavioral fidelity with the real implementation.

Similarly, `MockManager.EstimateSendSize` does not check snapshot existence.

**Suggested fix**
Add existence checks:
```go
m.mu.RLock()
_, ok := m.snapshots[snapshot]
m.mu.RUnlock()
if !ok {
    return ErrNotFound
}
```

---

### P3-3: `MockManager.Receive` does not create a dataset entry

**Location:** `internal/sandbox/zfs/mock.go:427-438`

**Problem**
Real `zfs receive` creates a new dataset (or updates an existing one). `MockManager.Receive` reads and discards the input but does not create a dataset entry in `m.datasets`. This means `DatasetExists` after `Receive` returns false, which is inconsistent with real ZFS behavior. For mock fidelity, `Receive` should create a dataset entry.

**Suggested fix**
Add dataset creation in `Receive`, similar to `CreateDataset` but with appropriate defaults.

---

### P3-4: Test harness fault injection tests are shallow compared to plan

**Location:** `internal/sandbox/zfs/harness_test.go:100-145`

**Problem**
The test harness plan (09-sandbox-zfs-test-harness.md) specifies detailed fault injection tests including:
- FI1: Context cancellation during a long `zfs send` with verification that no zombie processes are left and subsequent operations work
- FI2: Disk space exhaustion with verification of `ErrNoSpace` classification and rollback still working
- FI3: Concurrent destroy during operations
- FI4: Double destroy returning `ErrDatasetNotFound`
- FI5: Rollback to nonexistent snapshot

The implementation has FI1-FI5 tests but they are simplified stubs:
- FI1 just tests a pre-cancelled context, not a mid-operation cancellation
- FI2 just verifies `classifyError` returns `ErrNoSpace` for the right stderr string (pure unit test, not integration)
- FI3 is an injected permission error test, not concurrent destroy
- FI4 tests injected busy error, not double destroy
- FI5 tests injected not-found error, not an actual rollback to nonexistent

These are all valid unit tests, but they don't match the plan's fault injection scenarios. The plan's tests are integration-tagged (requiring real ZFS), so it's acceptable to defer them, but the current tests should not be named `TestFI*` if they don't match the plan's FI scenarios — it creates confusion about what's been tested.

**Suggested fix**
Either:
(a) Rename the current tests to reflect what they actually test (e.g., `TestInjectedPermissionError`), OR
(b) Add a comment at the top of the FI section: `// FI tests below exercise error paths via mock injection. Full FI1-FI5 from the test harness plan require real ZFS and are integration-tagged.`

---

### P3-5: Deterministic simulation tests are minimal

**Location:** `internal/sandbox/zfs/harness_test.go:147-181`

**Problem**
The plan's DS1 test specifies 100 random operations with state verification after each. DS2 specifies creating 10 snapshots, rolling back to snap-005, and verifying snap-000 through snap-005 exist. DS3 specifies clone independence with filesystem writes.

The implementation's DS1 is a simple create-snapshot-destroy trace. DS2 creates 2 snapshots, rolls back to s0, and checks only 1 remains. DS3 just clones without verifying independence. These test basic happy paths but miss the property being tested (random operation sequences, multi-snapshot rollback boundary, filesystem isolation).

**Suggested fix**
Enhance DS2 to use more snapshots (at least 5) and verify the exact set remaining. DS1 could use a small random operation generator. DS3 is acceptable as-is since filesystem isolation requires real ZFS.

---

### P3-6: Benchmark functions use `b.Loop()` which is not standard

**Location:** `internal/sandbox/zfs/harness_test.go:220-254`

**Problem**
The benchmarks use `b.Loop()` instead of the traditional `for i := 0; i < b.N; i++` pattern. This is the new Go 1.24 benchmark API which is slightly more ergonomic. The project's `go.mod` specifies `go 1.24.3`, so this is fine.

**Suggested fix**
No action needed. Noting for awareness only — this is the correct modern Go pattern.

---

### P3-7: Missing test for `ValidateName` with names containing `@`

**Location:** `internal/sandbox/zfs/validate_test.go`

**Problem**
The validation tests include `"pool/x@bad"` as an invalid name case, which correctly validates that `@` is rejected in dataset names. However, there's no explicit test for snapshot full names containing multiple `@` signs (e.g., `"pool/ds@snap@extra"`). The `ValidateSnapshotFullName` uses `SplitN(name, "@", 2)` which handles this correctly (the second part would be `"snap@extra"` which fails `ValidateSnapshotName`), but an explicit test would document this edge case.

**Suggested fix**
Low priority. Add a test case for `ValidateSnapshotFullName("pool/ds@snap@extra")` to document the expected behavior.

---

## Summary

10 findings: 0 P0, 0 P1, 4 P2, 6 P3

**Verdict**: Conditional approval pending P2 fixes

**P2 items requiring action:**
1. Error sentinel granularity mismatch with plan — at minimum document the deviation, ideally add fine-grained sentinels
2. `parseRatio` boundary bug at 1% capacity — real correctness issue, should be fixed
3. Missing compile-time interface assertions — trivial to add, prevents drift
4. Undocumented `-F` flag on `Receive` — document or remove

**Review notes:**
- All tests pass with `-race` flag, no race conditions detected
- `go vet` and `gofmt` clean on the ZFS package (existing staticcheck warnings are in unrelated packages)
- Code structure follows the plan well: separate files for dataset, snapshot, pool, transfer, validation, errors, cli, mock
- CLI executor design with injectable `commandRunner`/`streamRunner` is good for testability
- Input validation is thorough — all entry points validate names before reaching CLI
- MockManager has proper mutex discipline with consistent `RLock`/`Lock` usage
- Property-based tests (P1-P5) are well-structured with rapid
- Security tests cover key injection vectors
- Concurrent stress tests (ST1-ST3) exercise the mock's thread safety
- The harness tests, while simplified from the plan's integration-level specs, provide reasonable unit-level coverage
- Overall code quality is solid with clean Go idioms
