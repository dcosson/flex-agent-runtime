# Review: 11-sandbox-host-service.add02 (coder-1-sea)

- Source doc: `docs/plans/11-sandbox-host-service.add02.md`
- Reviewed commit: f45b7ec
- Reviewer: coder-1-sea

## Findings

### P1 - RPC seam is changed but the plan declares RPC layer unchanged

**Problem**
The overview states “What does NOT change: The RPC layer” ([docs/plans/11-sandbox-host-service.add02.md:31](docs/plans/11-sandbox-host-service.add02.md:31)).
But section 6.1 introduces a new `CreateSessionResponse.ServerCapabilities` field and a capability-negotiation flow that depends on it ([docs/plans/11-sandbox-host-service.add02.md:406](docs/plans/11-sandbox-host-service.add02.md:406), [docs/plans/11-sandbox-host-service.add02.md:410](docs/plans/11-sandbox-host-service.add02.md:410), [docs/plans/11-sandbox-host-service.add02.md:420](docs/plans/11-sandbox-host-service.add02.md:420)).
That is an RPC contract change (API types + codec + client/server mapping + compatibility behavior), not just internal behavior.

**Required fix**
Make the seam explicit and consistent:
1. Either remove capability negotiation via `CreateSessionResponse` and keep RPC truly unchanged, or
2. update the plan to declare RPC contract changes and add concrete migration details (proto/API type updates, codec updates, client/server rollout order, backward compatibility behavior when old/new peers mix).

---

### P1 - Local-disk backend path safety requirements are underspecified for SessionID-derived directories

**Problem**
`CreateSession` local-disk path formation uses `filepath.Join(svc.config.SessionsRootDir, sessionID)` ([docs/plans/11-sandbox-host-service.add02.md:242](docs/plans/11-sandbox-host-service.add02.md:242)) and creates it directly with `os.MkdirAll` ([docs/plans/11-sandbox-host-service.add02.md:243](docs/plans/11-sandbox-host-service.add02.md:243)).
The plan does not require normalization/validation of `sessionID` for this filesystem boundary (e.g. traversal segments, absolute paths, platform separator edge cases). Given `CreateSession` accepts caller-provided IDs in current behavior, this is a security-critical seam.

**Required fix**
Add an explicit session ID filesystem safety contract for local-disk mode:
1. Define allowed session ID charset/length and reject anything else before path join.
2. Resolve and enforce the final path remains under `SessionsRootDir`.
3. Add explicit security tests for traversal and separator variants (Unix + Windows forms).

---

### P2 - Constructor signature change is declared, but migration sequencing across existing callers is not concrete

**Problem**
The plan changes `NewSandboxHostService` from `*SandboxHostService` to `(*SandboxHostService, error)` ([docs/plans/11-sandbox-host-service.add02.md:110](docs/plans/11-sandbox-host-service.add02.md:110), [docs/plans/11-sandbox-host-service.add02.md:138](docs/plans/11-sandbox-host-service.add02.md:138), [docs/plans/11-sandbox-host-service.add02.md:479](docs/plans/11-sandbox-host-service.add02.md:479)).
It says “all callers must be updated,” but does not enumerate caller groups and ordering (tests, RPC server wiring, cmd entrypoints, harness stack builders), which raises risk of partial migration and compile/runtime drift during rollout.

**Required fix**
Add a concrete migration checklist with affected call sites and rollout order:
1. update constructor call sites,
2. propagate error handling strategy at each call boundary,
3. define temporary compile gates/CI steps to prevent partial migration merges.

---

### P2 - Acceptance criteria and test-harness mapping are missing for add02-specific behavior

**Problem**
This addendum introduces substantial new branching behavior (4 backend matrices, capability negotiation, local-disk semantics, no-gVisor execution path), but the doc does not define add02 acceptance criteria or a dedicated test harness mapping section. The plan currently relies on narrative examples only.

**Required fix**
Add explicit add02 acceptance criteria and cross-reference test lanes for each new seam:
1. backend matrix behavior (zfs/gvisor combinations),
2. capability negotiation mismatch failure,
3. local-disk create/destroy semantics,
4. snapshot/rollback behavior in non-ZFS modes,
5. constructor validation errors for invalid backend/manager combinations.

---

## Summary

4 findings: 0 P0, 2 P1, 2 P2, 0 P3

**Verdict**: Approved with revisions
