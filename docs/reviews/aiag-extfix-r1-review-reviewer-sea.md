# Code Review: external test make targets fix (R1, reviewer-sea)

- Bead: N/A (follow-up fix)
- Commit range: e920d63..a5a6fe6
- Plan doc: docs/plans/17-external-testing.md
- Reviewer: reviewer-sea
- Review commit: a5a6fe6

## Findings

### P2 - test-external-tier3 wraps native tests in Docker compose

**Location:** `Makefile:254-262`

**Problem**
Tier 3 tests are designed to run on a native host with real ZFS and gVisor — the package doc says "require a dedicated Linux machine with real ZFS, gVisor (runsc)" and tests call `common.RequireZFS(t)` which checks `exec.LookPath("zfs")` and `lsmod` on the host. The CI workflow (`e2e.yml:75-85`) runs tier3 directly on a `[self-hosted, linux, zfs]` runner without Docker.

The Makefile target starts `docker compose --profile full` then runs native-tagged tests. This creates two problems: (1) the sandbox-host container conflicts with a natively-running sandbox-host, and (2) the native prerequisite checks (`RequireZFS`, `RequireGVisor`) test the Docker host, not the container, which is the correct behavior for tier3 but the Docker compose lifecycle is unnecessary overhead.

Currently harmless since all tier3 tests `t.Skip("TODO")`, but the wiring is architecturally wrong for when tests are implemented.

**Suggested fix**
Remove the Docker compose wrapping from `test-external-tier3`. It should be:
```makefile
test-external-tier3:
	$(GO) test $(GO_TEST_RACE) -v -tags=native -timeout=20m ./tests/external/tier3/...
```
This matches the CI workflow and the tier3 design. If tier3 tests need the stubserver, they should start it in-process (like tier1) or expect it to be running externally.

---

### P3 - test-external-tier2 only supports --profile full

**Location:** `Makefile:244-252`

**Problem**
The target hardcodes `--profile full` which requires ZFS and gVisor support in the Docker host. The CI workflow has separate jobs for `--profile minimal` and `--profile full`. A developer on macOS Docker Desktop cannot run `make test-external-tier2` since the full profile requires ZFS.

**Suggested fix**
Default to `--profile minimal` and allow override:
```makefile
EXTERNAL_COMPOSE_PROFILE ?= minimal
```
Or provide two targets: `test-external-tier2-minimal` and `test-external-tier2-full`.

---

## Summary

2 findings: 0 P0, 0 P1, 1 P2, 1 P3

**Verdict**: Approved with revisions

The Dockerfile.sandbox-host Go version bump to 1.24 is correct. The `EXTERNAL_COMPOSE_FILE` variable and `trap`-based teardown pattern in the Makefile are well-designed. The P2 (tier3 Docker wrapping) should be fixed since it contradicts the tier3 architecture.
