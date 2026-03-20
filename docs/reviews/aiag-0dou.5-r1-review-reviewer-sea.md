# Code Review: aiag-0dou.5 (R1, reviewer-sea)

- Bead: aiag-0dou.5
- Commit range: e09340e..a32295e
- Plan doc: docs/plans/11-sandbox-host-service.add01-test-harness.md (deferred items §F5, O1-O3, SEC1, SEC3-SEC4, E2E3-E2E5)
- Reviewer: reviewer-sea
- Review commit: a32295e

## Findings

### P3 - E2E5 exercises 2 tools instead of plan's "5-tool sequence"

**Location:** `internal/sandbox/environment/harness_e2e_remote_test.go:246-278`

**Problem**
The test harness plan §E2E5 says "Run same 5-tool sequence through [each environment]." The implementation only exercises `read_file` and `bash` (2 tools). Missing: `write_file`, `edit_file`, and one of `grep`/`glob`.

This is not blocking — the compliance suite already covers these paths per-provider, and the transparency test's primary goal (consistent `ToolResponse` structure across all 5 environments) is met with 2 tools. But adding `write_file` at minimum would strengthen the cross-provider parity check.

**Suggested fix**
Add `write_file` and optionally one more tool to the E2E5 sequence. Low priority.

---

### P3 - O1 golden does not verify all plan-specified payload fields

**Location:** `internal/sandbox/environment/harness_fault_oracle_security_test.go:415-429`

**Problem**
Plan §O1 specifies verifying `template_id`, `timeout`, and `metadata` in the create payload. The test checks `template_id`, `session_id`, and `timeout_seconds` — but not `metadata`. This is minor since the implementation may not send metadata, and the test correctly verifies the actual wire format. Noting for completeness.

**Suggested fix**
No fix needed. If metadata is added later, extend the golden.

---

### P3 - F5 SSH error messages don't include contextual info per plan spec

**Location:** `internal/sandbox/environment/harness_fault_oracle_security_test.go:649-658`

**Problem**
Plan §F5 specifies errors should include contextual information: "error with machine ID context" for connection refused, "error suggesting key configuration" for auth failure, "error with duration context" for timeout. The tests only verify the error wraps `fly: execute bash` and the raw error text. The underlying implementation may or may not include this context — the test doesn't enforce it.

This is a plan deviation but not a code bug. The error messages from the Fly provider are still clear and actionable.

**Suggested fix**
Consider a follow-up to enrich Fly SSH error messages with machine ID and duration context, then update the tests to verify. Not blocking.

---

## Summary

3 findings: 0 P0, 0 P1, 0 P2, 3 P3

**Verdict**: Approved

The implementation is thorough and well-structured:
- **O1-O3 wire format goldens** properly record HTTP calls with `sync.Mutex`-protected call slices, verifying create payloads, lifecycle endpoints, and tool execution bodies for all three remote providers
- **F5 SSH failure scenarios** cover 4 error types (connection refused, auth failed, timeout, connection dropped) plus context timeout propagation — each with isolated environment instances
- **SEC1 API key handling** verifies Bearer token in header (not URL/error) for all 3 providers on auth failure paths
- **SEC3 parameter sanitization** verifies Fly shell quoting blocks injection (`unsafe'; touch /tmp/pwned; echo '` → properly quoted) and command pass-through for E2B/Daytona
- **SEC4 SSH key handling** confirms invalid keys don't leak material in errors and missing pinned host key is rejected
- **E2E3** exercises full E2B lifecycle (create, read, bash with progress, snapshot rejection, pause, resume, destroy)
- **E2E4** validates destroy+recreate fallback path with call counting
- **E2E5** runs transparency checks across all 5 provider types with factory pattern

All 3 findings are P3 — minor plan deviations that don't affect correctness or coverage quality. The deferred test harness items from Batch 5 are now implemented.
