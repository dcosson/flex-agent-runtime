# Review: 11-sandbox-host-service.add01 (reviewer-sea)

- Source doc: `docs/plans/11-sandbox-host-service.add01.md`
- Test harness doc: `docs/plans/11-sandbox-host-service.add01-test-harness.md`
- Reviewed commit: 35a2fde
- Reviewer: reviewer-sea

## Findings

### P1 - Concurrent access to mutable environment state is unprotected

**Problem**
E2B (§5.3 lines 640-651), Daytona (§5.4 lines 782-793), and Fly (§5.5 lines 850-867) environments all have mutable state fields (`state`, `sandboxID`/`workspaceID`/`machineID`, `labels`, etc.) that are read and written without any synchronization primitive (mutex, atomic). The sequence diagram (§2.3) shows RuntimeController calling lifecycle methods (Pause, Resume, Destroy) while the Agent Loop concurrently calls ExecuteTool. ExecuteTool reads `e.state` (e.g., E2B line 700: `if e.state != StateActive`), while Pause writes `e.state` (line 723: `e.state = StatePaused`). This is a data race. The stress tests ST1 and ST2 in the test harness explicitly verify no races under concurrency, but the plan doesn't specify how race-freedom is achieved.

**Required fix**
Add a `sync.RWMutex` (or equivalent) to each remote environment struct. Lifecycle methods take a write lock; ExecuteTool takes a read lock for state checks. Document the locking strategy in each implementation. NativeSandboxEnvironment may not need this if the server-side SandboxHostService handles all state, but the three remote environments definitely do.

---

### P1 - LocalEnvironment.Pause violates test harness property P1

**Problem**
`LocalCapabilities` (§4.2 line 373) sets `Pause: false`. `LocalEnvironment.Pause()` (§5.1 line 445-447) returns `nil`. Test harness property P1 states: "if `!caps.Pause` → `err := env.Pause(ctx)` → `assert errors.Is(err, ErrCapabilityNotSupported)`." LocalEnvironment fails this invariant — Pause is false but Pause() succeeds. The same issue applies to Resume and Destroy, though those are less likely to cause problems since they're also no-ops.

Similarly, `LocalCapabilities.Snapshots = false` and `LocalCapabilities.Rollback = false` but `LocalEnvironment.CreateSnapshot()` and `Rollback()` correctly return `ErrCapabilityNotSupported`. So the Pause/Resume no-op behavior is inconsistent with the snapshot/rollback pattern.

**Required fix**
Either (a) set `LocalCapabilities.Pause = true` — semantically, "calling Pause on this environment succeeds and the caller doesn't need to take alternative action," which is the correct behavior for All Local mode, OR (b) have `LocalEnvironment.Pause()` return `ErrCapabilityNotSupported`. Option (a) is preferable because in All Local mode, the caller doesn't need special handling — a no-op pause is fine. Update the capability semantics doc to clarify: `Pause: true` means "Pause() will succeed" (even if it's a no-op), `Pause: false` means "Pause() will return ErrCapabilityNotSupported."

---

### P2 - No state query method on ExecutionEnvironment

**Problem**
The architecture doc's `RuntimeController` interface (00-architecture.md line 48) includes `SessionState(ctx context.Context, id string) (SessionState, error)`. The RuntimeController needs to query session state. But `ExecutionEnvironment` has no `Status()` or `State()` method. The remote environments track state internally (`e.state`) but don't expose it through the interface. How does RuntimeController implement `SessionState()` when it delegates lifecycle to an `ExecutionEnvironment`?

If RuntimeController tracks state independently (shadow state), it can drift from the environment's actual state (e.g., E2B sandbox auto-stops after 24h, but RuntimeController still thinks it's active). If the environment exposes state, the RuntimeController can query it. Neither approach is specified.

**Required fix**
Either (a) add `State() SessionState` to the `ExecutionEnvironment` interface — each environment returns its current state (created, active, paused, destroyed), or (b) explicitly document that RuntimeController maintains its own state machine and doesn't query the environment, including how it handles environment-side state changes (timeouts, auto-stops, crashes). Option (a) is cleaner.

---

### P2 - SSH host key verification strategy undefined for Fly

**Problem**
`FlySandboxEnvironment` uses SSH for all tool execution (§5.5 lines 888-898). The test harness SEC4 requires "SSH host key verification is enforced (no `InsecureIgnoreHostKey`)." However, the plan doesn't specify how to obtain or verify host keys for dynamically created Fly machines. Machines are created on-demand — their host keys aren't known in advance. Options include: pinning keys in the machine image, using Fly's API to retrieve keys after creation, or using certificate-based host key verification. Without a specified strategy, implementors will likely use `InsecureIgnoreHostKey` and the SEC4 test will either fail or be skipped.

**Required fix**
Specify the SSH host key verification strategy for Fly machines. If the custom image pins a known host key, document that. If Fly's API can return the machine's host key after creation, document the API call. If using SSH certificates, specify the CA and signing flow.

---

### P2 - `isFileOp()` helper referenced but never defined

**Problem**
E2B's `ExecuteTool` (§5.3 line 708: `case isFileOp(req.ToolName)`) and Fly's `ExecuteTool` (§5.5 line 893) both reference an `isFileOp()` helper to route file operations separately from command operations. This function is never defined anywhere in the plan. The tool classification criteria (which tools are "file ops" vs "process ops") and the function's package location are unspecified. The parent plan 11 has a `router.go` Tier classifier (§2.1), but that classifies into Tier 1/2 (in-process vs container), which is a NativeSandbox-specific concept — remote environments don't have tiers.

**Required fix**
Define `isFileOp()` — specify which tool names it matches (presumably read, write, edit, grep, glob), where it lives (shared utility in the environment package or a helper in each remote implementation), and whether it shares logic with the Tier 1/2 classifier or is independent.

---

### P2 - Missing acceptance criteria section

**Problem**
The plan has detailed unit and integration test coverage (§10) and a comprehensive test harness doc, but no acceptance criteria section. Per plan conventions, acceptance criteria should be 3-8 end-user-facing scenarios that cross at least one component boundary and prove the component works in the real system. Examples: "Agent loop executes a bash tool through E2BSandboxEnvironment and receives streamed output," "RuntimeController pauses a NativeSandboxEnvironment session, resumes it, and the agent's working directory state is preserved."

**Required fix**
Add an acceptance criteria section with 3-5 scenarios that exercise the ExecutionEnvironment interface from the agent loop or RuntimeController perspective, crossing at least one component boundary each.

---

### P3 - Import flow claim is inaccurate

**Problem**
§2.4 (line 157) says the interface package `internal/sandbox/environment` is "minimal: types + interface only." However, §3.2 (lines 287-290) shows:
```go
type ToolRequest = tools.ToolRequest
type ToolResponse = tools.ToolResponse
type ToolProgress = tools.ToolProgress
```
These type aliases require importing `internal/tools`, which is an implementation package containing tool execution logic. The interface package is not truly minimal — it depends on the tools package. If `internal/tools` ever needs to reference environment types (e.g., for a tool that creates snapshots), this creates a circular import risk.

**Required fix**
Either (a) acknowledge the `internal/tools` import in the import flow description, or (b) consider whether the shared types (`ToolRequest`, `ToolResponse`, `ToolProgress`) should live in a separate minimal types package that both `internal/sandbox/environment` and `internal/tools` import. This is a structural concern, not blocking.

---

### P3 - `SessionConfig.Options` typed as `any` loses compile-time safety

**Problem**
`SessionConfig.Options` (§3.2 line 275) is typed as `any`. Each environment does a runtime type assertion (e.g., E2B line 676: `e2bOpts, ok = config.Options.(E2BOptions)`). A caller passing the wrong options type gets a runtime error instead of a compile-time error. This is a common Go pattern for extensibility but worth calling out — the test harness should explicitly cover wrong-type Options to verify the error path.

**Required fix**
Either (a) add an interface like `EnvironmentOptions` with a marker method that each options struct implements, providing at least some type safety, or (b) add a note in §10 testing section to explicitly test the wrong-Options-type error path. Option (b) is sufficient if the team prefers the `any` approach.

---

### P3 - Dead code in NativeSandboxEnvironment stream loop

**Problem**
In the `ExecuteTool` recv loop (§5.2 lines 557-575):
```go
for {
    msg, recvErr := stream.Recv()
    if recvErr != nil {
        if final != nil {
            break
        }
        return nil, ...
    }
    ...
    if msg.Response != nil {
        final = msg.Response
        break  // exits loop immediately
    }
}
```
The `if final != nil { break }` branch is unreachable. `final` is only set on the line `final = msg.Response`, which is immediately followed by `break`. So when the loop reaches `recvErr != nil`, `final` is always nil. The dead branch suggests the author intended to continue receiving after the response (to drain the stream), but the `break` prevents that. Either the `break` after setting `final` should be removed (to allow draining), or the `if final != nil` check should be removed as dead code.

**Required fix**
Clarify the intended stream protocol. If the server sends the response as the last message before closing the stream, remove the `if final != nil` dead branch. If the server might send additional messages after the response, remove the `break` after setting `final` and let the loop drain until EOF.

---

### P3 - Orphaned sandbox recovery mechanism unspecified

**Problem**
Open Question #2 in the plan (not numbered in a section, referenced in the overview context) acknowledges the risk of orphaned sandboxes when the process managing a remote environment crashes. If the RuntimeController process dies after creating an E2B sandbox but before destroying it, the sandbox runs until its 24h timeout (E2B) or indefinitely (Daytona, Fly). There's no lease, heartbeat, or garbage collection mechanism specified. The test harness F2 covers creation failure recovery, but not mid-session process crash.

**Required fix**
Specify a cleanup strategy: (a) lease-based TTL where environments auto-destroy after N minutes without a heartbeat, (b) label-based GC where a background sweep finds and destroys orphaned environments by matching labels, (c) defer to provider-specific mechanisms (E2B's 24h timeout, Fly's auto-stop). At minimum, document the expected behavior and any manual recovery steps.

---

## Summary

10 findings: 2 P1, 4 P2, 4 P3

**Verdict**: Approved with revisions

The plan presents a clean, well-motivated unification of `ToolBackend` and sandbox lifecycle into a single `ExecutionEnvironment` interface. The five implementations cover the full spectrum from local to cloud. The P1 findings (data races in remote environments, Pause capability inconsistency) must be addressed before implementation. The P2 findings (state query, SSH keys, isFileOp, acceptance criteria) should be resolved to prevent integration issues. The P3 findings are minor but would improve clarity.
