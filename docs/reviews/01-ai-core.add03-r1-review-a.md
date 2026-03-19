# Review: 01-ai-core.add03 -- Separate API Client Type from Provider, Fallback Provider

**Reviewer:** Reviewer A (Round 1)
**Date:** 2026-03-18
**Plan reviewed:** docs/plans/01-ai-core.add03.md

---

## Summary

The plan proposes a well-motivated separation of protocol implementation (APIClient) from service endpoint configuration (ProviderConfig). The architecture is sound, the motivation is clear, and the migration path is reasonable. However, there are several issues ranging from blocking to minor that should be addressed before implementation.

---

## Findings

### R1A-1 | P0 | Anthropic betaHeaders cannot be represented in ProviderSpecific

**Description:** The current Anthropic provider stores `betaHeaders []string` and adds them via `req.Header.Add("anthropic-beta", beta)` -- note the use of `Add` (not `Set`), because multiple beta feature flags are sent as separate header values on the same header key. The proposed `ProviderConfig.ProviderSpecific` is `map[string]string`, which can only hold a single string value for `apiVersion`. There is no way to express a list of beta headers. Similarly, `ProviderConfig.Headers` is `map[string]string`, which uses `Set` semantics (one value per key), not `Add` semantics (multiple values per key).

This means migrating the Anthropic provider will lose the ability to send multiple `anthropic-beta` header values unless this is addressed.

**Suggested fix:** Either:
- Change `ProviderSpecific` to `map[string]any` so it can hold `[]string` values (complicates JSON deserialization).
- Add a `MultiHeaders map[string][]string` field to `ProviderConfig` for headers that need `Add` semantics.
- Store beta headers as a comma-separated string in `ProviderSpecific["betaHeaders"]` and have the Anthropic client parse it (fragile but simple).
- Best option: change `Headers map[string]string` to `Headers map[string][]string` in both `ProviderConfig` and `ProviderEndpoint`, using `Add` for all of them. Single-value headers just have a one-element slice. This is more correct per HTTP spec anyway.

---

### R1A-2 | P0 | directAPIKey on ProviderConfig breaks JSON round-tripping and deep copy

**Description:** The plan adds an unexported `directAPIKey string` field to `ProviderConfig`. This has several issues:

1. `deepCopyProviderConfig` copies exported fields fine but the unexported `directAPIKey` field is copied by Go's value semantics in struct assignment -- this works, but only because `deepCopyProviderConfig` receives and returns by value. However, if the deep copy function is refactored to take a pointer in the future, this will silently break.

2. More critically, `ProviderConfig` is used as a JSON-serializable type (it has json tags on all exported fields, and the catalog deserialization uses `json.Unmarshal` into it). Having behavior depend on an unexported field that can't be serialized creates a split-brain: a `ProviderConfig` loaded from JSON vs one created via `RegisterCustomProvider` behave differently in ways that aren't visible through the public API.

3. `ResolveEndpoint` needs to check `cfg.directAPIKey` but this field is unexported. If `ResolveEndpoint` is in the same package (which it is), this works syntactically. But it makes testing harder -- tests can't construct a `ProviderConfig` with `directAPIKey` set without going through `RegisterCustomProvider`.

**Suggested fix:** Instead of an unexported field, store direct API keys in a separate unexported map (`directAPIKeys map[string]string`, keyed by provider name) at the module level, similar to how the registries work. `RegisterCustomProvider` writes to this map, and `ResolveEndpoint` checks it. This keeps `ProviderConfig` a clean, fully-serializable struct. Alternatively, add a `DirectAPIKey string` as an exported field with `json:"-"` to exclude it from serialization.

---

### R1A-3 | P1 | Embedding resolution has an unresolved ambiguity for API client type lookup

**Description:** Section 3.11 identifies an important nuance: a single provider (e.g., "openai") may use different API client types for chat (`openai-completions`) vs embeddings (`openai-embeddings`). The plan correctly notes that `EmbeddingModel.API` should be used for embedding client lookup, not `ProviderConfig.APIClientType`.

However, the naming of `ProviderConfig.APIClientType` is now misleading -- it only refers to the *chat* API client type. For embedding models under the same provider, a different client type is needed. This could confuse implementers.

Additionally, the embedding catalog format in section 6.3 retains `"api"` in the embedding model JSON, but there's no discussion of validation: what happens if someone adds an embedding model whose `Provider` references a `ProviderConfig` that doesn't exist in the catalog's `providers` section? The chat model catalog loading panics in this case (line ~605 of the plan), but the embedding catalog loading doesn't have this validation.

**Suggested fix:**
- Rename `ProviderConfig.APIClientType` to `ProviderConfig.ChatAPIClientType` or add a comment that this is specifically for chat completions. Better yet, add an `EmbeddingAPIClientType` field to `ProviderConfig` so both are declarative.
- Add validation in the embedding catalog `init()` that `model.Provider` matches a registered provider config, analogous to the chat catalog panic.

---

### R1A-4 | P1 | No mechanism for EmbeddingAPIClient to receive ProviderEndpoint

**Description:** Section 3.11 shows the `EmbeddingAPIClient` interface with `Embed(ctx, endpoint, model, req)` taking a `ProviderEndpoint`. However, the current `EmbeddingProvider` interface (in `embedding_provider.go`) is `Embed(ctx, model, req)` -- no endpoint parameter. The plan says the same separation applies to embedding providers, but the actual migration path for the `EmbeddingProvider` interface is not spelled out in the migration section (4.x).

The `Embed()` entry point in section 3.11 calls `ResolveEndpoint(cfg, StreamOptions{})` -- but `StreamOptions` is a chat-centric type. Embeddings have no `StreamOptions`. This is a conceptual mismatch.

**Suggested fix:**
- Define an `EmbeddingOptions` type (even if minimal/empty for now) that `ResolveEndpoint` can accept, or make `ResolveEndpoint` generic enough to accept just an API key override and headers.
- Explicitly list the `EmbeddingProvider` -> `EmbeddingAPIClient` migration in the migration section (4.x), parallel to the chat provider migration.
- Add `EmbeddingAPIClient` registry functions (`RegisterEmbeddingAPIClient`, `GetEmbeddingAPIClient`, `ClearEmbeddingAPIClients`) to the file organization and testing sections.

---

### R1A-5 | P1 | sourceID removal is not addressed

**Description:** The current `RegisterProvider` takes a `sourceID string` parameter used for bulk unregistration via `UnregisterProviders(sourceID)`. The new `RegisterProviderConfig` and `RegisterAPIClient` do not have `sourceID` parameters, and there is no equivalent of `UnregisterProviders` for provider configs. The plan removes `UnregisterProviders` functionality without replacement.

If callers depend on `sourceID`-based bulk unregistration (e.g., for teardown when a set of providers is no longer needed), this is a breaking change without a migration path.

**Suggested fix:** Either document that `sourceID` is intentionally removed and explain why it's no longer needed (e.g., `UnregisterProviderConfig(name)` replaces it for individual removal), or add a `sourceID` field to `ProviderConfig` and a `UnregisterProviderConfigsBySource(sourceID)` function. At minimum, note this removal in the migration section.

---

### R1A-6 | P1 | RegisterCustomProvider validates API client type exists, but API clients may not be registered yet

**Description:** `RegisterCustomProvider` calls `GetAPIClient(cfg.APIClientType)` to validate that the API client type exists before registering the provider config. However, if custom providers are registered early (e.g., in `init()` or during config loading), API clients may not be registered yet -- the provider packages' `init()` functions may not have run.

Go's `init()` order depends on import graph, and if custom provider registration happens in a different package that imports before the provider packages, the validation will fail spuriously.

**Suggested fix:** Make the validation lazy -- check at `Stream()`/`Embed()` time (which already validates via `GetAPIClient`), not at registration time. Or document clearly that `RegisterCustomProvider` must be called after API clients are registered. The registration-time validation adds little value since `Stream()` already validates.

---

### R1A-7 | P2 | Catalog format change requires atomic migration of all existing tests

**Description:** The catalog.json format change from flat `provider -> modelID -> Model` to the new `{providers: {}, models: {}}` structure is a breaking change to the embedded catalog. All existing tests that depend on the catalog loading will need to pass before and after this change. There's no incremental migration path described -- it's a big-bang change to the catalog format.

The plan should describe whether this migration happens in a single commit or if there's a transitional period. Given that `init()` panics on parse failure, a half-migrated catalog will crash the entire package.

**Suggested fix:** Add a note to the migration section specifying that the catalog format change must be atomic: new `catalog.json` + new `init()` parsing code + updated `catalogFile` struct must land in the same commit. Tests that construct mock catalogs or test catalog loading will need to be updated in the same commit.

---

### R1A-8 | P2 | BaseURL override at call time is not supported

**Description:** The plan removes `BaseURL` from `Model` and makes it a provider-level concern. However, there's no mechanism to override the base URL per-call in `StreamOptions`. Currently, `StreamOptions` has `APIKey` for per-call key override and `Headers` for per-call header override, but no `BaseURL` field.

This means a caller who needs to route a single request to a different endpoint (e.g., for A/B testing, or failover to a backup) must register a new provider config. This is functional but heavier than a per-call override.

**Suggested fix:** Consider adding `BaseURL string` to `StreamOptions` (empty means use provider default). The `ResolveEndpoint` function would check `opts.BaseURL` before `cfg.BaseURL`. This is a minor convenience but consistent with how `APIKey` already works.

---

### R1A-9 | P2 | AssistantMessage.Provider set by stream entry points -- but how?

**Description:** Section 3.12/4.5 states that `AssistantMessage.Provider` and `AssistantMessage.API` are set by the stream entry points before delegating to the API client. However, looking at the proposed `Stream()` function in section 3.7, there is no code that sets these fields. The current code has providers setting these directly (e.g., `Provider: "openai"` in `sendErrorEvent`).

If the stream entry point is supposed to inject provider/API info, there needs to be a mechanism -- either wrapping the returned `EventStream` to annotate events, or passing the provider name to the API client so it can set it. The plan doesn't specify which approach to use.

**Suggested fix:** Specify the mechanism. Options:
1. The `ProviderEndpoint.ProviderName` field is already passed to `APIClient.Stream()`, so client implementations can use it to set `AssistantMessage.Provider`. This requires API clients to be aware of provider names.
2. The stream entry point wraps the `EventStream` and annotates events with provider/API info. More complex but keeps API clients truly stateless about their identity.

Either way, document the chosen approach explicitly.

---

### R1A-10 | P2 | No test for overwriting a catalog provider with RegisterCustomProvider

**Description:** The test matrix in section 5.2 covers custom provider registration and multiple providers per API client type. However, there is no test for what happens when `RegisterCustomProvider` is called with a name that matches an existing catalog provider (e.g., `Name: "openai"`). This would overwrite the catalog-loaded provider config with new settings (potentially different base URL, credentials, etc.).

This is a valid use case (e.g., pointing "openai" at a proxy), but it should be tested to ensure the overwrite is clean and the old config doesn't leak through.

**Suggested fix:** Add a test case: `TestCustomProviderOverwritesCatalogProvider` -- register a custom provider with the same name as a catalog provider, verify the new config is used.

---

### R1A-11 | P2 | CustomModelOpts duplicates most of Model struct fields

**Description:** `CustomModelOpts` has `Name`, `Reasoning`, `Input`, `Cost`, `ContextWindow`, `MaxTokens`, `Compat` -- which is essentially `Model` minus `ID`, `Provider`, and `API`. This duplication means any future field added to `Model` must also be added to `CustomModelOpts`.

**Suggested fix:** Consider using `Model` directly with `RegisterCustomModel` just setting the provider-derived fields, or embed a partial struct. Alternatively, accept this duplication as intentional API design (keeping `CustomModelOpts` focused on what the caller should provide) and add a comment noting the correspondence.

---

### R1A-12 | P2 | CalculateCost for custom models with zero pricing is mentioned in Open Questions but not tested

**Description:** Section 8 (Open Questions) mentions that `CalculateCost` returns zero cost for custom models with zero pricing and callers must handle `Usage.Cost.Total == 0` as "unknown" rather than "free." However, no test in section 5.2 covers this scenario, and there's no guidance on how callers should distinguish "free" (e.g., local Ollama) from "unknown pricing."

**Suggested fix:** Add a test for `CalculateCost` with zero-valued `ModelCost`. Consider adding a `PricingKnown bool` field to `Model` or documenting a convention (e.g., custom models always have `Cost` zero-valued, catalog models always have it set).

---

### R1A-13 | P3 | JSON tag inconsistency: baseUrl vs baseURL

**Description:** The plan uses `json:"baseUrl"` (camelCase with lowercase 'rl') throughout, matching the existing catalog format. The Go field is `BaseURL` (with uppercase 'URL'). This is consistent with Go convention for acronyms in field names and is correct. Just noting that `baseUrl` in JSON is slightly unusual (most JSON APIs use `baseUrl` or `base_url`), but it matches the existing catalog so this is fine.

No change needed -- this is just a note for awareness.

---

### R1A-14 | P3 | ClearAPIClients and ClearProviderConfigs should be documented as test-only

**Description:** Both `ClearAPIClients()` and `ClearProviderConfigs()` are documented with "For testing." in comments, matching the pattern of existing `ClearProviders()` and `ClearModels()`. Consider adding a build tag or at least a more prominent warning to prevent production use.

**Suggested fix:** Minor: add `// For testing only. Do not call in production code.` to match the level of warning. Not blocking.

---

### R1A-15 | P3 | Missing file in file organization: resolve.go tests

**Description:** Section 7 lists `resolve.go` as a new file, and section 5.2 lists `TestResolveEndpoint` and `TestResolveEndpointEnvVars` as new tests. The test file `resolve_test.go` should be listed in the file organization for completeness.

**Suggested fix:** Add test files to the file organization list, or note that test files follow the standard `*_test.go` convention and aren't explicitly listed.

---

## Summary Table

| ID | Severity | Title |
|----|----------|-------|
| R1A-1 | P0 | Anthropic betaHeaders cannot be represented in ProviderSpecific |
| R1A-2 | P0 | directAPIKey on ProviderConfig breaks JSON round-tripping and deep copy |
| R1A-3 | P1 | Embedding resolution has an unresolved ambiguity for API client type lookup |
| R1A-4 | P1 | No mechanism for EmbeddingAPIClient to receive ProviderEndpoint |
| R1A-5 | P1 | sourceID removal is not addressed |
| R1A-6 | P1 | RegisterCustomProvider validates API client type exists, but API clients may not be registered yet |
| R1A-7 | P2 | Catalog format change requires atomic migration of all existing tests |
| R1A-8 | P2 | BaseURL override at call time is not supported |
| R1A-9 | P2 | AssistantMessage.Provider set by stream entry points -- but how? |
| R1A-10 | P2 | No test for overwriting a catalog provider with RegisterCustomProvider |
| R1A-11 | P2 | CustomModelOpts duplicates most of Model struct fields |
| R1A-12 | P2 | CalculateCost for custom models with zero pricing is mentioned but not tested |
| R1A-13 | P3 | JSON tag inconsistency note: baseUrl vs baseURL |
| R1A-14 | P3 | ClearAPIClients and ClearProviderConfigs should be documented as test-only |
| R1A-15 | P3 | Missing file in file organization: resolve.go tests |
