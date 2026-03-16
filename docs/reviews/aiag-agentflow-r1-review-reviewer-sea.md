# Code Review: tier2 agent-flow e2e test (R1, reviewer-sea)

- Bead: N/A (follow-up implementation)
- Commit range: 5af266e..8d0ac0e
- Plan doc: docs/plans/17-external-testing.md (Section 12)
- Reviewer: reviewer-sea
- Review commit: 8d0ac0e

## Scope

4 files changed (281 insertions, 5 deletions):
- `tests/external/tier2/agent_flow_test.go` — Full agent round-trip: agent runtime → stubserver → sandbox-host RPC → tool result → final response
- `testdata/fixtures/anthropic-tool-call-bash.sse` — SSE fixture: bash tool_use with `printf tier2-e2e-ok`
- `testdata/fixtures/anthropic-final-assistant.sse` — SSE fixture: text response containing `tier2-e2e-ok`
- `tests/external/tier2/docker_test.go` — Renamed test functions `_AllConfigs`/`_ZFSConfigs`/etc → `_ActiveConfig` for accuracy

## Findings

No findings. The implementation is well-crafted:

1. **fixtureSelectorTransport** — Custom `http.RoundTripper` that injects `X-Fixture` headers (first call → `anthropic-tool-call-bash`, subsequent → `anthropic-final-assistant`) and captures request bodies. Thread-safe: `atomic.AddInt64` for call counting, `sync.Mutex` for body slice. Request cloning via `req.Clone()` + `Header.Clone()` preserves original request integrity.

2. **Full round-trip wiring** — Creates real Anthropic provider pointed at stubserver URL, registers with unique sourceID, wires `executeFn` to dispatch tool calls via Connect RPC to sandbox-host, constructs `NativeDriver` + `Agent`. All API types match their definitions (`DriverConfig.Model`, `DriverConfig.Tools`, `DriverConfig.SystemPrompt`).

3. **executeFn bridge** — Correctly maps `tools.ToolRequest` → `api.ExecuteToolRequest` and `api.ExecuteToolResponse` → `tools.ToolResponse`. Uses `codec.FromAPIContentBlocks` for content block conversion with text fallback when content blocks are empty.

4. **Event assertions** — Verifies `EventToolStarted`, `EventToolCompleted`, `EventTurnCompleted` lifecycle events and final assistant message text. Checks request count (≥2) and `tool_result` presence in second request body. Good coverage of the full agent loop contract.

5. **SSE fixtures** — Well-formed Anthropic streaming protocol. `tool-call-bash.sse` emits single-chunk `input_json_delta`, `final-assistant.sse` emits single text delta. Both include proper `message_start`/`message_delta`/`message_stop` envelope.

6. **Cleanup** — `defer UnregisterProviders(sourceID)`, `defer mustDestroySession`, `defer a.Stop(ctx)`. No resource leaks.

7. **Test renames** — `_AllConfigs` → `_ActiveConfig` accurately reflects that tests now run against the single active backend configuration, not all four.

## Summary

0 findings

**Verdict**: Approved
