# Seam Review: embedding-api (horizontal) — reviewer-sea

- Mode: horizontal
- Seam: embedding-api (EmbeddingProvider interface and embedding registry/catalog contract)
- Reviewed commit: ff2f6a3
- Reviewer: reviewer-sea
- Plan docs reviewed:
  - `docs/plans/01-ai-core.md`
  - `docs/plans/01-ai-core.add01.md`
  - `docs/plans/02-provider-anthropic.md`
  - `docs/plans/03-provider-openai.md`
  - `docs/plans/04-provider-google.md`

## Seam Boundaries Analyzed

### Seam: 01-ai-core ↔ 01-ai-core.add01

**Plan docs**: `01-ai-core.md` (canonical core), `01-ai-core.add01.md` (embedding extension)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | `EmbeddingProvider` follows the same `API() string` pattern as `Provider`. Registry functions (`Register/Get/Unregister/Clear`) mirror the chat equivalents exactly. |
| Data formats/types | PASS | `EmbeddingModel` is intentionally separate from `Model`. `EmbeddingUsage` is simpler (single `Tokens`/`Cost` fields) vs chat's detailed `Usage` struct — appropriate since embeddings only have input tokens. `EmbeddingCost.PerMTok` is the embedding equivalent of `ModelCost.Input`. |
| Lifecycle ordering | PASS | Embedding catalog loads at `init()` via separate `//go:embed embedding_catalog.json`, independent of chat catalog's `init()`. No ordering dependency. |
| Configuration contracts | PASS | Separate `embedding_catalog.json` file with flat `[]EmbeddingModel` format avoids conflicting with chat's `map[string]map[string]Model` format. |
| Error handling | PASS | `ProviderError` from 01-ai-core is reused as-is. The addendum's `Embed()` entry point preserves `ProviderError` type (returns err without wrapping). Existing error codes (`ErrContextOverflow`, `ErrRateLimit`, etc.) all applicable to embedding errors. |

---

### Seam: 01-ai-core.add01 ↔ 02-provider-anthropic

**Plan docs**: `01-ai-core.add01.md` (embedding contract), `02-provider-anthropic.md` (provider)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS | Anthropic explicitly has no embedding API. The addendum header correctly notes "02-provider-anthropic (no embedding)". No interface to satisfy. |
| Data formats/types | PASS | No shared data types beyond what's already in 01-ai-core. |
| Lifecycle ordering | PASS | Independent — Anthropic provider registers only `ai.Provider`, embedding registry is separate. |
| Configuration contracts | PASS | No embedding models for Anthropic in the embedding catalog. |
| Error handling | PASS | No interaction. |

---

### Seam: 01-ai-core.add01 ↔ 03-provider-openai

**Plan docs**: `01-ai-core.add01.md` (embedding contract), `03-provider-openai.md` (provider)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | FAIL | `API()` method collision — see P1 finding #1. Plan 03's provider struct returns `"openai-completions"` from `API()`, but embedding catalog entries have `"api": "openai-embeddings"`. The same struct cannot satisfy both interfaces with different return values. |
| Data formats/types | PASS | Embedding request/response types are independent from chat types. |
| Lifecycle ordering | PASS | Chat and embedding providers register independently. |
| Configuration contracts | PASS | Separate catalog files, no conflict. |
| Error handling | PASS | Both use `ProviderError` from 01-ai-core. |

---

### Seam: 01-ai-core.add01 ↔ 04-provider-google

**Plan docs**: `01-ai-core.add01.md` (embedding contract), `04-provider-google.md` (provider)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | FAIL | Same `API()` collision as OpenAI. Google chat provider API is separate from `"google-embeddings"`. See P1 finding #1. |
| Data formats/types | PASS | Embedding types independent from chat types. |
| Lifecycle ordering | PASS | Independent registration. |
| Configuration contracts | PASS | Separate catalog files. |
| Error handling | PASS | Both use `ProviderError`. |

---

## Acceptance Criteria Cross-Reference

| Acceptance criterion (source doc) | Seams touched | Status | Details |
|-----------------------------------|---------------|--------|---------|
| AC1: `ai.Embed(ctx, "text-embedding-3-small", req)` returns correct embeddings (add01) | add01 ↔ 01-core | PASS | Uses `GetEmbeddingModel` + `GetEmbeddingProvider` from core registries. Mock provider feasible. |
| AC2: Thread-safe registration doesn't affect chat registry (add01) | add01 ↔ 01-core | PASS | Separate `embeddingProviderMu`/`embeddingModelMu` from chat registries. Truly independent. |
| AC3: Embedding models load from `embedding_catalog.json` (add01) | add01 ↔ 01-core/models | PASS | Separate `//go:embed` file, separate `init()`. |
| AC4: Request validation rejects bad inputs (add01) | add01 only | PASS | Self-contained in `Embed()` entry point. |
| AC5: Cost calculation from token count and pricing (add01) | add01 only | PASS | Uses `EmbeddingModel.Cost.PerMTok`, independent of chat's `CalculateCost`. |
| AC6: Task type mapping per-provider (add01) | add01 ↔ 03, 04 | FAIL | Addendum defines mapping tables but plans 03/04 don't document embedding support. Providers need addenda or updates. |

## Seam Compatibility Matrix

| Seam | Signatures | Data | Lifecycle | Config | Errors | Overall |
|------|-----------|------|-----------|--------|--------|---------|
| 01-core ↔ add01 | PASS | PASS | PASS | PASS | PASS | PASS |
| add01 ↔ 02-anthropic | PASS | PASS | PASS | PASS | PASS | PASS |
| add01 ↔ 03-openai | FAIL | PASS | PASS | PASS | PASS | FAIL |
| add01 ↔ 04-google | FAIL | PASS | PASS | PASS | PASS | FAIL |

## Findings

### P1 [IG] - `API()` method collision prevents single struct from implementing both Provider and EmbeddingProvider

**Seam**: 01-ai-core.add01 ↔ 03-provider-openai, 01-ai-core.add01 ↔ 04-provider-google
**Category**: Interface signatures

**Problem**
Both `Provider` (01-ai-core.md §5.1) and `EmbeddingProvider` (01-ai-core.add01.md §5.1) define `API() string`. The addendum §2 shows a single struct satisfying both interfaces:

```go
var _ ai.Provider = (*openaiProvider)(nil)
var _ ai.EmbeddingProvider = (*openaiProvider)(nil)
```

But the embedding model catalog (add01 §7) uses API identifiers like `"openai-embeddings"` and `"google-embeddings"`, while the chat catalog uses `"openai-completions"` and Google's chat API identifier. A Go struct can only have one `API()` method — it can't return `"openai-completions"` for chat registration and `"openai-embeddings"` for embedding registration.

The `Embed()` entry point (add01 §5.5) resolves providers via `GetEmbeddingProvider(model.API)` where `model.API` comes from the embedding catalog. `RegisterEmbeddingProvider` (add01 §5.2) registers under `p.API()`. If the struct's `API()` returns the chat identifier, the embedding lookup fails.

**Required fix**
Either:
1. **Separate structs per interface** (recommended): Each provider package has a chat `Provider` struct and a separate `EmbeddingProvider` struct, each with its own `API()` return value. Update add01 §2 code example to show this pattern instead of the single-struct example.
2. **Remove `API()` from `EmbeddingProvider`**: Change `RegisterEmbeddingProvider` to accept an explicit API string parameter: `RegisterEmbeddingProvider(api string, p EmbeddingProvider, sourceID string)`. This decouples the API key from the provider struct.
3. **Use the same API identifier for both**: Change embedding catalog entries to use the same API as chat (e.g., `"openai-completions"` for both). But this loses the semantic distinction and complicates provider lookup logic.

Option 1 is cleanest and follows Go's small-interface idiom. The add01 §6.1 component diagram already shows separate `openai_embed[openai/embedding.go]` boxes, suggesting this was the intent. The §2 code example is misleading and should be updated.

---

### P1 [IG] - `BatchEmbed` utility causes infinite recursion when called from provider `Embed()`

**Seam**: 01-ai-core.add01 (internal — `BatchEmbed` ↔ `EmbeddingProvider.Embed()`)
**Category**: Interface signatures / data flow

**Problem**
The addendum §5.4 defines `BatchEmbed(ctx, provider, model, req)` which calls `provider.Embed(ctx, model, req)` for each sub-batch (and also when `len(req.Texts) <= model.MaxBatchSize` — the small-batch fast path). The same section states: "Provider adapters call `BatchEmbed` from their `Embed()` implementation rather than implementing splitting themselves."

This creates a circular call chain:
1. Caller → `ai.Embed(ctx, modelID, req)` (top-level, add01 §5.5)
2. → `provider.Embed(ctx, model, req)` (delegates to provider)
3. → `ai.BatchEmbed(ctx, provider, model, req)` (provider uses batch utility)
4. → `provider.Embed(ctx, model, subBatchReq)` (BatchEmbed calls provider for sub-batch)
5. → step 3 again → **infinite recursion**

This affects ALL providers that implement `EmbeddingProvider`, not just a single seam.

**Required fix**
Change `BatchEmbed` to accept a function for the actual single-batch API call instead of calling `provider.Embed()`:

```go
type EmbedFunc func(ctx context.Context, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error)

func BatchEmbed(ctx context.Context, embedFn EmbedFunc, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error)
```

Provider pattern:
```go
func (p *openaiEmbeddingProvider) Embed(ctx context.Context, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
    return ai.BatchEmbed(ctx, p.embedSingle, model, req)
}

func (p *openaiEmbeddingProvider) embedSingle(ctx context.Context, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
    // actual HTTP call to provider API
}
```

This breaks the recursion while preserving the shared batch-splitting logic.

---

### P2 - Provider plans (03, 04) don't document embedding support expectations

**Seam**: 01-ai-core.add01 ↔ 03-provider-openai, 01-ai-core.add01 ↔ 04-provider-google
**Category**: Acceptance criteria

**Problem**
The addendum header says "Depended on by: ... 03-provider-openai, 04-provider-google" and §8 shows provider adapters in the component diagram. The addendum's AC6 states task type mapping should be "verified per-provider in provider plans." However, plans 03 and 04 contain zero mention of `EmbeddingProvider`, embedding models, task type mapping, or embedding support in their Connected Components, Acceptance Criteria, or Testing Strategy sections.

This means:
- No implementation guidance exists for embedding support in those providers
- No acceptance criteria verify that embedding works for OpenAI or Google
- No test coverage is specified for embedding adapter code

**Required fix**
Either:
1. Write addenda for plans 03 and 04 (e.g., `03-provider-openai.add01.md`, `04-provider-google.add01.md`) covering embedding adapter implementation, acceptance criteria, and test strategy, OR
2. Add embedding sections to the existing plans (simpler if the embedding adapter code is straightforward)

At minimum, each provider plan should define: file layout for embedding code, API-to-wire-format mapping, task type mapping, batch behavior, and acceptance criteria for the embedding path.

---

### P3 - `Embed()` entry point takes `modelID string` vs chat's `Stream()` taking `Model` struct

**Seam**: 01-ai-core.add01 ↔ 01-ai-core
**Category**: Interface signatures

**Problem**
Chat entry points (01-ai-core §7):
```go
func Stream(ctx, model Model, llmCtx Context, opts StreamOptions) *EventStream
```

Embedding entry point (01-ai-core.add01 §5.5):
```go
func Embed(ctx context.Context, modelID string, req EmbeddingRequest) (*EmbeddingResponse, error)
```

Chat expects the caller to pass a full `Model` struct; embedding resolves the model internally from a string ID. This is a different calling convention. The embedding version is arguably better UX (callers don't need to look up the model first), but it's inconsistent.

**Required fix**
None required — this is a deliberate design choice and the embedding API is simpler. Document the rationale in the addendum if not already present (the flat model registry justifies simpler lookup). Low priority.

---

### P3 - Error classification pattern differs between chat and embedding

**Seam**: 01-ai-core.add01 ↔ 01-ai-core
**Category**: Error handling

**Problem**
For chat errors, `IsContextOverflow(msg *AssistantMessage, contextWindow int)` (01-ai-core §11.2) operates on the final `AssistantMessage`. For embedding errors, overflow detection would need `errors.As(err, &pe)` with `pe.Code == ErrContextOverflow` on the returned `error`.

These are different patterns. A caller familiar with the chat error path would need to learn a different approach for embeddings.

**Required fix**
None strictly required — the difference is inherent to the streaming vs synchronous API surface. Consider adding a helper like `IsEmbeddingError(err error, code ProviderErrorCode) bool` for consistency, but this is low priority.

---

## Summary

5 seam boundaries analyzed, 5 findings: 0 P0, 2 P1, 1 P2, 2 P3

Seam compatibility: 3/5 seams fully compatible (01-core↔add01, add01↔02-anthropic are clean; add01↔03-openai and add01↔04-google have P1 `API()` collision)

**Verdict**: Seams compatible with revisions — the two P1 findings (API method collision and BatchEmbed recursion) are design-level issues that should be fixed in the addendum before implementation begins. The P2 (missing provider plan embedding sections) can be addressed during planning for those providers' embedding work.
