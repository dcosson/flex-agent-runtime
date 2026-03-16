# Code Review: embedding stubserver support (R1, reviewer-sea)

- Bead: N/A (follow-up implementation)
- Commit range: 37af1eb..ff93a8c
- Plan doc: docs/plans/17-external-testing.md
- Reviewer: reviewer-sea
- Review commit: ff93a8c

## Scope

7 files changed (1081 insertions, 2 deletions):
- `internal/ai/testutil/stubserver/stubserver_json.go` — JSON serving + fault injection (130 lines)
- `internal/ai/testutil/stubserver/stubserver_json_test.go` — 9 unit tests (343 lines)
- `tests/external/tier1/embedding_stubserver_test.go` — 19 E2E tests across 3 providers (530 lines)
- `testdata/fixtures/openai-embedding.json` — OpenAI embedding response fixture
- `testdata/fixtures/google-embedding.json` — Google embedding response fixture
- `testdata/fixtures/cohere-embedding.json` — Cohere embedding response fixture
- `cmd/stubserver/main.go` — Path-based routing for SSE vs JSON handlers

## Test Verification

- `go test ./internal/ai/testutil/stubserver/... -run JSON` — 9/9 PASS
- `go test ./tests/external/tier1/... -run Embedding` — 19/19 PASS

## Findings

No findings. The implementation is clean and thorough:

1. **stubserver_json.go** — Clean parallel to SSE serving. `WithJSONFixture`, `WithJSONFixtureFunc`, `WithJSONFault` options follow the same pattern as SSE counterparts. Fault injection reuses existing `serveThrottle` and `serveEmptyBody` for Throttle/EmptyBody modes, adds JSON-specific handlers for `TCPReset` (partial write + hijack), `Malformed` (corrupt JSON), and `Backpressure` (delayed response). `NewJSONServer`/`NewJSONFaultServer` convenience constructors are consistent with SSE API.

2. **Fault injection coverage** — All 5 fault modes work correctly for JSON:
   - `TCPReset`: Partial byte write then hijack/close. `AfterEvents` reinterpreted as byte count (documented in comment). Immediate (0 bytes) and partial paths both handled.
   - `Malformed`: Custom or default corrupt JSON body with 200 status (tests parse failure at client).
   - `Backpressure`: `time.Sleep` before writing valid response. Tests verify delay ≥40ms.
   - `Throttle`: Delegates to existing `serveThrottle` (429 + Retry-After).
   - `EmptyBody`: Delegates to existing `serveEmptyBody` (custom status, zero-length body).

3. **Unit tests (9)** — Cover all fixture formats (OpenAI/Google/Cohere), fixture func routing, request capture, TCP reset (3 variants), malformed (4 variants), backpressure timing, throttle 429, empty body, and sequence server retry. Good edge case coverage.

4. **E2E embedding tests (19)** — Systematic coverage: 3 providers × 6 scenarios (success, throttle 429, server error 500, malformed JSON, TCP reset, backpressure) + 1 auth header propagation test. Each test wires a real provider implementation (`openai.NewEmbedding`, `google.NewEmbedding`, `cohere.NewEmbedding`) to an in-process stubserver. Error tests verify `ProviderError` with correct error codes (`ErrRateLimit`, `ErrServerError`).

5. **Fixtures** — All 3 JSON files contain 2 embedding vectors each, matching the `Texts: []string{"hello", "world"}` requests in success tests. OpenAI includes usage tokens (10), Cohere includes billed_units (2), Google has no token field (matches provider behavior).

6. **Standalone binary routing** — Clean split: explicit routes for `/embeddings`, `/v1/embeddings`, `/v2/embed` → JSON handler. Fallback `/` catches Google's dynamic `batchEmbedContents` path and routes to JSON; everything else falls through to SSE handler. The `strings.Contains` match is broad but acceptable for a test utility.

## Summary

0 findings

**Verdict**: Approved
