# Code Review: aiag-0bh.1 (R1, coder-2-sea)

- Bead: aiag-0bh.1
- Commit range: 39ab673..5fd235d (termmux implementation commits)
- Plan doc: docs/plans/09-h2-termmux-port.md
- Reviewer: coder-2-sea
- Review commit: a057c0a (HEAD of batch4/termmux-core)

---

## Findings

### P1 — Session.Stop() lacks idempotency guard, causing resource leak on natural exit

**Location:** `internal/termmux/session.go:132-172`

**Problem**

`Stop()` checks `s.stopped` at the top but never sets it to `true`. The `stopped` flag is only set by the PipeOutput goroutine when the child process exits. This causes two issues:

1. **Natural exit resource leak**: When the child exits naturally, PipeOutput sets `stopped=true` and closes `exitNotify`. The context watcher goroutine sees `<-s.exitNotify` and exits *without* calling `Stop()`. Nobody performs cleanup — `VT.Close()`, `clients.CloseAll()`, `monitor.Close()`, and `cancelFn()` are never called. The PTY fd, monitor event loop, and client channels leak.

2. **Concurrent Stop() race**: If `Stop()` is called from two goroutines simultaneously (e.g., context cancellation fires and external `Kill()` is called), both pass the `stopped` guard and proceed to kill/cleanup concurrently. While individual operations have their own guards (VT.Close checks nil, monitor.Close uses sync.Once), the overall flow is fragile.

```go
// Current code — never sets stopped=true:
func (s *Session) Stop() {
    s.mu.Lock()
    if s.stopped {      // only true when child exits naturally
        s.mu.Unlock()
        return
    }
    s.mu.Unlock()
    // ... cleanup that never runs for natural exits
}
```

**Suggested fix**

Set `s.stopped = true` under the lock at the beginning of `Stop()`:
```go
func (s *Session) Stop() {
    s.mu.Lock()
    if s.stopped {
        s.mu.Unlock()
        return
    }
    s.stopped = true
    s.mu.Unlock()
    // ... proceed with cleanup
}
```

Also add cleanup to the natural exit path. Either:
- Have the context watcher call a cleanup method when `exitNotify` fires, or
- Have the PipeOutput goroutine's defer chain call cleanup directly

---

### P2 — VirtualTerminal uses Darwin-only PTY syscalls without build tags

**Location:** `internal/termmux/virtual_terminal.go:238-280`

**Problem**

The `openPTY`, `ptsname`, `grantpt`, `unlockpt` functions use Darwin-specific ioctls (`syscall.TIOCPTYGNAME`, `syscall.TIOCPTYGRANT`, `syscall.TIOCPTYUNLK`). These constants don't exist in Go's `syscall` package on Linux. The file has no `//go:build darwin` tag, so `go build` on Linux would fail with compilation errors.

The comment on line 254 says "On Darwin, use TIOCPTYGNAME ioctl" — confirming awareness that this is platform-specific — but no platform gating is in place.

**Suggested fix**

Add `//go:build darwin` to `virtual_terminal.go` and create a `virtual_terminal_linux.go` with Linux PTY implementation (using `TIOCGPTN` and `/dev/pts/N`). Alternatively, use `github.com/creack/pty` which handles platform differences.

---

### P2 — ConfigDirManager.InjectFile allows path traversal

**Location:** `internal/termmux/config_dir.go:46-58`

**Problem**

`InjectFile` constructs `fullPath` from `filepath.Join(dir, relativePath)` without validating that the result stays within the session directory. A `relativePath` like `../../etc/shadow` would resolve to a path outside the config directory, potentially overwriting arbitrary files.

This is a system boundary — `relativePath` originates from orchestrator input — so input validation is required.

```go
// Current code — no traversal check:
fullPath := filepath.Join(dir, relativePath)
os.WriteFile(fullPath, data, 0644)
```

**Suggested fix**

Validate the resolved path stays within the session directory:
```go
fullPath := filepath.Clean(filepath.Join(dir, relativePath))
if !strings.HasPrefix(fullPath, filepath.Clean(dir)+string(os.PathSeparator)) {
    return fmt.Errorf("relative path %q escapes config directory", relativePath)
}
```

---

### P2 — Session log tailer offset tracking off-by-one for non-newline-terminated lines

**Location:** `internal/termmux/eventsrc/sessionlog/tailer.go:98-117`

**Problem**

The poll function counts `bytesRead += int64(len(line)) + 1` for each scanned line, assuming every line ends with `\n`. If the file's last line has no trailing newline, `bufio.Scanner.Scan()` still returns it, but the `+1` overcounts by one byte. On the next poll, `info.Size() < offset` triggers a false "file truncated" detection, resetting the offset to 0 and re-reading the entire file — potentially emitting duplicate events to all consumers.

```go
for scanner.Scan() {
    line := scanner.Bytes()
    bytesRead += int64(len(line)) + 1 // +1 wrong if no trailing newline
    // ...
}
```

While JSONL files typically end with newlines, this is a silent data-correctness issue when they don't.

**Suggested fix**

Clamp `newOffset` to `info.Size()` to prevent the false truncation:
```go
if newOffset > info.Size() {
    newOffset = info.Size()
}
```

Or replace the manual byte counting with position tracking via the file handle.

---

### P2 — EventStore.ReadAll loses typed event data payloads

**Location:** `internal/termmux/eventsrc/eventstore/store.go:46-62`

**Problem**

`AgentEvent.Data` is typed as `any`. When `json.Unmarshal` deserializes events from JSONL, the `Data` field becomes `map[string]interface{}` instead of the original typed struct (e.g., `SessionStartedData`, `TurnCompletedData`). Consumers reading events back from the store cannot use type assertions to access typed fields — they get untyped maps.

```go
var evt monitor.AgentEvent
json.Unmarshal(scanner.Bytes(), &evt) // evt.Data is map[string]interface{}
```

**Suggested fix**

Implement a custom `UnmarshalJSON` on `AgentEvent` that uses the `Type` field to deserialize `Data` into the correct Go type. Or use `json.RawMessage` for the `Data` field and provide typed accessor methods.

---

### P3 — PipeOutput panic leaves exitNotify unclosed (zombie session risk)

**Location:** `internal/termmux/session.go:97-123`

**Problem**

If the PipeOutput goroutine panics (despite recovery), it exits without closing `s.exitNotify`. The context watcher goroutine blocks forever on `<-s.exitNotify`, and the session becomes a zombie — it can't be waited on, and its resources are never cleaned up.

```go
go func() {
    defer func() {
        if r := recover(); r != nil { /* log only */ }
    }()
    err := s.VT.PipeOutput(...)
    // if PipeOutput panics, this code never runs:
    close(s.exitNotify) // ← never reached
}()
```

**Suggested fix**

Add `defer close(s.exitNotify)` early in the goroutine's defer chain (defers execute LIFO, so it runs after panic recovery):
```go
go func() {
    defer close(s.exitNotify) // always runs
    defer func() {
        if r := recover(); r != nil { /* log */ }
    }()
    // ...
}()
```

---

### P3 — OTEL server uses fmt.Printf to stdout for errors

**Location:** `internal/termmux/eventsrc/otelserver/server.go:96`

**Problem**

```go
fmt.Printf("OTEL server error: %v\n", err)
```

Uses `fmt.Printf` (stdout), while the rest of the codebase consistently uses `fmt.Fprintf(os.Stderr, ...)` for error output. Errors directed to stdout may be captured as program output or lost in piped scenarios.

**Suggested fix**

Change to `fmt.Fprintf(os.Stderr, "OTEL server error: %v\n", err)` or inject a `*slog.Logger`.

---

## Plan Compliance

| Plan Section | Status | Notes |
|---|---|---|
| §3.1 TermmuxDriverAdapter | PASS | Interface and conversation types in `driver/driver.go` |
| §3.2 Session | PASS | Session struct with VT, monitor, clients |
| §3.3 VirtualTerminal | PASS | PTY lifecycle, write timeout, resize, kill |
| §3.4 AgentMonitor | PASS | State machine, metrics, fan-out, idle detection |
| §3.5 Event Types | PARTIAL | Types defined locally in `monitor/events.go` instead of importing from `internal/agent` (commented as intentional) |
| §4.1 Panic Recovery | PASS | Consistent recovery throughout — but see P3 finding on exitNotify |
| §4.2 PTY Write Timeout | PASS | WritePTY with timeout + hung child detection in Session |
| §4.3 Per-Connection Isolation | PASS | Client.Send non-blocking with per-client drop |
| §4.4 Non-Blocking Fan-Out | PASS | Both monitor and ClientManager use select/default |
| §5.1 OTEL Server | PASS | Per-session HTTP on random port, drain timeout |
| §5.3 Session Log Tailer | PASS | Polling with configurable interval (see P2 offset bug) |
| §7 Config Directory | PASS | ConfigDirManager with stable paths (see P2 traversal) |
| §8 Session Log Conversion | PASS | Canonical types defined; driver implementations are separate bead |
| §10 Package Structure | PASS | Matches plan layout |

## Concurrency Review

- **sync.RWMutex usage**: Correct throughout — Session, ClientManager, Monitor all use appropriate locking
- **Channel patterns**: Buffered channels with non-blocking send for fan-out (correct, matches plan §4.4)
- **sync.Once for Close**: Monitor.Close uses sync.Once (correct)
- **Race detector**: All tests pass with `-race`
- **Idle timer**: Uses `time.AfterFunc` with separate mutex (`idleTimerMu`) to avoid lock ordering issues with main state mutex

## Resource Leak Review

- **PTY fd cleanup**: Only runs through Stop() — **not called on natural exit** (P1 finding)
- **Client channels**: Closed via CloseAll in Stop() — same issue
- **Monitor goroutine**: Stopped via Close() in Stop() — same issue
- **OTEL server**: Stopped via SessionEventSources.Stop() — separate lifecycle
- **Session log tailer**: Stopped via context cancellation — correct

## Test Quality

Tests are thorough for the scope:
- 60+ test cases across 11 test files
- All major code paths covered: lifecycle, attach/detach, fan-out, state transitions, metrics, OTEL endpoints, tailer polling, event store round-trip
- Concurrent tests present for ClientManager, Monitor, Session
- Edge cases: double-start, double-close, capacity limits, reattach

Gaps:
- No test for natural session exit cleanup (related to P1)
- No test for concurrent Stop() calls
- No path traversal test for ConfigDirManager.InjectFile
- No test for tailer behavior with non-newline-terminated files
- Missing integration test for Session → Monitor → EventStore pipeline

---

## Summary

7 findings: 1 P1, 4 P2, 2 P3

**Verdict**: Approved with revisions

The implementation is well-structured, matches the plan closely, and has good test coverage. The panic recovery and non-blocking patterns from the h2 codebase are preserved correctly. The P1 session cleanup issue must be fixed before merge — it causes resource leaks in the normal (non-killed) session exit path. The P2 findings are important for robustness and security but are not blocking.
