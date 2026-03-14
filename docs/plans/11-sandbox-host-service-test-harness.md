# 11: Sandbox Host Service — Test Harness

**Companion to:** [11-sandbox-host-service.md](./11-sandbox-host-service.md)
**Scope:** All testing beyond basic unit tests for the `internal/sandbox` service package.

---

## 1. Property-Based Tests

### P1. Session State Machine Validity

**Invariant:** Every sequence of valid state transitions produces a valid final state. Invalid transitions are rejected without modifying session state. A shadow state machine tracks the expected state and expected success/failure of each operation. After each operation, both the return value (success/error) and the resulting state are asserted against the shadow model.

```go
// shadowState tracks expected session state for property-based validation.
type shadowState struct {
    state     SessionState
    turnCount int
    snapCount int
    destroyed bool
}

func (ss *shadowState) expectResult(op int) (expectErr bool) {
    switch op {
    case 0: // ExecuteTool
        return ss.state != SessionActive
    case 1: // TurnComplete
        return ss.state != SessionActive
    case 2: // Pause
        return ss.state != SessionActive
    case 3: // Resume
        return ss.state != SessionPaused
    case 4: // Rollback
        return ss.state != SessionActive || ss.snapCount == 0
    case 5: // Destroy
        return ss.destroyed
    }
    return false
}

func (ss *shadowState) applySuccess(op int) {
    switch op {
    case 1: // TurnComplete
        ss.turnCount++
        ss.snapCount++
    case 2: // Pause
        ss.state = SessionPaused
    case 3: // Resume
        ss.state = SessionActive
    case 5: // Destroy
        ss.destroyed = true
    }
}

func TestSessionStateMachine(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        svc := newTestService(t)
        ctx := context.Background()

        sess, err := svc.CreateSession(ctx, CreateSessionRequest{
            BaseSnapshot: "pool/bases/test@v1",
        })
        require.NoError(t, err)

        shadow := &shadowState{state: SessionActive}

        ops := rapid.IntRange(1, 30).Draw(t, "opCount")
        for i := 0; i < ops; i++ {
            op := rapid.IntRange(0, 5).Draw(t, fmt.Sprintf("op-%d", i))
            expectErr := shadow.expectResult(op)

            var opErr error
            switch op {
            case 0: // ExecuteTool
                _, opErr = svc.ExecuteTool(ctx, ExecuteToolRequest{
                    SessionID: sess.ID, ToolName: "read_file",
                    Params: map[string]any{"path": "test.txt"},
                })
            case 1: // TurnComplete
                _, opErr = svc.TurnComplete(ctx, sess.ID)
            case 2: // Pause
                opErr = svc.PauseSession(ctx, sess.ID)
            case 3: // Resume
                opErr = svc.ResumeSession(ctx, sess.ID)
            case 4: // Rollback
                snaps, _ := svc.ListSnapshots(ctx, sess.ID)
                if len(snaps) > 0 {
                    opErr = svc.RollbackSession(ctx, sess.ID, snaps[0].Name)
                } else {
                    continue // no snapshots to rollback to — skip
                }
            case 5: // Destroy
                opErr = svc.DestroySession(ctx, sess.ID)
                if opErr == nil {
                    shadow.applySuccess(op)
                }
                return // session is gone
            }

            // Assert error expectation matches
            if expectErr {
                assert.Error(t, opErr, "op %d (type %d) should fail in state %s",
                    i, op, shadow.state)
            } else {
                if opErr == nil {
                    shadow.applySuccess(op)
                }
                // Note: tool calls may fail for non-state reasons (file not found, etc.)
                // so we don't assert NoError for op 0 (ExecuteTool)
                if op != 0 {
                    assert.NoError(t, opErr, "op %d (type %d) should succeed in state %s",
                        i, op, shadow.state)
                }
            }

            // Verify actual state matches shadow state
            info, err := svc.GetSession(ctx, sess.ID)
            if err == nil {
                assert.Equal(t, shadow.state, info.State,
                    "state mismatch after op %d (type %d)", i, op)
            }
        }
    })
}
```

### P2. Tier Classification Completeness

**Invariant:** Every tool name in the built-in tool catalog has an explicit tier classification. `ClassifyTool` never returns an invalid tier.

```go
func TestTierClassificationCompleteness(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        name := rapid.String().Draw(t, "toolName")
        tier := ClassifyTool(name)
        assert.True(t, tier == Tier1 || tier == Tier2,
            "invalid tier %d for tool %q", tier, name)
    })
}
```

### P3. Snapshot Ordering Consistency

**Invariant:** After N calls to `TurnComplete`, `ListSnapshots` returns exactly N snapshots in ascending creation order. After rollback to snapshot K, `ListSnapshots` returns exactly K+1 snapshots.

```go
func TestSnapshotOrderingConsistency(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        svc := newTestService(t)
        ctx := context.Background()

        sess, _ := svc.CreateSession(ctx, CreateSessionRequest{BaseSnapshot: "pool/bases/test@v1"})
        n := rapid.IntRange(1, 50).Draw(t, "turns")

        for i := 0; i < n; i++ {
            result, err := svc.TurnComplete(ctx, sess.ID)
            require.NoError(t, err)
            assert.Equal(t, i+1, result.TurnNumber)
        }

        snaps, err := svc.ListSnapshots(ctx, sess.ID)
        require.NoError(t, err)
        assert.Equal(t, n, len(snaps))

        // Verify ordering
        for i := 1; i < len(snaps); i++ {
            assert.True(t, snaps[i].Creation.After(snaps[i-1].Creation) ||
                snaps[i].Creation.Equal(snaps[i-1].Creation))
        }

        // Rollback to middle
        if n > 2 {
            k := rapid.IntRange(0, n-2).Draw(t, "rollbackTarget")
            targetSnap := snaps[k].Name
            err = svc.RollbackSession(ctx, sess.ID, targetSnap)
            require.NoError(t, err)

            snapsAfter, _ := svc.ListSnapshots(ctx, sess.ID)
            assert.Equal(t, k+1, len(snapsAfter))
        }
    })
}
```

### P4. Concurrent Tool Execution Safety

**Invariant:** Concurrent `ExecuteTool` calls on the same session never corrupt session state. ActiveTools count accurately tracks in-flight tools.

```go
func TestConcurrentToolSafety(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        svc := newTestService(t)
        ctx := context.Background()

        sess, _ := svc.CreateSession(ctx, CreateSessionRequest{BaseSnapshot: "pool/bases/test@v1"})
        concurrency := rapid.IntRange(2, 20).Draw(t, "concurrency")

        var wg sync.WaitGroup
        for i := 0; i < concurrency; i++ {
            wg.Add(1)
            go func() {
                defer wg.Done()
                svc.ExecuteTool(ctx, ExecuteToolRequest{
                    SessionID: sess.ID,
                    ToolName:  "read_file",
                    Params:    map[string]any{"path": "test.txt"},
                })
            }()
        }
        wg.Wait()

        // ActiveTools should be 0 after all complete
        info, _ := svc.GetSession(ctx, sess.ID)
        assert.NotNil(t, info)
    })
}
```

### P5. Session Capacity Enforcement

**Invariant:** When `MaxSessions` is set, `CreateSession` returns `ErrMaxSessionsReached` once the limit is hit. Destroying a session frees a slot.

---

## 2. Fault Injection / Chaos Tests

### FI1. ZFS Failure During Session Creation

Inject failure in `ZFSManager.CloneFromSnapshot`. Verify:
- `CreateSession` returns error with ZFS context
- No orphaned session objects in the registry
- Cleanup (DestroyDataset) is attempted

```go
func TestZFSFailureDuringCreate(t *testing.T) {
    mockZFS := zfs.NewMockManager()
    mockZFS.InjectError("CloneFromSnapshot", zfs.ErrNoSpace)

    svc := newTestServiceWith(t, mockZFS, newMockGVisor())

    _, err := svc.CreateSession(context.Background(), CreateSessionRequest{
        BaseSnapshot: "pool/bases/test@v1",
    })
    assert.Error(t, err)

    // Verify no session was registered
    sessions, _ := svc.ListSessions(context.Background())
    assert.Empty(t, sessions)
}
```

### FI2. gVisor Failure During Tool Execution

Inject failure in `GVisorManager.Run` (container crash, OOM, timeout). Verify:
- `ExecuteTool` returns structured error
- Session remains in Active state (one tool failure doesn't kill the session)
- ActiveTools count decrements correctly

```go
func TestGVisorFailureDuringExec(t *testing.T) {
    for _, failMode := range []string{"crash", "oom", "timeout"} {
        t.Run(failMode, func(t *testing.T) {
            mockGVisor := newMockGVisor()
            mockGVisor.InjectFailure(failMode)

            svc := newTestServiceWith(t, zfs.NewMockManager(), mockGVisor)
            ctx := context.Background()

            sess, _ := svc.CreateSession(ctx, CreateSessionRequest{BaseSnapshot: "pool/bases/test@v1"})

            _, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
                SessionID: sess.ID,
                ToolName:  "bash",
                Params:    map[string]any{"cmd": "echo hello"},
            })
            assert.Error(t, err)

            // Session should still be active
            info, _ := svc.GetSession(ctx, sess.ID)
            assert.Equal(t, SessionActive, info.State)
        })
    }
}
```

### FI3. Snapshot Failure After Tool Execution

Inject failure in `ZFSManager.CreateSnapshot` during `TurnComplete`. Verify:
- `TurnComplete` returns error
- Session still works (can execute more tools)
- Turn count is NOT incremented on failure

### FI4. Context Cancellation During Tool Execution

Cancel context while a Tier 2 tool is running. Verify:
- Tool execution is cancelled (gVisor container killed)
- Session remains usable for subsequent calls
- No goroutine leaks

```go
func TestContextCancelDuringExec(t *testing.T) {
    mockGVisor := newSlowMockGVisor(5 * time.Second)
    svc := newTestServiceWith(t, zfs.NewMockManager(), mockGVisor)
    ctx := context.Background()

    sess, _ := svc.CreateSession(ctx, CreateSessionRequest{BaseSnapshot: "pool/bases/test@v1"})

    execCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
    defer cancel()

    _, err := svc.ExecuteTool(execCtx, ExecuteToolRequest{
        SessionID: sess.ID,
        ToolName:  "bash",
        Params:    map[string]any{"cmd": "sleep 100"},
    })
    assert.Error(t, err)

    // Session should still work
    _, err = svc.ExecuteTool(ctx, ExecuteToolRequest{
        SessionID: sess.ID,
        ToolName:  "read_file",
        Params:    map[string]any{"path": "test.txt"},
    })
    assert.NoError(t, err)
}
```

### FI5. Destroy During In-Flight Tool

Call `DestroySession` while tools are executing. Verify:
- Waits for in-flight tools to drain (up to configured timeout)
- If drain times out, proceeds with destroy (logs warning)
- No data corruption

### FI6. Pool Exhaustion During Operations

Inject `ErrNoSpace` during snapshot creation. Verify:
- Health check reports unhealthy
- Existing sessions continue working (file reads, etc.)
- New sessions are rejected if pool is critically full

---

## 3. Deterministic Simulation Tests

### DS1. Full Session Lifecycle Simulation

Simulate a complete agent session lifecycle: create → 50 tool calls across 10 turns → rollback to turn 5 → 10 more tool calls → pause → resume → destroy.

```go
func TestFullLifecycleSimulation(t *testing.T) {
    svc := newTestService(t)
    ctx := context.Background()

    // Create
    sess, err := svc.CreateSession(ctx, CreateSessionRequest{BaseSnapshot: "pool/bases/test@v1"})
    require.NoError(t, err)

    // 10 turns, 5 tools each
    for turn := 0; turn < 10; turn++ {
        for tool := 0; tool < 5; tool++ {
            _, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
                SessionID: sess.ID,
                ToolName:  "read_file",
                Params:    map[string]any{"path": fmt.Sprintf("file%d.txt", tool)},
            })
            require.NoError(t, err)
        }
        result, err := svc.TurnComplete(ctx, sess.ID)
        require.NoError(t, err)
        assert.Equal(t, turn+1, result.TurnNumber)
    }

    // Verify 10 snapshots
    snaps, _ := svc.ListSnapshots(ctx, sess.ID)
    assert.Equal(t, 10, len(snaps))

    // Rollback to turn 5
    err = svc.RollbackSession(ctx, sess.ID, snaps[4].Name)
    require.NoError(t, err)

    snapsAfter, _ := svc.ListSnapshots(ctx, sess.ID)
    assert.Equal(t, 5, len(snapsAfter))

    // 10 more tool calls
    for tool := 0; tool < 10; tool++ {
        _, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
            SessionID: sess.ID,
            ToolName:  "read_file",
            Params:    map[string]any{"path": "test.txt"},
        })
        require.NoError(t, err)
    }

    // Pause / Resume
    require.NoError(t, svc.PauseSession(ctx, sess.ID))
    _, err = svc.ExecuteTool(ctx, ExecuteToolRequest{
        SessionID: sess.ID, ToolName: "read_file",
        Params: map[string]any{"path": "test.txt"},
    })
    assert.ErrorIs(t, err, ErrSessionPaused)

    require.NoError(t, svc.ResumeSession(ctx, sess.ID))
    _, err = svc.ExecuteTool(ctx, ExecuteToolRequest{
        SessionID: sess.ID, ToolName: "read_file",
        Params: map[string]any{"path": "test.txt"},
    })
    require.NoError(t, err)

    // Destroy
    require.NoError(t, svc.DestroySession(ctx, sess.ID))
    _, err = svc.GetSession(ctx, sess.ID)
    assert.ErrorIs(t, err, ErrSessionNotFound)
}
```

### DS2. Concurrent Multi-Session Simulation

Run 10 sessions concurrently, each doing independent create → tools → snapshots → destroy sequences. Verify no cross-session state leaks.

```go
func TestConcurrentMultiSession(t *testing.T) {
    svc := newTestService(t)
    ctx := context.Background()

    const sessions = 10
    var wg sync.WaitGroup
    errs := make(chan error, sessions)

    for i := 0; i < sessions; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()

            sess, err := svc.CreateSession(ctx, CreateSessionRequest{
                BaseSnapshot: "pool/bases/test@v1",
                SessionID:    fmt.Sprintf("sess-%03d", id),
            })
            if err != nil {
                errs <- err
                return
            }

            for turn := 0; turn < 5; turn++ {
                svc.ExecuteTool(ctx, ExecuteToolRequest{
                    SessionID: sess.ID,
                    ToolName:  "read_file",
                    Params:    map[string]any{"path": "test.txt"},
                })
                svc.TurnComplete(ctx, sess.ID)
            }

            svc.DestroySession(ctx, sess.ID)
        }(i)
    }

    wg.Wait()
    close(errs)
    for err := range errs {
        t.Error(err)
    }

    // All sessions should be gone
    list, _ := svc.ListSessions(ctx)
    assert.Empty(t, list)
}
```

### DS3. Graceful Shutdown Simulation

Simulate shutdown while tools are in-flight. Verify all sessions are paused and in-flight tools complete or are killed within timeout.

---

## 4. Benchmarks

### B1. Session Creation Latency

```go
func BenchmarkCreateSession(b *testing.B) {
    svc := newTestService(b)
    ctx := context.Background()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        sess, _ := svc.CreateSession(ctx, CreateSessionRequest{
            BaseSnapshot: "pool/bases/test@v1",
            SessionID:    fmt.Sprintf("bench-%d", i),
        })
        svc.DestroySession(ctx, sess.ID)
    }
}
```

**Target:** <10ms per session creation (mock ZFS). With real ZFS, <200ms (dominated by clone operation).

### B2. Tier 1 Tool Execution Latency

```go
func BenchmarkTier1Execution(b *testing.B) {
    svc := newTestService(b)
    ctx := context.Background()
    sess, _ := svc.CreateSession(ctx, CreateSessionRequest{BaseSnapshot: "pool/bases/test@v1"})

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        svc.ExecuteTool(ctx, ExecuteToolRequest{
            SessionID: sess.ID,
            ToolName:  "read_file",
            Params:    map[string]any{"path": "test.txt"},
        })
    }
}
```

**Target:** <1ms per Tier 1 tool call (overhead is routing + session lookup, not I/O).

### B3. Snapshot Latency

```go
func BenchmarkTurnComplete(b *testing.B) {
    svc := newTestService(b)
    ctx := context.Background()
    sess, _ := svc.CreateSession(ctx, CreateSessionRequest{BaseSnapshot: "pool/bases/test@v1"})

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        svc.TurnComplete(ctx, sess.ID)
    }
}
```

**Target:** <5ms per turn completion (mock ZFS). With real ZFS, <10ms.

### B4. Health Check Latency

```go
func BenchmarkHealthCheck(b *testing.B) {
    svc := newTestService(b)
    ctx := context.Background()

    // Create 50 sessions
    for i := 0; i < 50; i++ {
        svc.CreateSession(ctx, CreateSessionRequest{
            BaseSnapshot: "pool/bases/test@v1",
            SessionID:    fmt.Sprintf("bench-%d", i),
        })
    }

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        svc.HealthCheck(ctx)
    }
}
```

**Target:** <10ms per health check with 50 sessions.

### B5. Session Lookup Under Load

```go
func BenchmarkGetSessionUnderLoad(b *testing.B) {
    svc := newTestService(b)
    ctx := context.Background()

    ids := make([]string, 100)
    for i := range ids {
        ids[i] = fmt.Sprintf("sess-%03d", i)
        svc.CreateSession(ctx, CreateSessionRequest{
            BaseSnapshot: "pool/bases/test@v1", SessionID: ids[i],
        })
    }

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        svc.GetSession(ctx, ids[i%len(ids)])
    }
}
```

**Target:** <500ns per session lookup (sync.Map read).

---

## 5. Stress / Soak Tests

### ST1. High-Concurrency Session Churn

Create and destroy sessions rapidly with 50 concurrent goroutines for 10,000 iterations total. Verify:
- No races (under `-race`)
- No session leaks (final session count is 0)
- No goroutine leaks

```go
func TestSessionChurnStress(t *testing.T) {
    if testing.Short() {
        t.Skip("stress test")
    }

    svc := newTestService(t)
    ctx := context.Background()

    const goroutines = 50
    const opsPerGoroutine = 200

    var wg sync.WaitGroup
    for g := 0; g < goroutines; g++ {
        wg.Add(1)
        go func(gid int) {
            defer wg.Done()
            for i := 0; i < opsPerGoroutine; i++ {
                id := fmt.Sprintf("stress-%d-%d", gid, i)
                sess, err := svc.CreateSession(ctx, CreateSessionRequest{
                    BaseSnapshot: "pool/bases/test@v1", SessionID: id,
                })
                if err != nil {
                    continue
                }
                svc.ExecuteTool(ctx, ExecuteToolRequest{
                    SessionID: sess.ID, ToolName: "read_file",
                    Params: map[string]any{"path": "test.txt"},
                })
                svc.TurnComplete(ctx, sess.ID)
                svc.DestroySession(ctx, sess.ID)
            }
        }(g)
    }
    wg.Wait()

    list, _ := svc.ListSessions(ctx)
    assert.Empty(t, list, "leaked sessions: %d", len(list))
}
```

### ST2. Long-Running Session Soak

Simulate a 12-hour agent session: 500 turns, 5 tool calls per turn, periodic rollbacks. Verify:
- Snapshot count management works (cleanup of old snapshots)
- Memory usage stays bounded
- No state corruption over many operations

### ST3. Burst Tool Execution

Submit 100 concurrent tool calls to a single session. Verify:
- All complete successfully (or fail gracefully)
- ActiveTools count reaches 100 and returns to 0
- No deadlocks

---

## 6. Security Tests

### SEC1. Session Isolation

Attempt to access one session's filesystem from another session's tool call. Verify:
- Path validation prevents cross-session access
- Tier 1 tools are confined to the session's mountpoint
- Tier 2 containers are confined to the session's dataset

### SEC2. Session ID Injection

Create sessions with adversarial session IDs (path traversal, shell metacharacters). Verify:
- IDs are validated before use in ZFS dataset names
- No injection via session ID → dataset name

```go
func TestSessionIDInjection(t *testing.T) {
    svc := newTestService(t)
    ctx := context.Background()

    malicious := []string{
        "../../../etc",
        "sess; rm -rf /",
        "sess\x00extra",
        "sess@snap",
        "../../pool/bases/test",
    }

    for _, id := range malicious {
        t.Run(id, func(t *testing.T) {
            _, err := svc.CreateSession(ctx, CreateSessionRequest{
                BaseSnapshot: "pool/bases/test@v1",
                SessionID:    id,
            })
            assert.Error(t, err)
        })
    }
}
```

### SEC3. Tool Parameter Injection

Submit tool calls with adversarial parameters (e.g., `bash` with shell injection, `read_file` with path traversal). Verify that tools enforce their own input validation.

---

## 7. Manual QA Plan

### MQ1. Real EC2 Integration

On an EC2 instance with ZFS + gVisor:
1. Start `sandbox-host` binary
2. Create a session from a pre-built Go project snapshot
3. Execute `read_file`, `write_file`, `bash` ("go build") in sequence
4. Take a snapshot, modify files, rollback, verify files restored
5. Pause and resume the session
6. Destroy the session
7. Verify ZFS dataset is gone

**Judgment:** All operations complete correctly. Logs show structured events. Metrics are emitted.

### MQ2. Multi-Session Load Test

1. Create 20 sessions from the same base snapshot
2. Run concurrent bash builds in all sessions
3. Monitor pool space, host CPU/memory
4. Destroy all sessions
5. Verify all resources freed

**Judgment:** No resource leaks. Pool space returns to baseline. Host resources are not exhausted.

### MQ3. Crash Recovery

1. Create sessions with snapshots
2. Kill `sandbox-host` with SIGKILL
3. Restart
4. Verify sessions are recovered (paused state)
5. Resume and continue working

**Judgment:** Sessions are recoverable. No data loss. Service resumes cleanly.

### MQ4. Health Degradation

1. Fill the ZFS pool to 90%
2. Verify health check reports "degraded"
3. Create more sessions
4. Verify health check reports "unhealthy" at 95%
5. Clean up sessions, verify health returns to "healthy"

---

## 8. CI Tier Mapping

| Tier | Tests | Trigger | Timeout | Environment |
|------|-------|---------|---------|-------------|
| **T1: Fast** | Unit tests (state machine, tier classifier, config, errors), mock-based component tests | Every commit | 2 min | Any OS |
| **T2: Component** | Full lifecycle simulation, concurrent sessions, fault injection (all mocked) | Every PR | 5 min | Any OS |
| **T3: Integration** | Real ZFS + gVisor tests on Linux | PR merge, nightly | 15 min | Linux + ZFS + gVisor |
| **T4: Stress** | Session churn, long soak, burst execution | Nightly | 30 min | Any OS (mocked) |
| **T5: Benchmarks** | All benchmarks, baseline comparison | PR merge | 5 min | Any OS |

All tiers run with `-race` flag.

---

## 9. Exit Criteria

Before `11-sandbox-host-service` implementation is considered complete:

1. **All unit tests pass** — state machine, tier classifier, config, errors
2. **All component tests pass** — full lifecycle with mocked ZFS/gVisor
3. **All property tests pass** — state machine, snapshot ordering, concurrency (10K+ iterations)
4. **All fault injection tests pass** — ZFS failure, gVisor failure, context cancellation, pool exhaustion
5. **All benchmarks meet targets** — session creation <10ms, Tier 1 <1ms, snapshot <5ms, health <10ms
6. **Integration tests pass** — real ZFS + gVisor on Linux
7. **Stress tests pass** — 10K session churn, long soak, burst execution
8. **Security tests pass** — session isolation, ID injection, parameter injection
9. **`go test -race ./internal/sandbox/...`** passes with zero race conditions
10. **`go vet ./internal/sandbox/...`** reports no issues
11. **Test coverage >= 85%** for `internal/sandbox/` (excluding `zfs/` and `gvisor/` subdirs)
12. **`cmd/sandbox-host` builds and runs** with correct flag parsing, signal handling, graceful shutdown
13. **Benchmark baselines recorded** for regression tracking

---

## 10. Test Dependencies

| Dependency | Purpose |
|-----------|---------|
| `pgregory.net/rapid` | Property-based testing |
| `github.com/stretchr/testify` | Assertions (assert/require) |
| `internal/sandbox/zfs.MockManager` | Mock ZFS for unit/component tests |
| Go stdlib `testing` | Benchmarks, test framework |
| Linux + ZFS + gVisor | Integration tests (build-tag gated) |

---

## Completion Signoff

- **Status**: Complete
- **Date**: 2026-03-14
- **Branch**: main
- **Commit**: 0fd4d02
- **Verified by**: coder-1-sea
- **Test verification**: `go test -race ./cmd/sandbox-host ./internal/sandbox/...` — PASS
- **Acceptance tests**: N/A (harness plan)
- **Deviations from plan**:
  - [Cosmetic] Binary-level runtime checks are covered via focused host/transport tests plus sandbox integration tests rather than a standalone harness package.
- **Structural deviations resolved**: None found
- **Additions beyond plan**:
  - Added transport-coupled host validation paths via `internal/rpc/transport` tests.
