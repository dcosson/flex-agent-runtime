# 01-ai-core.add03 — Round 1 Review B

**Reviewer:** Reviewer B (independent)
**Date:** 2026-03-18
**Plan reviewed:** `docs/plans/01-ai-core.add03.md`

---

## Findings

### R1B-1 | P0 | Anthropic beta headers cannot be expressed through ProviderSpecific map

The current Anthropic provider supports `betaHeaders []string` -- a list of values all set via `req.Header.Add("anthropic-beta", beta)`. The plan proposes moving provider-specific config to `ProviderSpecific map[string]string`, which is a string-to-string map. This cannot represent a list of beta header values. The `anthropic-version` maps cleanly to a single string, but beta headers are multi-valued.

This is not a hypothetical concern -- the Anthropic API actively uses beta headers (e.g., `prompt-caching-2024-07-31`, `max-tokens-3-5-sonnet-2024-07-15`) and these are sent as multiple `anthropic-beta` header values on the same request.

**Suggested fix:** Either:
1. Change `ProviderSpecific` to `map[string]any` so it can hold `[]string` values, or
2. Add a `ProviderSpecificHeaders map[string][]string` field alongside `ProviderSpecific` for multi-valued headers, or
3. Use a convention in `ProviderSpecific` like `betaHeaders: "header1,header2,header3"` with comma-separated values and document that the Anthropic client splits on commas. This is the simplest approach but least clean.
4. Extend `Headers map[string]string` to `Headers map[string][]string` so multi-valued headers can be expressed generally.

Option 1 is the most flexible but loses JSON type safety. Option 3 is pragmatic. The plan should pick one and document the Anthropic beta header migration path explicitly.

---

### R1B-2 | P1 | sourceID-based unregistration is silently dropped with no replacement

The current provider registry supports `UnregisterProviders(sourceID string)` which removes all providers registered with a given sourceID. This is used extensively in tests and integration harnesses (e.g., `tests/external/tier2/agent_flow_test.go`, `tests/external/tier1/stubserver_test.go`, `tests/integration/harness/runner.go`, `tests/integration/mode3/harness/remote_env.go`). The plan's new `ProviderConfig` registry has `UnregisterProviderConfig(name string)` for single-provider removal but no sourceID-based bulk unregistration.

The plan does not address how test cleanup patterns migrate. Tests currently do:
```go
sourceID := "tier2-agent-flow-" + t.Name()
ai.RegisterProvider(provider, sourceID)
defer ai.UnregisterProviders(sourceID)
```

Without sourceID, test cleanup must track and remove each provider config and API client individually, which is more fragile and verbose.

**Suggested fix:** Either:
1. Retain a sourceID/tag mechanism on `RegisterProviderConfig` for bulk cleanup, or
2. Document that tests should use `ClearProviderConfigs()` in a `t.Cleanup`, or
3. Add a `RegisterProviderConfigForTest(t *testing.T, cfg ProviderConfig)` helper that auto-cleans on test completion.

At minimum, the migration plan (Section 4) should document how existing test cleanup patterns translate.

---

### R1B-3 | P1 | directAPIKey on ProviderConfig breaks JSON round-trip and deep copy

The plan adds an unexported `directAPIKey string` field to `ProviderConfig` for custom providers that supply keys directly. This creates several issues:

1. **Deep copy**: `deepCopyProviderConfig` copies exported fields but the unexported `directAPIKey` field will be copied by Go's value semantics for the struct, so this works for the current design. However, if `ProviderConfig` is later stored by pointer, this becomes a bug.

2. **JSON round-trip**: If a `ProviderConfig` with `directAPIKey` is serialized and deserialized (e.g., for persistence, logging, or config export), the `directAPIKey` is silently lost. This is stated in the plan ("Not serialized to JSON") but could surprise callers who serialize configs.

3. **Security**: The API key is stored in-memory in a struct that gets deep-copied freely. While the exported `APIKey` in `StreamOptions` has the same issue, proliferating copies of keys is not ideal.

4. **Mixing exported/unexported fields**: Having both exported JSON-tagged fields and an unexported field on the same struct is an unusual Go pattern that may confuse implementers.

**Suggested fix:** Consider an alternative approach: store direct API keys in a separate map (`directAPIKeys map[string]string` keyed by provider name) within the provider registry module. `ResolveEndpoint` checks this map as step 2 in the key resolution order. This avoids polluting `ProviderConfig` with unexported state and avoids the round-trip issue. `ClearProviderConfigs` would also clear this map.

---

### R1B-4 | P1 | Embedding resolution uses ProviderConfig.APIClientType for chat, but embedding models need a different client type

The plan acknowledges this in Section 3.11 with the "important nuance" note, but the resolution is somewhat awkward. The `ProviderConfig` has a single `APIClientType` field (e.g., `"openai-completions"` for the OpenAI provider), but embedding models need a different client type (e.g., `"openai-embeddings"`). The plan resolves this by having `Embed()` use `model.API` instead of `cfg.APIClientType` for client lookup.

This means:
- For chat: `model.Provider` -> `ProviderConfig.APIClientType` -> `APIClient` (model.API is redundant, derived from provider)
- For embeddings: `model.Provider` -> `ProviderConfig` (for base URL/key only), `model.API` -> `EmbeddingAPIClient`

This asymmetry is confusing. The chat path ignores `model.API` in favor of deriving it from the provider config, but the embedding path relies on `model.API` directly.

**Suggested fix:** Consider adding an optional `EmbeddingAPIClientType` field to `ProviderConfig`, or accept the asymmetry but document it prominently. Alternatively, the `ProviderConfig` could have a map of capability-to-client-type:
```go
ClientTypes map[string]string // e.g., {"chat": "openai-completions", "embedding": "openai-embeddings"}
```
This is cleaner but may be over-engineering. At minimum, add a clear comment in the `ProviderConfig` struct documenting that `APIClientType` only applies to chat completions.

---

### R1B-5 | P1 | EmbeddingAPIClient registry is mentioned but not fully specified

Section 3.11 introduces `EmbeddingAPIClient` interface and mentions an `EmbeddingAPIClient` registry (`GetEmbeddingAPIClient`), but the plan does not include the full registry specification (Register, Get, Clear functions, thread safety, etc.) like it does for the API client registry in Section 3.2 and the provider config registry in Section 3.4. This is a gap that could lead to inconsistent implementation.

**Suggested fix:** Add a Section 3.11.1 with the full `EmbeddingAPIClient` registry specification, following the same pattern as Section 3.2. Include `RegisterEmbeddingAPIClient`, `GetEmbeddingAPIClient`, `ClearEmbeddingAPIClients`.

---

### R1B-6 | P1 | Catalog validation during init() will panic on missing providers but existing code already handles this at runtime

The new catalog loading in Section 3.8 does:
```go
provCfg, ok := catalog.Providers[providerName]
if !ok {
    panic(...)
}
```

This is appropriate for build-time embedded catalogs, but if the catalog format is ever externalized (loaded from a file at runtime), panicking is unacceptable. The plan should clarify that this is only for the embedded catalog, and any runtime catalog loading (e.g., for custom providers) should return errors.

More importantly, the embedding catalog (Section 6.3) is loaded from a separate file. The embedding catalog references providers by name (e.g., `"provider": "openai"`), but these providers are defined in the main catalog's `"providers"` section. The plan does not specify which `init()` runs first. If the embedding catalog init runs before the main catalog init, `GetProviderConfig("openai")` will fail during embedding model registration.

**Suggested fix:**
1. Document the init order requirement (main catalog before embedding catalog), or
2. Defer provider validation for embedding models to call time rather than init time, or
3. Merge both catalogs into a single file with a single `init()`.

---

### R1B-7 | P2 | No mechanism to override base URL per-call for embeddings

The `Embed()` entry point (Section 3.11) creates the endpoint with `ResolveEndpoint(cfg, StreamOptions{})` -- passing empty `StreamOptions`. Chat callers can pass `opts.APIKey` and `opts.Headers` to override provider defaults per-call, but embedding callers have no equivalent mechanism. The `EmbeddingRequest` struct has no API key or headers field.

For the immediate scope this may be acceptable (embeddings are typically not called with per-call overrides), but it means custom providers that use `directAPIKey` via `RegisterCustomProvider` will work for embeddings (since the key is on the provider config), but explicit per-call key overrides won't work.

**Suggested fix:** Either:
1. Add `APIKey string` and `Headers map[string]string` to `EmbeddingRequest`, or
2. Add an `EmbeddingOptions` struct parameter to `Embed()`, or
3. Document this limitation as out of scope for now.

---

### R1B-8 | P2 | Embedding catalog still references baseUrl but plan says it is removed

Section 6.3 shows the new embedding catalog format with `baseUrl` removed:
```json
{
    "id": "text-embedding-3-small",
    ...
    "provider": "openai",
    ...
}
```

But the current `EmbeddingModel` struct (in `internal/ai/embedding.go`) still has `BaseURL string` field with `json:"baseUrl"`. The plan says `baseUrl` is removed from embedding models (Section 6.3: "Note: `baseUrl` removed from embedding models"), but does not show the updated `EmbeddingModel` struct.

The existing embedding providers (`openai/embedding.go`, `google/embedding.go`, `cohere/embedding.go`) actively use `model.BaseURL` as a fallback:
```go
if b := strings.TrimSpace(model.BaseURL); b != "" {
    baseURL = b
}
```

The migration plan (Section 4) does not explicitly mention updating embedding provider implementations to use `ProviderEndpoint.BaseURL` instead of `model.BaseURL`.

**Suggested fix:** Add the updated `EmbeddingModel` struct to the plan (removing `BaseURL`), and add embedding provider migration steps to Section 4.1 (each embedding provider must switch from `model.BaseURL` to `endpoint.BaseURL`).

---

### R1B-9 | P2 | CustomProviderConfig duplicates fields already on ProviderConfig

`CustomProviderConfig` has fields `Name`, `APIClientType`, `BaseURL`, `KeyEnvVars`, `Headers`, `ProviderSpecific` that are identical to `ProviderConfig` fields, plus an `APIKey` field. The `RegisterCustomProvider` function constructs a `ProviderConfig` from these.

This duplication means two types to maintain with the same fields. If a field is added to `ProviderConfig`, it must also be added to `CustomProviderConfig` or custom providers lose access to it.

**Suggested fix:** Consider embedding `ProviderConfig` in `CustomProviderConfig`:
```go
type CustomProviderConfig struct {
    ProviderConfig
    APIKey string // direct key, takes precedence over KeyEnvVars
}
```
Or just accept a `ProviderConfig` with an additional `APIKey` parameter in `RegisterCustomProvider`.

---

### R1B-10 | P2 | Google provider URL construction uses version path segment, but ProviderSpecific only has string values

The Google provider currently constructs URLs like:
```
baseURL + "/" + version + "/models/" + modelID + ":streamGenerateContent"
```

The plan moves `version` to `ProviderSpecific["apiVersion"]`, which works for string lookup. However, the plan's example Anthropic usage shows `endpoint.ProviderSpecific["apiVersion"]` used as a header value, while Google needs it as a URL path segment. The plan should confirm that this dual usage (header value for Anthropic, URL path segment for Google) is intentional and both client implementations know to use ProviderSpecific differently.

**Suggested fix:** Add a brief note in Section 3.10 (or the Google client refactor example) showing how the Google client uses `endpoint.ProviderSpecific["apiVersion"]` for URL construction, not just header values. This confirms the design intent.

---

### R1B-11 | P2 | No migration plan for the public re-export layer (ai/ package)

Section 7 mentions `ai/ai.go` is "CHANGED: re-exports updated types" but doesn't specify what changes. The current public `ai/` package re-exports `Provider`, `RegisterProvider`, `GetProvider`, `UnregisterProviders`, etc. All of these are being replaced with `APIClient`, `ProviderConfig`, `RegisterAPIClient`, `RegisterProviderConfig`, etc.

The plan should specify:
- Which new types are exported from `ai/`
- Which old exports are removed
- Whether `Provider` type alias is removed from `ai/` (since the interface is retired)

This affects all callers (demos, tests, integration harnesses) that import from `ai/` rather than `internal/ai`.

**Suggested fix:** Add a subsection to Section 7 or Section 4 listing the public API changes:
- Removed: `Provider`, `RegisterProvider`, `GetProvider`, `UnregisterProviders`
- Added: `APIClient`, `ProviderConfig`, `ProviderEndpoint`, `CustomProviderConfig`, `RegisterAPIClient`, `GetAPIClient`, `RegisterProviderConfig`, `GetProviderConfig`, `RegisterCustomProvider`, `RegisterCustomModel`, `ResolveEndpoint`

---

### R1B-12 | P2 | Testing section does not cover embedding provider refactoring

Section 5 (Testing) covers chat-related tests comprehensively but does not include tests for:
- `EmbeddingAPIClient` registry (register, get, clear, concurrency)
- Embedding resolution through the new provider config (base URL from provider config, client type from model.API)
- Embedding custom provider registration and model lookup

Only `TestEmbeddingResolution` is mentioned, but the detail is thin.

**Suggested fix:** Add embedding-specific test cases to the testing tables:
- `TestEmbeddingAPIClientRegistry` -- parallel to `TestAPIClientRegistry`
- `TestEmbeddingCustomProvider` -- register custom provider, register embedding model against it, verify `Embed()` resolves correctly
- `TestEmbeddingBaseURLFromProvider` -- verify embedding providers no longer use `model.BaseURL`

---

### R1B-13 | P2 | ResolveEndpoint does not support base URL override from StreamOptions

The `ResolveEndpoint` function uses `cfg.BaseURL` unconditionally. There's no mechanism for a caller to override the base URL per-call via `StreamOptions`. While this is rarely needed, it could be useful for testing (pointing a production provider config at a mock server for a single call).

The current OpenAI provider ignores `model.BaseURL` (as noted in the problem statement), but the plan removes `BaseURL` from Model entirely. This means there's no per-call URL override mechanism at all.

**Suggested fix:** Consider adding `BaseURL string` to `StreamOptions` with resolution order: `opts.BaseURL` > `cfg.BaseURL`. Or document this as intentionally not supported (all URL overrides must go through `RegisterCustomProvider`).

---

### R1B-14 | P3 | Naming inconsistency: apiClientType in JSON vs APIClientType in Go

The catalog JSON uses `"apiClientType"` (camelCase) while the Go struct tag is `json:"apiClientType"` which matches. However, the plan text alternates between "API client type" and "apiClientType" and "client type name". The `APIClient.ClientType()` method returns the identifier, but `ProviderConfig` calls it `APIClientType`. This is fine but a comment should clarify they refer to the same value.

**Suggested fix:** Add a one-line comment on `ProviderConfig.APIClientType`: "Must match a registered APIClient's ClientType() return value."

This is already stated in the plan text but should be in the code comment on the struct field.

---

### R1B-15 | P3 | deepCopyProviderConfig does not need to be called on Get if the registry stores value types

The `ProviderConfig` is a value type (struct, not pointer). `GetProviderConfig` returns `deepCopyProviderConfig(cfg)` which clones the maps. But since `providerConfigRegistry` stores `ProviderConfig` by value (not pointer), reading from the map already returns a copy of the struct. The only mutable reference fields that need copying are the maps/slices (`KeyEnvVars`, `Headers`, `ProviderSpecific`). The current implementation handles this correctly, so this is just a note that the deep copy is indeed necessary for map/slice fields even with value-type storage.

No action needed -- this is a confirmation that the design is correct.

---

### R1B-16 | P3 | Open question about CalculateCost for zero pricing could use a type-level signal

The plan states custom models with zero pricing return zero cost and callers must treat `Usage.Cost.Total == 0` as "unknown" rather than "free". This is ambiguous -- some models genuinely have zero-cost tiers (e.g., Google Gemini embedding at $0.00/MTok in the catalog). A caller cannot distinguish "unknown pricing" from "genuinely free".

**Suggested fix:** Consider adding a `PricingKnown bool` field to `ModelCost` or a sentinel value. Or accept the ambiguity and document it.

---

## Summary

| ID | Severity | Title |
|----|----------|-------|
| R1B-1 | P0 | Anthropic beta headers cannot be expressed through ProviderSpecific map |
| R1B-2 | P1 | sourceID-based unregistration dropped with no replacement |
| R1B-3 | P1 | directAPIKey on ProviderConfig breaks JSON round-trip and deep copy expectations |
| R1B-4 | P1 | Embedding resolution asymmetry between chat and embedding client type lookup |
| R1B-5 | P1 | EmbeddingAPIClient registry not fully specified |
| R1B-6 | P1 | Catalog init order dependency between main and embedding catalogs |
| R1B-7 | P2 | No per-call API key or header override for embeddings |
| R1B-8 | P2 | EmbeddingModel struct update and provider migration not shown |
| R1B-9 | P2 | CustomProviderConfig duplicates ProviderConfig fields |
| R1B-10 | P2 | Google ProviderSpecific usage for URL path vs header not clarified |
| R1B-11 | P2 | Public re-export layer changes not specified |
| R1B-12 | P2 | Testing section missing embedding provider coverage |
| R1B-13 | P2 | No per-call base URL override mechanism |
| R1B-14 | P3 | Naming clarification for apiClientType across layers |
| R1B-15 | P3 | Deep copy correctness confirmation (no action needed) |
| R1B-16 | P3 | Zero pricing ambiguity for custom vs genuinely free models |
