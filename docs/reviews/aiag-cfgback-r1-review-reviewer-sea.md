# Code Review: sandbox-host configurable backends (R1, reviewer-sea)

- Bead: N/A (follow-up fix)
- Commit range: e84fa40..38f895f
- Plan doc: docs/plans/11-sandbox-host-service.add02.md (configurable backends)
- Reviewer: reviewer-sea
- Review commit: 38f895f

## Scope

8 files changed (198 insertions, 89 deletions):
- `cmd/sandbox-host/config.go` — Added `StorageBackend`, `ContainerRuntime`, `SessionsRootDir` fields; renamed env var prefix `SANDBOX_HOST_` → `SANDBOX_`; conditional validation by backend/runtime
- `cmd/sandbox-host/config_test.go` — Updated env var names; added `TestConfigValidateLocalDiskNone` for local-disk+none path
- `cmd/sandbox-host/main.go` — Conditional ZFS/gVisor manager initialization; passes backend/runtime/root-dir to `ServiceConfig`; added `/health` route
- `tests/external/tier2/docker_test.go` — `configuredConfigs()` filters to active backend; early skip for ZFS/gVisor-specific tests
- `tests/external/tier3/native_test.go` — Package doc updated to compose-backed
- `.github/workflows/e2e.yml` — Tier3 CI composes up `--profile full`, sets env, composes down in `always`
- `Makefile` — Tier3 make target mirrors CI compose workflow
- `tests/external/docker/docker-compose.e2e.yaml` — Stubserver port remap 19090:9090

## Findings

No findings. The implementation is clean:

1. **Config struct & validation** — `StorageBackend`/`ContainerRuntime` are typed as `sandbox.StorageBackend`/`sandbox.ContainerRuntime` with exhaustive switch validation. Conditional field requirements (pool/bases/sessions for ZFS, sessions-root-dir for local-disk, bundle-base-dir for gVisor) are correct. Invalid values fall through to the `default` case with a clear error.

2. **Env var rename** — Clean rename from `SANDBOX_HOST_*` to `SANDBOX_*` prefix, matching the compose env vars (`SANDBOX_STORAGE_BACKEND`, `SANDBOX_CONTAINER_RUNTIME`, etc.). Tests updated consistently.

3. **Conditional manager init** — `main.go` only creates ZFS manager when `StorageBackend == StorageBackendZFS` and gVisor manager when `ContainerRuntime == ContainerRuntimeGVisor`. Nil managers are correctly handled downstream — `NewSandboxHostService` validates manager presence against config.

4. **ServiceConfig population** — Uses `DefaultServiceConfig()` as base, then overrides `StorageBackend`, `ContainerRuntime`, and conditionally sets `SessionsRootDir`. This preserves defaults for unset fields.

5. **Health endpoint** — Simple `/health` returning `200 ok\n` via `http.ServeMux`, properly wrapping the RPC transport at `/`. Correct for Docker compose healthchecks.

6. **Test adjustments** — `configuredConfigs()` correctly filters `StandardConfigs()` to only the active backend/runtime combo. ZFS and gVisor tests add early skips when the active config doesn't support those features.

7. **CI & Makefile** — Tier3 CI and make target consistently use compose full profile with matching env vars. `always` step for teardown.

## Summary

0 findings

**Verdict**: Approved
