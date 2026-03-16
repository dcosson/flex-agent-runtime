# Code Review: llm-demo and embedding-demo CLIs (R1, reviewer-sea)

- Bead: N/A (addendum implementation)
- Commit range: ff93a8c..4b65ac7
- Plan doc: docs/plans/01-ai-core.add02.md
- Reviewer: reviewer-sea
- Review commit: 4b65ac7

## Scope

8 files changed (808 insertions, 1 deletion):
- `cmd/llm-demo/main.go` — Interactive chat loop with tool-use (261 lines)
- `cmd/llm-demo/calc.go` — Recursive-descent arithmetic expression evaluator (157 lines)
- `cmd/llm-demo/calc_test.go` — Calculator unit tests: 6 success + 6 error cases (52 lines)
- `cmd/embedding-demo/main.go` — Embed + rank CLI with stdin pipe support (138 lines)
- `cmd/embedding-demo/rank.go` — Cosine similarity + ranking (52 lines)
- `cmd/embedding-demo/rank_test.go` — Similarity + ranking unit tests (42 lines)
- `docs/plans/01-ai-core.add02.md` — Addendum plan doc (95 lines)
- `Makefile` — `build-llm-demo` and `build-embedding-demo` targets

## Test Verification

- `go test ./cmd/llm-demo/...` — 12/12 PASS (6 success + 6 error)
- `go test ./cmd/embedding-demo/...` — 2/2 PASS
- `go build ./cmd/llm-demo && go build ./cmd/embedding-demo` — both compile

## Findings

No findings. Clean, minimal implementation:

1. **llm-demo main.go** — Well-structured chat loop: `registerChatProviders` → `resolveChatModel` (catalog + fallback) → stdin scanner → `runAssistantTurn`. Tool loop bounded at 8 iterations. `ai.TransformMessages` correctly applies per-model message transforms. Providers registered with unique sourceID `"cmd-llm-demo"`. Flag/env precedence is correct (`LLM_PROVIDER`, `LLM_MODEL` env vars with flag overrides).

2. **calc.go** — Correct recursive-descent parser with proper operator precedence (expr → term → factor → number). Handles unary +/-, parentheses, whitespace, division by zero. Validates full consumption of input (no trailing garbage). Clean and readable.

3. **calc_test.go** — Good coverage: basic arithmetic, precedence (`2+3*4=14`), parentheses, whitespace, unary minus, nested parens. Error cases: empty, unclosed paren, dangling operator, double operator, divide by zero, non-numeric.

4. **embedding-demo main.go** — Clean flow: register providers → parse flags → optionally read from stdin pipe → embed all texts + query in single call → split vectors → rank by cosine. Repeatable `-text` flag via `stringList` custom `flag.Value`. Stdin pipe detection via `os.Stdin.Stat()` mode check is idiomatic.

5. **rank.go** — Correct cosine similarity: dot product / (sqrt(norm_a) * sqrt(norm_b)). Handles zero-length and zero-norm vectors. Computation in float64 from float32 vectors. `sort.SliceStable` for deterministic ordering.

6. **rank_test.go** — Tests identical vectors (cosine=1), orthogonal vectors (cosine=0), and ranking order (descending by score, A > B > C).

7. **Plan doc** — Concise addendum documenting goals, runtime flows, tool contract, build surface, and acceptance criteria. All 4 acceptance criteria met.

8. **Makefile** — Build targets create `bin/` directory, output to `bin/llm-demo` and `bin/embedding-demo`. Help text updated.

## Summary

0 findings

**Verdict**: Approved
