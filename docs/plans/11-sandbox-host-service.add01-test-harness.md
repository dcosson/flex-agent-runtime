# 11 Addendum 01 Test Harness: ExecutionEnvironment Abstraction

**Parent:** [11-sandbox-host-service.add01](./11-sandbox-host-service.add01.md)

---

## P: Property-Based Tests

### P1. Environment Interface Completeness

**Invariant:** Every `ExecutionEnvironment` implementation returns consistent results for capability-gated operations. If a capability is false, the corresponding method returns `ErrCapabilityNotSupported`.

```
Property: forall env ExecutionEnvironment:
    caps := env.Capabilities()
    if !caps.Snapshots:
        _, err := env.CreateSnapshot(ctx, "any-name")
        assert errors.Is(err, ErrCapabilityNotSupported)
    if !caps.Rollback:
        err := env.Rollback(ctx, "any-snap")
        assert errors.Is(err, ErrCapabilityNotSupported)
    if !caps.Pause:
        err := env.Pause(ctx)
        assert errors.Is(err, ErrCapabilityNotSupported)
        err = env.Resume(ctx)
        assert errors.Is(err, ErrCapabilityNotSupported)
    if caps.Pause:
        // Pause=true means Pause()/Resume() succeed (even if no-op, e.g. LocalEnvironment)
        err := env.Pause(ctx)
        assert err == nil
        err = env.Resume(ctx)
        assert err == nil
```

Run against all five environments (with mocked backends). Generates random operation sequences.

### P2. Lifecycle State Machine Consistency

**Invariant:** For any valid operation sequence, the lifecycle state transitions are consistent. Operations on environments in invalid states return appropriate errors.

```
Property: forall ops []LifecycleOp (generated):
    env := createEnvironment(envType)
    env.Create(ctx, config)
    for _, op := range ops:
        err := applyOp(env, op)
        expectedState := stateMachine.transition(currentState, op)
        if expectedState == invalid:
            assert err != nil
        else:
            assert err == nil
```

### P3. Tool Execution Determinism

**Invariant:** For the same tool request, the same environment returns structurally equivalent responses (same content blocks, same exit code type).

```
Property: forall env ExecutionEnvironment, toolReq ToolRequest:
    resp1, err1 := env.ExecuteTool(ctx, toolReq, nil)
    resp2, err2 := env.ExecuteTool(ctx, toolReq, nil)
    if err1 == nil && err2 == nil:
        assert len(resp1.Content) == len(resp2.Content)
        assert resp1.ExitCode == resp2.ExitCode
```

Use deterministic mock backends to ensure repeatability.

### P4. Capabilities Are Static

**Invariant:** `Capabilities()` returns the same value regardless of when it is called or what operations have been performed.

```
Property: forall env ExecutionEnvironment, ops []Operation:
    caps1 := env.Capabilities()
    applyOps(env, ops)
    caps2 := env.Capabilities()
    assert caps1 == caps2
```

### P5. NativeSandboxEnvironment Parity

**Invariant:** For any tool request, `NativeSandboxEnvironment.ExecuteTool()` produces the same `ToolResponse` as the old `SandboxClient.ExecuteTool()` path (modulo timing/metadata fields).

```
Property: forall req ToolRequest:
    // Old path
    oldResp, oldErr := sandboxClient.ExecuteTool(ctx, sessionID, req, nil)
    // New path
    newResp, newErr := nativeEnv.ExecuteTool(ctx, req, nil)
    assert (oldErr == nil) == (newErr == nil)
    if oldErr == nil:
        assert contentEqual(oldResp.Content, newResp.Content)
        assert oldResp.ExitCode == newResp.ExitCode
```

Use `MemorySandboxService` as the backend for both paths.

---

## F: Fault Injection Tests

### F1. Environment API Failure Handling

For each remote environment (E2B, Daytona, Fly), inject failures at the HTTP transport level:

- **Connection refused** -> `ErrUnavailable`
- **HTTP 429 (rate limit)** -> retryable error with backoff hint
- **HTTP 500 (server error)** -> internal error, not retried
- **HTTP 401 (unauthorized)** -> authentication error, not retried
- **Request timeout** -> context deadline exceeded

Verify: each failure mode produces a clearly typed error, no panics, no goroutine leaks.

### F2. Environment Creation Failure Recovery

Inject failure during Create at different stages:

- **API call succeeds but response parsing fails** -> no orphaned resources (environment should clean up)
- **API call times out** -> environment not initialized, error returned
- **Partial creation** (e.g., E2B sandbox created but local state update fails) -> sandbox ID returned in error for manual cleanup

### F3. Mid-Execution Failure

During `ExecuteTool`:

- **Environment API drops connection** -> error returned, no partial results
- **Progress callback panics** -> panic recovered, tool execution still completes, error includes callback failure
- **Context cancelled** -> in-flight API call cancelled, error returned promptly

### F4. NativeSandboxEnvironment RPC Failure

For `NativeSandboxEnvironment`, inject ConnectRPC transport failures:

- **Server unavailable** -> `ErrUnavailable`
- **Stream interrupted mid-progress** -> partial progress delivered, final error returned
- **Server returns malformed response** -> error with details, no panic
- **ToolCallID mismatch in response** -> error with mismatch details

### F5. SSH Failure (FlySandboxEnvironment)

For `FlySandboxEnvironment`:

- **SSH connection refused** -> error with machine ID context
- **SSH authentication failure** -> error suggesting key configuration
- **SSH command timeout** -> error with duration context
- **SSH connection drops mid-output** -> partial output in error

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

### O4. NativeSandboxEnvironment Type Mapping

Golden tests verifying type conversion between environment types and RPC API types:

1. `environment.SessionConfig` -> `api.CreateSessionRequest` field mapping
2. `environment.ToolRequest` -> `api.ExecuteToolRequest` field mapping
3. `api.ExecuteToolResponse` -> `environment.ToolResponse` field mapping

---

## C: Contract Tests

### C1. ExecutionEnvironment Compliance Suite

A shared test suite that every `ExecutionEnvironment` implementation must pass. Tests are parameterized by environment:

```go
func RunEnvironmentComplianceSuite(t *testing.T, env environment.ExecutionEnvironment, config environment.SessionConfig) {
    t.Run("Capabilities", func(t *testing.T) {
        caps := env.Capabilities()
        // Capabilities are a valid struct (no panics)
        _ = caps.Snapshots
        _ = caps.Pause
    })

    t.Run("FullLifecycle", func(t *testing.T) {
        // Create -> Execute -> Destroy
        err := env.Create(ctx, config)
        require.NoError(t, err)

        resp, err := env.ExecuteTool(ctx, readReq, nil)
        require.NoError(t, err)
        require.NotNil(t, resp)

        err = env.Destroy(ctx)
        require.NoError(t, err)
    })

    t.Run("PauseResume", func(t *testing.T) {
        env2 := createFreshEnv()
        env2.Create(ctx, config)
        defer env2.Destroy(ctx)

        if !env2.Capabilities().Pause {
            err := env2.Pause(ctx)
            assert.ErrorIs(t, err, environment.ErrCapabilityNotSupported)
            return
        }
        // Test pause/resume cycle
        require.NoError(t, env2.Pause(ctx))
        require.NoError(t, env2.Resume(ctx))
    })

    t.Run("SnapshotRollback", func(t *testing.T) {
        env3 := createFreshEnv()
        env3.Create(ctx, config)
        defer env3.Destroy(ctx)

        if !env3.Capabilities().Rollback {
            err := env3.Rollback(ctx, "any")
            assert.ErrorIs(t, err, environment.ErrCapabilityNotSupported)
            return
        }
        // Test snapshot + rollback cycle
        env3.ExecuteTool(ctx, writeReq, nil)
        snap, err := env3.CreateSnapshot(ctx, "pre-change")
        require.NoError(t, err)
        require.NotEmpty(t, snap.ID)
        env3.ExecuteTool(ctx, writeReq2, nil)
        require.NoError(t, env3.Rollback(ctx, snap.ID))
        // Verify rolled back state
        resp, _ := env3.ExecuteTool(ctx, readReq, nil)
        // Content should match pre-second-write state
        _ = resp
    })

    t.Run("DestroyedEnvironmentErrors", func(t *testing.T) {
        env4 := createFreshEnv()
        env4.Create(ctx, config)
        env4.Destroy(ctx)
        _, err := env4.ExecuteTool(ctx, readReq, nil)
        assert.Error(t, err)
    })
}
```

Run against: `LocalEnvironment`, `NativeSandboxEnvironment` (with `MemorySandboxService`), `E2BSandboxEnvironment` (with HTTP mock), `DaytonaSandboxEnvironment` (with HTTP mock), `FlySandboxEnvironment` (with HTTP + SSH mock).

---

## B: Benchmarks

### B1. Environment Selection Latency

Benchmark the environment factory/selection path. Target: < 100ns per selection.

```go
func BenchmarkEnvironmentSelection(b *testing.B) {
    for i := 0; i < b.N; i++ {
        _ = createEnvironment("local")
    }
}
```

### B2. NativeSandboxEnvironment Overhead vs Direct SandboxClient

Compare the latency of `NativeSandboxEnvironment.ExecuteTool()` vs direct `SandboxClient.ExecuteTool()` to measure the adapter overhead. The environment abstraction should add < 1us of overhead per call.

```go
func BenchmarkNativeEnvironmentOverhead(b *testing.B) {
    // Setup with MemorySandboxService
    b.Run("DirectClient", func(b *testing.B) { ... })
    b.Run("ViaEnvironment", func(b *testing.B) { ... })
}
```

### B3. Capability Check Latency

Benchmark `Capabilities()` calls. Target: < 10ns (it returns a static struct).

### B4. Type Conversion Overhead

Benchmark the type conversion between environment types and RPC API types in `NativeSandboxEnvironment`. Target: < 100ns per conversion.

---

## ST: Stress Tests

### ST1. Concurrent Environment Lifecycle

50 goroutines each creating, using, and destroying environments. Run with `-race`. Verify:

- No races in internal state
- All environments cleaned up at end
- Capability checks safe under concurrency

### ST2. Concurrent Tool Execution

100 concurrent `ExecuteTool` calls on the same environment across multiple goroutines. Verify:

- No races in environment internals
- All responses correspond to correct requests (no cross-contamination)
- Progress callbacks are isolated per call

### ST3. Rapid Environment Switching

Simulate an orchestrator managing multiple environments of different types:

- Create 10 LocalEnvironments
- Create 10 E2BSandboxEnvironments (mocked)
- Execute tools on all 20 environments concurrently
- Destroy all environments
- Verify no cross-environment state leaks

### ST4. Session Limit Enforcement

For environments with `ConcurrentSessions > 0`, create environments up to and beyond the limit:

- Verify `ErrSessionLimitReached` at capacity
- Verify destroying an environment frees a slot
- Verify concurrent creation at limit is correctly serialized

---

## SEC: Security Tests

### SEC1. API Key Handling

- API keys (E2B, Daytona, Fly) are not logged in error messages
- API keys are not included in any returned metadata
- If API key is missing, error says "missing API key" not the key value
- API keys are passed via Authorization header, not URL parameters

### SEC2. Session ID Validation

- Session IDs with path traversal characters (`../`, `..\\`) are rejected
- Session IDs with null bytes are rejected
- Very long session IDs (>256 chars) are rejected
- Empty session ID returns an error (SessionID is required per §3.2)

### SEC3. Tool Parameter Sanitization

- File path parameters with path traversal are rejected by the environment (not just downstream)
- Command injection attempts in bash tool parameters are not possible at the environment level (they're handled by the sandbox, but environment should not add new vectors)

### SEC4. SSH Key Handling (FlySandboxEnvironment)

- SSH private keys are not logged
- SSH key file permissions are validated (not world-readable)
- SSH host key verification is enforced (no `InsecureIgnoreHostKey`)

---

## E2E: End-to-End Environment Tests

### E2E1. LocalEnvironment Full Cycle

Full agent-like workflow through the local environment:

1. Create environment (no-op, returns nil)
2. Execute read_file, write_file, bash tools
3. Attempt CreateSnapshot -- verify `ErrCapabilityNotSupported`
4. Pause (no-op, returns nil — Capabilities().Pause == true)
5. Resume (no-op, returns nil)
6. Execute more tools — verify still works after pause/resume
7. Destroy (marks destroyed)
8. Attempt ExecuteTool — verify `ErrNotActive`

### E2E2. NativeSandboxEnvironment Full Cycle (with MemorySandboxService)

Full agent-like workflow through the native sandbox environment:

1. Create environment (allocates ZFS dataset)
2. Execute read_file, write_file, bash tools
3. CreateSnapshot, verify snapshot ID
4. Execute more tools
5. Rollback to prior snapshot
6. Verify rollback restored state
7. Pause environment
8. Resume environment
9. Destroy environment

### E2E3. E2BSandboxEnvironment Full Cycle (with Mock Server)

Full workflow through E2B environment with a realistic HTTP mock:

1. Create environment (mock returns sandbox ID)
2. Execute file read (mock returns file content)
3. Execute bash command (mock streams output)
4. CreateSnapshot -- returns `ErrCapabilityNotSupported`
5. Pause (mock accepts pause)
6. Resume (mock accepts resume)
7. Destroy (mock accepts kill)

### E2E4. Environment Fallback Behavior

Test orchestrator behavior when an environment lacks capabilities:

1. Use E2B environment
2. Orchestrator calls CreateSnapshot -- gets `ErrCapabilityNotSupported`, continues without snapshots
3. Orchestrator attempts Rollback -- gets `ErrCapabilityNotSupported`, uses alternative recovery (destroy + recreate)
4. Verify orchestrator adapts gracefully without errors to the user

### E2E5. Environment Transparency

Verify that the agent loop sees consistent behavior regardless of environment:

1. Run same 5-tool sequence through `LocalEnvironment`
2. Run same 5-tool sequence through `NativeSandboxEnvironment` (with MemorySandboxService)
3. Run same 5-tool sequence through `E2BSandboxEnvironment` (mocked)
4. Verify `ToolResponse` structure is compatible across all environments (content blocks present, exit code set for bash)

---

## CI Tier Mapping

| Tier | Tests | When |
|------|-------|------|
| Tier 1 (PR) | P1-P5, F1-F5, O1-O4, C1, SEC1-SEC4 | Every PR |
| Tier 2 (Merge) | B1-B4, ST1-ST4, E2E1-E2E5 | Post-merge |
| Tier 3 (Nightly) | Live environment integration tests (E2B with real API key) | Nightly |

---

## Exit Criteria

1. All property tests pass with rapid (100+ iterations)
2. All fault injection tests pass -- no panics on any error path, no goroutine leaks
3. Golden tests match expected wire formats for all three remote environments
4. Contract compliance suite passes for all five environment implementations
5. Benchmarks confirm < 1us overhead for NativeSandboxEnvironment adapter
6. Stress tests pass with `-race` -- no races in any environment
7. E2E tests verify full lifecycle for local, native sandbox, and mocked remote environments
8. Backward compatibility: `NativeSandboxEnvironment` produces identical results to direct `SandboxClient` path
9. 85%+ code coverage on `internal/sandbox/environment/**` files

---

## Round 1 Review Disposition

| # | Reviewer | Severity | Summary | Disposition | Notes |
|---|----------|----------|---------|-------------|-------|
| 1 | coder-1-sea | P1 | Pause capability contract: P1 property fails for LocalEnvironment | Incorporated | P1 property updated with positive branch for Pause=true |
| 2 | coder-1-sea | P1 | Destroy compliance: DestroyedEnvironmentErrors test fails for Local | Incorporated | E2E1 updated: Destroy marks destroyed, ExecuteTool returns ErrNotActive |
| 3 | reviewer-sea | P1 | Concurrent access: stress tests require sync but plan had none | Incorporated | §3.4 concurrency contract covers sync strategy; ST1/ST2 validate |
| 4 | reviewer-sea | P1 | LocalEnvironment.Pause violates P1 invariant | Incorporated | P1 property now tests both Pause=true and Pause=false branches |

## Round 2 Review Disposition

| # | Reviewer | Severity | Summary | Disposition | Notes |
|---|----------|----------|---------|-------------|-------|
| 1 | reviewer-sea | P3 | SEC2 inconsistent with required SessionID | Incorporated | Updated to require error on empty SessionID |
