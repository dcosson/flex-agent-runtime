# Review: 04-provider-google (coder-1-sea)

- Source doc: `docs/plans/04-provider-google.md`
- Reviewed commit: 048ca418f610ebe7f4854d04d66cc56916e6d657
- Reviewer: coder-1-sea

## Findings

### P1 - Safety-blocked responses can leak partial content before error

**Problem**
The stream loop processes candidate parts before checking `finishReason` (`docs/plans/04-provider-google.md:695-700`), then only after the stream ends emits safety error (`docs/plans/04-provider-google.md:718-720`). If a `SAFETY` finish reason arrives with candidate content, text/tool events may already be emitted, conflicting with the acceptance requirement that no blocked-content garbage is returned (`docs/plans/04-provider-google.md:1084`).

**Required fix**
Gate candidate-part emission on safety status: detect `SAFETY` finish before emitting content for that chunk (or retract/avoid emitting content from blocked candidates). Add harness assertions that safety-blocked runs emit zero user-visible content events.

---

## Summary

1 findings: 0 P0, 1 P1, 0 P2, 0 P3

**Verdict**: Approved with revisions
