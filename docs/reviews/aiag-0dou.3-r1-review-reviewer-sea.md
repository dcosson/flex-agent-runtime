# Code Review: aiag-0dou.3 (R1, reviewer-sea)

- Bead: aiag-0dou.3
- Commit range: 4ce3a70..abe9249
- Plan doc: docs/plans/11-sandbox-host-service.add01.md §5.4
- Reviewer: reviewer-sea
- Review commit: abe9249

## Findings

### P2 - `doJSON` and `cloneLabels` duplicated from E2B

**Location:** `internal/sandbox/environment/daytona/daytona.go:308-449`

**Problem**
`doJSON` (HTTP helper with auth, error body truncation, JSON encode/decode) and `cloneLabels` are identical copies from `internal/sandbox/environment/e2b/e2b.go`. The `executeFileOp` and `executeCommand` methods are also nearly identical. With Fly as a third provider, this pattern will be copied again.

**Suggested fix**
Extract a shared `httputil` or `apiutil` package under `internal/sandbox/environment/` with the common `doJSON` helper and maybe `executeFileOp`/`executeCommand` if the API shape is consistent across providers. This can be a follow-up bead — it doesn't block this review.

---

### P3 - Compliance test includes Fly import from parallel work

**Location:** `internal/sandbox/environment/compliance_test.go:17,25-88`

**Problem**
The compliance test diff includes `TestFlyEnvironmentComplianceSuite` and an import of `internal/sandbox/environment/fly`. This is from coder-1-sea's parallel work (aiag-0dou.4), not from this commit. Noting for awareness — no issue since both are landing to main.

**Suggested fix**
No fix needed.

---

### P3 - `created` and `labels` fields stored but not exposed (same as E2B)

**Location:** `internal/sandbox/environment/daytona/daytona.go:216-217`

**Problem**
Same pattern as E2B — `created` and `labels` are stored in Create but never read. Consistent with E2B, so not a concern for now.

**Suggested fix**
Address when E2B addresses it (or in a shared refactor).

---

## Summary

3 findings: 0 P0, 0 P1, 1 P2, 2 P3

**Verdict**: Approved with revisions

The implementation is clean and well-tested:
- Correctly follows E2B pattern with write-lock lifecycle (Destroy uses `e.mu.Lock()` across HTTP call)
- Pause/Resume/Snapshots/Rollback properly return `ErrCapabilityNotSupported`
- Good test coverage: create payload/options/validation, file/command routing, lifecycle, capabilities, error handling, idempotent destroy
- Compliance suite integration ensures behavioral contract
- Daytona-specific options (target, git URL, env vars) properly handled

The P2 (shared HTTP helper duplication) is a follow-up item, not blocking. Code is correct and consistent.
