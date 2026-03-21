# E2E Wiring Review: Live API Tests (tests/liveapi/)

- Reviewer: reviewer-sea
- Review commit: 1759456
- Date: 2026-03-20

## Wiring Verification

### 1. Credential Loading ✅

**Path:** `credentials.go:setCredentialEnvVars()` → `loadCredentials()` → `parseEnvFile()`

- `parseEnvFile()` uses `strings.Cut(line, "=")` which correctly handles KEY=VALUE format
- Skips blank lines and `#` comments
- `setCredentialEnvVars()` only sets env vars not already present (`if os.Getenv(k) == ""`), correctly implementing file-as-default, env-as-override
- All 5 provider keys covered: ANTHROPIC_API_KEY, OPENAI_API_KEY, GOOGLE_API_KEY, OPENROUTER_API_KEY, COHERE_API_KEY
- Matches `~/.flexagent/test-credentials.env` format from Plan 22 §10

**Verdict:** Wiring correct.

### 2. Provider Registration ✅

**Path:** `liveapi_test.go:registerProviders()` → `provider{X}.Register()` / `RegisterEmbeddingClient()`

- Chat providers registered: anthropic (`anthropic-messages`), openai (`openai-completions`), google (`google-genai`)
- Embedding providers registered: openai (`openai-embeddings`), google (`google-embeddings`), cohere (`cohere-embeddings`)
- OpenRouter uses the same `openai-completions` API client type — correctly relies on catalog-based config routing (baseUrl → `https://openrouter.ai/api/v1`)
- Cohere correctly has NO chat `Register()` call (embeddings only)
- Provider configs loaded from embedded `models/catalog.json` at init time before `registerProviders()` runs — correct ordering (init runs before TestMain)

**Verdict:** Wiring correct.

### 3. Stream Wiring ✅

**Path:** `ai.StreamSimple()` → `GetProviderConfig()` → `GetAPIClient()` → `ResolveEndpoint()` → `client.StreamSimple()` → goroutine → HTTP POST → SSE scanner → provider processor → `.C` channel

- Tests consume events via `for ev := range es.C` then call `es.Result()` — correct terminal-event pattern
- `EventStream.C` is buffered (32 events), goroutine produces, test consumes — no deadlock risk
- Context timeouts (60s-90s) propagate to HTTP request via `context.WithTimeout` — correct cancellation chain
- SSE scanner uses zero-copy parsing with size limits (1 MiB per line, 4 MiB per event) — no memory exhaustion risk

**Verdict:** Wiring correct.

### 4. Tool Call Wiring ✅

**Path:** `ai.Tool{Parameters: json.RawMessage}` → `llmCtx.Tools` → provider `buildRequest()` → wire format

- Test creates `ai.Tool` with `json.RawMessage` JSON schema — this is the correct type for tool parameters
- `TransformMessages()` adapts the conversation across providers (role mapping, tool result formatting)
- Turn 1: user message + tool → provider returns `*ai.ToolCall` content block with `ID` and `Name`
- Turn 2: `ai.ToolResultMessage{ToolCallID: toolCall.ID}` — correctly links result to call via ID
- Anthropic maps tools to `tools[]` array; OpenAI maps to `tools[].function`; Google maps to `functionDeclarations`
- Tool result content uses `[]ai.ContentBlock{&ai.TextContent{Text: "..."}}` — correct content block type

**Verdict:** Wiring correct.

### 5. Embedding Wiring ✅

**Path:** `ai.Embed(ctx, modelID, req)` → `GetEmbeddingModel()` → `GetProviderConfig()` → `GetEmbeddingAPIClient()` → `client.Embed()`

- OpenAI: POST to `{baseURL}/embeddings` with Bearer auth
- Google: POST to `{baseURL}/{version}/models/{modelID}:batchEmbedContents` with API key
- Cohere: POST to `{baseURL}/embed` with Bearer auth
- Test verifies `len(resp.Embeddings) == 2` (matching 2 input texts) and `len(emb.Values) >= MinDims`
- All providers return standardized `[]float32` in `Embedding.Values` — uniform access works

**Verdict:** Wiring correct.

### 6. Error Paths ✅

**Path:** HTTP error → `providerError(status, msg)` → `classifyHTTPError()` → `ProviderError` → `sendErrorEvent()` → `EventError` → `es.Result()` returns error

- **TestBadModelName**: Constructs `ai.Model` directly (bypassing catalog) with `API: "anthropic-messages"`. This reaches the Anthropic API which returns a 4xx error. The error flows through `classifyHTTPError()` → `ProviderError` → `EventError` → `es.Result()` returns non-nil error. Correctly exercises the error classification code path.
- **TestInvalidAPIKey**: Sets bad OPENAI_API_KEY via `os.Setenv`, re-registers provider. OpenAI returns 401 → classified as `ErrAuth` by `classifyHTTPError()`. Error propagates through `EventStream` to `Result()`. Test restores key and re-registers in defer. Correctly exercises auth error path.
- Neither test checks `IsContextOverflow` directly, but that function targets a different error category (prompt too long, token limit). The tested error types (bad model, bad key) are correctly classified via `classifyHTTPError()` without invoking overflow detection.

**Verdict:** Wiring correct.

## Findings

### P2 - TestOpenRouterRouting may always skip

**Location:** `tests/liveapi/liveapi_test.go:269`

**Problem**
`TestOpenRouterRouting` calls `ai.GetModel("openrouter", "openai/gpt-4o-mini")`, but `openai/gpt-4o-mini` is not present in the OpenRouter section of `models/catalog.json`. The catalog has DeepSeek models under openrouter but not OpenAI-via-OpenRouter models. The test handles this gracefully with `t.Skipf` but this means OpenRouter routing is never actually tested — it silently skips every time.

**Suggested fix**
Use a model ID that exists in the catalog's openrouter section (e.g., `deepseek/deepseek-chat`) or add `openai/gpt-4o-mini` to the openrouter catalog entries.

---

### P3 - TestInvalidAPIKey modifies global state

**Location:** `tests/liveapi/liveapi_test.go:337-378`

**Problem**
`TestInvalidAPIKey` modifies `OPENAI_API_KEY` env var and re-registers the provider globally. While the defer restores it, this is inherently unsafe if tests ever run in parallel (`-parallel` flag). The `liveapi` build tag and sequential nature of API tests make this safe in practice, but it's fragile.

**Suggested fix**
Add `t.Setenv("OPENAI_API_KEY", "sk-invalid-key-for-testing")` (available since Go 1.17) which automatically restores the value and marks the test as incompatible with `t.Parallel()`. This also handles the provider re-registration: after `t.Setenv` restores the key, the next test's `Register()` call will pick up the correct key.

Note: The provider re-registration (`provideropenai.Register()`) would still modify global state, so this is a partial fix. Acceptable given the sequential nature of these tests.

---

## Summary

2 findings: 0 P0, 0 P1, 1 P2, 1 P3

All 6 wiring paths verified correct. The credential loading, provider registration, stream/embed dispatch, tool call round-trip, and error classification all trace through correctly from test code to HTTP calls and back. The P2 on OpenRouter should be addressed since it means that test path is effectively dead code.
