# Code Review: aiag-kfg (R1, reviewer-sea)

- Bead: aiag-kfg
- Commit range: c83ea8c
- Plan doc: docs/plans/03-provider-openai.md, docs/plans/04-provider-google.md
- Reviewer: reviewer-sea
- Review commit: c83ea8c

## Findings

### P3 - Google plan doc signoff still lists thinkingLevel as outstanding gap

**Location:** `docs/plans/04-provider-google.md:1236`

**Problem**
The Completion Signoff section still lists "Gemini 3 `thinkingLevel` mode: wire type exists but `mapThinkingLevel` function and `StreamSimple` integration are not implemented" as an outstanding gap. However, aiag-aps (merged earlier today) implemented `mapThinkingLevel` and wired it into `StreamSimple`. The signoff is now stale.

Similarly, the TopP/TopK gap line at :1237 was updated by this commit to remove TopP/TopK but still references `StopSequences` and `CandidateCount` — that part is correct.

**Suggested fix**
Remove or update the thinkingLevel gap line in the Completion Signoff. This could be a separate micro-fix since it's a doc-only change.

---

## Summary

1 findings: 0 P0, 0 P1, 0 P2, 1 P3

**Verdict**: Approved

**Review notes:**
- OpenAI plan doc correctly updates `ErrBadRequest` → `ErrUnknown` in the 400 status classification pseudocode
- OpenAI plan doc correctly removes the TopP outstanding gap from signoff
- Google plan doc correctly updates all `ErrBadRequest` references to `ErrUnknown` in both pseudocode and the error classification table
- Google plan doc correctly updates `ErrContentFilter` → `ErrUnknown` for safety block handling
- Google signoff gap list updated to remove TopP/TopK — only StopSequences and CandidateCount remain
- Changes are minimal and precisely scoped to the error code alignment task
