# Review: 01-ai-core.add01 (reviewer-sea)

- Source doc: `docs/plans/01-ai-core.add01.md`
- Test harness: `docs/plans/01-ai-core.add01-test-harness.md`
- Reviewed commit: 4d31d68
- Reviewer: reviewer-sea

## Findings

### P1 - Catalog format in §7 is incompatible with existing catalog.json structure

**Problem**
Section 7 shows the catalog.json gaining a new top-level `"embeddingModels"` key alongside a `"models"` key:
```json
{
  "models": [ ... ],
  "embeddingModels": [ ... ]
}
```

But the existing `catalog.json` uses a completely different structure: `{provider: {modelId: Model}}`, e.g.:
```json
{
  "anthropic": {
    "claude-sonnet-4-20250514": { ... }
  }
}
```

The existing `init()` in `models.go` unmarshals into `map[string]map[string]Model`. Adding an `"embeddingModels"` array to this file would cause a type mismatch at init — `[]EmbeddingModel` cannot unmarshal into `map[string]Model`. This is a breaking change that the plan doesn't address.

Additionally, the plan shows embedding models as a flat array, while chat models use a nested provider→modelID structure. The plan needs to reconcile these.

**Required fix**
Choose one of:
- (a) Use a separate file (`models/embedding_catalog.json`) with its own `//go:embed` directive and init function. Cleanest separation, no risk to existing code.
- (b) Restructure both catalogs under a unified wrapper type that the init code unmarshals into (`type catalogFile struct { Models map[string]map[string]Model; EmbeddingModels []EmbeddingModel }`). But this changes the existing init code.
- (c) Use the same `{provider: {modelId: EmbeddingModel}}` structure for embedding models, consistent with the existing format.

Whichever approach is chosen, the plan must describe the init code changes explicitly.

---

### P2 - GetEmbeddingProvider returns (provider, bool) but GetProvider returns (provider, error)

**Problem**
The existing chat provider registry (`registry.go:26-34`) returns `(Provider, error)` from `GetProvider`:
```go
func GetProvider(api string) (Provider, error) {
    ...
    return nil, fmt.Errorf("no provider registered for API: %s", api)
}
```

The plan's `GetEmbeddingProvider` (§5.2) returns `(EmbeddingProvider, bool)`:
```go
func GetEmbeddingProvider(api string) (EmbeddingProvider, bool) {
    ...
    return rp.provider, ok
}
```

This is an API inconsistency for two registries that follow the same pattern. Callers would need different error handling depending on which registry they're querying.

**Required fix**
Use the same return signature — either both return `(T, error)` or both return `(T, bool)`. Since `GetProvider` already returns `(Provider, error)` and is used in production, `GetEmbeddingProvider` should match: `(EmbeddingProvider, error)`. Update the `Embed()` entry point accordingly.

---

### P2 - Embedding model registry uses flat map but chat model registry uses nested provider→modelID map

**Problem**
The existing chat model registry (`models.go:17`) uses:
```go
modelRegistry = make(map[string]map[string]Model) // provider -> modelID -> Model
```
with `GetModel(provider, modelID string) (Model, error)`.

The plan's embedding model registry (§5.3) uses:
```go
embeddingModelRegistry = make(map[string]EmbeddingModel) // modelID -> EmbeddingModel
```
with `GetEmbeddingModel(id string) (EmbeddingModel, bool)`.

This means:
1. Different lookup semantics — chat models require provider name, embedding models don't.
2. Embedding model IDs must be globally unique, while chat model IDs can overlap across providers.
3. No way to query "all embedding models for a provider" without scanning the whole registry.

**Required fix**
Either justify the simpler flat registry (e.g., "embedding model IDs are globally unique across all providers") or use the same nested structure. If the flat structure is intentional, document why and add `ListEmbeddingModelsByProvider(provider string) []EmbeddingModel` for consistency.

---

### P2 - MinDims validation missing from Embed() entry point

**Problem**
The `Embed()` function in §5.4 validates `req.Dimensions > model.MaxDims` (line 399-401) but does not validate `req.Dimensions < model.MinDims`. The `EmbeddingModel` struct (§4.4) has a `MinDims` field, and models like `gemini-embedding-001` have `minDims: 128`.

If a caller requests 64 dimensions on a model with MinDims=128, no error is raised — the request goes to the provider, which may fail with an opaque API error.

**Required fix**
Add MinDims validation:
```go
if req.Dimensions > 0 && model.MinDims > 0 && req.Dimensions < model.MinDims {
    return nil, fmt.Errorf("requested dimensions %d below min %d for model %s",
        req.Dimensions, model.MinDims, modelID)
}
```

---

### P2 - Missing EmbeddingEncoding values for uint8 and ubinary

**Problem**
Section 3.1's comparison table lists `uint8` and `ubinary` as supported quantization formats for Cohere and Voyage:
> `embedding_types` (float, int8, uint8, binary, ubinary) — Cohere
> `output_dtype` (float, int8, uint8, binary, ubinary) — Voyage

But section 4.2 only defines four `EmbeddingEncoding` constants: `float`, `base64`, `int8`, `binary`. The `uint8` and `ubinary` formats are missing.

**Required fix**
Either add `EmbeddingEncodingUint8` and `EmbeddingEncodingUBinary` constants, or explicitly document that these formats are out of scope and will be added when Cohere/Voyage providers are implemented. The mapping table in §4.2 should be consistent with the comparison table in §3.1.

---

### P2 - URP §11.1 OnProgress callback not included in EmbeddingRequest type

**Problem**
Section 11.1 (Automatic Batch Splitting with Progress) describes:
> "Report progress via a callback on `EmbeddingRequest` (optional `OnProgress func(completed, total int)`)"

But the `EmbeddingRequest` struct in §4.3 does not include this field. Either the type definition or the URP section is out of sync.

**Required fix**
Add `OnProgress func(completed, total int)` to the `EmbeddingRequest` struct in §4.3, or move the progress callback to a separate batch options type that wraps `EmbeddingRequest`. The URP section should match the type definition.

---

### P2 - Dependencies section contradicts URP rate limiter requirement

**Problem**
Section 11.3 (Rate Limit Aware Batching) states:
> "The adapter should accept a `*rate.Limiter` (from `golang.org/x/time/rate`)"

Section 16 (Implementation Notes: Dependencies) states:
> "No new external dependencies. Uses only stdlib (`context`, `fmt`, `sync`) and the existing `internal/ai` infrastructure."

These contradict each other. `golang.org/x/time/rate` is not part of stdlib.

**Required fix**
Either:
- Add `golang.org/x/time/rate` to the dependencies section, or
- Move the rate limiter to a URP-only section that's explicitly deferred to provider implementation, or
- Use a simpler stdlib-only throttle (e.g., `time.Ticker`)

---

### P2 - No error classification for embedding errors (ProviderError pattern)

**Problem**
The parent plan (01-ai-core) defines `ProviderError` with error codes and `IsRetryable()` / `IsContextOverflow()` classification. The chat path uses this extensively. The embedding plan's `Embed()` entry point (§5.4) wraps provider errors with plain `fmt.Errorf`:
```go
return nil, fmt.Errorf("embed %s: %w", modelID, err)
```

This means callers cannot distinguish retryable errors (429, 503) from permanent errors (400, 401). The test harness (F1) explicitly expects error classification: "429 (rate limit) → returned as a retryable error with retry-after hint."

**Required fix**
Define how `ProviderError` applies to the embedding path. Either:
- Embedding providers return `ProviderError` (same type as chat), or
- Define an `EmbeddingError` type with similar classification

The `Embed()` entry point should preserve the provider error's type, not wrap it with `fmt.Errorf` (which strips type information). Use `%w` or return the error unwrapped.

---

### P3 - Test harness O1 tests provider wire formats, which are out of scope for this addendum

**Problem**
Test harness O1 (Request Wire Format) specifies golden JSON bodies for OpenAI, Google, and Cohere provider adapters. But the addendum's scope explicitly states:
> "Out of scope: Provider implementations (covered in plans 02-04 and future provider plans)"

The core addendum contains no provider adapters, so O1 tests cannot run against this implementation. They belong in the provider-specific test harnesses.

**Required fix**
Move O1 to the provider-specific test harness docs (or note it as "deferred to provider plans"). Replace with a mock-based golden test for the `Embed()` entry point flow (which O3 already covers).

---

### P3 - AC1 example missing ctx parameter

**Problem**
AC1 reads:
> `ai.Embed("text-embedding-3-small", req)` with a mock provider...

But the function signature in §5.4 is:
```go
func Embed(ctx context.Context, modelID string, req EmbeddingRequest) (*EmbeddingResponse, error)
```

The example is missing the `ctx` parameter.

**Required fix**
Update AC1 to: `ai.Embed(ctx, "text-embedding-3-small", req)`.

---

### P3 - No ClearEmbeddingProviders or ClearEmbeddingModels functions for test teardown

**Problem**
The existing registry has `ClearProviders()` and `ClearModels()` for test teardown (used in parallel tests to reset global state). The plan's embedding registry (§5.2, §5.3) has `UnregisterEmbeddingProviders(sourceID)` and `ListEmbeddingModels()` but no `Clear*()` equivalents.

Tests that register mock embedding providers/models need a way to clean up. Without `Clear*()` functions, tests leak state into each other.

**Required fix**
Add `ClearEmbeddingProviders()` and `ClearEmbeddingModels()` to the registry API, matching the existing chat registry pattern.

---

### P3 - Batch splitting logic deferred to providers but tested in core harness

**Problem**
The plan says (§5.1): "Providers handle batching internally." But the test harness P1 (Batch Splitting Preserves Ordering) and F2 (Partial Batch Failure) test batch splitting behavior. If batch splitting is a provider concern, these tests should be in the provider test harnesses, not the core harness.

Alternatively, if there should be a shared batch splitting utility in the core (which would be more robust — one implementation vs. each provider independently implementing the same logic), the plan should specify it.

**Required fix**
Either:
- Move P1 and F2 to provider-specific test harnesses, or
- Add a shared `BatchEmbed` utility to the core that handles splitting, merging, and progress reporting, so the logic is tested once centrally

---

## Summary

12 findings: 0 P0, 1 P1, 7 P2, 4 P3

**Verdict**: Approved with revisions

The plan is well-structured and covers the embedding API surface thoroughly. The provider comparison (§3) is excellent. The main issues are: (1) the catalog format change would break the existing init code (P1), (2) several API inconsistencies with the existing chat registry patterns (P2s — return types, registry structure, error classification), and (3) some mismatches between the type definitions and URP/test harness sections. None of these are architecturally blocking — they're all addressable with targeted fixes.
