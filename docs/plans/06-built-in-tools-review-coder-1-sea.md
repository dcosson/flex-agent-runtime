# Review: 06-built-in-tools (coder-1-sea)

- Source doc: `docs/plans/06-built-in-tools.md`
- Reviewed commit: 048ca418f610ebe7f4854d04d66cc56916e6d657
- Reviewer: coder-1-sea

## Findings

### P1 - [IG] ToolBackend contract cannot carry remote progress updates

**Problem**
`ToolBackend` is unary request/response only (`docs/plans/06-built-in-tools.md:117-135`), but the bash tool contract requires streaming incremental updates via `onUpdate` (`docs/plans/06-built-in-tools.md:213-215`) and acceptance criteria require progressive updates (`docs/plans/06-built-in-tools.md:296-299`). This creates a contract mismatch for `SandboxBackend`, where remote execution cannot surface incremental output through the current seam.

**Required fix**
Extend the backend contract with progress streaming (callback channel or stream API) and define RPC propagation expectations for remote backends. Update component/integration tests to validate progressive updates through both Local and Sandbox backends.

---

### P2 - Git tool surface is underspecified for deterministic implementation

**Problem**
The plan mixes `git_*` as Tier 1/2 with policy language but leaves exact command set and tier mapping partly open (`docs/plans/06-built-in-tools.md:150-159`, `docs/plans/06-built-in-tools.md:247-269`). This invites divergent implementations across tool factories/backends.

**Required fix**
Specify an explicit V1 git command matrix with fixed tier assignment and policy outcomes per command, including deterministic handling for unsupported commands.

---

## Summary

2 findings: 0 P0, 1 P1, 1 P2, 0 P3

**Verdict**: Approved with revisions
