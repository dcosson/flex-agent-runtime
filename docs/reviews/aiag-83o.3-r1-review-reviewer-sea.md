# Code Review: aiag-83o.3 (R1, reviewer-sea)

- Bead: aiag-83o.3
- Commit range: 6d6d607..193c462
- Plan doc: docs/plans/11-sandbox-host-service.add01-test-harness.md
- Reviewer: reviewer-sea
- Review commit: 193c462

## Findings

### P2 - ST1 uses 25 goroutines but plan specifies 50

**Location:** `internal/sandbox/environment/harness_stress_test.go:23`

**Problem**
The test harness plan (ST1) specifies "50 goroutines each creating, using, and destroying environments." The implementation uses 25. Similarly, ST2 uses 50 concurrent ExecuteTool calls but the plan says 100.

These aren't blocking but should match the plan spec to ensure the stress tests exercise the concurrency surface as intended.

**Suggested fix**
Increase to 50 goroutines in ST1 and 100 in ST2, or update the test harness doc to reflect the actual values with rationale (e.g., CI timeout constraints).

---

### P2 - P5 duplicates TestLocalEnvironmentParityWithLocalBackend from local_test.go

**Location:** `internal/sandbox/environment/harness_property_test.go:161-186`

**Problem**
`TestP5_LocalProviderParity` is nearly identical to `TestLocalEnvironmentParityWithLocalBackend` in `local/local_test.go` — both create a LocalEnvironment and LocalBackend, run the same read_file tool request through both, and compare content lengths. The duplication isn't harmful but is unnecessary. The harness plan (P5) specifies parity between NativeSandboxEnvironment and the old SandboxClient path, not LocalEnvironment vs LocalBackend (which is already tested).

**Suggested fix**
Either (a) remove P5 from the harness since parity is covered by `local_test.go`, or (b) change P5 to test NativeSandboxEnvironment vs SandboxClient as the plan specifies (using the mock service). Option (b) is the correct implementation per the test harness doc.

---

### P3 - SEC2 tests empty path but not path traversal

**Location:** `internal/sandbox/environment/harness_fault_oracle_security_test.go:128-151`

**Problem**
The test is named `TestSEC2_LocalPathTraversalSanitization` but only tests an empty path parameter. The test harness plan (SEC2) specifies testing session IDs with path traversal characters (`../`, `..\\`), null bytes, and very long IDs. The current test checks none of these — it verifies empty path returns a validation error, which is useful but doesn't test path traversal.

SEC3 in the plan specifies file path parameters with path traversal — that's what this test's name implies but doesn't actually test.

**Suggested fix**
Add test cases for `"../../../etc/passwd"` and `"..\\windows\\system32"` paths to verify the tool execution layer rejects or sandboxes them. Note: the underlying tool implementations may handle this, but the security test should verify it at the environment level.

---

### P3 - ST3 doesn't test concurrent execution across environments

**Location:** `internal/sandbox/environment/harness_stress_test.go:74-106`

**Problem**
The test harness plan (ST3) specifies creating 10 Local + 10 E2B environments and executing tools on all 20 concurrently. The implementation creates environments sequentially (one local, one native per iteration) and destroys each pair before creating the next. There's no concurrent execution across multiple environments.

This is understandable since E2B isn't implemented yet, but even with Local + Native, the test could create all environments first, then execute concurrently across all of them, then destroy. The current serial approach doesn't test cross-environment concurrency.

**Suggested fix**
Restructure to create N local + N native environments, execute tools concurrently across all of them, then destroy. This better matches the plan's intent of testing cross-environment state isolation.

---

## Summary

4 findings: 0 P0, 0 P1, 2 P2, 2 P3

**Verdict**: Approved with revisions

## R1 Disposition

All findings addressed at bd72a78. Re-reviewed and verified.

| # | Severity | Summary | Disposition | Notes |
|---|----------|---------|-------------|-------|
| 1 | P2 | ST1/ST2 goroutine counts below plan spec | Incorporated | ST1 now 50, ST2 now 100 — matches plan. |
| 2 | P2 | P5 duplicates local parity test | Incorporated | Reworked to NativeSandboxEnvironment parity vs direct service stream path. Correct per plan. |
| 3 | P3 | SEC2 missing path traversal cases | Incorporated | Now tests empty, `../../../etc/passwd`, and `..\\windows\\system32` paths. |
| 4 | P3 | ST3 sequential instead of concurrent | Incorporated | Creates 10 local + 10 native, executes concurrently across all 20, then destroys. |

**Final verdict**: Approved
