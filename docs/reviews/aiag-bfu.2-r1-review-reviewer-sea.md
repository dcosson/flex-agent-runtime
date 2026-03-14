# Code Review: aiag-bfu.2 (R1, reviewer-sea)

- Bead: aiag-bfu.2
- Commit range: 1070660..d0bf886
- Plan doc: docs/plans/01-ai-core.add01.md §8.2, §8.3, §8.4
- Reviewer: reviewer-sea
- Review commit: d0bf886

## Findings

### P3 - Google adapter returns zero usage instead of estimating from input text

**Location:** `internal/ai/provider/google/embedding.go:142`

**Problem**
Plan §8.2 says "(no usage reported by Google — estimate from input text)". The implementation returns an empty `EmbeddingUsage{}`, so `Embed()` will compute zero cost for Google embeddings. This means cost tracking is blind for Google models.

**Suggested fix**
Either (a) estimate tokens from input text length (rough heuristic: ~4 chars per token) and populate `Usage.Tokens`, or (b) accept zero cost for V1 and add a doc comment on line 142 noting the estimation is deferred. Option (b) is fine if cost tracking isn't critical yet.

---

### P3 - Cohere extractEmbeddings has significant duplication across quantized types

**Location:** `internal/ai/provider/cohere/embedding.go:141-204`

**Problem**
The `int8` and `binary` cases (both `[]int8`) are structurally identical, and the `uint8` and `ubinary` cases (both `[]uint8`) are also identical. Four cases share the same loop-convert-copy pattern with only the source field and target type varying. This is correct but verbose (~60 lines that could be ~20).

**Suggested fix**
Consider extracting a generic helper or collapsing cases with shared logic. Low priority — the explicit switch is readable and correct. Only worth changing if a 5th encoding type is added.

---

### P3 - Google Unspecified task type omission could be more explicit

**Location:** `internal/ai/provider/google/embedding.go:85-87`

**Problem**
When `EmbeddingTaskUnspecified` is the task type, `googleTaskTypes` maps it to `""`. The condition `if tt, ok := googleTaskTypes[req.TaskType]; ok && tt != ""` skips setting `r.TaskType`, relying on `omitempty` to exclude it from JSON. This works but is subtle — a reader has to trace through the map lookup + empty string check + omitempty to understand the behavior. The test at `embedding_test.go:107` correctly verifies this path.

**Suggested fix**
No code change needed. The test coverage documents the behavior. Could add a brief comment at line 85 if desired.

---

## Summary

3 findings: 0 P0, 0 P1, 0 P2, 3 P3

**Verdict**: Approved

Both adapters follow the OpenAI pattern leader cleanly — separate `EmbeddingProvider` structs, `BatchEmbed(ctx, p.embedSingle, ...)` delegation, `httptest` mock servers, defensive copies on output vectors. Google adapter correctly implements `batchEmbedContents` endpoint format with API key in query param, task type mapping (all 6 types including Unspecified→omit), outputDimensionality, and reuses existing `contentObj`/`part` wire types. Cohere adapter correctly handles the unique type-keyed response structure with `extractEmbeddings`, `input_type` required field (defaulting to `search_document`), quantized encodings (int8/uint8/binary/ubinary) with `Raw` field population, and `meta.billed_units.input_tokens` usage extraction. Both adapters have thorough test coverage: registration, success round-trip, task/input type mapping (all 6 types), baseURL override, HTTP error classification (429/401/403/500), batch splitting with progress. Cohere additionally tests all 4 quantized encoding types and dimension/default-encoding behavior. All tests pass with `-race`.
