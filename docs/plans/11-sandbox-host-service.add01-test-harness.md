# 11 Addendum 01 Test Harness: Sandbox Provider Abstraction

**Parent:** [11-sandbox-host-service.add01](./11-sandbox-host-service.add01.md)

---

## P: Property-Based Tests

### P1. Provider Interface Completeness

**Invariant:** Every `SandboxProvider` implementation returns consistent results for capability-gated operations. If a capability is false, the corresponding method returns `ErrCapabilityNotSupported` (for error-returning methods) or an empty/zero result (for result-returning methods).

```
Property: forall provider SandboxProvider:
    caps := provider.Capabilities()
    if !caps.ExplicitSnapshots:
        _, err := provider.CreateSnapshot(ctx, "any-session", "any-name")
        assert errors.Is(err, ErrCapabilityNotSupported)
    if !caps.Rollback:
        err := provider.RollbackSession(ctx, "any-session", "any-snap")
        assert errors.Is(err, ErrCapabilityNotSupported)
    if !caps.Pause:
        err := provider.PauseSession(ctx, "any-session")
        assert errors.Is(err, ErrCapabilityNotSupported)
        err = provider.ResumeSession(ctx, "any-session")
        assert errors.Is(err, ErrCapabilityNotSupported)
    if !caps.PerTurnSnapshots:
        result, err := provider.TurnComplete(ctx, "any-session")
        assert err == nil
        assert result.SnapshotID == "" || result is tracking-only
```

Run against all four providers (with mocked backends). Generates random session IDs and operation sequences.

### P2. Session State Machine Consistency

**Invariant:** For any valid operation sequence, the session state transitions are consistent with the state machine defined in plan 11 section 2.4. Operations on sessions in invalid states return appropriate errors.

```
Property: forall ops []SessionOp (generated):
    session := provider.CreateSession(ctx, req)
    for _, op := range ops:
        err := applyOp(provider, session.ID, op)
        expectedState := stateMachine.transition(currentState, op)
        if expectedState == invalid:
            assert err != nil
        else:
            assert err == nil
            info := provider.GetSession(ctx, session.ID)
            assert info.State == expectedState
```

### P3. Tool Execution Determinism

**Invariant:** For the same tool request, the same provider returns structurally equivalent responses (same content blocks, same exit code type, same snapshot ID presence/absence).

```
Property: forall provider SandboxProvider, toolReq ToolRequest:
    resp1, err1 := provider.ExecuteTool(ctx, sessionID, toolReq, nil)
    resp2, err2 := provider.ExecuteTool(ctx, sessionID, toolReq, nil)
    if err1 == nil && err2 == nil:
        assert len(resp1.Content) == len(resp2.Content)
        assert resp1.ExitCode == resp2.ExitCode
        assert (resp1.SnapshotID == "") == (resp2.SnapshotID == "")
```

Use deterministic mock backends to ensure repeatability.

### P4. Capabilities Are Static

**Invariant:** `Capabilities()` returns the same value regardless of when it is called or what operations have been performed.

```
Property: forall provider SandboxProvider, ops []Operation:
    caps1 := provider.Capabilities()
    applyOps(provider, ops)
    caps2 := provider.Capabilities()
    assert caps1 == caps2
```

### P5. NativeSandboxProvider Parity

**Invariant:** For any tool request, `NativeSandboxProvider.ExecuteTool()` produces the same `ToolResponse` as the old `SandboxClient.ExecuteTool()` path (modulo timing/metadata fields).

```
Property: forall req ToolRequest:
    // Old path
    oldResp, oldErr := sandboxClient.ExecuteTool(ctx, sessionID, req, nil)
    // New path
    newResp, newErr := localProvider.ExecuteTool(ctx, sessionID, req, nil)
    assert (oldErr == nil) == (newErr == nil)
    if oldErr == nil:
        assert contentEqual(oldResp.Content, newResp.Content)
        assert oldResp.ExitCode == newResp.ExitCode
        assert oldResp.SnapshotID == newResp.SnapshotID
```

Use `MemorySandboxService` as the backend for both paths.

---

## F: Fault Injection Tests

### F1. Provider API Failure Handling

For each remote provider (E2B, Daytona, Fly), inject failures at the HTTP transport level:

- **Connection refused** → `ErrProviderUnavailable`
- **HTTP 429 (rate limit)** → retryable error with backoff hint
- **HTTP 500 (server error)** → internal error, not retried
- **HTTP 401 (unauthorized)** → authentication error, not retried
- **Request timeout** → context deadline exceeded

Verify: each failure mode produces a clearly typed error, no panics, no goroutine leaks.

### F2. Session Creation Failure Recovery

Inject failure during session creation at different stages:

- **API call succeeds but response parsing fails** → no orphaned resources (provider should clean up)
- **API call times out** → session not registered locally, error returned
- **Partial creation** (e.g., E2B sandbox created but metadata store fails) → session ID returned in error for manual cleanup

### F3. Mid-Execution Failure

During `ExecuteTool`:

- **Provider API drops connection** → error returned, no partial results
- **Progress callback panics** → panic recovered, tool execution still completes, error includes callback failure
- **Context cancelled** → in-flight API call cancelled, error returned promptly

### F4. Local Provider RPC Failure

For `NativeSandboxProvider`, inject ConnectRPC transport failures:

- **Server unavailable** → `ErrProviderUnavailable`
- **Stream interrupted mid-progress** → partial progress delivered, final error returned
- **Server returns malformed response** → error with details, no panic
- **ToolCallID mismatch in response** → error with mismatch details

### F5. SSH Failure (Fly Provider)

For `FlyMachineSandboxProvider`:

- **SSH connection refused** → error with machine ID context
- **SSH authentication failure** → error suggesting key configuration
- **SSH command timeout** → error with duration context
- **SSH connection drops mid-output** → partial output in error

---

## O: Oracle / Golden Tests

### O1. E2B API Wire Format

Golden test fixtures for E2B API requests:

1. **Create sandbox request**: Verify JSON body matches E2B API spec (`{ "template_id": "...", "timeout": ..., "metadata": {...} }`)
2. **Run command request**: Verify command execution payload (`{ "cmd": "...", "timeout": ... }`)
3. **Filesystem read request**: Verify path encoding and response parsing
4. **Pause/resume requests**: Verify correct HTTP methods and endpoints

Compare against recorded real API responses (sanitized).

### O2. Daytona API Wire Format

Golden test fixtures for Daytona API requests:

1. **Create workspace request**: Verify payload structure
2. **code_run request**: Verify execution payload
3. **Filesystem operations**: Verify file read/write payloads

### O3. Fly Machines API Wire Format

Golden test fixtures for Fly API requests:

1. **Create machine request**: Verify machine config, volume attachment, image specification
2. **Suspend/start requests**: Verify correct endpoints and methods
3. **Destroy request**: Verify machine + volume cleanup sequence

### O4. Local Provider Type Mapping

Golden tests verifying type conversion between provider types and RPC API types:

1. `provider.CreateSessionRequest` → `api.CreateSessionRequest` field mapping
2. `api.Session` → `provider.SessionInfo` field mapping
3. `provider.ToolRequest` → `api.ExecuteToolRequest` field mapping
4. `api.ExecuteToolResponse` → `provider.ToolResponse` field mapping

---

## C: Contract Tests

### C1. SandboxProvider Compliance Suite

A shared test suite that every `SandboxProvider` implementation must pass. Tests are parameterized by provider:

```go
func RunProviderComplianceSuite(t *testing.T, p provider.SandboxProvider, backend TestBackend) {
    t.Run("Name", func(t *testing.T) {
        assert.NotEmpty(t, p.Name())
    })

    t.Run("Capabilities", func(t *testing.T) {
        caps := p.Capabilities()
        // Capabilities are a valid struct (no panics)
        _ = caps.PerTurnSnapshots
        _ = caps.Pause
    })

    t.Run("FullLifecycle", func(t *testing.T) {
        // Create → Execute → TurnComplete → Destroy
        sess, err := p.CreateSession(ctx, createReq)
        require.NoError(t, err)
        require.NotEmpty(t, sess.ID)

        resp, err := p.ExecuteTool(ctx, sess.ID, readReq, nil)
        require.NoError(t, err)
        require.NotNil(t, resp)

        snap, err := p.TurnComplete(ctx, sess.ID)
        require.NoError(t, err)
        if p.Capabilities().PerTurnSnapshots {
            require.NotEmpty(t, snap.SnapshotID)
        }

        err = p.DestroySession(ctx, sess.ID)
        require.NoError(t, err)
    })

    t.Run("PauseResume", func(t *testing.T) {
        if !p.Capabilities().Pause {
            err := p.PauseSession(ctx, sess.ID)
            assert.ErrorIs(t, err, provider.ErrCapabilityNotSupported)
            return
        }
        // Test pause/resume cycle
        sess, _ := p.CreateSession(ctx, createReq)
        require.NoError(t, p.PauseSession(ctx, sess.ID))
        info, _ := p.GetSession(ctx, sess.ID)
        assert.Equal(t, provider.SessionStatePaused, info.State)
        require.NoError(t, p.ResumeSession(ctx, sess.ID))
        info, _ = p.GetSession(ctx, sess.ID)
        assert.Equal(t, provider.SessionStateActive, info.State)
        p.DestroySession(ctx, sess.ID)
    })

    t.Run("SnapshotRollback", func(t *testing.T) {
        if !p.Capabilities().Rollback {
            err := p.RollbackSession(ctx, sess.ID, "any")
            assert.ErrorIs(t, err, provider.ErrCapabilityNotSupported)
            return
        }
        // Test snapshot + rollback cycle
        sess, _ := p.CreateSession(ctx, createReq)
        p.ExecuteTool(ctx, sess.ID, writeReq, nil)
        snap, _ := p.TurnComplete(ctx, sess.ID)
        p.ExecuteTool(ctx, sess.ID, writeReq2, nil)
        require.NoError(t, p.RollbackSession(ctx, sess.ID, snap.SnapshotID))
        // Verify rolled back state
        resp, _ := p.ExecuteTool(ctx, sess.ID, readReq, nil)
        // Content should match pre-second-write state
        p.DestroySession(ctx, sess.ID)
    })

    t.Run("DestroyedSessionErrors", func(t *testing.T) {
        sess, _ := p.CreateSession(ctx, createReq)
        p.DestroySession(ctx, sess.ID)
        _, err := p.ExecuteTool(ctx, sess.ID, readReq, nil)
        assert.Error(t, err)
    })

    t.Run("NonexistentSessionErrors", func(t *testing.T) {
        _, err := p.ExecuteTool(ctx, "nonexistent-session", readReq, nil)
        assert.Error(t, err)
    })
}
```

Run against: `NativeSandboxProvider` (with `MemorySandboxService`), `E2BSandboxProvider` (with HTTP mock), `DaytonaSandboxProvider` (with HTTP mock), `FlyMachineSandboxProvider` (with HTTP + SSH mock).

### C2. SandboxBackend + Provider Integration Contract

Verify that `SandboxBackend` correctly delegates to any `SandboxProvider`:

```go
func TestSandboxBackendWithProvider(t *testing.T, p provider.SandboxProvider) {
    sess, _ := p.CreateSession(ctx, createReq)
    backend := tools.NewSandboxBackend(p, sess.ID)

    // ToolBackend interface compliance
    resp, err := backend.ExecuteTool(ctx, toolReq, nil)
    require.NoError(t, err)
    require.NotNil(t, resp)

    // Progress callback propagation
    var progress []string
    resp, err = backend.ExecuteTool(ctx, bashReq, func(p tools.ToolProgress) {
        progress = append(progress, p.Content)
    })
    require.NoError(t, err)
    // For providers with StreamingProgress, verify progress was received
    if p.Capabilities().StreamingProgress {
        assert.NotEmpty(t, progress)
    }
}
```

---

## B: Benchmarks

### B1. Provider Selection Latency

Benchmark the provider factory/selection path. Target: < 100ns per selection.

```go
func BenchmarkProviderSelection(b *testing.B) {
    for i := 0; i < b.N; i++ {
        _ = selectProvider("local")
    }
}
```

### B2. NativeSandboxProvider Overhead vs Direct SandboxClient

Compare the latency of `NativeSandboxProvider.ExecuteTool()` vs direct `SandboxClient.ExecuteTool()` to measure the adapter overhead. The provider abstraction should add < 1us of overhead per call.

```go
func BenchmarkLocalProviderOverhead(b *testing.B) {
    // Setup with MemorySandboxService
    b.Run("DirectClient", func(b *testing.B) { ... })
    b.Run("ViaProvider", func(b *testing.B) { ... })
}
```

### B3. Capability Check Latency

Benchmark `Capabilities()` calls. Target: < 10ns (it returns a static struct).

### B4. Type Conversion Overhead

Benchmark the type conversion between provider types and RPC API types in `NativeSandboxProvider`. Target: < 100ns per conversion.

---

## ST: Stress Tests

### ST1. Concurrent Session Lifecycle

50 goroutines each creating, using, and destroying sessions on the same provider. Run with `-race`. Verify:

- No races in session map
- No session ID collisions
- All sessions cleaned up at end
- Capability checks safe under concurrency

### ST2. Concurrent Tool Execution

100 concurrent `ExecuteTool` calls on the same session across multiple goroutines. Verify:

- No races in provider internals
- All responses correspond to correct requests (no cross-contamination)
- Progress callbacks are isolated per call

### ST3. Rapid Provider Switching

Simulate an orchestrator switching between providers for different sessions:

- Create 10 sessions on local provider
- Create 10 sessions on E2B provider (mocked)
- Execute tools on all 20 sessions concurrently
- Destroy all sessions
- Verify no cross-provider state leaks

### ST4. Session Limit Enforcement

For providers with `ConcurrentSessions > 0`, create sessions up to and beyond the limit:

- Verify `ErrSessionLimitReached` at capacity
- Verify destroying a session frees a slot
- Verify concurrent creation at limit is correctly serialized

---

## SEC: Security Tests

### SEC1. API Key Handling

- API keys (E2B, Daytona, Fly) are not logged in error messages
- API keys are not included in `SessionInfo.Metadata`
- If API key is missing, error says "missing API key" not the key value
- API keys are passed via Authorization header, not URL parameters

### SEC2. Session ID Validation

- Session IDs with path traversal characters (`../`, `..\\`) are rejected
- Session IDs with null bytes are rejected
- Very long session IDs (>256 chars) are rejected
- Empty session ID handled gracefully (auto-generated or error)

### SEC3. Tool Parameter Sanitization

- File path parameters with path traversal are rejected by the provider (not just the backend)
- Command injection attempts in bash tool parameters are not possible at the provider level (they're handled by the sandbox, but provider should not add new vectors)

### SEC4. SSH Key Handling (Fly Provider)

- SSH private keys are not logged
- SSH key file permissions are validated (not world-readable)
- SSH host key verification is enforced (no `InsecureIgnoreHostKey`)

---

## E2E: End-to-End Provider Tests

### E2E1. Local Provider Full Cycle (with MemorySandboxService)

Full agent-like workflow through the local provider:

1. Select local provider
2. Create session
3. Execute read_file, write_file, bash tools
4. Call TurnComplete, verify snapshot
5. Execute more tools
6. Rollback to prior snapshot
7. Verify rollback restored state
8. Pause session
9. Resume session
10. Destroy session

### E2E2. E2B Provider Full Cycle (with Mock Server)

Full workflow through E2B provider with a realistic HTTP mock:

1. Create session (mock returns sandbox ID)
2. Execute file read (mock returns file content)
3. Execute bash command (mock streams output)
4. Call TurnComplete (returns empty snapshot)
5. Pause (mock accepts pause)
6. Resume (mock accepts resume)
7. Destroy (mock accepts kill)

### E2E3. Provider Fallback Behavior

Test orchestrator behavior when a provider lacks capabilities:

1. Use E2B provider
2. Orchestrator calls TurnComplete → gets empty result, continues without snapshots
3. Orchestrator attempts Rollback → gets `ErrCapabilityNotSupported`, uses alternative recovery (destroy + recreate)
4. Verify orchestrator adapts gracefully without errors to the user

### E2E4. SandboxBackend Transparency

Verify that the agent loop sees identical behavior regardless of provider:

1. Run same 5-tool sequence through `SandboxBackend` backed by local provider
2. Run same 5-tool sequence through `SandboxBackend` backed by E2B provider (mocked)
3. Verify `ToolResponse` structure is compatible (content blocks present, exit code set for bash)
4. Verify `AgentToolResult` produced by `NewSandboxTools` is identical in structure

---

## CI Tier Mapping

| Tier | Tests | When |
|------|-------|------|
| Tier 1 (PR) | P1-P5, F1-F5, O1-O4, C1-C2, SEC1-SEC4 | Every PR |
| Tier 2 (Merge) | B1-B4, ST1-ST4, E2E1-E2E4 | Post-merge |
| Tier 3 (Nightly) | Live provider integration tests (E2B with real API key) | Nightly |

---

## Exit Criteria

1. All property tests pass with rapid (100+ iterations)
2. All fault injection tests pass — no panics on any error path, no goroutine leaks
3. Golden tests match expected wire formats for all three remote providers
4. Contract compliance suite passes for all four provider implementations
5. Benchmarks confirm < 1us overhead for local provider adapter
6. Stress tests pass with `-race` — no races in any provider
7. E2E tests verify full lifecycle for local and mocked remote providers
8. Backward compatibility: `NativeSandboxProvider` produces identical results to direct `SandboxClient` path
9. 85%+ code coverage on `internal/sandbox/provider/**` files
