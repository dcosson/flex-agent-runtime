# Code Review: tier2 test implementation (R1, reviewer-sea)

- Bead: N/A (follow-up implementation)
- Commit range: 38f895f..5af266e
- Plan doc: docs/plans/17-external-testing.md
- Reviewer: reviewer-sea
- Review commit: 5af266e

## Scope

1 file changed (190 insertions, 175 deletions):
- `tests/external/tier2/docker_test.go` — Replaced all TODO/skip stubs with real RPC scenarios against a live sandbox-host via Docker compose

## Findings

No findings. The implementation is clean:

1. **Test helpers** — `activeConfig`, `newSandboxClient`, `mustCreateSession`, `mustDestroySession`, `mustExecuteTool` are well-factored with proper `t.Helper()` calls, context timeouts (20s), and `defer mustDestroySession` cleanup patterns. Auth token injection via `SANDBOX_AUTH_TOKEN` env var matches compose config.

2. **TestDockerLifecycle_AllConfigs** — Creates session, executes bash echo, verifies content, destroys. Exercises the full session lifecycle against the active backend config.

3. **TestDockerSnapshot_ZFSConfigs** — Branches on `cfg.SupportsSnapshots()` to verify both success path (create + list snapshots) and error path (CreateSnapshot should fail on non-ZFS). Good dual-path coverage.

4. **TestDockerTierRouting_GVisorConfigs** — Verifies tier metadata: `read_file` at tier 1, `bash` at tier 2 (gvisor) or tier 1 (none). Correct per the execution tier model.

5. **TestDockerStreamingProgress** — Exercises `CallServerStream` path, receives progress + final response, validates content. `progressCount` suppressed with `_ =` — reasonable as a placeholder for future assertion when progress events are stable.

6. **TestDockerCapabilities_AllConfigs** — Verifies capabilities struct from `CreateSessionResponse` matches `BackendConfig` expectations (Snapshots, Rollback, TierRouting, StreamingProgress, Pause). Complete coverage.

7. **API type alignment** — All request/response types (`CreateSessionRequest`, `ExecuteToolRequest`, `ExecuteToolStreamMessage`, `Capabilities`) match `internal/rpc/api/types.go` exactly.

## Summary

0 findings

**Verdict**: Approved
