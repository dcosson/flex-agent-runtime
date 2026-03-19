# Code Review: aiag-7fu.2 (R1, reviewer-sea)

- Bead: aiag-7fu.2
- Commit range: 11205ca..63dd83f
- Plan doc: docs/plans/01-ai-core.add03.md (§3.7, §3.10-3.12, §4.1, §4.5-4.6, §5.2)
- Reviewer: reviewer-sea
- Review commit: 63dd83f

## Findings

### P1 - StreamSimple drops per-call opts when resolving endpoint

**Location:** `internal/ai/stream.go:33`

**Problem**
`StreamSimple` passes `StreamOptions{}` (empty) to `ResolveEndpoint` instead of `opts.StreamOptions`. This means any per-call API key or header overrides set via `SimpleStreamOptions.StreamOptions.APIKey` or `.Headers` are silently dropped during the new resolution path. The plan (line 586) explicitly shows `ResolveEndpoint(cfg, opts.StreamOptions)`.

The `Stream` function at line 17 correctly passes `opts` to `ResolveEndpoint`, so only `StreamSimple` is affected.

**Suggested fix**
Change line 33 from:
```go
endpoint := ResolveEndpoint(cfg, StreamOptions{})
```
to:
```go
endpoint := ResolveEndpoint(cfg, opts.StreamOptions)
```

---

### P2 - Embed entry point silently falls through on EmbeddingAPIClientType resolution failure

**Location:** `internal/ai/embedding_api.go:31-37`

**Problem**
The new `Embed()` resolution path tries `GetProviderConfig`, then `GetEmbeddingAPIClient(cfg.EmbeddingAPIClientType)`. If the provider config exists but `EmbeddingAPIClientType` is empty (which is valid — the plan says to fall back to `model.API`), the code silently falls through to the legacy path instead of trying `model.API` as the plan specifies (§3.11, lines 1064-1067).

The plan's resolution logic is:
1. Prefer `ProviderConfig.EmbeddingAPIClientType`
2. Fall back to `EmbeddingModel.API`
3. Only if both fail, error out

The current code skips step 2 and falls to the legacy path.

**Suggested fix**
```go
if cfg, err := GetProviderConfig(model.Provider); err == nil {
    clientType := cfg.EmbeddingAPIClientType
    if clientType == "" {
        clientType = model.API
    }
    if client, err := GetEmbeddingAPIClient(clientType); err == nil {
        endpoint := ResolveEndpoint(cfg, StreamOptions{})
        resp, err := client.Embed(ctx, endpoint, model, req)
        if err != nil {
            return nil, err
        }
        return finalizeEmbeddingResponse(resp, model)
    }
}
```

---

### P2 - Legacy shims retained despite plan §3.12 stating removal

**Location:** `internal/ai/provider/openai/provider.go`, `anthropic/provider.go`, `google/provider.go`, `internal/ai/registry.go`

**Problem**
The plan §3.12 states: "The old Provider interface and RegisterProvider / GetProvider functions are removed." The bead description also lists "Remove old Provider interface and registry.go — §3.12". However, the implementation retains the old `Provider` interface, `registry.go`, and legacy `Provider` wrapper types in all three provider packages.

This is understandable — there are 4 callers in `tests/` that still use `RegisterProvider`. However, it deviates from the plan and the project CLAUDE.md which says "DO NOT create fallbacks or leave around old behavior for backwards compatibility."

**Suggested fix**
Either:
1. Migrate the remaining 4 callers (`tests/integration/harness/runner.go`, `tests/integration/mode3/harness/remote_env.go`, `tests/external/tier1/stubserver_test.go`, `tests/external/tier2/agent_flow_test.go`) to the new pattern and remove the legacy shims, OR
2. Create a follow-up bead to track this cleanup, acknowledging it as intentional phased migration

Option 2 is fine if it's tracked — the concern is that without tracking, these shims may linger indefinitely.

---

### P2 - Demo `_ = key` assignments are dead code

**Location:** `demos/llm-demo/main.go:182,187,192`

**Problem**
The `registerChatProviders` function now checks for API keys but assigns them to `_ = key` — dead code that only exists to suppress the "unused variable" warning. The env var checks remain for the availability indication (appending to `available`), but the `key` variable is now unnecessary.

**Suggested fix**
Replace:
```go
if key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); key != "" {
    _ = key
    available = append(available, anthropicProvider)
}
```
with:
```go
if strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != "" {
    available = append(available, anthropicProvider)
}
```

---

### P3 - Old Register functions have signature mismatch with new ones

**Location:** `ai/provider/anthropic/reexport.go`, `ai/provider/google/reexport.go`

**Problem**
The public reexport layer exports both old `Register(Config, sourceID)` and new `Register(ClientConfig)` under the same name `Register`. This is not currently an issue because the old `Register` was removed from the internal packages and replaced by the new one — but the old signature callers in the integration tests (which still import and call `Register(Config{...}, sourceID)`) must be using the old `provider.Register` directly.

On closer inspection, the old `Register(Config, sourceID)` function was removed from the internal packages — only the new `Register(ClientConfig)` exists. The integration test callers use `ai.RegisterProvider(provider, sourceID)` directly, not the package-level `Register`. So this is not a compilation issue. No action needed.

**Suggested fix**
None — verified no conflict.

---

### P3 - Missing google embedding client registration

**Location:** `ai/provider/google/reexport.go`

**Problem**
The Google reexport layer does not export `NewEmbeddingClient` or `RegisterEmbeddingClient`, while OpenAI and Cohere do. Google does support embeddings (`gemini-embedding-001`). This may be because the Google embedding provider hasn't been refactored to `EmbeddingClient` yet in this commit.

**Suggested fix**
Check if Google's embedding provider was also refactored. If not, create a follow-up bead or note. If it was but the reexports are missing, add them.

---

### P3 - wireRequest.Headers field removed (good cleanup from R1 finding)

**Location:** `internal/ai/provider/anthropic/types_wire.go`

**Problem**
This is not a problem — noting that the R1 P3 finding about the vestigial `wireRequest.Headers` field was addressed. The field was removed entirely, along with the code that set it in `request.go:47`. Good cleanup.

**Suggested fix**
None — addressed.

---

## Plan Compliance Check

| Plan Section | Status | Notes |
|---|---|---|
| §3.7 Stream entry points | ⚠️ | Stream() correct; StreamSimple() has P1 bug with opts resolution |
| §3.10 API client impl changes | ✅ | All three providers correctly refactored to Client pattern |
| §3.11 Embedding provider consistency | ⚠️ | EmbeddingAPIClient implemented for openai/cohere; Embed() resolution missing model.API fallback |
| §3.12 Remove old Provider interface | ⚠️ | Retained as legacy shim — pragmatic but deviates from plan |
| §4.1 Provider package changes | ✅ | All 9 steps followed (rename, interface, endpoint param, etc.) |
| §4.5 AssistantMessage changes | ✅ | Provider name set from endpoint.ProviderName in all sendErrorEvent |
| §4.6 Embedding provider migration | ✅ | OpenAI + Cohere migrated; resolveBaseURL deleted |
| §5.2 Missing tests from R1 | ✅ | TestMultipleProvidersPerAPIClient and TestMultiValuedHeaders added |

## Summary

7 findings: 0 P0, 1 P1, 3 P2, 3 P3

**Verdict**: Approved with revisions

The migration is well-executed — all three chat provider implementations and two embedding providers are correctly refactored to the stateless Client pattern with ProviderEndpoint. The legacy Provider wrappers are cleanly implemented. The P1 `StreamSimple` opts bug is the only must-fix. The Embed() resolution fallback (P2) should also be addressed to match the plan's EmbeddingAPIClientType resolution logic.
