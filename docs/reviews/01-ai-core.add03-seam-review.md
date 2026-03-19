# Seam Review: 01-ai-core.add03 (API Client / Provider Separation)

**Reviewer:** seam-review agent
**Date:** 2026-03-18
**Plan reviewed:** docs/plans/01-ai-core.add03.md (status: Reviewed, post-R2)
**Source files examined:** models.go, registry.go, stream.go, options.go, provider.go, embedding.go, embedding_models.go, embedding_api.go, embedding_provider.go, embedding_registry.go, provider/openai/provider.go, provider/openai/stream.go, provider/openai/embedding.go, provider/anthropic/provider.go, provider/google/provider.go, provider/cohere/embedding.go, models/catalog.json

---

## 1. Vertical Slices

### 1.1 Chat Completion Path

**Trace:** Caller -> `Stream(model)` -> `GetProviderConfig(model.Provider)` -> `GetAPIClient(cfg.APIClientType)` -> `ResolveEndpoint(cfg, opts)` -> `client.Stream(endpoint, model, ...)` -> HTTP request using `endpoint.BaseURL + path`, `endpoint.APIKey`, `endpoint.Headers`

**Current state:** `stream.go` calls `GetProvider(model.API)` which returns a `Provider` interface with baseURL/apiKey baked in. The plan changes this to a two-step lookup: provider config by name, then API client by type.

**Handoff analysis:**

- `model.Provider` (string) -> `GetProviderConfig(name)` -> `ProviderConfig`: Clean handoff. The model struct already has a `Provider` field (currently used as a grouping key in the model registry). The plan tightens its semantics to require a registered provider name. No type mismatch.

- `cfg.APIClientType` (string) -> `GetAPIClient(clientType)` -> `APIClient`: Clean handoff. The API client type string on `ProviderConfig` must match an `APIClient.ClientType()` return value. No compile-time enforcement but validated at call time with clear error messages.

- `ResolveEndpoint(cfg, opts)` -> `ProviderEndpoint`: Produces a resolved endpoint from config + opts. Key resolution order (opts.APIKey > directAPIKeys > env vars > empty) is well-specified. Header merging uses Add semantics.

- `client.Stream(ctx, endpoint, model, llmCtx, opts)` -> HTTP request: The client uses `endpoint.BaseURL` to construct the URL, `endpoint.APIKey` for auth, and `endpoint.Headers` for provider-level headers. Model-level headers (`model.Headers`) applied separately.

**Finding SR-1 (P1): `StreamOptions.Headers` type mismatch between current code and plan.** The current `options.go` has `Headers map[string]string` (line 30). The plan specifies changing this to `map[string][]string` (Section 3.5). The plan acknowledges this change in Section 3.5 and Section 7 (options.go listed as CHANGED). However, the `ResolveEndpoint` function in Section 3.5 iterates `opts.Headers` as `map[string][]string`. This is correctly noted but worth tracking: the current `applyHeaders` in `openai/stream.go` iterates `opts.Headers` as `map[string]string` (line 105-107). Every call site that constructs `StreamOptions` with Headers must be updated. The plan's migration section (4.1-4.6) does not explicitly call out callers that construct StreamOptions with headers -- only provider-level changes are listed.

**Finding SR-2 (P2): `BuildBaseOptions` apiKey parameter removal impact.** The plan (Section 3.5 note) says `BuildBaseOptions` has its `apiKey` parameter removed since key resolution moves to `ResolveEndpoint`. Current code in `openai/stream.go` line 22 calls `ai.BuildBaseOptions(model, &opts, p.apiKey)`. After the refactor, providers no longer have `p.apiKey`, so this parameter removal is consistent. However, every provider's `StreamSimple` implementation calls `BuildBaseOptions` with an apiKey argument. The plan should ensure all three providers (openai, anthropic, google) update their `StreamSimple` implementations. This is covered by Section 4.1 step 5 ("Remove stored baseURL and apiKey fields") but is implicit rather than explicit about `BuildBaseOptions` call sites.

**Finding SR-3 (P2): `sendErrorEvent` in openai/stream.go hardcodes `Provider: "openai"`.** After the refactor, `sendErrorEvent` should use `endpoint.ProviderName` instead (as specified in Section 3.7 and 4.5). The current code at line 123 uses a hardcoded string `"openai"`. The plan's example in Section 3.10 does not show an updated `sendErrorEvent`, but the principle is stated. This will need to be caught during implementation -- the `sendErrorEvent` function signature needs to accept `ProviderEndpoint` (or at least the provider name string).

### 1.2 Embedding Path

**Trace:** Caller -> `Embed(modelID)` -> `GetEmbeddingModel(modelID)` -> `GetProviderConfig(model.Provider)` -> resolve embedding client type (prefer `cfg.EmbeddingAPIClientType`, fallback `model.API`) -> `GetEmbeddingAPIClient(clientType)` -> `ResolveEndpoint(cfg, StreamOptions{})` -> `client.Embed(endpoint, model, req)`

**Current state:** `embedding_api.go` calls `GetEmbeddingProvider(model.API)` directly. Providers store baseURL/apiKey internally. `model.BaseURL` is used as a fallback in OpenAI and Cohere embedding providers via `resolveBaseURL(model)`.

**Handoff analysis:**

- `GetProviderConfig(model.Provider)`: The `EmbeddingModel` struct currently has a `Provider` field (e.g., "openai"). This maps to the same provider config registry used for chat. Clean handoff.

- Embedding client type resolution: `cfg.EmbeddingAPIClientType` (preferred) or `model.API` (fallback). Both are strings that must match an `EmbeddingAPIClient.ClientType()`. This dual-path is documented in Section 3.11.

- `ResolveEndpoint(cfg, StreamOptions{})`: The embedding path passes an empty `StreamOptions` since there are no per-call overrides for embeddings. This means API key resolution falls through to directAPIKeys and env vars. This works but means per-call API key override is not available for embeddings. The plan acknowledges this as intentional (Section 3.11).

**Finding SR-4 (P1): `EmbedFunc` type signature incompatible with new `EmbeddingAPIClient` interface.** The current `EmbedFunc` in `embedding.go` line 81 is:
```go
type EmbedFunc func(ctx context.Context, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error)
```
The new `EmbeddingAPIClient.Embed()` takes a `ProviderEndpoint` parameter:
```go
Embed(ctx context.Context, endpoint ProviderEndpoint, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error)
```
`BatchEmbed` uses `EmbedFunc` internally. After the refactor, `EmbedFunc` must be updated to include `ProviderEndpoint`, OR `BatchEmbed` must be restructured. The plan notes this in Section 7 ("EmbedFunc type signature needs updating to include ProviderEndpoint, or removed if superseded") but does not make a definitive decision. This needs resolution before implementation -- it affects all three embedding provider implementations (openai, google, cohere) that call `ai.BatchEmbed(ctx, p.embedSingle, model, req)`.

**Finding SR-5 (P1): `model.BaseURL` removal breaks existing embedding provider `resolveBaseURL`.** OpenAI embedding provider (`openai/embedding.go` line 131-136) and Cohere embedding provider (`cohere/embedding.go` line 186-191) both have a `resolveBaseURL(model)` method that checks `model.BaseURL` first, falling back to `p.baseURL`. After the refactor:
- `model.BaseURL` is removed from `EmbeddingModel`
- `p.baseURL` is removed from the provider struct (stateless client)
- The base URL comes from `endpoint.BaseURL`

This is a clean replacement but the `resolveBaseURL` method must be entirely removed, and the calling code in `embedSingle` must use `endpoint.BaseURL` instead. The plan covers this in Section 4.6 step 3 but does not mention that `resolveBaseURL` helper methods need deletion. Minor gap, easily caught during implementation.

### 1.3 Custom Provider Path

**Trace:** `RegisterCustomProvider(cfg)` -> validates Name/APIClientType/BaseURL -> stores direct API key in `directAPIKeys` map -> `RegisterProviderConfig(cfg.ProviderConfig)` -> Later: `RegisterCustomModel(providerName, modelID, opts)` -> `GetProviderConfig(providerName)` -> builds `Model` with derived API field -> `RegisterModel(m)` -> Later: `Stream()` resolves normally through provider config + API client.

**Handoff analysis:**

- `RegisterCustomProvider` validation: Checks Name, APIClientType, BaseURL non-empty. Does NOT validate that APIClientType matches a registered API client (intentional, documented in Section 3.9). Clean.

- Direct API key storage: `directAPIKeys[cfg.Name] = cfg.APIKey`. `ResolveEndpoint` checks this map as step 2. The mutex pattern uses separate locks (`directAPIKeysMu` for the key map, `providerConfigMu` for the config registry). No deadlock risk since `ResolveEndpoint` only reads `directAPIKeys` (RLock) and `UnregisterProviderConfig` acquires `providerConfigMu` then `directAPIKeysMu` -- consistent lock ordering.

**Finding SR-6 (P2): Lock ordering in `UnregisterProviderConfig`.** Section 3.4 shows `UnregisterProviderConfig` acquiring `providerConfigMu.Lock()` first, then `directAPIKeysMu.Lock()`. `ClearProviderConfigs` follows the same order. `ResolveEndpoint` acquires only `directAPIKeysMu.RLock()`. This is consistent -- no deadlock. But `RegisterCustomProvider` (Section 3.9) acquires `directAPIKeysMu.Lock()` first, then calls `RegisterProviderConfig` which acquires `providerConfigMu.Lock()`. This is the REVERSE order of `UnregisterProviderConfig`. Under concurrent registration and unregistration, this is a classic ABBA deadlock:
- Thread 1 (RegisterCustomProvider): Lock directAPIKeysMu, then Lock providerConfigMu
- Thread 2 (UnregisterProviderConfig): Lock providerConfigMu, then Lock directAPIKeysMu

This must be fixed. Either: (a) `RegisterCustomProvider` should store the direct key AFTER calling `RegisterProviderConfig`, or (b) both functions should acquire locks in the same order.

### 1.4 Catalog Loading Path

**Trace:** `init()` -> parse `catalog.json` (new format with `providers` + `models` sections) -> Phase 1: register provider configs -> Phase 2: register chat models (derive API from provider) -> Phase 3: register embedding models (from embedding catalog, derive API from provider's EmbeddingAPIClientType).

**Current state:** `models.go` init() parses a flat `map[string]map[string]Model` from `catalog.json`. `embedding_models.go` has a separate init() parsing `embedding_catalog.json` as `[]EmbeddingModel`.

**Handoff analysis:**

- The plan consolidates into a single `init()` in `models.go` for providers + chat models + embedding models. This means `embedding_models.go` no longer has its own `init()`.

**Finding SR-7 (P2): Embedding catalog loading location ambiguity.** The plan says "single init() in models.go" (Section 3.8) for all three phases, including embedding models. But currently `embedding_models.go` has its own `//go:embed models/embedding_catalog.json` directive and `init()` function. The plan's Section 7 lists `embedding_models.go` as needing to lose its separate `init()`. However, Section 6.3 says the embedding catalog format is a JSON array (not nested under "providers"), and it needs to reference the already-registered provider configs. If the embedding catalog init is moved into `models.go`, the `//go:embed` directive for `embedding_catalog.json` must also move there, or the `embeddingCatalogJSON` var must be accessible from `models.go`. This is workable (both files are in the same package) but the plan should explicitly state whether `embedding_catalog.json` loading stays in `embedding_models.go` (called from models.go init) or moves entirely into `models.go`.

---

## 2. Horizontal Seams

### 2.1 Type Consistency: Headers `map[string][]string`

The plan mandates `map[string][]string` for headers across:
- `ProviderConfig.Headers` -- Section 3.3: YES
- `ProviderEndpoint.Headers` -- Section 3.3: YES
- `Model.Headers` -- Section 3.6: YES (changed from `map[string]string`)
- `EmbeddingModel.Headers` -- Section 3.11: YES (changed from `map[string]string`)
- `StreamOptions.Headers` -- Section 3.5 note: YES (changed from `map[string]string`)

**Current code types:**
- `Model.Headers`: `map[string]string` (models.go line 32)
- `EmbeddingModel.Headers`: `map[string]string` (embedding.go line 73)
- `StreamOptions.Headers`: `map[string]string` (options.go line 30)

All three need changing. The plan is consistent about this.

**Finding SR-8 (P2): `deepCopyModel` must be updated for `map[string][]string` Headers.** Current `deepCopyModel` (models.go lines 131-156) copies `Headers` as `map[string]string`. After the type change to `map[string][]string`, the deep copy must clone the inner slices, matching the pattern used in `deepCopyProviderConfig` (plan Section 3.4). The plan does not explicitly call this out for `deepCopyModel` (it only mentions `deepCopyEmbeddingModel` in Section 3.11). This is an omission.

### 2.2 Registry Lifecycle Consistency

| Registry | Register | Get | Clear | Unregister |
|----------|----------|-----|-------|------------|
| API Client | RegisterAPIClient | GetAPIClient | ClearAPIClients | N/A (not needed) |
| Embedding API Client | RegisterEmbeddingAPIClient | GetEmbeddingAPIClient | ClearEmbeddingAPIClients | N/A |
| Provider Config | RegisterProviderConfig | GetProviderConfig | ClearProviderConfigs | UnregisterProviderConfig |
| Model (existing) | RegisterModel | GetModel | ClearModels | N/A |
| Embedding Model (existing) | RegisterEmbeddingModel | GetEmbeddingModel | ClearEmbeddingModels | N/A |

The API Client and Embedding API Client registries do NOT have individual Unregister functions. This is intentional -- API clients are protocol implementations registered once at init, not dynamically added/removed. Provider configs DO have Unregister because providers can be added/removed at runtime (custom providers).

Consistent and well-reasoned.

### 2.3 Deep Copy Completeness

| Type | Mutable Fields | Deep Copy Function | Status |
|------|---------------|-------------------|--------|
| ProviderConfig | KeyEnvVars ([]string), Headers (map[string][]string), ProviderSpecific (map[string]string) | deepCopyProviderConfig | Specified in plan Section 3.4 |
| Model | Headers (map[string][]string after change), Input ([]string), Compat (*ModelCompat with ReasoningEffortMap) | deepCopyModel | Existing, needs update for Header type change (SR-8) |
| EmbeddingModel | Headers (map[string][]string after change) | deepCopyEmbeddingModel | Existing, needs update (plan Section 3.11 notes this) |
| ProviderEndpoint | Headers (map[string][]string), ProviderSpecific (map[string]string) | N/A (not stored in registry) | Not needed -- created fresh by ResolveEndpoint |

### 2.4 Error Propagation

- Missing provider config: `GetProviderConfig` returns `fmt.Errorf("no provider registered: %s", name)` -- clear.
- Missing API client: `GetAPIClient` returns `fmt.Errorf("no API client registered for type: %s", clientType)` -- clear.
- Stream wraps: `fmt.Errorf("provider %q uses API client type %q: %w", ...)` -- includes both provider name and client type in error chain. Good.
- Missing embedding API client: `GetEmbeddingAPIClient` same pattern. Good.
- Missing model: Existing `GetModel` error messages unchanged. Good.

### 2.5 Thread Safety

All registries use `sync.RWMutex` with RLock for reads and Lock for writes. Consistent.

**SR-6 (already noted):** Lock ordering issue between `RegisterCustomProvider` and `UnregisterProviderConfig`.

### 2.6 PricingKnown Field

- Catalog models: `PricingKnown = true` set in init() Phase 2 (Section 3.8). Correct.
- Custom models: `PricingKnown = false` by default via zero-value of `CustomModelOpts.PricingKnown` (Section 3.9). Correct.
- `CalculateCost`: The plan mentions PricingKnown in the context of distinguishing free vs unknown (Section 8, Open Questions) but does not specify any change to `CalculateCost` behavior itself. `CalculateCost` currently calculates regardless of PricingKnown. The field is informational for callers to interpret the result. This is fine.

---

## 3. Findings Summary

| ID | Severity | Title | Description |
|----|----------|-------|-------------|
| SR-1 | P1 | StreamOptions.Headers callers not enumerated | Plan changes `StreamOptions.Headers` from `map[string]string` to `map[string][]string` but migration section does not enumerate all call sites constructing StreamOptions with Headers. Risk of missed call sites during implementation. |
| SR-2 | P2 | BuildBaseOptions apiKey removal implicit in migration | All three providers call `BuildBaseOptions` with apiKey. The parameter removal is implied by provider changes but not explicitly called out. Low risk but worth noting. |
| SR-3 | P2 | sendErrorEvent hardcodes provider name | Provider error event helpers (at minimum in openai) hardcode `Provider: "openai"`. Must be updated to use `endpoint.ProviderName`. Not shown in plan's example code. |
| SR-4 | P1 | EmbedFunc type signature undecided | `EmbedFunc` must gain a `ProviderEndpoint` parameter (or be removed) to match the new `EmbeddingAPIClient.Embed()` signature. The plan defers this decision. All three embedding providers (openai, google, cohere) that use `BatchEmbed` are affected. |
| SR-5 | P1 | resolveBaseURL helper deletion not mentioned | OpenAI and Cohere embedding providers have `resolveBaseURL(model)` methods that use `model.BaseURL` (which is being removed) and `p.baseURL` (which is being removed). These methods must be deleted and replaced with `endpoint.BaseURL`. The plan covers the principle but not these specific methods. |
| SR-6 | P1 | ABBA deadlock in RegisterCustomProvider vs UnregisterProviderConfig | `RegisterCustomProvider` acquires `directAPIKeysMu` then `providerConfigMu`. `UnregisterProviderConfig` acquires `providerConfigMu` then `directAPIKeysMu`. This is a deadlock under concurrent access. Fix: store direct key AFTER calling `RegisterProviderConfig` in `RegisterCustomProvider`. |
| SR-7 | P2 | Embedding catalog init() consolidation unclear | Plan says single init() in models.go but does not specify where the `//go:embed models/embedding_catalog.json` directive lives or how embedding model loading is invoked from the unified init(). |
| SR-8 | P2 | deepCopyModel not updated for map[string][]string Headers | Plan calls out deepCopyEmbeddingModel update (Section 3.11) but does not mention the equivalent update needed for deepCopyModel when Model.Headers changes type. |

---

## 4. Verdict

**The plan needs one more pass to address the P1 findings before implementation.**

Specifically:

1. **SR-6 (ABBA deadlock)** is a correctness bug in the plan's pseudocode. `RegisterCustomProvider` must store the direct API key AFTER calling `RegisterProviderConfig`, not before, to maintain consistent lock ordering. This is a one-line reorder in Section 3.9.

2. **SR-4 (EmbedFunc)** needs a definitive decision. The recommended resolution: update `EmbedFunc` to include `ProviderEndpoint` as its second parameter, matching the `EmbeddingAPIClient.Embed()` signature. `BatchEmbed` then passes the endpoint through to each batch call. This keeps the shared batch-splitting utility working cleanly.

3. **SR-5 (resolveBaseURL deletion)** is low risk but should be noted in Section 4.6 to avoid confusion during implementation. Add a bullet: "Delete resolveBaseURL helper methods -- base URL now comes exclusively from endpoint.BaseURL."

4. **SR-1 (StreamOptions.Headers callers)** should be addressed by adding a note to Section 4.2 that all callers constructing `StreamOptions` with `Headers` must update from `map[string]string` to `map[string][]string`. A `grep` for `Headers:.*map\[string\]string` will catch these during implementation.

The P2 findings (SR-2, SR-3, SR-7, SR-8) are minor gaps that competent implementers will catch but should ideally be noted in the plan for completeness.

Overall the plan is thorough and well-structured. The R1 and R2 reviews already caught and resolved the major architectural issues (multi-valued headers, direct API key storage, embedding API client registry, init ordering). The remaining findings above are implementation-level wiring details rather than architectural problems.
