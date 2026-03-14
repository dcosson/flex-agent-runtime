# R2 Code Review: aiag-glo.1 — RPC Layer

**Reviewer:** reviewer-sea
**Branch:** batch4/rpc-layer @ 5857621
**Date:** 2026-03-13
**R1 Review:** docs/reviews/aiag-glo.1-r1-review-reviewer-sea.md (15 findings: 2 P1, 8 P2, 5 P3)

---

## R1 Finding Disposition

All 15 R1 findings verified against the incorporation commit (ede5762..5857621):

| # | Sev | Title | Status |
|---|-----|-------|--------|
| F1 | P1 | Server error mapping hardcoded | **Fixed** — All handlers now use `rpc.MapError(err)`. `mapError` exported as `MapError`. Added `RPCError` passthrough guard. Test `TestMapError` covers 6 error→code mappings. |
| F2 | P1 | ExecuteToolStream doesn't stream | **Fixed** — Channel-based streaming with `OnProgress` callback wired through sandbox→gVisor. Added `chanExecuteToolStream`, `progressWriter` for stdout/stderr tee, `StdoutWriter`/`StderrWriter` on `ContainerOptions`. Server test verifies `seenProgress`. |
| F3 | P2 | Typo CodeFailedPreconditon | **Fixed** — Renamed to `CodeFailedPrecondition`. All references updated. |
| F4 | P2 | Missing permission_denied/resource_exhausted codes | **Fixed** — Added `CodePermissionDenied`, `CodeResourceExhausted`. Added `zfs.ErrPoolFull → CodeResourceExhausted` mapping. |
| F5 | P2 | ExecuteTool marked non-retryable | **Fixed** — Moved to retryable list. Test updated to assert `IsRetryableMethod("ExecuteTool") == true`. |
| F6 | P2 | No idempotency cache eviction | **Fixed** — Added `sweepLoop()` goroutine (1-min ticker), `evictExpired()`, `Close()` with `atomic.Bool` guard. Test `TestSandboxServerIdempotencySweepEvictsExpired` verifies eviction. All existing tests call `srv.Close()` via `t.Cleanup`. |
| F7 | P2 | Client loses structured content | **Fixed** — Added `ContentBlocks []ContentBlock` to `api.ExecuteToolResponse`. Codec `ToAPIContentBlocks`/`FromAPIContentBlocks` handles text, thinking, image, tool_call round-trip. Client `decodeResponseContent` prefers structured blocks. Test `TestContentBlockRoundTrip` covers 4 block types. |
| F8 | P2 | wrapClientError ignores parameters | **Fixed** — Now delegates to `rpc.WrapRPCError(err, sessionID, toolName)`. Test verifies `session_id` and `tool_name` in error details. |
| F9 | P2 | Double-close fragility in event stream closeFn | **Fixed** — Added `sync.Once` wrapper (`closeOnce`) in `StreamAgentEvents`. |
| F10 | P2 | Publish silently drops events | **Fixed** — Added `atomic.Int32` drop counter with `slog.Warn` log including `session_id` and `dropped_count`. |
| F11 | P3 | ResourceSpec.CPUs int→float64 | **Fixed** — `api.ResourceSpec.CPUs` changed to `float64`. Codec updated accordingly. |
| F12 | P3 | No API version interceptor | **Partially addressed** — Added `minSupportedAPIVersion` constant and `MinSupportedAPIVersion()` getter. Interceptor deferred to ConnectRPC binding (acceptable). |
| F13 | P3 | No codec tests | **Fixed** — Added `sandbox_map_test.go` with 7 test functions covering all mapping functions plus content block round-trip. |
| F14 | P3 | Duplicate ClassifyTool | **Fixed** — Deleted `internal/sandbox/router.go`. `execute.go` now imports `tools.ClassifyTool`. `tools.ClassifyTool` updated to include `git_push`, `git_clone`, `git_fetch`, `git_pull`. |
| F15 | P3 | main.go skeleton not wired | **Not addressed** — Expected follow-up item, not part of RPC layer scope. |

---

## New Findings

No new findings. The incorporation is thorough and introduces no regressions.

---

## Test Verification

```
go test ./internal/rpc/... -count=1          — PASS (all 5 packages)
go test -race ./internal/rpc/... -count=1    — PASS (all 5 packages)
```

---

## Verdict

**Approved.** Both P1s fully resolved. All P2s addressed. P3 F12 acceptably deferred. P3 F15 is out of scope for this bead. Code quality is strong — the streaming implementation, structured content round-trip, and cache eviction are all well-designed.
