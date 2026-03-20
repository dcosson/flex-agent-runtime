# Code Review: aiag-7fu.3 (R1, reviewer-sea)

- Bead: aiag-7fu.3
- Commit range: 4a6d3ed..ff1ad9f
- Plan doc: docs/plans/01-ai-core.add03.md (§3.8, §6.1-6.3, §4.2-4.4, §5.2-5.3, §7.1)
- Reviewer: reviewer-sea
- Review commit: ff1ad9f

## Findings

### P2 - Catalog missing plan-specified provider headers

**Location:** `internal/ai/models/catalog.json`, `scripts/update-model-catalog.py:PROVIDER_CONFIGS`

**Problem**
The plan §3.8 and §6.2 specify that:
- Anthropic should have `headers: {"anthropic-beta": ["prompt-caching-2024-07-31", "max-tokens-3-5-sonnet-2024-07-15"]}` and `embeddingApiClientType: "anthropic-embeddings"`
- OpenRouter should have `headers: {"HTTP-Referer": ["https://flex-agent-runtime"], "X-Title": ["flex-agent-runtime"]}`

Neither is present in the catalog or the Python script's `PROVIDER_CONFIGS`.

The `embeddingApiClientType` for Anthropic is reasonable to omit (Anthropic doesn't offer embeddings), but the headers are plan-specified and functional — the Anthropic beta headers enable prompt caching and extended max tokens, and the OpenRouter headers are required by OpenRouter's ToS for attribution.

**Suggested fix**
Add the headers to `PROVIDER_CONFIGS` in the Python script and regenerate the catalog. If the Anthropic beta headers are already sent by the Anthropic client code and the OpenRouter headers aren't needed, add a plan note explaining the deviation.

---

### P2 - Embedding catalog `api` field is always present (never derived)

**Location:** `scripts/update-model-catalog.py:_embedding_model()`, `internal/ai/models/embedding_catalog.json`

**Problem**
The plan §6.3 states: "The `api` field in embedding catalog JSON entries is **optional** when the provider has `EmbeddingAPIClientType` set. If omitted, `API` is derived from `ProviderConfig.EmbeddingAPIClientType` at load time."

The Go loading code (`loadEmbeddingCatalog` in `embedding_models.go`) correctly implements derivation: if `m.API == "" && provCfg.EmbeddingAPIClientType != ""`, it sets `m.API = provCfg.EmbeddingAPIClientType`. However, the Python script always emits `api` for every embedding model, so the derivation path is never exercised.

This is functional — the explicit `api` value takes precedence — but the plan intended the catalog to be leaner (no redundant `api` when it matches the provider's `EmbeddingAPIClientType`). The derivation code path also goes untested at the catalog level.

**Suggested fix**
In the Python script, omit the `api` field from embedding models when it matches `PROVIDER_CONFIGS[provider].get("embeddingApiClientType")`. This exercises the Go derivation path and aligns with the plan. Add a test case in `TestCatalogNewFormat` that verifies at least one embedding model has `api` omitted in JSON but correctly derived at load time.

---

### P3 - TestCatalogNewFormat hardcodes model IDs that may change

**Location:** `internal/ai/models_test.go:700-703`

**Problem**
The test hardcodes specific model IDs like `"claude-sonnet-4-20250514"`, `"gpt-4o"`, `"gemini-2.5-pro"`. These will break when the catalog is regenerated with the Python script (which fetches live model data). The test validates the catalog format correctly but uses brittle anchors.

**Suggested fix**
Instead of checking for specific model IDs, verify structural properties: at least one model exists per expected provider, each model has the expected fields (name non-empty, API derived correctly from provider config, etc.). Or, keep the specific IDs but add a comment noting they match REQUIRED_MODELS in the Python script.

---

### P3 - Embedding catalog tests check `m.API != tc.wantAPI` but API is always explicit

**Location:** `internal/ai/models_test.go:748-756`

**Problem**
The embedding catalog test checks that each embedding model has the expected `API` value. Since all embedding models explicitly include `api` in the JSON (as noted in P2 above), this test only verifies explicit values, not the derivation logic. The derivation path (`api` omitted, derived from `EmbeddingAPIClientType`) is only tested indirectly by unit tests that manually construct models.

**Suggested fix**
Add a test case that modifies the raw JSON (or creates a synthetic catalog) with `api` omitted for a model whose provider has `EmbeddingAPIClientType`, and verifies it's correctly derived. This would cover the derivation path end-to-end.

---

### P3 - Python script `_is_fresh` uses `json.load` on potentially large catalog

**Location:** `scripts/update-model-catalog.py:_is_fresh()`

**Problem**
The freshness check parses the entire catalog JSON just to read the `lastUpdated` field. The catalog is ~2000+ lines. This is a minor efficiency concern — the script is a build tool run infrequently, so it doesn't matter in practice. Noting for completeness.

**Suggested fix**
None needed — acceptable for a build tool.

---

## Plan Compliance Check

| Plan Section | Status | Notes |
|---|---|---|
| §3.8 Catalog structure changes | ⚠️ | New format implemented correctly; missing plan-specified headers for anthropic and openrouter |
| §3.12 Remove old Provider interface | ✅ | `registry.go` deleted, `Provider` interface removed, all legacy shims removed from provider packages |
| §4.2 Caller migration | ✅ | All callers migrated: integration harness, tier1/tier2 tests, agent tests, codeinterp tests |
| §4.3 Demo CLI migration | ✅ | `demos/embedding-demo/main.go` updated |
| §4.4 Model registry migration | ✅ | Provider-scoped, PricingKnown set |
| §5.2 New tests - TestCatalogNewFormat | ✅ | Comprehensive test covering providers, chat models, embedding models |
| §5.3 Property tests | ✅ | TestResolutionDeterminism, TestRegistryIsolation, TestDeepCopyIntegrity all implemented with rapid |
| §6.1-6.2 Catalog format migration | ✅ | New format with lastUpdated, providers, models sections |
| §6.3 Embedding catalog | ✅ | Envelope format with lastUpdated, baseUrl removed, loadEmbeddingCatalog with provider validation |
| §7.1 Public re-export layer | ✅ | Legacy types removed from ai/ package, Google embedding reexport added |
| P1/P2 fixes from task 2 review | ✅ | StreamSimple opts and Embed() model.API fallback both fixed in 4a6d3ed |

## Summary

5 findings: 0 P0, 0 P1, 2 P2, 3 P3

**Verdict**: Approved with revisions

This is a well-executed final task that delivers the catalog format migration, full legacy shim removal, and property tests. The legacy removal is thorough — 36 files changed across the entire codebase with all callers migrated to the new pattern. The stream and embed entry points are now clean direct paths (no fallbacks). The catalog loading code correctly derives `model.API` from provider config at init time. Property tests cover determinism, isolation, and deep copy integrity.

The P2 findings are about plan deviations in catalog content (missing headers, always-explicit embedding API field) rather than correctness bugs. The codebase compiles, all tests pass (`make check` and `make test` clean), and the architecture matches the plan's intent.
