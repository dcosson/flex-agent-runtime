# Code Review: aiag-q7c.1 (R1, reviewer-sea)

- Bead: aiag-q7c.1
- Commit range: 5ea9e4c..8d122fb
- Plan doc: docs/plans/11-sandbox-host-service.add02.md
- Reviewer: reviewer-sea
- Review commit: 8d122fb

## Findings

### P1 - Constructor missing exhaustive enum validation

**Location:** `internal/sandbox/service.go:91-108`

**Problem**
Plan §2.3 specifies two exhaustive `switch` statements that reject unknown `StorageBackend` and `ContainerRuntime` values at constructor time:

```go
switch cfg.StorageBackend {
case StorageBackendZFS, StorageBackendLocalDisk:
default:
    return nil, fmt.Errorf("sandbox: unknown storage_backend: %q", cfg.StorageBackend)
}
```

The implementation omits both switches entirely. The constructor goes directly from `mergeDefaultConfig` to backend/manager consistency checks (lines 98-106). This means:

1. `StorageBackend: "invalid"` passes construction — only caught at `CreateSession` time by the `default` case in the storage switch (service.go:213-215).
2. `ContainerRuntime: ""` silently becomes `"gvisor"` via `mergeDefaultConfig` instead of being rejected.

Plan AC3 explicitly requires: *"Call with `StorageBackend: "invalid"` → verify error. Call with `ContainerRuntime: ""` → verify error."* Neither scenario produces the expected constructor error.

The test file `configurable_backends_test.go` has no test cases for unknown/empty enum rejection, so this gap is untested.

**Suggested fix**
Add the two exhaustive switch statements from plan §2.3 after `mergeDefaultConfig`, before the consistency checks. Add test cases for `StorageBackend: "invalid"` and `ContainerRuntime: ""` (with no gvisor manager, to distinguish from the consistency check error).

---

### P2 - mergeDefaultConfig gated on SnapshotPrefix instead of unconditional

**Location:** `internal/sandbox/service.go:95-97`

**Problem**
The plan §2.3 shows `mergeDefaultConfig` running unconditionally:

```go
cfg = mergeDefaultConfig(cfg)
```

The implementation gates it on `cfg.SnapshotPrefix == ""`:

```go
if cfg.SnapshotPrefix == "" {
    cfg = mergeDefaultConfig(cfg)
}
```

This means if a caller sets `SnapshotPrefix` but leaves other fields at their zero values (e.g., `ToolTimeout`, `PoolSpaceWarnThreshold`), they won't receive defaults. The condition also interacts poorly with the missing enum validation: whether empty enums get default values depends on whether SnapshotPrefix was set, creating inconsistent behavior.

**Suggested fix**
Call `mergeDefaultConfig(cfg)` unconditionally, as specified in the plan. If the intent is to detect "fully explicit config vs needs defaults", use a more meaningful signal (e.g., check whether `StorageBackend` is set, since that's the primary config field introduced by this addendum).

---

### P2 - NativeSandboxEnvironment constructor uses variadic config instead of required parameter

**Location:** `internal/sandbox/environment/native/native.go:33`

**Problem**
Plan §3.2 specifies `NativeSandboxConfig` as a required parameter:

```go
func NewNativeSandboxEnvironment(
    service api.SandboxService,
    config NativeSandboxConfig,
    logger *slog.Logger,
) *NativeSandboxEnvironment
```

The implementation uses a variadic pattern that makes config optional:

```go
func NewNativeSandboxEnvironment(service api.SandboxService, logger *slog.Logger, cfg ...NativeSandboxConfig) *NativeSandboxEnvironment {
    config := DefaultConfig()
    if len(cfg) > 0 {
        config = cfg[0]
    }
    ...
}
```

This contradicts the plan's core principle: *"Configuration is explicit... There is no silent fallback."* (§1). The variadic signature allows callers to accidentally construct an environment with implicit ZFS+gVisor defaults, which is the opposite of what this addendum set out to achieve. Existing tests like `createTestEnv` (native_test.go:923-930) call the constructor without config and silently get ZFS+gVisor, masking whether the test is actually exercising the right backend configuration.

**Suggested fix**
Make `NativeSandboxConfig` a required parameter as the plan specifies. Update all existing callers/tests to explicitly pass the config they intend.

---

### P3 - api.Capabilities has 7 fields instead of plan's 5

**Location:** `internal/rpc/api/types.go:22-30`

**Problem**
Plan §9.2 explicitly specifies 5 fields for the RPC transport type: `{Snapshots, Rollback, Pause, TierRouting, StreamingProgress}`. The plan notes that `MaxSessionDuration` and `ConcurrentSessions` exist on `environment.Capabilities` but are "not relevant to capability negotiation."

The implementation includes all 7 fields from `environment.Capabilities` on `api.Capabilities`, and the codec maps them through. While the extra fields are always zero-valued in current usage, including them on the wire type adds unnecessary surface area and blurs the distinction the plan drew between domain types and RPC types.

**Suggested fix**
Consider removing `MaxSessionDuration` and `ConcurrentSessions` from `api.Capabilities` to match the plan. If they're needed for future use, document why they're included.

---

### P3 - Codec function naming deviates from plan

**Location:** `internal/rpc/codec/sandbox_map.go:27`

**Problem**
Plan §9.2 specifies `codec.ToAPICapabilities()` and `codec.FromAPICapabilities()`. The implementation uses `codec.FromEnvironmentCapabilities()` and omits the reverse mapping entirely.

The `From{Source}` naming is consistent with other codec functions (`FromSessionInfo`, `FromSnapshotResult`), so this is not an internal inconsistency — it's a plan-vs-implementation naming difference. The reverse mapping isn't needed since the client reads `ServerCapabilities` fields directly.

**Suggested fix**
No code change needed. Cosmetic deviation that's consistent with existing codec conventions.

---

### P3 - validateSessionIDForPath is unexported vs plan's public ValidateSessionID

**Location:** `internal/sandbox/config.go:28`

**Problem**
Plan §4.1 specifies `ValidateSessionID(id string) error` as a shared, public function. The implementation uses `validateSessionIDForPath(sessionID string) error` (unexported) wrapped inside `safeSessionPath`. The session ID validation is effectively the same, but the function is not available to other packages that might want to validate session IDs independently (e.g., the ZFS path also using session IDs as dataset name components, per plan §4.1).

**Suggested fix**
Consider exporting as `ValidateSessionID` per the plan, since the ZFS backend also uses session IDs in paths.

---

## Summary

6 findings: 1 P1, 2 P2, 3 P3

**Verdict**: Approved with revisions

The P1 (missing exhaustive enum validation in constructor) must be fixed — it's a plan compliance gap that means invalid configs aren't caught at construction time as specified in AC3. The P2s (conditional mergeDefaultConfig, variadic constructor config) should be addressed as they undermine the "explicit config, no silent fallback" principle that is central to this addendum's design. The P3s are cosmetic and can be addressed if convenient.

Overall the implementation is solid: path safety, capability negotiation, dynamic capabilities, tier downgrade, snapshot guards, and test coverage for the major flows are all well-implemented and match the plan's intent.
