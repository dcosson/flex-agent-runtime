# Code Review: aiag-0dou.2 (R1, reviewer-sea)

- Bead: aiag-0dou.2
- Commit range: e52074c..c6bae39
- Plan doc: docs/plans/11-sandbox-host-service.add01.md §5.3
- Reviewer: reviewer-sea
- Review commit: c6bae39

## Findings

### P1 - TOCTOU race in Pause/Resume/Destroy state transitions

**Location:** `internal/sandbox/environment/e2b/e2b.go:242-298`

**Problem**
`Pause`, `Resume`, and `Destroy` all follow the pattern: read state under `RLock` → release lock → make HTTP call → acquire `Lock` → update state. Between releasing `RLock` and acquiring `Lock`, another goroutine can change state. Concrete scenario:

1. Goroutine A calls `Resume()`, reads `StatePaused`, releases `RLock`
2. Goroutine B calls `Destroy()`, reads `StatePaused` (id != ""), releases `RLock`
3. Goroutine B sends DELETE, acquires Lock, sets `StateDestroyed`, releases Lock
4. Goroutine A's POST resume returns (E2B might reject, might not), acquires Lock, sets `StateActive`
5. State is now `StateActive` but sandbox is destroyed on the server

This could cause subsequent `ExecuteTool` calls to fail with confusing API errors instead of the expected `ErrNotActive`.

**Suggested fix**
Hold the write lock across the HTTP call (since these are infrequent lifecycle operations, the contention cost is negligible), or re-check state after acquiring the write lock and abort if it changed:

```go
func (e *E2BSandboxEnvironment) Pause(ctx context.Context) error {
    e.mu.Lock()
    defer e.mu.Unlock()
    if e.state != environment.StateActive || e.sandboxID == "" {
        return environment.ErrNotActive
    }
    if err := e.doJSON(ctx, http.MethodPost, "/sandboxes/"+e.sandboxID+"/pause", map[string]any{}, nil); err != nil {
        return fmt.Errorf("e2b: pause: %w", err)
    }
    e.state = environment.StatePaused
    return nil
}
```

---

### P2 - Destroy from paused state not tested

**Location:** `internal/sandbox/environment/e2b/e2b.go:279-298`

**Problem**
`Destroy` allows destroying from any non-destroyed state (it only checks `state == StateDestroyed` to short-circuit). This is correct — you should be able to destroy a paused sandbox. But the compliance test only covers destroy-from-active. A test for destroy-from-paused would verify this path.

**Suggested fix**
Add a test case that creates, pauses, then destroys. Verify state transitions to `StateDestroyed` and that `ExecuteTool` returns `ErrNotActive`.

---

### P3 - `doJSON` response body not fully consumed on success with nil `out`

**Location:** `internal/sandbox/environment/e2b/e2b.go:403-405`

**Problem**
When `out == nil`, the code does `io.Copy(io.Discard, resp.Body)` to drain the body for connection reuse. This is correct and good practice. No issue — just documenting that this was checked.

**Suggested fix**
No fix needed.

---

### P3 - `created` and `labels` fields stored but never exposed

**Location:** `internal/sandbox/environment/e2b/e2b.go:222-223`

**Problem**
The `Create` method stores `e.created` and `e.labels` but these are never read or exposed. They don't appear in any method return value or log output. This is harmless — likely placeholder for future observability — but dead fields could be confusing.

**Suggested fix**
Either add them to structured log output (e.g., in lifecycle state transitions) or remove them. Low priority.

---

## Summary

4 findings: 0 P0, 1 P1, 1 P2, 2 P3

**Verdict**: Approved with revisions

The implementation is well-structured:
- Clean option pattern with functional options
- Proper `doJSON` HTTP helper with error body truncation, auth header, content-type handling
- Good use of `IsFileOp` for routing decisions
- Compliance test integration ensures behavioral contract
- Thorough unit tests covering create, lifecycle, tool routing, error states, timeout capping

The P1 (TOCTOU in state transitions) should be addressed since downstream providers (Daytona, Fly) will likely copy this pattern. Fix is straightforward — hold write lock across lifecycle HTTP calls.
