# Code Review: Live API Tests (R1, reviewer-sea)

- Bead: N/A (standalone commit)
- Commit range: 2b22f20..1759456
- Plan doc: N/A
- Reviewer: reviewer-sea
- Review commit: 1759456

## Findings

No findings. This is a clean, well-structured implementation.

## Summary

0 findings: 0 P0, 0 P1, 0 P2, 0 P3

**Verdict**: Approved

The implementation is solid:

- **liveapi_test.go** (400 lines): 6 test functions covering chat streaming (3 providers), tool call round-trip with calculator tool (schema → tool_use → tool_result → verify response), embeddings (3 providers with dimension verification and non-zero checks), OpenRouter routing, bad model name error, and invalid API key error. All tests use `skipWithoutKey` for graceful credential gating. Context timeouts at every call (30-90s).
- **credentials.go** (76 lines): Clean env-file parser with correct lookup order (file → env override), matching the Plan 22 §10 credential management pattern. Uses `strings.Cut` for parsing, handles comments and empty lines.
- **Makefile**: `test-liveapi` target with `-tags=liveapi -v -count=1 -timeout 300s`. Build tag ensures these never run in normal CI.
- **TestInvalidAPIKey**: Correctly saves/restores env var and re-registers provider. Defer ensures cleanup even on failure. Sequential execution (live API tests) makes the os.Setenv approach safe.
- **TestToolCallRoundTrip**: Thorough 2-turn flow — sends tool schema, verifies tool_use block with correct name, sends tool result, verifies final response contains "91". Uses `TransformMessages` for proper provider adaptation.
