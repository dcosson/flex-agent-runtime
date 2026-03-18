# Code Review: aiag-agy.3 (R1, reviewer-sea)

- Bead: aiag-agy.3
- Commit range: e407509..d586657
- Plan doc: docs/plans/21-serve-orchestrator.md (§3.9-3.10, §6 Bead 3, §7)
- Reviewer: reviewer-sea
- Review commit: d586657

## Findings

### P2-1 — Two concurrency tests from plan §7.3 not implemented

**Location:** `internal/orchestrator/health_test.go`

**Problem**
Plan §7.3 explicitly lists these concurrency tests:
- "Concurrent SendMessage to different sessions"
- "Client-canceled stream while backend stream is active does not leak goroutines"

Neither is present. `TestConcurrentCreateSessionAcrossModes` covers concurrent creates, and `TestDestroySessionWhileStreamingDoesNotDeadlock` covers concurrent destroy+stream, but the two listed tests are missing.

The goroutine-leak test is particularly valuable because the `serviceEventReceiver` rewrite in `internal/agent/service.go` removed `close(r.ch)` and now relies on the `done` channel — a goroutine-leak test would exercise this path and confirm no leak under cancellation.

**Suggested fix**
Add:
1. A test that creates N sessions and sends messages concurrently to each, verifying all complete without error and no cross-session contamination.
2. A test that starts a streaming SendMessage, cancels the caller's context mid-stream, and asserts goroutine count returns to baseline within a short timeout (e.g., using `runtime.NumGoroutine()` before/after).

---

### P3-1 — Health checks are serial within a cycle

**Location:** `internal/orchestrator/health.go:36-39`

**Problem**
`checkAllSessions` iterates sessions sequentially. Each `checkSessionHealth` call does an HTTP health probe with up to 3s timeout, plus potentially a `GetProcessStatus` call (another 3s). With N sessions, a single health cycle could take up to 6N seconds in the worst case. At 100 sessions with slow/unreachable backends, one cycle would take ~10 minutes, far exceeding the 15s default `HealthInterval`.

**Suggested fix**
Low priority for now — with the expected session counts this won't be a problem. If session counts grow, fan out health checks with a bounded worker pool (e.g., `N` goroutines reading from a session channel). No action needed for this bead.

---

### P3-2 — Close() does not Abort active turns before DestroySession

**Location:** `internal/orchestrator/orchestrator.go:115-129`

**Problem**
Plan §3.10 specifies the shutdown sequence as: (1) reject new sessions, (2) abort active turns, (3) destroy sessions, (4) destroy sandboxes, (5) stop health monitor. The current `Close()` waits for in-flight operations (which effectively drains currently-executing proxy calls) and then destroys sessions directly without an explicit Abort step.

For most cases this is fine since backend `DestroySession` implementations should handle aborting their own active turns. But for agent-direct sessions where the backend agent is a remote process, an explicit Abort before Destroy would give the agent a chance to cleanly wind down before the sandbox is killed.

**Suggested fix**
Consider adding an `Abort` call before `destroySessionEntry` in the Close loop for agent-direct sessions. Low priority — backend Destroy should handle this.

---

### P3-3 — Redundant timeout layers on health HTTP client

**Location:** `internal/orchestrator/orchestrator.go:53`, `internal/orchestrator/health.go:135`

**Problem**
The `healthClient` has a 5s `http.Client.Timeout`, and each health check also creates a `context.WithTimeout(ctx, healthProbeTimeout)` with `healthProbeTimeout = 3s`. The context timeout (3s) will always fire before the client timeout (5s), making the client-level timeout dead code. This isn't wrong but is confusing — a reader might think the 5s timeout matters.

**Suggested fix**
Either remove the client-level timeout (set to 0 and rely on per-request context) or align them. Minor nit.

---

## Non-finding notes

**service.go race fix is correct.** The `serviceEventReceiver` rewrite replaces `close(r.ch)` with `close(r.done)` signaling, and `Recv()` uses a loop that drains buffered events before reporting EOF. The TOCTOU window where `push()` sends to `ch` after `done` is closed is handled by the drain loop in `Recv()`. The removal of `close(r.ch)` eliminates the panic-on-send-to-closed-channel race. Clean fix.

**Mock thread safety.** All `mockAgentService` methods now properly lock/unlock `mu`, which is necessary since health checks run concurrently with proxy call tests. Good.

**snapshotProxyTarget abstraction.** Consolidating the lock-snapshot-check-unhealthy pattern into one method that all 8 proxy methods call is a clean refactor that eliminates repetitive lock/check code and ensures consistent unhealthy gating.

## Summary

4 findings: 0 P0, 0 P1, 1 P2, 3 P3

**Verdict**: Approved with revisions

The health monitoring implementation is well-designed. The unlock-check-relock pattern in `checkSessionHealth` correctly avoids holding the mutex during I/O. The `snapshotProxyTarget` abstraction cleanly gates all proxy methods on health state. The threshold-based process status probing adds useful crash detection without being noisy. Test coverage is thorough with health transitions, recovery, process exit detection, tools-sandbox health routing, concurrent creates, streaming+destroy interleaving, provisioning cancellation, and codec fidelity. The service.go race fix is a valuable bonus.

The only P2 is two missing concurrency tests that the plan explicitly calls for. The P3s are minor.
