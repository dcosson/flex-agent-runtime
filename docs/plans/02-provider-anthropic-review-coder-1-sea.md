# Review: 02-provider-anthropic (coder-1-sea)

- Source doc: `docs/plans/02-provider-anthropic.md`
- Reviewed commit: 048ca418f610ebe7f4854d04d66cc56916e6d657
- Reviewer: coder-1-sea

## Findings

### P1 - Final tool-call arguments can be silently corrupted on parse failure

**Problem**
The algorithm explicitly falls back to `lastValid` when strict parse of the final accumulated tool JSON fails (`docs/plans/02-provider-anthropic.md:257-259`). This can execute a tool with stale or truncated arguments instead of failing safely. The companion harness only tests convergence for valid JSON chunking (`docs/plans/02-provider-anthropic-test-harness.md:23-35`) and does not require a hard-failure path for malformed terminal payloads.

**Required fix**
Require strict parse success at tool-call finalization. If final parse fails, emit a terminal provider/tool-call parse error and do not emit `toolcall_end` with fallback arguments. Add explicit malformed-terminal-JSON tests to the harness.

---

## Summary

1 findings: 0 P0, 1 P1, 0 P2, 0 P3

**Verdict**: Approved with revisions
