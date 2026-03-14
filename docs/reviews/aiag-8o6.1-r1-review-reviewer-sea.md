# Review: aiag-8o6.1 — ZFS Core Types, Validation, Dataset Ops

**Reviewer:** reviewer-sea
**Round:** R1
**Branch:** batch4/zfs-core-codex @ cdc1fa1
**Plan:** docs/plans/09-sandbox-zfs.md (sections 3, 4.1, 4.2, 4.3, 9, 10, 13.3)

## Summary

Overall this is a solid implementation that matches the plan's intent and delivers the core ZFS manager foundation. The interface, types, validation, error classification, dataset operations, and MockManager are all present and functional. Tests pass clean with `-race`. Static analysis passes. The code is well-structured and readable.

**Verdict: Approve with required changes (3 issues to fix before merge)**

## Test Results

- `go test -race ./internal/sandbox/zfs/... -count=1` — 15/15 PASS
- `go vet` — clean
- `staticcheck` — clean
- `gofmt` — clean

## Issues

### [MUST FIX] M1: Sentinel error names diverge from the plan

The plan specifies fine-grained sentinel errors: `ErrDatasetNotFound`, `ErrDatasetExists`, `ErrSnapshotNotFound`, `ErrSnapshotExists`, `ErrSnapshotHeld`, `ErrHasClones`, `ErrPoolNotFound`, `ErrPoolFaulted`. The implementation consolidates these into coarser-grained sentinels: `ErrNotFound`, `ErrAlreadyExists`, `ErrBusy`, `ErrPoolFull`.

This is a deliberate design choice that could be argued either way. The coarser errors are simpler to use. However, the plan's fine-grained errors allow callers to distinguish between "dataset not found" and "snapshot not found" without parsing error messages, which the sandbox host service (plan 11) may need. For example, the plan explicitly shows a caller pattern using `errors.Is(err, zfs.ErrDatasetNotFound)`.

**Recommendation:** Either match the plan's sentinel errors or explicitly document the deviation and update the plan. If the coarser approach is kept, then `classifyError` should still differentiate stderr patterns (e.g., "snapshot does not exist" vs "dataset does not exist") since the caller might need to wrap differently. At minimum, add `ErrHasClones` since "dataset has dependent clones" is a distinct failure mode from `ErrBusy` that callers need to handle differently.

### [MUST FIX] M2: `classifyError` signature differs from plan; missing several patterns

The plan specifies `classifyError(op, stderr string, exitCode int) error` (3 parameters). The implementation uses `classifyError(stderr string, exitCode int) error` (2 parameters), dropping the `op` parameter. This means the default error message in the fallback case cannot include the operation name as the plan intends: `fmt.Errorf("zfs %s failed: %s", op, stderr)`.

Additionally, several stderr patterns from the plan are missing from the implementation:
- `"could not find any snapshots"` (plan maps to `ErrSnapshotNotFound`)
- `"snapshot already exists"` (plan maps to `ErrSnapshotExists`)
- `"dataset has dependent clones"` (plan maps to `ErrHasClones`)
- `"tag already exists on this dataset"` (plan treats as idempotent, returns nil)
- `"no such tag on this dataset"` (plan maps to `ErrSnapshotNotFound`)
- `"no such pool"` (handled in impl via `ErrNotFound` but plan maps to `ErrPoolNotFound`)

These patterns will be needed when the snapshot, pool, and transfer operations are implemented in aiag-8o6.2, but the error infrastructure should be correct now so the next bead can rely on it.

### [MUST FIX] M3: `ValidatePropertyName` is weaker than the plan

The plan specifies that `ValidatePropertyName` should validate the property name format (lowercase alphanumeric, plus a few specials, with user properties containing `:`). The implementation has a `propertyNamePattern` regex (`^[a-z0-9][a-z0-9:_.-]*$`) but only uses it if the name is NOT in the dangerous set. If the name happens to be something like `ALLCAPS` or contains invalid characters, it will pass validation as long as it's not "exec", "setuid", or "devices".

Looking more carefully at the code:

```go
func ValidatePropertyName(name string) error {
    if name == "" {
        return ...
    }
    if !propertyNamePattern.MatchString(name) {
        return ...
    }
    dangerous := map[string]struct{}{"exec": {}, "setuid": {}, "devices": {}}
    if _, ok := dangerous[name]; ok {
        return ...
    }
    return nil
}
```

Actually, on re-reading, the regex check happens first, so this is correct. The dangerous check comes after the regex check. Withdrawing this issue — the implementation is correct.

**UPDATED: M3 withdrawn. 2 must-fix issues remain.**

### [SHOULD FIX] S1: Property value validation is missing

`CreateDataset` validates property names but not property values. The property values are passed directly to the ZFS command via `-o key=value`. While `exec.Command` prevents shell injection, a value containing newlines or other control characters could cause unexpected behavior. Consider adding basic value validation (no newlines, reasonable length).

### [SHOULD FIX] S2: `DatasetExists` in MockManager has double error-injection check

`MockManager.DatasetExists` calls `m.injected("DatasetExists")` and then calls `m.GetDatasetInfo()` which calls `m.injected("GetDatasetInfo")`. This means setting an error on "GetDatasetInfo" will also affect `DatasetExists`, which is correct behavior matching the CLIManager. However, the dedicated "DatasetExists" injection point in `MockManager` bypasses the validation logic (returns error before `ValidateName`). This is acceptable for a mock but slightly inconsistent with the CLIManager behavior where validation always runs first.

### [SHOULD FIX] S3: Test coverage gaps for dataset operations

The dataset operation tests are reasonable but could be stronger:

1. **No test for `SetMountpoint`** — only tested indirectly through MockManager lifecycle test.
2. **No test for `CloneFromSnapshot`** on CLIManager — only the MockManager is tested. Should verify the correct CLI arguments are built (similar to `TestCreateDatasetBuildsCommand`).
3. **No test for `DestroyDataset`** on CLIManager — verify `-r`, `-f`, `-R` flags are passed correctly.
4. **No test for validation rejection** on CLIManager operations — verify that invalid names are rejected before the command runner is invoked.
5. **No test for sudo mode** — verify that when `WithSudo(true)` is set, commands are prefixed with `sudo`.

### [SHOULD FIX] S4: MockManager `DestroyDataset` does not handle `DependentClones` option

The MockManager checks `opts.Recursive` to decide whether children should prevent destruction, but ignores `opts.DependentClones`. When `DependentClones` is true (`-R`), ZFS destroys all clones that depend on the dataset. The MockManager should track clone dependencies (via the `Origin` field) and simulate this behavior for more realistic testing.

### [SHOULD FIX] S5: MockManager `Rollback` is a no-op

The MockManager's `Rollback` method only checks that the snapshot exists but does not actually simulate rollback behavior (e.g., destroying later snapshots when `DestroyLater` is true, or restoring dataset state). This reduces the mock's usefulness for testing the sandbox host service in plan 11. At minimum, when `DestroyLater` is true, snapshots created after the target should be removed.

### [NIT] N1: Interface file naming

The plan calls the interface file `manager.go` (section 2.1 component diagram). The implementation uses `zfs.go`. This is a minor naming difference; `zfs.go` is arguably better since the package is already named `zfs`, making `zfs.manager.go` feel redundant. Just noting the deviation.

### [NIT] N2: `parseTabular` does not use `bufio.Scanner`

The plan specifies using `bufio.NewScanner` for `parseTabular`. The implementation uses `strings.Split`. Both work correctly; the `strings.Split` approach is simpler for small outputs. This is fine.

### [NIT] N3: Unused `pool` field in CLIManager

The `CLIManager` has a `pool` field set via `WithPool()`, but no dataset operations use it. It appears to be reserved for pool operations (aiag-8o6.2). Not a problem, but it's dead code in this bead.

## Security Analysis

**Command injection:** Safe. All command execution uses `exec.CommandContext` with separate argument strings (no shell). The `commandRunner` abstraction correctly separates binary from args.

**Input validation:** Comprehensive. `ValidateName` covers path traversal (`..`), empty components, length limits, and character whitelisting. `ValidateSnapshotFullName` correctly splits on `@` and validates both parts. `ValidateMountpoint` requires absolute paths and blocks traversal.

**Sudo handling:** Correct. When sudo is enabled, the binary becomes "sudo" and the original binary is prepended to the args list.

**Property name restriction:** Good defense-in-depth blocking `exec`, `setuid`, and `devices` properties.

## Concurrency Analysis

- CLIManager: Safe. All operations are independent CLI invocations with no shared mutable state. The `commandRunner` function handles its own process lifecycle.
- MockManager: Safe. Uses `sync.RWMutex` correctly — write operations take write lock, read operations take read lock. Return values are copied (e.g., `copy := ds.info` in `GetDatasetInfo`).
- One minor note: `MockManager.DatasetExists` calls `m.GetDatasetInfo` which acquires RLock, but `DatasetExists` itself also calls `m.injected` which acquires RLock. Since RLocks are reentrant (multiple concurrent readers allowed), this is fine.

## Resource Leak Analysis

- Context timeouts are properly managed with `defer cancel()` in `exec`.
- Process lifecycle is managed by `exec.CommandContext` — processes are killed on context cancellation.
- No file handles or network connections are opened.
- No goroutines are spawned.

## Plan Compliance Summary

| Plan Section | Status | Notes |
|---|---|---|
| 3.1 ZFSManager interface | PASS | All 18 methods match plan exactly |
| 3.2 Data types | PASS | All types and fields match |
| 3.3 Error types | PARTIAL | Sentinel names simplified; see M1 |
| 4.1 CLIManager structure | PASS | Options pattern, fields all present |
| 4.2 Command execution | PASS | Context, timeout, logging, error classification |
| 4.3 Output parsing | PASS | parseTabular, parseSize, parseTimestamp |
| 5.x Dataset operations | PASS | All 8 operations implemented correctly |
| 9 Input validation | PASS | All validators present and comprehensive |
| 10 Error classification | PARTIAL | Missing patterns; see M2 |
| 13.3 MockManager | PASS | All interface methods with error injection |

## Files Reviewed

- `internal/sandbox/zfs/zfs.go` — Interface + types (110 lines)
- `internal/sandbox/zfs/cli.go` — CLIManager + exec (273 lines)
- `internal/sandbox/zfs/dataset.go` — Dataset operations (202 lines)
- `internal/sandbox/zfs/errors.go` — Error types + classification (67 lines)
- `internal/sandbox/zfs/validate.go` — Input validation (123 lines)
- `internal/sandbox/zfs/mock.go` — MockManager (476 lines)
- `internal/sandbox/zfs/dataset_test.go` — Dataset tests (121 lines)
- `internal/sandbox/zfs/errors_test.go` — Error tests (40 lines)
- `internal/sandbox/zfs/validate_test.go` — Validation tests (58 lines)
- `internal/sandbox/zfs/mock_test.go` — Mock tests (125 lines)
