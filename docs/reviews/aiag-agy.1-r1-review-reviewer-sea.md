# Code Review: aiag-agy.1 (R1, reviewer-sea)

- Bead: aiag-agy.1
- Commit range: 3e23f36 (single commit)
- Plan doc: docs/plans/21-serve-orchestrator.md §3.1–3.6, §6 Bead 1
- Reviewer: reviewer-sea
- Review commit: 3e23f36

## Findings

### P2-1 — Auth token exposed as CLI argument to remote agent

**Location:** `cmd/flexagent/serve_orchestrator.go:150-153`

**Problem**
The auth token is appended to `agentLaunchArgs` as `--auth-token <token>`, making it visible in `/proc/PID/cmdline` and `ps` output on the remote EC2 instance. The token is also passed via env var `FLEXAGENT_AUTH_TOKEN` (lines 155-158), which the agent's `serve agent` command already reads via `envOrDefault`. The CLI arg is redundant and a worse security practice.

**Suggested fix**
Remove the `--auth-token` flag from `agentLaunchArgs` and rely solely on the `FLEXAGENT_AUTH_TOKEN` env var in `agentEnv`.

---

### P2-2 — Missing compensation cleanup tests for intermediate failure steps

**Location:** `internal/orchestrator/orchestrator_test.go`

**Problem**
Two compensation paths in `createAgentDirectSession` are untested:
1. **AgentServiceFactory failure** (proxy.go:78-85) — should trigger KillProcess + DestroySandbox. Currently only the LaunchProcess failure path is tested (`TestCreateSessionLaunchFailureCompensatesWithDestroySandbox`).
2. **Backend CreateSession failure** (proxy.go:87-95) — should trigger DestroySession on the agent + Close + KillProcess + DestroySandbox, the most complex 4-step compensation.

These are the paths that prevent resource leaks when provisioning partially succeeds. Without test coverage, regressions here could silently leak EC2 instances or orphaned agent processes.

**Suggested fix**
Add two tests: one with a factory that returns an error (verify KillProcess and DestroySandbox are called), and one with a mock AgentService whose CreateSession returns an error (verify the full 4-step compensation sequence and ordering).

---

### P3-1 — Close() idempotency and in-flight drain not tested

**Location:** `internal/orchestrator/orchestrator_test.go`

**Problem**
Plan §7.1 lists "Close() is idempotent and waits for in-flight operations" as a test case. `TestCloseRejectsCreateSession` verifies post-close rejection, but neither idempotency (second Close() returns nil) nor in-flight waiting (Close blocks until an active CreateSession completes) is tested.

**Suggested fix**
Add a test that calls Close() twice and checks the second call returns nil. Optionally add a test with a slow mock CreateSandbox to verify Close() blocks until the in-flight operation finishes.

---

### P3-2 — `destroySessionEntry` ignores passed context

**Location:** `internal/orchestrator/proxy.go:297`

**Problem**
The function accepts `_ context.Context` but creates independent `context.WithTimeout(context.Background(), stepTimeout)` for each step. The per-session context passed from `Close()` (orchestrator.go:103) is unused. With 4 teardown steps, worst-case per-session teardown takes 4×ShutdownTimeout (4×30s = 2m), which exceeds the caller's intended single ShutdownTimeout envelope.

**Suggested fix**
Low priority. The bounded total is acceptable for the small number of steps. Consider documenting this behavior or, for a stricter bound, deriving step timeouts from the passed context. Can be deferred.

---

### P3-3 — `buildDirectControl` unconditionally called

**Location:** `cmd/flexagent/serve_orchestrator.go:144`

**Problem**
`buildDirectControl` is always invoked (including AWS config loading), even though future bead 2 will add sandbox-host mode where direct mode may not be configured. Currently fine since validate() requires DirectAMIID, but will need gating when sandbox-host support lands.

**Suggested fix**
Gate behind `if strings.TrimSpace(cfg.DirectAMIID) != ""`. Can be deferred to bead 2 since validate() enforces the invariant today.

---

### P3-4 — `isIgnorableTeardownErr` broad string matching

**Location:** `internal/orchestrator/proxy.go:370-373`

**Problem**
String matching on `"already"` in error messages could suppress unrelated errors whose messages happen to contain the word. For best-effort teardown this is an acceptable trade-off, but more specific patterns would reduce false-positive suppression.

**Suggested fix**
Consider matching `"already destroyed"`, `"already stopped"`, or `"already exited"` instead of bare `"already"`. Low priority given teardown's best-effort semantics.

---

## Summary

6 findings: 0 P0, 0 P1, 2 P2, 4 P3

**Verdict**: Approved with revisions

The implementation is solid and well-structured. The orchestrator correctly implements the full AgentService proxy with session ID translation, compensation cleanup at every failure step, ordered destroy teardown, and clean Close() lifecycle. The mock infrastructure is thorough and the test suite covers the critical paths — placement routing, ID rewriting, event fidelity, destroy ordering, max sessions, and post-close rejection. The serve_orchestrator.go CLI wiring faithfully follows the plan's flag table and matches the established `serve agent` / `serve all` patterns.

The two P2 findings should be addressed before closing the bead: the auth token CLI arg leak is a straightforward removal, and the compensation tests fill an important coverage gap for resource leak prevention.
