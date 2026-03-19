# Code Review: aiag-7fu.1 (R1, reviewer-sea)

- Bead: aiag-7fu.1
- Commit range: da3779f..11205ca
- Plan doc: docs/plans/01-ai-core.add03.md (§3.1-3.6, §3.9, §3.11, §5.2)
- Reviewer: reviewer-sea
- Review commit: 11205ca

## Findings

### P2 - Embedding providers ignore ProviderEndpoint in embedSingle

**Location:** `internal/ai/provider/openai/embedding.go:64`, `internal/ai/provider/google/embedding.go:72`, `internal/ai/provider/cohere/embedding.go:81`

**Problem**
All three embedding providers construct a `ProviderEndpoint` in their `Embed()` method and pass it through `BatchEmbed`, but their `embedSingle` methods blank-receive it (`_ ai.ProviderEndpoint`) and continue to use `p.baseURL` and `p.apiKey` directly. This means the `ProviderEndpoint` argument is dead — it flows through the entire `BatchEmbed` pipeline but is never consumed.

This is a known transitional state since bead aiag-7fu.2 handles the full migration of entry points and provider client implementations. However, the current diff introduces a subtle inconsistency: `EmbedFunc`'s new signature accepts `ProviderEndpoint`, but all concrete implementations ignore it.

**Suggested fix**
No immediate fix needed — this is correctly scoped to aiag-7fu.2. Flag this as a verification point during that bead's review: once entry points migrate, these `_` parameters must be wired through to the HTTP calls, and the `p.baseURL`/`p.apiKey` fields should be removed from the embedding provider structs.

---

### P2 - Missing test: TestMultipleProvidersPerAPIClient

**Location:** Plan §5.2, test list

**Problem**
The plan's test list (§5.2) specifies `TestMultipleProvidersPerAPIClient`: "Register 'openai' and 'openrouter' both using 'openai-completions', verify independent resolution with different base URLs and keys." This test is not present in the implementation. While the individual registry tests cover basic CRUD, this end-to-end test would verify the core value proposition of the separation — that multiple providers can independently share an API client type.

**Suggested fix**
Add a test in `resolve_test.go` or `provider_registry_test.go` that registers two providers with the same `APIClientType` but different `BaseURL`/`KeyEnvVars`, calls `ResolveEndpoint` for each, and verifies the endpoints are independent.

---

### P2 - Missing test: TestMultiValuedHeaders

**Location:** Plan §5.2, test list

**Problem**
The plan specifies `TestMultiValuedHeaders`: "Verify multi-valued headers (e.g., Anthropic beta) are sent via Add semantics, not Set." While `TestResolveEndpoint/header_merge_same_key` covers the resolution side (verifying two values for the same key are preserved in the endpoint), there's no test that verifies the provider `applyHeaders` functions use `Add` rather than `Set` semantics. The diff does change all three providers from `Set` to `Add`, but this behavior isn't asserted in a test.

**Suggested fix**
Add a test (likely in the provider packages or as an integration test) that verifies multi-valued headers actually appear as separate HTTP header entries when applied. Alternatively, if the resolve-level test is considered sufficient coverage, note the rationale in a test comment.

---

### P3 - Anthropic wireRequest.Headers field updated but unused in new path

**Location:** `internal/ai/provider/anthropic/types_wire.go:20`

**Problem**
The `wireRequest.Headers` field was changed from `map[string]string` to `map[string][]string` (with `json:"-"` tag). This field appears to be set during request building but applied via the separate `applyHeaders` function, not serialized. The type change maintains consistency with the broader `map[string][]string` migration, but since this field is `json:"-"` and the struct is only used internally, it's worth confirming that the wireRequest.Headers population code (if any) was also updated.

**Suggested fix**
Verify the wireRequest builder code correctly populates this field with the new type. If the field is vestigial and only `applyHeaders` is used, consider removing it entirely to avoid confusion.

---

### P3 - ClearProviderConfigs nested lock acquisition

**Location:** `internal/ai/provider_registry.go:73-80`

**Problem**
`ClearProviderConfigs` acquires `providerConfigMu` then `directAPIKeysMu`. `UnregisterProviderConfig` follows the same order. `RegisterCustomProvider` calls `RegisterProviderConfig` (acquires `providerConfigMu`) then acquires `directAPIKeysMu`. The lock ordering is consistent across all call sites — this is good and matches the plan's explicit comment about ABBA deadlock avoidance. No actual bug here, just noting the verification was done.

**Suggested fix**
None — lock ordering is correct and consistent.

---

### P3 - BuildBaseOptions apiKey removal leaves callers responsible

**Location:** `internal/ai/options.go:55`

**Problem**
`BuildBaseOptions` no longer accepts an `apiKey` parameter. The three `StreamSimple` implementations (openai, anthropic, google) correctly removed the `p.apiKey` argument from their calls. Since these provider implementations still store `apiKey` on their struct (they haven't been migrated to `APIClient` yet in this bead), the API key will need to be threaded through `ResolveEndpoint` when the migration happens in aiag-7fu.2.

**Suggested fix**
No fix needed now — just a verification point for the next bead. The current `StreamSimple` implementations still use `p.apiKey` directly in their `applyHeaders` functions, which is correct for the transitional state.

---

## Plan Compliance Check

| Plan Section | Status | Notes |
|---|---|---|
| §3.1 APIClient interface | ✅ | Matches plan exactly |
| §3.2 API client registry | ✅ | Matches plan exactly |
| §3.2.1 Embedding API client registry | ✅ | Matches plan exactly |
| §3.3 ProviderConfig & ProviderEndpoint | ✅ | All fields match, directAPIKeys map present |
| §3.4 Provider registry | ✅ | deepCopyProviderConfig, all CRUD ops, lock ordering correct |
| §3.5 ResolveEndpoint | ✅ | 4-step key resolution, header merge with Add semantics |
| §3.6 Model struct changes | ✅ | BaseURL removed, PricingKnown added, Headers→map[string][]string |
| §3.9 Custom provider | ✅ | Validation, registration, direct API key storage |
| §3.11 Embedding provider consistency | ✅ | EmbeddingAPIClient interface, EmbeddingModel changes, EmbedFunc signature |
| §5.2 New tests | ⚠️ | Most present; TestMultipleProvidersPerAPIClient and TestMultiValuedHeaders missing |

## Summary

6 findings: 0 P0, 0 P1, 3 P2, 3 P3

**Verdict**: Approved with revisions

The implementation is solid and closely matches the plan. All core types, interfaces, and registries are correctly implemented with proper deep-copy isolation and concurrent-access safety. The transitional bridge pattern for embedding providers is well-structured. The two missing tests from §5.2 are the main gap — adding them would complete the test coverage the plan specifies.
