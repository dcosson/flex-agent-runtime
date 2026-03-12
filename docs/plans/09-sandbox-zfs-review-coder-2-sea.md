# 09: ZFS Dataset & Snapshot Management — Review Findings (coder-2-sea, R1)

**Plan:** [09-sandbox-zfs.md](./09-sandbox-zfs.md)
**Test Harness:** [09-sandbox-zfs-test-harness.md](./09-sandbox-zfs-test-harness.md)
**Reviewer:** coder-2-sea
**Round:** 1
**Date:** 2026-03-11

---

## Summary

The plan and test harness are well-structured and cover the core ZFS management surface area. The main gaps are around mock/real implementation conformance, missing test coverage for pool import/export, and underspecified CLI command timeout behavior.

## Findings

### [F1] MockManager may diverge from CLIManager behavior — P2

**Section:** Test Harness, Property Tests (P4)
**Issue:** Property test P4 verifies MockManager consistency with expected ZFS semantics, but there is no mechanism to ensure MockManager stays in sync with CLIManager as the implementation evolves. Over time, behavioral drift between the mock and the real implementation would silently degrade the value of all tests that rely on MockManager.
**Recommendation:** Add a conformance test suite that runs the same operation sequences against both MockManager and a real ZFS pool (in the integration test tier), comparing results. This ensures the mock faithfully reproduces real CLI behavior and catches drift early.

### [F2] SetMountpoint path validation is ambiguously specified — P3

**Section:** Test Harness, Section 2 (SetMountpoint)
**Issue:** The test harness notes "ZFS itself may allow this, but our wrapper should reject obvious traversal — (or the sandbox host service layer handles this — document either way)." This leaves it unclear whether path validation is the responsibility of the ZFS manager or the sandbox host service layer, which could lead to inconsistent implementations or missed validation.
**Recommendation:** Validate mountpoint paths in the ZFS manager itself as a defense-in-depth measure. Document in the plan that the sandbox host service also validates at a higher level, but the ZFS manager rejects obvious path traversal (e.g., `..` components, non-absolute paths) regardless.

### [F3] No specification of command execution timeout defaults — P2

**Section:** Plan, CLIManager / FI1 (Fault Injection)
**Issue:** The CLIManager shells out to zfs/zpool commands. The plan mentions context cancellation in FI1 but does not specify whether all CLI commands receive a default timeout context or whether each caller must supply one. A hung zfs command (e.g., `zfs send` on a degraded pool) with no timeout could block indefinitely and stall the sandbox host.
**Recommendation:** Specify a configurable default command timeout in CLIManager. Use shorter defaults for metadata operations (e.g., 30s for list, get, set, snapshot, destroy) and longer/configurable timeouts for data-intensive operations (send/recv). Callers should still be able to override via their own context deadlines.

### [F4] Benchmark B3 rollback test measures snapshot creation + rollback combined — P3

**Section:** Test Harness, Benchmarks (B3)
**Issue:** The BenchmarkRollback test creates a snapshot inside the `b.N` loop, which means it benchmarks snapshot creation and rollback together rather than isolating rollback latency. This makes it impossible to determine the actual cost of rollback alone.
**Recommendation:** Pre-create a pool of snapshots before the benchmark loop begins and only measure rollback operations within `b.N`. This isolates rollback latency and produces actionable performance data.

### [F5] Pool import/export in interface but not tested — P2

**Section:** Plan (interface scope) vs. Test Harness (coverage gap)
**Issue:** The plan lists pool import and export as in-scope operations on the ZFSManager interface, but the test harness contains no tests for pool import/export. These operations are operationally critical for EBS volume migration scenarios.
**Recommendation:** Add integration tests covering the full pool export/import cycle: create pool with data, export, verify pool is no longer visible, import, verify data integrity post-import. Also test error cases such as importing an already-imported pool and exporting a pool with busy datasets.

### [F6] Stress test ST1 snapCount tracking may drift — P3

**Section:** Test Harness, Stress Tests (ST1)
**Issue:** In the rapid snapshot cycle stress test, `snapCount` is manually tracked and adjusted on rollback. If the rollback target calculation is wrong (e.g., off-by-one in `snapCount-5`), the final assertion will fail spuriously, making the test flaky for reasons unrelated to the code under test.
**Recommendation:** Replace manual `snapCount` tracking with calls to `ListSnapshots` to verify the actual snapshot count at assertion points. This makes the test self-correcting and eliminates a class of false failures from bookkeeping errors in the test itself.

---

## Statistics

- Total findings: 6
- P0 (blocking): 0
- P1 (significant): 0
- P2 (moderate): 3
- P3 (minor): 3
