# 01-ai-core.add03 Round 2 Review

**Reviewer:** R2 Reviewer
**Plan version:** Draft (R1 reviews incorporated)
**Date:** 2026-03-18

---

## Overall Assessment

The plan is well-structured and the R1 findings were thoroughly incorporated. The disposition table is comprehensive, and the incorporated changes are internally consistent in the areas they touch. The `map[string][]string` header type change is applied consistently across `ProviderConfig`, `ProviderEndpoint`, `Model`, catalog JSON examples, `deepCopyProviderConfig`, and `applyHeaders`. The `directAPIKeys` module-level map is a clean solution to the JSON round-trip problem. The embedding API client registry (Section 3.2.1) properly mirrors the chat API client registry pattern.

I found no P0 issues. There are two P1 findings that should be addressed before implementation, and a few P2/P3 items.

---

## Findings

### R2-1 (P1): StreamOptions.Headers type not updated to map[string][]string

The plan changes `ProviderConfig.Headers`, `ProviderEndpoint.Headers`, and `Model.Headers` to `map[string][]string`, but `StreamOptions.Headers` (Section 3.5, line where `ResolveEndpoint` merges `opts.Headers`) is consumed as `map[string][]string` without ever stating that `StreamOptions.Headers` must also change from `map[string]string` to `map[string][]string`.

Currently in the codebase (`options.go` line 30):
```go
Headers map[string]string
```

The `ResolveEndpoint` code in Section 3.5 does:
```go
for k, vs := range opts.Headers {
    headers[k] = append(headers[k], vs...)
}
```

This iterates `vs` as a `[]string`, implying `opts.Headers` is already `map[string][]string`. But this type change is never called out. The plan should explicitly state that `StreamOptions.Headers` changes from `map[string]string` to `map[string][]string`, and `options.go` should be listed in the file organization (Section 7) as a changed file.

Additionally, `BuildBaseOptions` in `options.go` currently takes an `apiKey` parameter and injects it into `StreamOptions.APIKey`. Under the new design, API keys are resolved by `ResolveEndpoint`, not by `BuildBaseOptions`. The plan should clarify what happens to `BuildBaseOptions` -- is the `apiKey` parameter removed? The current OpenAI `StreamSimple` calls `BuildBaseOptions(model, &opts, p.apiKey)` passing the stored API key, which no longer exists on the client struct.

**Recommendation:** Add `options.go` to Section 7 as CHANGED. Explicitly note `StreamOptions.Headers` type change. Clarify `BuildBaseOptions` signature change (remove `apiKey` param, since key resolution moves to `ResolveEndpoint`).

### R2-2 (P1): EmbeddingModel.Headers field silently dropped

The current `EmbeddingModel` struct has a `Headers map[string]string` field (embedding.go line 73). The plan's updated `EmbeddingModel` in Section 3.11 omits `Headers` entirely without noting the removal or providing rationale.

If embedding models need per-model headers (analogous to chat `Model.Headers`), they should be retained (as `map[string][]string` for consistency). If they are intentionally removed because embedding headers should come only from the provider config, this should be stated explicitly.

**Recommendation:** Either retain `Headers map[string][]string` on `EmbeddingModel` for parity with `Model`, or document the intentional removal with rationale.

### R2-3 (P2): EmbedFunc type not addressed

The current codebase defines `EmbedFunc` in `embedding.go` (line 81):
```go
type EmbedFunc func(ctx context.Context, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error)
```

This type signature does not include `ProviderEndpoint`. The plan introduces `EmbeddingAPIClient.Embed()` with `ProviderEndpoint` as a parameter but does not mention what happens to `EmbedFunc`. If it is still used anywhere (e.g., as a callback or adapter type), its signature needs updating. If it is superseded by `EmbeddingAPIClient`, it should be listed for removal.

**Recommendation:** Note disposition of `EmbedFunc` in Section 4.6 or Section 7 -- either update its signature or mark it for removal.

### R2-4 (P2): EmbeddingModel.PricingKnown added but deepCopy not updated

Section 3.11 adds `PricingKnown` to `EmbeddingModel`, which is fine (it's a bool, no deep copy needed). However, the `EmbeddingModel.Headers` field, if retained and changed to `map[string][]string`, would require `deepCopyEmbeddingModel` to be updated to handle slice values. The current deep copy only handles `map[string]string`. This is a minor implementation note but worth flagging since the R1 finding on deep copy for `ProviderConfig` was already addressed and this is the parallel case.

### R2-5 (P3): Embed() passes empty StreamOptions for key resolution

Section 3.11 shows:
```go
endpoint := ResolveEndpoint(cfg, StreamOptions{})
```

This works but is slightly odd -- `ResolveEndpoint` takes `StreamOptions` as a parameter, coupling embedding resolution to chat option types. A minor design smell. Not worth changing now (it works correctly), but if per-call embedding options are added later, consider an `EmbeddingOptions` type or a more generic options interface.

### R2-6 (P3): Catalog embedding model `api` field redundancy

Section 6.3 says: "When the provider has `EmbeddingAPIClientType` set, the embedding model's `API` field is derived from it at catalog load time." But the example embedding catalog JSON still shows `"api": "openai-embeddings"` on each model. If the field is derived at load time, it could be omitted from the JSON (like chat models no longer have `api`). The plan should clarify whether the `api` field in embedding catalog JSON is required, optional-with-override, or ignored when `EmbeddingAPIClientType` is set on the provider.

---

## R1 Disposition Verification

I spot-checked the following R1 incorporated findings against the plan text:

| R1 Finding | Verification |
|------------|-------------|
| R1A-1 + R1B-1 (Headers multi-valued) | Correctly applied across all relevant sections. `map[string][]string` used consistently in ProviderConfig, ProviderEndpoint, Model, deepCopy, applyHeaders, and catalog JSON. |
| R1A-2 + R1B-3 (directAPIKey) | Clean solution with module-level map. Lifecycle properly managed in Register/Unregister/Clear. Sequence diagram updated. |
| R1A-5 + R1B-2 (sourceID removal) | Clearly documented in Section 3.4 with migration path in 4.2. |
| R1A-3 + R1B-4 (Embedding client type) | EmbeddingAPIClientType added to ProviderConfig. Resolution path documented in Section 3.11 with fallback to model.API. |
| R1A-4 + R1B-5 (Embedding registry) | Section 3.2.1 fully specifies the embedding API client registry. |
| R1A-6 (Init ordering) | Correctly deferred validation to call time. |
| R1A-12 + R1B-16 (PricingKnown) | Added to Model and CustomModelOpts. Catalog=true, custom=false by default. Test case added. |

All checked findings are properly incorporated and internally consistent.

---

## Conclusion

The plan has converged well after R1. The two P1 findings (R2-1 and R2-2) are gaps in the change surface documentation rather than fundamental design issues -- they are things the implementer would almost certainly discover and handle correctly, but they should be explicitly documented in the plan to avoid ambiguity. Once those are addressed, the plan is ready for implementation.
