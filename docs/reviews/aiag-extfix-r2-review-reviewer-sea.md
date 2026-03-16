# Code Review: external test make targets fix (R2, reviewer-sea)

- Bead: N/A (follow-up fix)
- Commit range: 76fc143..9ad9e02
- Plan doc: docs/plans/17-external-testing.md
- Reviewer: reviewer-sea
- Review commit: 9ad9e02

## R1 Finding Disposition

| # | Severity | Summary | R2 Status |
|---|----------|---------|-----------|
| 1 | P2 | test-external-tier3 wraps native tests in Docker compose | Fixed — Docker compose removed, now runs `go test` directly matching CI workflow |
| 2 | P3 | test-external-tier2 hardcodes --profile full | Fixed — changed to `--profile minimal` with `local-disk`/`none` env vars |

## Verification

- **test-external-tier3**: Simple `$(GO) test ... -tags=native -timeout=20m ./tests/external/tier3/...` — no Docker lifecycle.
- **test-external-tier2**: Uses `--profile minimal` for compose up/down, env vars match minimal config (`SANDBOX_STORAGE_BACKEND=local-disk`, `SANDBOX_CONTAINER_RUNTIME=none`).

## Findings

No new findings.

## Summary

0 findings

**Verdict**: Approved
