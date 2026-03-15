# Code Review: aiag-83o.2 (R1, reviewer-sea)

- Bead: aiag-83o.2
- Commit range: a690d5c..88d423a
- Plan doc: docs/plans/11-sandbox-host-service.add01.md (§5.2, §7, §8)
- Reviewer: reviewer-sea
- Review commit: 88d423a

## Findings

### P1 - NativeSandboxEnvironment.State() uses context.Background() for RPC call

**Location:** `internal/sandbox/environment/native/native.go:134`

**Problem**
`State()` makes an RPC call to `GetSession` using `context.Background()`:
```go
resp, err := e.service.GetSession(context.Background(), &api.GetSessionRequest{SessionID: e.sessionID})
```

The plan specifies `State() SessionState` (no context parameter) which forces this. However, this means `State()` can block indefinitely on a network call with no cancellation support. If the RPC server is unreachable, this will hang until the underlying transport timeout (often 30s+ or indefinite for some gRPC configs).

The plan's §3.4 concurrency contract says `State()` takes a read lock for remote environments — but NativeSandboxEnvironment delegates to the server, so this becomes a blocking network call. If the agent loop calls `State()` during normal operation (e.g., to check health before executing a tool), a network hiccup could freeze the agent.

**Suggested fix**
Add an internal timeout to the `context.Background()` call: `ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)`. Alternatively, cache the last-known state locally and only refresh on lifecycle transitions, using the RPC as a fallback. This is a plan design issue but should be mitigated in the implementation.

---

### P2 - NativeSandboxEnvironment not registered in compliance suite

**Location:** `internal/sandbox/environment/native/native_test.go`

**Problem**
NativeSandboxEnvironment has comprehensive unit tests (29 tests covering lifecycle, state, capabilities, ExecuteTool, snapshots, rollback, errors) and a `TestInterfaceCompliance` compile check. However, it is not registered with `runEnvironmentComplianceSuite` from `compliance_test.go`. The compliance suite was designed to be the shared contract test that ALL ExecutionEnvironment implementations pass, and NativeSandboxEnvironment should use it.

The bead description says "Contract compliance suite passes for NativeSandboxEnvironment" — the unit tests effectively cover the same ground, but the compliance suite is the canonical contract verification. Registering with it ensures NativeSandboxEnvironment stays consistent as the suite evolves.

**Suggested fix**
Add a `TestNativeEnvironmentComplianceSuite` in the `environment_test` package (or a new file in the native package that imports the compliance suite helper). Use the mock service to back it.

---

### P2 - toolSchemas() creates full toolImpl structs but only uses metadata

**Location:** `internal/tools/factory.go:89-105`

**Problem**
`toolSchemas()` calls each tool constructor (e.g., `readFileTool(dummyRoot)`, `bashTool(dummyRoot)`) which creates full `toolImpl` structs including closures that capture `dummyRoot` in their execute functions. `NewEnvironmentTools` only uses `name`, `description`, and `schema` from these — the `execute` closures are never called. This creates unnecessary closures that capture a garbage root path (`"/"`).

This isn't a correctness bug, but it's wasteful and confusing — a reader might think the execute functions are used. More importantly, if a tool constructor has side effects (unlikely now, but possible in future), those would fire with a dummy root.

**Suggested fix**
Either: (a) extract tool metadata (name, description, schema) from the execution logic so `toolSchemas()` doesn't create execute closures, or (b) add a comment explaining that only metadata is used. Option (a) is cleaner but can be deferred to a follow-up since this is structurally consistent with how the deleted `buildSandboxAgentTools` worked.

---

### P3 - TestO3_SandboxBackendParity function name references deleted type

**Location:** `internal/rpc/rpctest/oracle_test.go:235`

**Problem**
The function name `TestO3_SandboxBackendParity` still references `SandboxBackend` which was deleted. The comment was updated to "RPC client parity oracle" but the function name was not.

**Suggested fix**
Rename to `TestO3_RPCClientParity` or similar.

---

### P3 - Destroy() doesn't check destroyed state before RPC call

**Location:** `internal/sandbox/environment/native/native.go:68-75`

**Problem**
`Destroy()` always makes the RPC call even if `destroyed` is already true (i.e., Destroy() called twice). The second call will try to destroy an already-deleted session. The RPC server likely returns an error but this is noisy and unnecessary.

This is minor because: (a) double-destroy is unlikely in normal flow, (b) the server should handle it gracefully, and (c) the plan doesn't specify idempotent Destroy.

**Suggested fix**
Add a guard: `if e.destroyed.Load() { return nil }` at the top of Destroy. Optional — up to implementor.

---

## Summary

5 findings: 0 P0, 1 P1, 2 P2, 2 P3

**Verdict**: Approved with revisions

## R1 Disposition

All findings addressed at 6d6d607. Re-reviewed and verified.

| # | Severity | Summary | Disposition | Notes |
|---|----------|---------|-------------|-------|
| 1 | P1 | State() uses context.Background() for RPC | Incorporated | Added 5s timeout via stateQueryTimeout constant. Well-documented comment explaining why. |
| 2 | P2 | NativeSandboxEnvironment not in compliance suite | Incorporated | TestNativeEnvironmentComplianceSuite added with complianceMockService. All subtests pass. |
| 3 | P2 | toolSchemas() creates unused execute closures | Incorporated | Explanatory comment added to toolSchemas() documenting why closures exist but are unused. Pragmatic — extracting metadata would be more error-prone. |
| 4 | P3 | TestO3_SandboxBackendParity stale name | Incorporated | Renamed to TestO3_RPCClientParity. |
| 5 | P3 | Destroy() no double-destroy guard | Incorporated | Guard added + TestDestroy_DoubleDestroy_Idempotent test verifying no RPC on second call. |

**Final verdict**: Approved
