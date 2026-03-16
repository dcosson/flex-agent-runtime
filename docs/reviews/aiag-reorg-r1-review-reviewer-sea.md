# Code Review: test directory reorg (R1, reviewer-sea)

- Bead: N/A (ad-hoc refactoring)
- Commit range: eb3225f..eecc198
- Plan doc: N/A
- Reviewer: reviewer-sea
- Review commit: eecc198

## Findings

### P1 - CI workflow still references old e2etests/ paths

**Location:** `.github/workflows/e2e.yml:17,31,34,43,60,62,63,85,94`

**Problem**
The CI workflow was not updated as part of the reorg. All 8 references to `e2etests/external/` still point to the old directory:
- Line 17: `./e2etests/external/tier1/...`
- Lines 31, 43, 60, 63: `e2etests/external/docker/docker-compose.e2e.yaml`
- Line 34: `./e2etests/external/tier2/...`
- Line 62: `./e2etests/external/tier2/...`
- Line 85: `./e2etests/external/tier3/...`
- Line 94: `e2etests/external/reports/`

All CI jobs will fail on PR since the paths no longer exist.

**Suggested fix**
Replace all `e2etests/external/` with `tests/external/` throughout the file. Also update `go-version: '1.23'` to `'1.24'` while touching the file (it's currently mismatched with go.mod's `go 1.24.3`).

---

### P3 - Pre-existing test failure: TestP5_ThresholdRuleSoundness

**Location:** `tests/integration/runtime/tests/property_test.go:392`

**Problem**
`make test` fails due to `TestP5_ThresholdRuleSoundness` — a property test with a floating point boundary condition (deltaPct == tolerance produces disagreement on regression classification). This is pre-existing (fails identically before and after the reorg) and not introduced by this change.

**Suggested fix**
Not in scope for this review. Recommend creating a bead to fix the boundary condition (use strict `>` instead of `>=` or vice versa in the threshold comparison).

---

## Summary

2 findings: 0 P0, 1 P1, 0 P2, 1 P3

**Verdict**: Approved with revisions

The reorg is clean and thorough — 106 files moved with all Go imports, plan doc references, review doc paths, docker-compose dockerfile paths, and Makefile targets correctly updated. The `tests/integration/` and `tests/external/` split is logical. Prereq checks changed from `t.Skip` to `t.Fatal` which is correct for tier 2/3 tests that should not silently pass when prerequisites are missing. The new `deps` and `test-harness-all` make targets are useful additions.

The one blocker is the CI workflow which still references the old paths and will break on every PR.
