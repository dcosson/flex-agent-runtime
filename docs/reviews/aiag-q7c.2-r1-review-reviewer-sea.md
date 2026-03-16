# Code Review: aiag-q7c.2 (R1, reviewer-sea)

- Bead: aiag-q7c.2
- Commit range: 6873181..dea037b
- Plan doc: docs/plans/17-external-testing.md
- Reviewer: reviewer-sea
- Review commit: dea037b

## Findings

### P2 - CI Tier 2 pipeline won't run Go tests against sandbox-host containers

**Location:** `.github/workflows/e2e.yml:26-31`, `tests/external/docker/docker-compose.e2e.yaml`

**Problem**
Plan §4.2 specifies a `test-runner` service in docker-compose that builds the Go test binary and runs it against the sandbox-host container, plus a `Dockerfile.test-runner` to build the test runner image. The implementation omits both.

The CI workflow's Tier 2 steps run only `docker compose up --build --abort-on-container-exit`, which starts the sandbox-host container and waits for it to exit. There is no step that runs `go test -tags=docker ./tests/external/tier2/...` against the running container. When the Tier 2 test stubs are eventually implemented, the CI pipeline will start the sandbox-host but never execute tests against it.

This is not urgent since all Tier 2 tests are stubs today, but the wiring needs to exist before the stubs are replaced with real tests.

**Suggested fix**
Either: (a) add a `test-runner` service to docker-compose and a `Dockerfile.test-runner` per the plan, so `docker compose up` also runs the test binary, or (b) change the CI workflow to start the containers in detached mode, then run `go test -tags=docker` from the CI runner with `SANDBOX_HOST_URL` pointing to the container's port.

---

### P3 - docker-compose services missing SANDBOX_AUTH_TOKEN

**Location:** `tests/external/docker/docker-compose.e2e.yaml`

**Problem**
Plan §4.2 specifies `SANDBOX_AUTH_TOKEN: "e2e-test-token"` in the environment for all sandbox-host services. The implementation omits this. While sandbox-host may default to no auth, the plan includes it for parity with how production deployments would be configured. When Tier 2 tests are implemented, they'll need to authenticate if the sandbox-host requires it.

**Suggested fix**
Add `SANDBOX_AUTH_TOKEN: "e2e-test-token"` to the environment of all three sandbox-host services.

---

### P3 - docker-compose uses deprecated version field

**Location:** `tests/external/docker/docker-compose.e2e.yaml:1`

**Problem**
The compose file uses `version: "3.9"`. Docker Compose v2 ignores this field and logs a deprecation warning. The plan shows "3.8" which has the same issue.

**Suggested fix**
Remove the `version` line entirely — Docker Compose v2 infers the format from the file structure.

---

## Summary

3 findings: 0 P0, 0 P1, 1 P2, 2 P3

**Verdict**: Approved with revisions

The implementation is well-structured and thorough. Tier 1 delivers 12 substantive, running tests covering multi-turn agent flows, bash execution, code interpreter, steering, follow-ups, abort, error recovery, concurrent subscribers, and cross-mode parity (local vs remote). Common helpers (prereq checks, backend configs, parity comparison, report types) are clean and reusable. Tier 2 and Tier 3 are properly scaffolded as build-tag-gated stubs with clear TODOs — this is appropriate since the sandbox-host binary isn't available yet. The Docker multi-stage Dockerfile with 3 targets (minimal/gvisor/full), docker-compose with profiles, ZFS init script, and CI workflow are all well-designed.

The P2 (missing test-runner wiring) should be addressed before the Tier 2 stubs are replaced with real implementations. The P3s are minor.
