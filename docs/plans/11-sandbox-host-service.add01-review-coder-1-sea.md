# Review: 11-sandbox-host-service.add01 (coder-1-sea)

- Source doc: `docs/plans/11-sandbox-host-service.add01.md`
- Reviewed commit: d290216
- Reviewer: coder-1-sea

## Findings

### P1 - Capability contract conflict for LocalEnvironment Pause/Resume

**Problem**
The addendum marks `LocalCapabilities.Pause` as `false` ([docs/plans/11-sandbox-host-service.add01.md:373](docs/plans/11-sandbox-host-service.add01.md:373)), and the harness requires `Pause/Resume` to return `ErrCapabilityNotSupported` whenever `caps.Pause == false` ([docs/plans/11-sandbox-host-service.add01-test-harness.md:22](docs/plans/11-sandbox-host-service.add01-test-harness.md:22), [docs/plans/11-sandbox-host-service.add01-test-harness.md:25](docs/plans/11-sandbox-host-service.add01-test-harness.md:25), [docs/plans/11-sandbox-host-service.add01-test-harness.md:217](docs/plans/11-sandbox-host-service.add01-test-harness.md:217)).
But LocalEnvironment is specified to return `nil` for `Pause/Resume` as no-ops ([docs/plans/11-sandbox-host-service.add01.md:445](docs/plans/11-sandbox-host-service.add01.md:445), [docs/plans/11-sandbox-host-service.add01.md:449](docs/plans/11-sandbox-host-service.add01.md:449)).
This is internally inconsistent between the plan contract and harness invariants.

**Required fix**
Choose one contract and make both docs consistent:
1. Either keep `Pause=false` and require `Pause/Resume -> ErrCapabilityNotSupported` for LocalEnvironment, or
2. treat Local no-op pause/resume as supported and set `LocalCapabilities.Pause=true` with harness exceptions updated accordingly.
Also clarify this rule once in the core interface section so all environments follow one capability/error semantic.

---

### P1 - Destroy semantics conflict with compliance suite for LocalEnvironment

**Problem**
The plan defines Local `Destroy` as a no-op ([docs/plans/11-sandbox-host-service.add01.md:453](docs/plans/11-sandbox-host-service.add01.md:453)) and Local `ExecuteTool` as regular local execution ([docs/plans/11-sandbox-host-service.add01.md:457](docs/plans/11-sandbox-host-service.add01.md:457)).
The shared compliance suite then requires that `ExecuteTool` on a destroyed environment returns an error for every implementation ([docs/plans/11-sandbox-host-service.add01-test-harness.md:250](docs/plans/11-sandbox-host-service.add01-test-harness.md:250), [docs/plans/11-sandbox-host-service.add01-test-harness.md:254](docs/plans/11-sandbox-host-service.add01-test-harness.md:254)).
These requirements cannot both be true for LocalEnvironment as currently specified.

**Required fix**
Define lifecycle semantics explicitly for LocalEnvironment and align the compliance suite:
1. If Local remains no-op lifecycle, scope `DestroyedEnvironmentErrors` to environments with real teardown, or
2. make Local track destroyed state and return `ErrNotActive` post-destroy.
Update unit/integration sections so one consistent lifecycle model is testable across all implementations.

---

### P2 - Concurrency safety requirements are underspecified despite concurrent harness expectations

**Problem**
The harness explicitly requires concurrent lifecycle and execute operations with `-race` ([docs/plans/11-sandbox-host-service.add01-test-harness.md:302](docs/plans/11-sandbox-host-service.add01-test-harness.md:302), [docs/plans/11-sandbox-host-service.add01-test-harness.md:310](docs/plans/11-sandbox-host-service.add01-test-harness.md:310)).
But plan snippets for remote environments show mutable shared fields (`state`, IDs, timestamps) with no synchronization strategy ([docs/plans/11-sandbox-host-service.add01.md:647](docs/plans/11-sandbox-host-service.add01.md:647), [docs/plans/11-sandbox-host-service.add01.md:723](docs/plans/11-sandbox-host-service.add01.md:723), [docs/plans/11-sandbox-host-service.add01.md:857](docs/plans/11-sandbox-host-service.add01.md:857), [docs/plans/11-sandbox-host-service.add01.md:908](docs/plans/11-sandbox-host-service.add01.md:908)).
Without a stated thread-safety contract, implementers can satisfy the interface but still violate the stress requirements.

**Required fix**
Add an explicit concurrency contract section:
1. Specify whether `ExecutionEnvironment` implementations must be goroutine-safe.
2. Define permitted concurrent method pairs (e.g., `ExecuteTool` concurrent with `Pause/Destroy` or not).
3. Require internal synchronization strategy (mutex/atomic/serialized event loop) for mutable environment state.
4. Mirror this in compliance/stress suites with deterministic assertions for allowed/disallowed interleavings.

---

### P2 - Session identity ownership is ambiguous when SessionConfig.SessionID is empty

**Problem**
`SessionConfig.SessionID` says “If empty, the environment generates one” ([docs/plans/11-sandbox-host-service.add01.md:266](docs/plans/11-sandbox-host-service.add01.md:266)).
However, the interface `Create(ctx, config) error` has no way for callers to retrieve the generated ID ([docs/plans/11-sandbox-host-service.add01.md:203](docs/plans/11-sandbox-host-service.add01.md:203)).
At the same time, migration text frames RuntimeController/environment selection as session-scoped orchestration ([docs/plans/11-sandbox-host-service.add01.md:1012](docs/plans/11-sandbox-host-service.add01.md:1012)).
This leaves ownership unclear: caller-assigned IDs vs environment-assigned IDs, and how external orchestrator state correlates to created sessions.

**Required fix**
Make session identity contract explicit and testable:
1. Prefer requiring caller-provided `SessionID` (reject empty), or
2. change `Create` to return created metadata including effective `SessionID`.
Then add a harness/contract test asserting this behavior.

---

## Summary

4 findings: 0 P0, 2 P1, 2 P2, 0 P3

**Verdict**: Approved with revisions
