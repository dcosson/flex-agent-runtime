# Code Review: aiag-83o.1 (R1, reviewer-sea)

- Bead: aiag-83o.1
- Commit range: 1179700..4cdb51d
- Plan doc: docs/plans/11-sandbox-host-service.add01.md (§3, §4, §5.1)
- Reviewer: reviewer-sea
- Review commit: 4cdb51d

## Findings

### P2 - Error sentinel strings differ from plan

**Location:** `internal/sandbox/environment/errors.go:5-11`

**Problem**
The plan (§3.3) specifies these error strings:
- `"capability not supported by environment"`
- `"environment not in active state"`
- `"environment unavailable"`
- `"session limit reached"`

The implementation uses:
- `"environment: capability not supported"`
- `"environment: not active"`
- `"environment: provider unavailable"` (different name: `ErrProviderUnavailable` vs plan's `ErrUnavailable`)
- `"environment: session limit reached"`

Additionally, `ErrSessionNotFound` (`"environment: session not found"`) exists in the code but is not defined in the plan's §3.3. The plan only mentions `ErrSessionNotFound` nowhere — this sentinel is an addition beyond plan.

The string differences are cosmetic and won't affect callers using `errors.Is()`. However, the name `ErrProviderUnavailable` vs `ErrUnavailable` is a contractual concern — downstream code (plan 13 RPC error mapping, agent loop retry logic) will reference the sentinel by name, and the plan says `ErrUnavailable`.

**Suggested fix**
Rename `ErrProviderUnavailable` to `ErrUnavailable` to match the plan. The extra `ErrSessionNotFound` sentinel is fine as an addition — just note it in the bead comment. String text differences are cosmetic.

---

### P2 - Compliance suite missing State() transition tests

**Location:** `internal/sandbox/environment/compliance_test.go:17-105`

**Problem**
The compliance suite (`runEnvironmentComplianceSuite`) does not test `State()` at any lifecycle point. The test harness doc (C1) specifies the compliance suite as covering all interface methods, and `State()` is a core interface method. The unit tests in `local_test.go` cover State() for LocalEnvironment specifically, but the compliance suite — which is meant to be run against every environment implementation — does not verify:
- `State() == StateActive` after `Create()`
- `State() == StateDestroyed` (or a terminal state) after `Destroy()`
- State correctness after Pause/Resume (if applicable)

Future implementations (NativeSandbox, E2B, etc.) will register with this suite, and without State() assertions, bugs in state tracking won't be caught by the shared suite.

**Suggested fix**
Add `State()` assertions to the compliance suite's `FullLifecycle` test (assert `StateActive` after Create, terminal state after Destroy) and `PauseResume` test (assert `StatePaused` after Pause if the environment tracks it).

---

### P2 - LocalEnvironment embeds tools.LocalBackend instead of plan's executeLocalTool

**Location:** `internal/sandbox/environment/local/local.go:17-20`

**Problem**
The plan (§5.1 line 532) specifies `executeLocalTool(ctx, e.workDir, req, onProgress)` as a standalone function, while the implementation stores a `*tools.LocalBackend` field and delegates to `e.backend.ExecuteTool()`. This works correctly now but creates a structural divergence: the plan intends LocalEnvironment to own tool dispatch directly (via a package-level function), while the code wraps the legacy `LocalBackend` type.

This means LocalEnvironment has a hard dependency on `tools.LocalBackend` — which is the type being replaced by this very migration. When `ToolBackend` is removed (§7.3), `tools.LocalBackend` will presumably be deleted, breaking LocalEnvironment.

**Suggested fix**
Either: (a) add an `executeLocalTool` function in the `local` package that wraps the underlying tool dispatch (so the `LocalBackend` dependency is an implementation detail of that function, easily swappable), or (b) document that `tools.LocalBackend` will be retained as internal implementation after the `ToolBackend` interface is removed. Option (a) is cleaner and matches the plan.

---

### P3 - Create() validates SessionID but plan says no-op

**Location:** `internal/sandbox/environment/local/local.go:31-38`

**Problem**
The plan (§5.1 line 508) shows `LocalEnvironment.Create()` as a pure no-op: `return nil // no-op: local filesystem is always available`. The implementation adds SessionID validation and destroyed-state check. The SessionID validation is good (matches §3.2 which says `SessionID` is required), but the plan should be updated to reflect this, or this is intentional divergence from the plan's no-op description.

The destroyed-state check in Create (`if e.destroyed.Load() { return ErrNotActive }`) is also reasonable but not specified — a destroyed LocalEnvironment shouldn't be re-created.

**Suggested fix**
This is a plan compliance nit. The code is better than the plan here. No code change needed — just noting the divergence for the disposition table.

---

### P3 - Compliance suite factory creates environments sharing state

**Location:** `internal/sandbox/environment/compliance_test.go:107-127`

**Problem**
`TestLocalEnvironmentComplianceSuite` creates a single factory that returns `local.NewLocalEnvironment(root, slog.Default())` with the same `root` directory. But each sub-test (`FullLifecycle`, `PauseResume`, `SnapshotRollback`, `DestroyedEnvironmentErrors`) calls `mkEnv(t)` which creates a fresh environment instance — this is correct, they get independent instances. However, they all share the same `root` temp directory with the same `input.txt` seed file.

For `DestroyedEnvironmentErrors`, after `Destroy()` the test calls `ExecuteTool()` and expects an error. If `FullLifecycle` ran first and modified the filesystem (it doesn't currently), it could affect later sub-tests. For LocalEnvironment this is harmless since tests only read `input.txt`, but for future environments registered with this suite, shared filesystem state across sub-tests could cause flaky tests.

**Suggested fix**
Consider having `mkEnv` create a per-sub-test temp directory (using the `t` parameter it receives), or document that the factory must return fully independent environments.

---

## Summary

5 findings: 0 P0, 0 P1, 3 P2, 2 P3

**Verdict**: Approved with revisions
