# Code Review: aiag-b9b (R1, reviewer-sea)

- Bead: aiag-b9b
- Commit range: bd8ab86 (on branch batch2-followup/compat-fixes)
- Plan doc: docs/plans/03-provider-openai.md
- Reviewer: reviewer-sea
- Review commit: 82f3385

## Findings

### P3 - Mistral tool ID character set is narrower than spec allows

**Location:** `internal/ai/provider/openai/compat.go:88-95`

**Problem**
`generateMistralToolID()` uses `hex.EncodeToString` which produces only `[0-9a-f]`. The Mistral spec says "9 alphanumeric characters" — hex is a valid subset of alphanumeric, so this works, but it uses only 16 of 36 possible characters per position. This reduces the ID space from 36^9 (~1e14) to 16^9 (~7e10). In practice this is still extremely unlikely to collide within a single conversation's tool calls, so this is cosmetic.

**Suggested fix**
No change needed. The hex subset satisfies the Mistral requirement. If desired in the future, could use `math/rand` with a base-36 alphabet for slightly more entropy, but the current approach is simpler and sufficient.

---

## Summary

1 findings: 0 P0, 0 P1, 0 P2, 1 P3

**Verdict**: Approved

**Review notes:**
- `requiresMistralToolIDs()` follows the established compat flag pattern (boolVal check)
- `normalizeToolCallID()` correctly delegates — no normalization for non-Mistral models
- The `toolIDMap` in `convertMessages()` is well-designed: builds the mapping during assistant message conversion, then looks up during tool result conversion, ensuring ID consistency across the conversation
- Test coverage is thorough: `O3b` tests both Mistral normalization (verifies 9-char hex IDs, verifies tool result references match) and non-Mistral passthrough
- `convertToolResult` signature change is correctly propagated to both callers in `request.go` and the two existing tests in `provider_test.go`
- Race detector passes clean
