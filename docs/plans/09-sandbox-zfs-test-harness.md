# 09: ZFS Dataset & Snapshot Management — Test Harness

**Companion to:** [09-sandbox-zfs.md](./09-sandbox-zfs.md)
**Scope:** All testing beyond basic unit tests for the `internal/sandbox/zfs` package.

---

## 1. Property-Based Tests

### P1. Name Validation Roundtrip

**Invariant:** Any name that passes `ValidateName()` can be used in `CreateDataset()` without causing `ErrInvalidName`. Any name that fails `ValidateName()` is rejected by all dataset operations before reaching the CLI.

```go
func TestNameValidationRoundtrip(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        // Generate random strings including adversarial characters
        name := rapid.StringMatching(`[a-zA-Z0-9_./:\-@]{0,300}`).Draw(t, "name")

        err := zfs.ValidateName(name)
        if err == nil {
            // Valid names must not contain path traversal or empty components
            assert.NotContains(t, name, "..")
            for _, part := range strings.Split(name, "/") {
                assert.NotEmpty(t, part)
                assert.LessOrEqual(t, len(part), 255)
            }
            assert.LessOrEqual(t, len(name), 1024)
        }
    })
}
```

### P2. Snapshot Name Never Contains Shell Metacharacters

**Invariant:** Any name that passes `ValidateSnapshotName()` contains only characters safe for use as CLI arguments (no spaces, quotes, backticks, semicolons, pipes, etc.).

```go
func TestSnapshotNameSafeForCLI(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        name := rapid.String().Draw(t, "name")
        err := zfs.ValidateSnapshotName(name)
        if err == nil {
            for _, c := range name {
                assert.False(t, unicode.IsSpace(c), "space in valid name")
                assert.NotContains(t, "'\"`$;|&\\(){}[]<>!#~", string(c),
                    "shell metacharacter in valid name: %c", c)
            }
        }
    })
}
```

### P3. Error Classification Totality

**Invariant:** `classifyError()` never returns `nil` for non-zero exit codes (except the idempotent hold case). Every non-zero exit produces a wrapped error.

```go
func TestClassifyErrorTotality(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        stderr := rapid.String().Draw(t, "stderr")
        exitCode := rapid.IntRange(1, 255).Draw(t, "exitCode")
        err := classifyError("TestOp", stderr, exitCode)
        assert.NotNil(t, err, "non-zero exit code must produce an error")
    })
}
```

### P4. Mock Manager Consistency

**Invariant:** The `MockManager` behaves identically to a real ZFS system for all operation sequences that do not depend on filesystem content. Specifically:
- `DatasetExists` returns `true` after `CreateDataset` and `false` after `DestroyDataset`
- `SnapshotExists` returns `true` after `CreateSnapshot` and `false` after `DestroySnapshot`
- `ListSnapshots` returns snapshots in creation order
- `Rollback` with `DestroyLater` removes later snapshots
- `CloneFromSnapshot` fails if the snapshot doesn't exist

```go
func TestMockManagerConsistency(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        mock := zfs.NewMockManager()
        ctx := context.Background()

        // Generate a sequence of operations
        ops := rapid.IntRange(1, 50).Draw(t, "opCount")
        datasets := make([]string, 0)
        snapshots := make(map[string][]string) // dataset -> snapshot names

        for i := 0; i < ops; i++ {
            op := rapid.IntRange(0, 4).Draw(t, fmt.Sprintf("op-%d", i))
            switch op {
            case 0: // create dataset
                name := fmt.Sprintf("pool/test-%d", i)
                err := mock.CreateDataset(ctx, name, zfs.DatasetOptions{})
                if err == nil {
                    datasets = append(datasets, name)
                    exists, _ := mock.DatasetExists(ctx, name)
                    assert.True(t, exists)
                }
            case 1: // create snapshot
                if len(datasets) == 0 {
                    continue
                }
                ds := datasets[rapid.IntRange(0, len(datasets)-1).Draw(t, "ds")]
                snap := fmt.Sprintf("snap-%d", i)
                info, err := mock.CreateSnapshot(ctx, ds, snap)
                if err == nil {
                    assert.Equal(t, snap, info.Name)
                    snapshots[ds] = append(snapshots[ds], snap)
                    exists, _ := mock.SnapshotExists(ctx, ds, snap)
                    assert.True(t, exists)
                }
            case 2: // list snapshots (verify order)
                if len(datasets) == 0 {
                    continue
                }
                ds := datasets[rapid.IntRange(0, len(datasets)-1).Draw(t, "ds")]
                listed, err := mock.ListSnapshots(ctx, ds)
                if err == nil {
                    expected := snapshots[ds]
                    assert.Equal(t, len(expected), len(listed))
                    for j, s := range listed {
                        assert.Equal(t, expected[j], s.Name)
                    }
                }
            case 3: // destroy dataset
                if len(datasets) == 0 {
                    continue
                }
                idx := rapid.IntRange(0, len(datasets)-1).Draw(t, "ds")
                ds := datasets[idx]
                err := mock.DestroyDataset(ctx, ds, zfs.DestroyOptions{Recursive: true})
                if err == nil {
                    exists, _ := mock.DatasetExists(ctx, ds)
                    assert.False(t, exists)
                    datasets = append(datasets[:idx], datasets[idx+1:]...)
                    delete(snapshots, ds)
                }
            case 4: // rollback
                if len(datasets) == 0 {
                    continue
                }
                ds := datasets[rapid.IntRange(0, len(datasets)-1).Draw(t, "ds")]
                snaps := snapshots[ds]
                if len(snaps) == 0 {
                    continue
                }
                idx := rapid.IntRange(0, len(snaps)-1).Draw(t, "snap")
                snap := snaps[idx]
                err := mock.Rollback(ctx, ds, snap, zfs.RollbackOptions{DestroyLater: true})
                if err == nil {
                    // Later snapshots should be destroyed
                    snapshots[ds] = snaps[:idx+1]
                    listed, _ := mock.ListSnapshots(ctx, ds)
                    assert.Equal(t, idx+1, len(listed))
                }
            }
        }
    })
}
```

Conformance extension:
- Add `TestMockManagerConformanceAgainstRealZFS` (integration-tagged) that replays the same operation trace against `MockManager` and `CLIManager` and compares normalized outcomes (existence checks, snapshot ordering, rollback behavior, error classes).

### P5. ValidateSnapshotFullName Decomposition

**Invariant:** `ValidateSnapshotFullName(dataset + "@" + snap)` succeeds if and only if both `ValidateName(dataset)` and `ValidateSnapshotName(snap)` succeed.

```go
func TestSnapshotFullNameDecomposition(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        dataset := rapid.StringMatching(`[a-zA-Z0-9_./:\-]{1,100}`).Draw(t, "dataset")
        snap := rapid.StringMatching(`[a-zA-Z0-9_.\-]{1,100}`).Draw(t, "snap")

        fullErr := zfs.ValidateSnapshotFullName(dataset + "@" + snap)
        dsErr := zfs.ValidateName(dataset)
        snapErr := zfs.ValidateSnapshotName(snap)

        if dsErr == nil && snapErr == nil {
            assert.NoError(t, fullErr)
        }
        if fullErr == nil {
            assert.NoError(t, dsErr)
            assert.NoError(t, snapErr)
        }
    })
}
```

---

## 2. Fault Injection / Chaos Tests

These tests require real ZFS (integration test build tag) and simulate failure conditions.

### FI1. Context Cancellation During Long Operations

Cancel the context during a `zfs send` of a large dataset. Verify:
- The process is killed promptly
- No zombie processes are left
- The partial send does not corrupt anything
- Subsequent operations on the source dataset work normally

```go
func TestSendContextCancellation(t *testing.T) {
    pool := setupTestPool(t)
    mgr := setupManager(t, pool)
    ctx := context.Background()

    // Create dataset with some data
    ds := pool + "/sendtest"
    require.NoError(t, mgr.CreateDataset(ctx, ds, zfs.DatasetOptions{}))
    mountpoint, _ := mgr.GetMountpoint(ctx, ds)
    createLargeFile(t, mountpoint, 100<<20) // 100MB
    info, _ := mgr.CreateSnapshot(ctx, ds, "snap1")

    // Start send with a context that cancels after 10ms
    cancelCtx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
    defer cancel()

    var buf bytes.Buffer
    err := mgr.Send(cancelCtx, ds+"@snap1", zfs.SendOptions{}, &buf)
    assert.Error(t, err)

    // Source dataset must still be functional
    _, err = mgr.CreateSnapshot(ctx, ds, "snap2")
    assert.NoError(t, err)
}
```

### FI2. Disk Space Exhaustion

Fill the pool to >95% capacity, then attempt operations. Verify:
- `CreateSnapshot` returns `ErrNoSpace` with descriptive context
- `CreateDataset` returns `ErrNoSpace`
- `Rollback` still works (it frees space by discarding later snapshots)
- Pool health reports correct space usage

```go
func TestDiskSpaceExhaustion(t *testing.T) {
    // Create a small pool (64MB)
    pool := setupSmallTestPool(t, 64<<20)
    mgr := setupManager(t, pool)
    ctx := context.Background()

    ds := pool + "/filltest"
    require.NoError(t, mgr.CreateDataset(ctx, ds, zfs.DatasetOptions{}))
    mountpoint, _ := mgr.GetMountpoint(ctx, ds)

    // Take initial snapshot
    _, err := mgr.CreateSnapshot(ctx, ds, "base")
    require.NoError(t, err)

    // Fill pool
    fillDataset(t, mountpoint, 60<<20) // 60MB of 64MB

    // Snapshot should fail
    _, err = mgr.CreateSnapshot(ctx, ds, "full")
    if err != nil {
        assert.ErrorIs(t, err, zfs.ErrNoSpace)
    }

    // Pool space should reflect exhaustion
    space, err := mgr.PoolSpace(ctx, pool)
    require.NoError(t, err)
    assert.Greater(t, space.Capacity, 0.90)
}
```

### FI3. Concurrent Destroy During Operations

Start a long-running `zfs send`, then destroy the source dataset from another goroutine. Verify:
- The send fails with an appropriate error (not a panic or hang)
- The dataset is actually destroyed
- No orphaned state

### FI4. Double Destroy

Attempt to destroy a dataset that has already been destroyed. Verify:
- Returns `ErrDatasetNotFound`, not a panic
- Idempotent when called with the same arguments

```go
func TestDoubleDestroy(t *testing.T) {
    pool := setupTestPool(t)
    mgr := setupManager(t, pool)
    ctx := context.Background()

    ds := pool + "/doubledestroy"
    require.NoError(t, mgr.CreateDataset(ctx, ds, zfs.DatasetOptions{}))

    // First destroy succeeds
    require.NoError(t, mgr.DestroyDataset(ctx, ds, zfs.DestroyOptions{}))

    // Second destroy returns not found
    err := mgr.DestroyDataset(ctx, ds, zfs.DestroyOptions{})
    assert.ErrorIs(t, err, zfs.ErrDatasetNotFound)
}
```

### FI5. Rollback to Nonexistent Snapshot

```go
func TestRollbackNonexistent(t *testing.T) {
    pool := setupTestPool(t)
    mgr := setupManager(t, pool)
    ctx := context.Background()

    ds := pool + "/rollbacktest"
    require.NoError(t, mgr.CreateDataset(ctx, ds, zfs.DatasetOptions{}))

    err := mgr.Rollback(ctx, ds, "doesnotexist", zfs.RollbackOptions{})
    assert.ErrorIs(t, err, zfs.ErrSnapshotNotFound)
}
```

---

## 3. Deterministic Simulation Tests

### DS1. Dataset Hierarchy Invariants

Simulate a sequence of create/clone/destroy/snapshot/rollback operations and verify invariants after each operation:
- A dataset that was created and not destroyed exists
- A clone's `Origin` field points to its parent snapshot
- Destroying a parent snapshot when a clone depends on it returns `ErrHasClones` (without `-R`)
- ListDatasets on a parent returns all child datasets
- ListSnapshots returns snapshots in creation order

```go
func TestDatasetHierarchySimulation(t *testing.T) {
    pool := setupTestPool(t)
    mgr := setupManager(t, pool)
    ctx := context.Background()

    // Model: track expected state in-memory
    type state struct {
        datasets  map[string]bool
        snapshots map[string][]string // dataset -> ordered snapshot names
        origins   map[string]string   // clone -> origin snapshot
    }
    s := &state{
        datasets:  make(map[string]bool),
        snapshots: make(map[string][]string),
        origins:   make(map[string]string),
    }

    // Execute 100 random operations, verify state after each
    for i := 0; i < 100; i++ {
        // ... random operation selection ...
        // After each operation, verify s matches what ZFS reports
        verifyState(t, ctx, mgr, pool, s)
    }
}

func verifyState(t *testing.T, ctx context.Context, mgr zfs.ZFSManager, pool string, s *state) {
    t.Helper()
    for name, exists := range s.datasets {
        actual, err := mgr.DatasetExists(ctx, name)
        require.NoError(t, err)
        assert.Equal(t, exists, actual, "dataset %s existence mismatch", name)
    }
    for ds, expectedSnaps := range s.snapshots {
        if !s.datasets[ds] {
            continue
        }
        listed, err := mgr.ListSnapshots(ctx, ds)
        require.NoError(t, err)
        assert.Equal(t, len(expectedSnaps), len(listed), "snapshot count mismatch for %s", ds)
        for j, snap := range listed {
            assert.Equal(t, expectedSnaps[j], snap.Name)
        }
    }
}
```

### DS2. Rollback Snapshot Pruning

Verify that `Rollback` with `DestroyLater` correctly destroys all snapshots newer than the target and only those snapshots.

```go
func TestRollbackSnapshotPruning(t *testing.T) {
    pool := setupTestPool(t)
    mgr := setupManager(t, pool)
    ctx := context.Background()

    ds := pool + "/prunetest"
    require.NoError(t, mgr.CreateDataset(ctx, ds, zfs.DatasetOptions{}))

    // Create 10 snapshots
    for i := 0; i < 10; i++ {
        _, err := mgr.CreateSnapshot(ctx, ds, fmt.Sprintf("snap-%03d", i))
        require.NoError(t, err)
    }

    // Rollback to snap-005
    err := mgr.Rollback(ctx, ds, "snap-005", zfs.RollbackOptions{DestroyLater: true})
    require.NoError(t, err)

    // Verify: snap-000 through snap-005 exist, snap-006 through snap-009 do not
    snaps, err := mgr.ListSnapshots(ctx, ds)
    require.NoError(t, err)
    assert.Equal(t, 6, len(snaps))
    for i, snap := range snaps {
        assert.Equal(t, fmt.Sprintf("snap-%03d", i), snap.Name)
    }
}
```

### DS3. Clone Independence

Verify that after cloning from a snapshot, writes to the clone do not affect the original dataset or any other clones from the same snapshot.

```go
func TestCloneIndependence(t *testing.T) {
    pool := setupTestPool(t)
    mgr := setupManager(t, pool)
    ctx := context.Background()

    // Create source and snapshot
    src := pool + "/source"
    require.NoError(t, mgr.CreateDataset(ctx, src, zfs.DatasetOptions{}))
    srcMount, _ := mgr.GetMountpoint(ctx, src)
    os.WriteFile(filepath.Join(srcMount, "shared.txt"), []byte("original"), 0644)
    _, err := mgr.CreateSnapshot(ctx, src, "base")
    require.NoError(t, err)

    // Create two clones
    clone1 := pool + "/clone1"
    clone2 := pool + "/clone2"
    require.NoError(t, mgr.CloneFromSnapshot(ctx, src+"@base", clone1))
    require.NoError(t, mgr.CloneFromSnapshot(ctx, src+"@base", clone2))

    // Modify clone1
    mount1, _ := mgr.GetMountpoint(ctx, clone1)
    os.WriteFile(filepath.Join(mount1, "shared.txt"), []byte("modified-by-clone1"), 0644)

    // Verify clone2 and source are unaffected
    mount2, _ := mgr.GetMountpoint(ctx, clone2)
    data2, _ := os.ReadFile(filepath.Join(mount2, "shared.txt"))
    assert.Equal(t, "original", string(data2))

    srcData, _ := os.ReadFile(filepath.Join(srcMount, "shared.txt"))
    assert.Equal(t, "original", string(srcData))
}
```

---

## 4. Benchmarks

### B1. Snapshot Creation Latency

```go
func BenchmarkSnapshotCreate(b *testing.B) {
    pool := setupTestPool(b)
    mgr := setupManager(b, pool)
    ctx := context.Background()

    ds := pool + "/benchsnap"
    require.NoError(b, mgr.CreateDataset(ctx, ds, zfs.DatasetOptions{}))

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, err := mgr.CreateSnapshot(ctx, ds, fmt.Sprintf("bench-%d", i))
        if err != nil {
            b.Fatal(err)
        }
    }
}
```

**Target:** <5ms per snapshot (includes process spawn overhead). The ZFS operation itself is microseconds; the overhead is `exec.Command`.

### B2. Clone Creation Latency

```go
func BenchmarkCloneFromSnapshot(b *testing.B) {
    pool := setupTestPool(b)
    mgr := setupManager(b, pool)
    ctx := context.Background()

    // Create source with data
    src := pool + "/benchsrc"
    require.NoError(b, mgr.CreateDataset(ctx, src, zfs.DatasetOptions{}))
    srcMount, _ := mgr.GetMountpoint(ctx, src)
    createLargeFile(b, srcMount, 100<<20) // 100MB
    _, err := mgr.CreateSnapshot(ctx, src, "base")
    require.NoError(b, err)

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        name := fmt.Sprintf("%s/clone-%d", pool, i)
        err := mgr.CloneFromSnapshot(ctx, src+"@base", name)
        if err != nil {
            b.Fatal(err)
        }
    }
}
```

**Target:** <10ms per clone regardless of source dataset size (clone is metadata-only).

### B3. Rollback Latency

```go
func BenchmarkRollback(b *testing.B) {
    pool := setupTestPool(b)
    mgr := setupManager(b, pool)
    ctx := context.Background()

    ds := pool + "/benchrollback"
    require.NoError(b, mgr.CreateDataset(ctx, ds, zfs.DatasetOptions{}))
    _, err := mgr.CreateSnapshot(ctx, ds, "base")
    require.NoError(b, err)

    // Pre-create snapshots so benchmark isolates rollback latency.
    for i := 0; i < b.N; i++ {
        mount, _ := mgr.GetMountpoint(ctx, ds)
        os.WriteFile(filepath.Join(mount, "bench.txt"), []byte(fmt.Sprintf("iter-%d", i)), 0644)
        mgr.CreateSnapshot(ctx, ds, fmt.Sprintf("iter-%d", i))
    }

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        target := fmt.Sprintf("iter-%d", i)
        err := mgr.Rollback(ctx, ds, target, zfs.RollbackOptions{DestroyLater: true})
        if err != nil {
            b.Fatal(err)
        }
    }
}
```

**Target:** <10ms per rollback. Rollback is a metadata operation.

### B4. ListSnapshots Scaling

Measure `ListSnapshots` performance as snapshot count grows.

```go
func BenchmarkListSnapshots(b *testing.B) {
    for _, count := range []int{10, 100, 500, 1000} {
        b.Run(fmt.Sprintf("n=%d", count), func(b *testing.B) {
            pool := setupTestPool(b)
            mgr := setupManager(b, pool)
            ctx := context.Background()

            ds := pool + "/benchlist"
            require.NoError(b, mgr.CreateDataset(ctx, ds, zfs.DatasetOptions{}))
            for i := 0; i < count; i++ {
                mgr.CreateSnapshot(ctx, ds, fmt.Sprintf("snap-%04d", i))
            }

            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                _, err := mgr.ListSnapshots(ctx, ds)
                if err != nil {
                    b.Fatal(err)
                }
            }
        })
    }
}
```

**Target:** <50ms for 1000 snapshots. Output parsing is the bottleneck, not ZFS.

### B5. Name Validation Throughput

```go
func BenchmarkValidateName(b *testing.B) {
    names := []string{
        "pool/sessions/sess-123",
        "pool/bases/go-project-v1",
        "a/b/c/d/e/f/g/h",
        "pool/" + strings.Repeat("x", 200),
    }
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        zfs.ValidateName(names[i%len(names)])
    }
}
```

**Target:** <500ns per validation. Regex compilation is cached.

### B6. Send/Recv Throughput

```go
func BenchmarkSendRecvThroughput(b *testing.B) {
    for _, sizeMB := range []int{1, 10, 100} {
        b.Run(fmt.Sprintf("size=%dMB", sizeMB), func(b *testing.B) {
            pool := setupTestPool(b)
            mgr := setupManager(b, pool)
            ctx := context.Background()

            src := pool + "/sendsrc"
            require.NoError(b, mgr.CreateDataset(ctx, src, zfs.DatasetOptions{}))
            mount, _ := mgr.GetMountpoint(ctx, src)
            createLargeFile(b, mount, int64(sizeMB)<<20)
            mgr.CreateSnapshot(ctx, src, "snap")

            b.ResetTimer()
            b.SetBytes(int64(sizeMB) << 20)
            for i := 0; i < b.N; i++ {
                var buf bytes.Buffer
                err := mgr.Send(ctx, src+"@snap", zfs.SendOptions{}, &buf)
                require.NoError(b, err)

                dst := fmt.Sprintf("%s/recvdst-%d", pool, i)
                err = mgr.Receive(ctx, dst, &buf)
                require.NoError(b, err)
            }
        })
    }
}
```

**Target:** Send/recv throughput should be limited by I/O, not our wrapper. Expect >500MB/s for in-memory vdev pools.

---

## 5. Stress / Soak Tests

### ST1. Rapid Snapshot Cycle

Simulate 12 hours of agent activity: create snapshots, occasionally rollback, occasionally destroy old snapshots. Run for 10,000 iterations (compressing 12 hours into minutes):

```go
func TestRapidSnapshotCycle(t *testing.T) {
    if testing.Short() {
        t.Skip("soak test")
    }

    pool := setupTestPool(t)
    mgr := setupManager(t, pool)
    ctx := context.Background()

    ds := pool + "/soak"
    require.NoError(t, mgr.CreateDataset(ctx, ds, zfs.DatasetOptions{}))
    mount, _ := mgr.GetMountpoint(ctx, ds)

    const iterations = 10_000

    for i := 0; i < iterations; i++ {
        // Write some data
        os.WriteFile(filepath.Join(mount, fmt.Sprintf("file-%d.txt", i%100)),
            []byte(fmt.Sprintf("data-%d", i)), 0644)

        // Take snapshot
        snapsBefore, err := mgr.ListSnapshots(ctx, ds)
        require.NoError(t, err)
        snapName := fmt.Sprintf("turn-%05d", len(snapsBefore))
        _, err := mgr.CreateSnapshot(ctx, ds, snapName)
        require.NoError(t, err)

        // Occasionally rollback (5% of the time)
        if i > 10 && i%20 == 0 {
            snaps, err := mgr.ListSnapshots(ctx, ds)
            require.NoError(t, err)
            if len(snaps) < 5 {
                continue
            }
            target := snaps[len(snaps)-5].Name
            err := mgr.Rollback(ctx, ds, target, zfs.RollbackOptions{DestroyLater: true})
            require.NoError(t, err)
        }
    }

    // Verify final state
    snaps, err := mgr.ListSnapshots(ctx, ds)
    require.NoError(t, err)
    assert.Greater(t, len(snaps), 0)
    for i := 1; i < len(snaps); i++ {
        assert.False(t, snaps[i].Creation.Before(snaps[i-1].Creation))
    }

    // Check pool health
    status, err := mgr.PoolStatus(ctx, pool)
    require.NoError(t, err)
    assert.Equal(t, zfs.PoolOnline, status.State)
}
```

### ST2. Concurrent Sessions Soak

Run 20 concurrent "sessions" (datasets), each doing create/write/snapshot/rollback cycles for 1000 iterations. Verify no cross-contamination and no ZFS errors.

```go
func TestConcurrentSessionsSoak(t *testing.T) {
    if testing.Short() {
        t.Skip("soak test")
    }

    pool := setupTestPool(t)
    mgr := setupManager(t, pool)
    ctx := context.Background()

    const sessions = 20
    const opsPerSession = 1000

    // Create base
    base := pool + "/base"
    require.NoError(t, mgr.CreateDataset(ctx, base, zfs.DatasetOptions{}))
    baseMount, _ := mgr.GetMountpoint(ctx, base)
    os.WriteFile(filepath.Join(baseMount, "shared.txt"), []byte("base-content"), 0644)
    _, err := mgr.CreateSnapshot(ctx, base, "initial")
    require.NoError(t, err)

    var wg sync.WaitGroup
    errs := make(chan error, sessions)

    for s := 0; s < sessions; s++ {
        wg.Add(1)
        go func(sessionID int) {
            defer wg.Done()
            ds := fmt.Sprintf("%s/session-%03d", pool, sessionID)
            if err := mgr.CloneFromSnapshot(ctx, base+"@initial", ds); err != nil {
                errs <- fmt.Errorf("session %d clone: %w", sessionID, err)
                return
            }
            mount, _ := mgr.GetMountpoint(ctx, ds)

            for i := 0; i < opsPerSession; i++ {
                // Write unique data
                os.WriteFile(filepath.Join(mount, "session.txt"),
                    []byte(fmt.Sprintf("session-%d-op-%d", sessionID, i)), 0644)

                // Snapshot
                _, err := mgr.CreateSnapshot(ctx, ds, fmt.Sprintf("op-%04d", i))
                if err != nil {
                    errs <- fmt.Errorf("session %d snapshot %d: %w", sessionID, i, err)
                    return
                }

                // Verify isolation — shared.txt must still be "base-content"
                data, err := os.ReadFile(filepath.Join(mount, "shared.txt"))
                if err != nil || string(data) != "base-content" {
                    errs <- fmt.Errorf("session %d: shared.txt corrupted", sessionID)
                    return
                }
            }

            // Cleanup
            mgr.DestroyDataset(ctx, ds, zfs.DestroyOptions{Recursive: true})
        }(s)
    }

    wg.Wait()
    close(errs)
    for err := range errs {
        t.Error(err)
    }
}
```

### ST3. Snapshot Space Growth Analysis

Create a session, simulate realistic agent activity (small file edits per turn), and measure snapshot space growth. Verify that space usage is proportional to actual data changes, not snapshot count.

```go
func TestSnapshotSpaceGrowth(t *testing.T) {
    if testing.Short() {
        t.Skip("soak test")
    }

    pool := setupTestPool(t)
    mgr := setupManager(t, pool)
    ctx := context.Background()

    ds := pool + "/spacegrowth"
    require.NoError(t, mgr.CreateDataset(ctx, ds, zfs.DatasetOptions{}))
    mount, _ := mgr.GetMountpoint(ctx, ds)

    // Write initial project (10MB)
    createProjectFiles(t, mount, 10<<20)
    _, err := mgr.CreateSnapshot(ctx, ds, "initial")
    require.NoError(t, err)

    const turns = 200
    spaceSamples := make([]int64, turns)

    for i := 0; i < turns; i++ {
        // Simulate small file edit (1-10KB change)
        editRandomFile(t, mount, 1024+rand.Intn(9*1024))
        mgr.CreateSnapshot(ctx, ds, fmt.Sprintf("turn-%03d", i))

        info, _ := mgr.GetDatasetInfo(ctx, ds)
        spaceSamples[i] = info.Used
    }

    // Space should grow roughly linearly with changes
    // Not exponentially (which would indicate a problem)
    firstQuarter := spaceSamples[turns/4-1]
    lastQuarter := spaceSamples[turns-1]
    ratio := float64(lastQuarter) / float64(firstQuarter)

    // Expect roughly 4x growth (linear), not 16x (exponential)
    assert.Less(t, ratio, 8.0, "space growth appears super-linear: ratio=%.1f", ratio)

    t.Logf("Space growth over %d turns: initial=%d final=%d ratio=%.1f",
        turns, spaceSamples[0], spaceSamples[turns-1], ratio)
}
```

---

## 6. Security Tests

### Sec1. Command Injection via Dataset Names

Verify that adversarial dataset names cannot inject shell commands:

```go
func TestCommandInjectionPrevention(t *testing.T) {
    injectionAttempts := []string{
        "pool/$(rm -rf /)",
        "pool/; rm -rf /",
        "pool/`rm -rf /`",
        "pool/test\nzpool destroy testpool",
        "pool/test\x00extra",
        "pool/../../etc/passwd",
        "pool/ --help",
        "pool/test; zpool destroy testpool",
        "pool/test | cat /etc/shadow",
        "pool/test && echo pwned",
    }

    for _, name := range injectionAttempts {
        t.Run(name, func(t *testing.T) {
            err := zfs.ValidateName(name)
            assert.ErrorIs(t, err, zfs.ErrInvalidName,
                "injection attempt not caught: %q", name)
        })
    }
}
```

### Sec2. Path Traversal via Mountpoint

Verify that `SetMountpoint` validates the path:

```go
func TestMountpointPathTraversal(t *testing.T) {
    traversalAttempts := []string{
        "/../../../etc",
        "/tmp/../../etc/shadow",
    }

    pool := setupTestPool(t)
    mgr := setupManager(t, pool)
    ctx := context.Background()
    ds := pool + "/pathtest"
    require.NoError(t, mgr.CreateDataset(ctx, ds, zfs.DatasetOptions{}))

    for _, path := range traversalAttempts {
        err := mgr.SetMountpoint(ctx, ds, path)
        require.Error(t, err)
    }
}
```

### Sec4. Pool Export/Import Cycle

Verify pool export/import behavior for migration workflows:

```go
func TestPoolExportImportCycle(t *testing.T) {
    pool := setupTestPool(t)
    mgr := setupManager(t, pool)
    ctx := context.Background()

    ds := pool + "/expimp"
    require.NoError(t, mgr.CreateDataset(ctx, ds, zfs.DatasetOptions{}))
    mp, _ := mgr.GetMountpoint(ctx, ds)
    require.NoError(t, os.WriteFile(filepath.Join(mp, "marker.txt"), []byte("ok"), 0o644))

    require.NoError(t, mgr.ExportPool(ctx, pool))
    require.NoError(t, mgr.ImportPool(ctx, pool, testPoolDevicePath(t, pool)))

    mp2, err := mgr.GetMountpoint(ctx, ds)
    require.NoError(t, err)
    data, err := os.ReadFile(filepath.Join(mp2, "marker.txt"))
    require.NoError(t, err)
    assert.Equal(t, "ok", string(data))
}
```

### Sec3. Property Injection

Verify that setting dangerous ZFS properties is blocked:

```go
func TestPropertyInjection(t *testing.T) {
    dangerous := []string{"exec", "setuid", "devices"}
    for _, prop := range dangerous {
        err := zfs.ValidatePropertyName(prop)
        assert.Error(t, err, "dangerous property %q not blocked", prop)
    }
}
```

---

## 7. Manual QA Plan

### MQ1. Real EC2 + EBS Integration

Before production deployment:
1. Launch an EC2 instance (c5.xlarge or similar)
2. Attach an EBS gp3 volume
3. Create a ZFS pool on the EBS volume
4. Run the full integration test suite against the real pool
5. Verify pool persists after instance reboot (stop/start)
6. Verify pool can be imported on a different instance after EBS detach/reattach

**Judgment criteria:** All tests pass. Pool survives reboot. EBS portability works.

### MQ2. ZFS Version Compatibility

Run integration tests against:
- OpenZFS 2.1.x (Ubuntu 22.04 default)
- OpenZFS 2.2.x (Ubuntu 24.04 default)
- OpenZFS 2.3.x (latest)

**Judgment criteria:** All tests pass on all versions. Error messages are consistent (classifyError works correctly).

### MQ3. Pool Recovery Scenario

1. Create a pool, create sessions with many snapshots
2. Kill the sandbox host process mid-operation (SIGKILL)
3. Restart and import the pool
4. Verify all sessions and snapshots are intact
5. Verify operations can resume normally

**Judgment criteria:** ZFS's transactional semantics protect data. No corruption after crash.

### MQ4. Space Exhaustion and Recovery

1. Fill a pool to >98% capacity
2. Observe error messages and behavior
3. Delete some snapshots to free space
4. Verify normal operations resume

**Judgment criteria:** Errors are clear and actionable. Recovery is smooth.

---

## 8. CI Tier Mapping

| Tier | Tests | Trigger | Timeout | Environment |
|------|-------|---------|---------|-------------|
| **T1: Fast** | Unit tests (validation, parsing, error classification, mock) | Every commit | 1 min | Any OS |
| **T2: Integration** | Integration tests (real ZFS operations) | PR merge, nightly | 10 min | Linux + ZFS (CI runner with `zfs` installed) |
| **T3: Stress** | Soak tests (10K iterations, concurrent sessions, space growth) | Nightly | 30 min | Linux + ZFS |
| **T4: Benchmarks** | All benchmarks, baseline comparison | PR merge | 5 min | Linux + ZFS |
| **T5: Extended** | Fault injection, security tests, property tests (10K iterations) | Release candidate | 20 min | Linux + ZFS |

All tiers run with `-race` flag.

### CI Environment Setup

Integration tests require a CI runner with ZFS. Options:
1. **Self-hosted runner** on an EC2 instance with ZFS pre-installed
2. **Docker container** with ZFS kernel modules (requires privileged mode)
3. **GitHub Actions** with a setup step that installs `zfsutils-linux`

Recommended: Option 3 for simplicity. The setup step:
```yaml
- name: Install ZFS
  run: |
    sudo apt-get update
    sudo apt-get install -y zfsutils-linux
    sudo modprobe zfs
```

File-backed pools (used by test harness) don't need real block devices.

---

## 9. Exit Criteria

Before `09-sandbox-zfs` implementation is considered complete:

1. **All unit tests pass** — validation, parsing, error classification, mock manager
2. **All integration tests pass** — real ZFS operations on file-backed test pool
3. **All property tests pass** — with 10K+ iterations each
4. **All benchmarks pass** — snapshot <5ms, clone <10ms, rollback <10ms, list-1000 <50ms
5. **All fault injection tests pass** — context cancellation, space exhaustion, concurrent destroy, double destroy, nonexistent rollback
6. **All security tests pass** — command injection, path traversal, property injection
7. **Stress tests pass** — 10K snapshot cycle, 20 concurrent sessions x 1000 ops, space growth is sub-linear
8. **MockManager passes** — property tests verify mock matches real ZFS behavior for all supported operation sequences
9. **`go test -race ./internal/sandbox/zfs/...`** passes with zero race conditions
10. **`go vet ./internal/sandbox/zfs/...`** reports no issues
11. **Test coverage ≥ 90%** for `internal/sandbox/zfs/` (measured on unit + integration tests)
12. **Benchmark baselines recorded** for regression tracking in CI

---

## 10. Test Dependencies

| Dependency | Purpose |
|-----------|---------|
| `pgregory.net/rapid` | Property-based testing |
| `github.com/stretchr/testify` | Assertions (assert/require) |
| Go stdlib `testing` | Benchmarks, test framework |
| Go stdlib `os/exec` | Test pool setup (zpool create/destroy) |
| Linux + `zfsutils-linux` | Integration/stress/benchmark tests |

## Review Disposition

| # | Reviewer | Severity | Summary | Disposition | Notes |
|---|----------|----------|---------|-------------|-------|
| 1 | coder-2-sea | P2 | Need mock-vs-real conformance to prevent drift | Incorporated | Added conformance extension for replaying traces across mock and real implementations. |
| 2 | coder-2-sea | P3 | SetMountpoint validation ownership/expectation unclear | Incorporated | Security test now requires rejection of traversal mountpoints. |
| 3 | coder-2-sea | P2 | Pool import/export missing from coverage | Incorporated | Added explicit export/import cycle security/integration test. |
| 4 | coder-2-sea | P3 | Rollback benchmark currently measures create+rollback combined | Incorporated | B3 now pre-creates snapshots and times rollback path only. |
| 5 | coder-2-sea | P3 | Stress snapshot count bookkeeping is brittle | Incorporated | ST1 now derives expectations from `ListSnapshots` and validates ordering. |
