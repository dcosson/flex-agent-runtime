# 11 Addendum 02: Configurable Infrastructure Backends

**Parent plan:** [11-sandbox-host-service.md](./11-sandbox-host-service.md)
**Depends on:** [11-sandbox-host-service.add01.md](./11-sandbox-host-service.add01.md) (ExecutionEnvironment interface)
**Status:** Draft
**Scope:** Make `NativeSandboxEnvironment` and `SandboxHostService` operate with or without ZFS and with or without gVisor, controlled by explicit configuration. No auto-detection.

---

## 1. Overview

The current `SandboxHostService` unconditionally requires ZFS for session storage and gVisor for Tier 2 tool execution. This addendum makes both infrastructure dependencies optional and explicitly configured, so that the native sandbox can run in degraded modes:

- **ZFS + gVisor** (full): snapshots, rollback, Tier 2 container isolation. Current behavior.
- **ZFS only** (no gVisor): snapshots and rollback work; all tools execute directly (no container isolation).
- **gVisor only** (no ZFS): Tier 2 isolation works; sessions use regular directories; no snapshots or rollback.
- **Neither** (local-disk + no container runtime): sessions use regular directories, all tools execute directly. Essentially a multi-session local execution backend.

**Key principle:** Configuration is explicit. If you declare `StorageBackend: "zfs"` but ZFS is not installed, `Create()` fails with a clear error. There is no silent fallback. The operator chooses what infrastructure to use and the system validates it.

**What changes:**
- New config types: `StorageBackend` and `ContainerRuntime` enums
- `NativeSandboxConfig` adds explicit backend declarations
- `Capabilities()` becomes dynamic, derived from config
- `SandboxHostService` handles nil `ZFSManager` and nil `GVisorManager`
- `NativeSandboxCapabilities` package var removed (capabilities are per-instance)

**What does NOT change:**
- The `ExecutionEnvironment` interface (addendum 01)
- ZFS and gVisor internal packages (`internal/sandbox/zfs`, `internal/sandbox/gvisor`)
- The RPC layer

---

## 2. Configuration Types

### 2.1 Storage Backend

```go
// internal/sandbox/config.go

// StorageBackend identifies the session storage mechanism.
type StorageBackend string

const (
    // StorageBackendZFS uses ZFS datasets for session storage.
    // Requires a running ZFS pool and the ZFSManager to be non-nil.
    // Enables: snapshots, rollback, quotas, COW clones.
    StorageBackendZFS StorageBackend = "zfs"

    // StorageBackendLocalDisk uses plain directories for session storage.
    // Sessions are created as subdirectories under a configured root path.
    // No snapshots, no rollback, no quotas.
    StorageBackendLocalDisk StorageBackend = "local-disk"
)

// ContainerRuntime identifies the container runtime for Tier 2 tool execution.
type ContainerRuntime string

const (
    // ContainerRuntimeGVisor uses gVisor (runsc) for Tier 2 tool isolation.
    // Requires runsc to be installed and the GVisorManager to be non-nil.
    ContainerRuntimeGVisor ContainerRuntime = "gvisor"

    // ContainerRuntimeNone executes all tools directly on the host.
    // No container isolation. All tools are effectively Tier 1.
    ContainerRuntimeNone ContainerRuntime = "none"
)
```

### 2.2 Updated ServiceConfig

```go
// internal/sandbox/types.go -- updated ServiceConfig

type ServiceConfig struct {
    // Infrastructure backends (required)
    StorageBackend   StorageBackend   // "zfs" or "local-disk"
    ContainerRuntime ContainerRuntime // "gvisor" or "none"

    // ZFS-specific config (only used when StorageBackend == "zfs")
    PoolName               string
    BasesDataset           string
    SessionsDataset        string
    DefaultSessionQuota    int64
    SnapshotPrefix         string
    MaxSnapshotsPerSession int
    PoolSpaceWarnThreshold float64
    PoolSpaceCritThreshold float64

    // Local-disk config (only used when StorageBackend == "local-disk")
    SessionsRootDir string // e.g. "/var/lib/sandbox/sessions"

    // gVisor-specific config (only used when ContainerRuntime == "gvisor")
    DefaultResources gvisor.ResourceSpec

    // General config (used regardless of backend)
    MaxSessions         int
    ToolTimeout         time.Duration
    HealthCheckInterval time.Duration
    PauseDrainTimeout   time.Duration
    ShutdownTimeout     time.Duration
    PerToolSnapshots    bool // only effective when StorageBackend == "zfs"
}
```

### 2.3 Updated Constructor with Validation

```go
func NewSandboxHostService(cfg ServiceConfig, z zfs.ZFSManager, g gvisor.GVisorManager, logger *slog.Logger) (*SandboxHostService, error) {
    if logger == nil {
        logger = slog.Default()
    }
    cfg = mergeDefaultConfig(cfg)

    // Validate backend/manager consistency
    if cfg.StorageBackend == StorageBackendZFS && z == nil {
        return nil, fmt.Errorf("sandbox: storage_backend is %q but ZFSManager is nil", cfg.StorageBackend)
    }
    if cfg.ContainerRuntime == ContainerRuntimeGVisor && g == nil {
        return nil, fmt.Errorf("sandbox: container_runtime is %q but GVisorManager is nil", cfg.ContainerRuntime)
    }
    if cfg.StorageBackend == StorageBackendLocalDisk && cfg.SessionsRootDir == "" {
        return nil, fmt.Errorf("sandbox: storage_backend is %q but sessions_root_dir is empty", cfg.StorageBackend)
    }

    return &SandboxHostService{
        config:  cfg,
        zfs:     z,       // may be nil when StorageBackend == "local-disk"
        gvisor:  g,       // may be nil when ContainerRuntime == "none"
        logger:  logger,
        started: time.Now(),
        metrics: &serviceMetrics{},
    }, nil
}
```

Note: the constructor signature changes from returning `*SandboxHostService` to returning `(*SandboxHostService, error)`. All callers must be updated.

---

## 3. Dynamic Capabilities

### 3.1 Removing NativeSandboxCapabilities

The static `NativeSandboxCapabilities` package variable in `internal/sandbox/environment/capabilities.go` is removed. `NativeSandboxEnvironment.Capabilities()` becomes config-driven.

### 3.2 NativeSandboxEnvironment Changes

```go
// internal/sandbox/environment/native/native.go

type NativeSandboxConfig struct {
    StorageBackend   StorageBackend
    ContainerRuntime ContainerRuntime
}

type NativeSandboxEnvironment struct {
    service   api.SandboxService
    logger    *slog.Logger
    config    NativeSandboxConfig
    sessionID string
    destroyed atomic.Bool
}

func NewNativeSandboxEnvironment(
    service api.SandboxService,
    config NativeSandboxConfig,
    logger *slog.Logger,
) *NativeSandboxEnvironment {
    return &NativeSandboxEnvironment{
        service: service,
        logger:  logger,
        config:  config,
    }
}

func (e *NativeSandboxEnvironment) Capabilities() environment.Capabilities {
    return environment.Capabilities{
        Snapshots:         e.config.StorageBackend == StorageBackendZFS,
        Rollback:          e.config.StorageBackend == StorageBackendZFS,
        Pause:             true, // always supported (session state management)
        TierRouting:       e.config.ContainerRuntime == ContainerRuntimeGVisor,
        StreamingProgress: true, // always supported
    }
}
```

### 3.3 Capability-Gated Method Behavior

When capabilities are false, the corresponding methods return `ErrCapabilityNotSupported` -- matching the existing contract from addendum 01:

```go
func (e *NativeSandboxEnvironment) CreateSnapshot(ctx context.Context, name string) (*environment.SnapshotInfo, error) {
    if e.config.StorageBackend != StorageBackendZFS {
        return nil, environment.ErrCapabilityNotSupported
    }
    // ... existing RPC call to service.CreateSnapshot ...
}

func (e *NativeSandboxEnvironment) Rollback(ctx context.Context, snapshotID string) error {
    if e.config.StorageBackend != StorageBackendZFS {
        return environment.ErrCapabilityNotSupported
    }
    // ... existing RPC call to service.RollbackSession ...
}
```

---

## 4. SandboxHostService Adaptations

### 4.1 CreateSession with Local-Disk Backend

```go
func (svc *SandboxHostService) CreateSession(ctx context.Context, req CreateSessionRequest) (*SessionInfo, error) {
    // ... existing session ID generation and max-session check ...

    var mountpoint string
    var dataset string

    switch svc.config.StorageBackend {
    case StorageBackendZFS:
        if req.BaseSnapshot == "" {
            return nil, fmt.Errorf("base snapshot is required for ZFS backend")
        }
        dataset = svc.config.SessionsDataset + "/" + sessionID
        if err := svc.zfs.CloneFromSnapshot(ctx, req.BaseSnapshot, dataset); err != nil {
            svc.sessions.Delete(sessionID)
            return nil, fmt.Errorf("clone base snapshot: %w", err)
        }
        // ... quota setup ...
        mp, err := svc.zfs.GetMountpoint(ctx, dataset)
        if err != nil {
            _ = svc.zfs.DestroyDataset(ctx, dataset, zfs.DestroyOptions{Recursive: true, Force: true})
            svc.sessions.Delete(sessionID)
            return nil, fmt.Errorf("get mountpoint: %w", err)
        }
        mountpoint = mp

    case StorageBackendLocalDisk:
        mountpoint = filepath.Join(svc.config.SessionsRootDir, sessionID)
        if err := os.MkdirAll(mountpoint, 0o755); err != nil {
            svc.sessions.Delete(sessionID)
            return nil, fmt.Errorf("create session directory: %w", err)
        }
    }

    sess := &Session{
        id:         sessionID,
        state:      SessionActive,
        dataset:    dataset, // empty string for local-disk
        mountpoint: mountpoint,
        created:    time.Now(),
        labels:     copyLabels(req.Labels),
    }
    svc.sessions.Store(sessionID, sess)
    svc.metrics.sessionsCreated.Add(1)
    info := sess.Info()
    return &info, nil
}
```

### 4.2 DestroySession with Local-Disk Backend

```go
func (svc *SandboxHostService) DestroySession(ctx context.Context, sessionID string) error {
    sess, err := svc.getSession(sessionID)
    if err != nil {
        return err
    }
    // ... existing drain logic ...

    switch svc.config.StorageBackend {
    case StorageBackendZFS:
        _ = svc.zfs.DestroyDataset(ctx, sess.dataset, zfs.DestroyOptions{Recursive: true, Force: true})
    case StorageBackendLocalDisk:
        _ = os.RemoveAll(sess.mountpoint)
    }

    svc.sessions.Delete(sessionID)
    svc.metrics.sessionsDestroyed.Add(1)
    return nil
}
```

### 4.3 ExecuteTool Without gVisor

When `ContainerRuntime` is `"none"`, all tool calls route to Tier 1 execution regardless of `tools.ClassifyTool()`:

```go
func (svc *SandboxHostService) ExecuteTool(ctx context.Context, req ExecuteToolRequest) (*ExecuteToolResponse, error) {
    // ... existing session lookup and active-tools tracking ...

    tier := tools.ClassifyTool(req.ToolName)
    if svc.config.ContainerRuntime == ContainerRuntimeNone {
        tier = tools.Tier1 // everything runs directly
    }

    switch tier {
    case tools.Tier1:
        resp, err = svc.executeTier1(ctx, mountpoint, req)
    case tools.Tier2:
        resp, err = svc.executeTier2(ctx, mountpoint, req)
    }
    // ... rest unchanged ...
}
```

### 4.4 Snapshot/Rollback Without ZFS

Snapshot and rollback operations on `SandboxHostService` check the storage backend:

```go
func (svc *SandboxHostService) CreateSnapshot(ctx context.Context, sessionID string, name string) (*SnapshotResult, error) {
    if svc.config.StorageBackend != StorageBackendZFS {
        return nil, ErrSnapshotsNotAvailable
    }
    // ... existing ZFS snapshot logic ...
}

func (svc *SandboxHostService) RollbackSession(ctx context.Context, sessionID string, snapshotID string) error {
    if svc.config.StorageBackend != StorageBackendZFS {
        return ErrSnapshotsNotAvailable
    }
    // ... existing ZFS rollback logic ...
}

func (svc *SandboxHostService) TurnComplete(ctx context.Context, sessionID string) (*SnapshotResult, error) {
    if svc.config.StorageBackend != StorageBackendZFS {
        return nil, nil // no-op: no snapshots to take
    }
    // ... existing turn snapshot logic ...
}
```

New error sentinel:

```go
var ErrSnapshotsNotAvailable = errors.New("sandbox: snapshots not available (requires ZFS storage backend)")
```

### 4.5 HealthCheck Without ZFS

When running without ZFS, `HealthCheck` skips pool health queries:

```go
func (svc *SandboxHostService) HealthCheck(ctx context.Context) (*HealthStatus, error) {
    health := &HealthStatus{
        SessionCount: svc.sessionCount(),
        ActiveTools:  svc.totalActiveTools(),
        Uptime:       time.Since(svc.started),
    }

    if svc.config.StorageBackend == StorageBackendZFS {
        // ... existing ZFS pool health checks ...
    } else {
        health.Status = "healthy" // local-disk: no pool to check
    }

    return health, nil
}
```

### 4.6 Shutdown Without gVisor

The existing `Shutdown` method already handles `nil` gVisor with a nil check (`if svc.gvisor != nil`). No change needed.

---

## 5. Error Behavior Summary

| Operation | ZFS configured, ZFS unavailable | local-disk configured | gVisor configured, runsc missing | no container runtime |
|-----------|-------------------------------|----------------------|----------------------------------|---------------------|
| `NewSandboxHostService` | Error: ZFSManager is nil | OK (z=nil accepted) | Error: GVisorManager is nil | OK (g=nil accepted) |
| `CreateSession` | ZFS clone fails (clear error) | `os.MkdirAll` on session dir | N/A | N/A |
| `CreateSnapshot` | ZFS command fails | `ErrSnapshotsNotAvailable` | N/A | N/A |
| `Rollback` | ZFS command fails | `ErrSnapshotsNotAvailable` | N/A | N/A |
| `ExecuteTool` (Tier 2) | N/A | N/A | gVisor Run fails | Downgrades to Tier 1 |
| `PerToolSnapshots` | Works | Silently skipped (no ZFS) | N/A | N/A |
| `HealthCheck` | ZFS pool errors reported | Returns "healthy" (no pool) | N/A | N/A |

---

## 6. NativeSandboxEnvironment Config Propagation

The `NativeSandboxConfig` on the client side mirrors what the server is configured with. The client needs to know the backend config to return accurate `Capabilities()` without querying the server. This is set at construction time by the orchestrator, which knows how the sandbox-host was deployed:

```go
// Orchestrator knows the sandbox-host is running with ZFS + no gVisor
env := native.NewNativeSandboxEnvironment(
    sandboxClient,
    native.NativeSandboxConfig{
        StorageBackend:   native.StorageBackendZFS,
        ContainerRuntime: native.ContainerRuntimeNone,
    },
    logger,
)

caps := env.Capabilities()
// caps.Snapshots == true, caps.TierRouting == false
```

The config could alternatively be fetched from the sandbox-host via an RPC `GetServerConfig` call, but explicit configuration is simpler and avoids an extra round-trip during initialization. If the client config does not match the server config, operations will fail with clear RPC errors (e.g., calling CreateSnapshot against a local-disk server returns an error from the server side).

---

## 7. Implementation Notes

1. **Constructor signature change:** `NewSandboxHostService` returns `(*SandboxHostService, error)` instead of `*SandboxHostService`. All callers (tests, `cmd/sandbox-host`, RPC server setup) must be updated.

2. **`NativeSandboxCapabilities` removal:** Delete the package-level var from `capabilities.go`. The compliance test harness and property tests that use it should pass a config-derived `Capabilities` instead.

3. **`PerToolSnapshots` on local-disk:** When `StorageBackend` is `local-disk`, the `PerToolSnapshots` config field is ignored (no snapshots to take). No error, just a no-op. Log a warning at startup if `PerToolSnapshots` is true with a non-ZFS backend.

4. **`BaseSnapshot` in CreateSession:** When using `local-disk`, the `BaseSnapshot` field in `CreateSessionRequest` is ignored (there's nothing to clone from). The session directory starts empty. If a pre-populated workspace is needed, the caller should copy files in after `Create()`.

5. **Config type placement:** `StorageBackend` and `ContainerRuntime` types go in `internal/sandbox/config.go`. The `NativeSandboxConfig` type goes in `internal/sandbox/environment/native/config.go` and re-exports the backend/runtime constants for convenience.
