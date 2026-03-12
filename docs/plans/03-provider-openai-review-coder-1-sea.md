# Review: 03-provider-openai (coder-1-sea)

- Source doc: `docs/plans/03-provider-openai.md`
- Reviewed commit: 048ca418f610ebe7f4854d04d66cc56916e6d657
- Reviewer: coder-1-sea

## Findings

### P1 - Tool-call finalization order is nondeterministic

**Problem**
`finalizeToolCalls` iterates `acc.toolStates` using Go map iteration (`docs/plans/03-provider-openai.md:587-589`), then emits `EventToolCallEnd` in that iteration order (`docs/plans/03-provider-openai.md:601-605`). This yields nondeterministic ordering when multiple tool calls are present, which can destabilize downstream logic expecting model-emitted order.

**Required fix**
Finalize tool calls in deterministic index order (sort indices first, then emit). Add an explicit harness assertion that end-event order matches tool-call index order.

---

### P1 - Final tool-call arguments can degrade to stale/empty payloads

**Problem**
On strict parse failure, the plan falls back to `lastValid` or even `{}` (`docs/plans/03-provider-openai.md:592-599`) and still emits `toolcall_end` (`docs/plans/03-provider-openai.md:601-605`). This can convert malformed model output into a seemingly valid tool invocation with incorrect parameters.

**Required fix**
Require strict parse success before `toolcall_end`. If final parse fails, emit a typed error and abort that turn/tool-call path. Extend harness coverage with malformed-terminal-arguments cases; current property coverage only asserts valid-object convergence (`docs/plans/03-provider-openai-test-harness.md:58-69`).

---

## Summary

2 findings: 0 P0, 2 P1, 0 P2, 0 P3

**Verdict**: Approved with revisions
