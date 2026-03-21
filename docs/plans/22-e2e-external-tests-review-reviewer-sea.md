# Review: 22-e2e-external-tests (reviewer-sea)

- Source doc: `docs/plans/22-e2e-external-tests.md`
- Reviewed commit: 88a2940
- Reviewer: reviewer-sea

## Findings

### P1 - FlexAgentClient missing RPC methods used by tests

**Problem**
The `FlexAgentClient` class (§2.3, lines 103-166) defines 9 methods but the `AgentService` interface (Plan 18 §3) defines 11 RPCs. Missing from the client:
- `Continue` — server-streaming RPC, essential for multi-turn agent workflows
- `ListSessions` — used by the `cleanup_leaked_sessions` fixture at line 294 (`local_orchestrator.list_sessions()`) but not defined in the client
- `ResumeSession` — needed for pause/resume tests (DM-L4, ZFS-D4)
- `CreateSnapshot` — used explicitly in the ZFS durability sequence diagram at line 518 (`CreateSnapshot(session_a, "checkpoint-1")`) but not in the client

The cleanup fixture will crash at runtime because `list_sessions()` doesn't exist. The ZFS durability tests can't work without `CreateSnapshot` and `ResumeSession`.

**Required fix**
Add all missing methods to `FlexAgentClient`. `Continue` needs streaming support like `subscribe_events`. `ListSessions`, `ResumeSession`, and `CreateSnapshot` are unary.

---

### P1 - SendMessage is server-streaming but client treats it as unary

**Problem**
The `send_message` method (§2.3, line 123-125) calls `resp.json()` — the unary pattern. But Plan 18 §3 line 136 says `SendMessage` returns `EventReceiver` (server stream), and §2.3 line 168 acknowledges that SendMessage uses streaming. The current implementation would fail because the response is newline-delimited JSON, not a single JSON object.

The tests at §4.1 (DM-L1: "send message -> get events") imply SendMessage produces events, but the client can't read them with `resp.json()`.

**Required fix**
`send_message` should return a streaming iterator (like `subscribe_events`), or the plan should clarify the intended pattern — perhaps `send_message` starts the turn and callers use `subscribe_events` to read events. Either way, the client code and test patterns need to be consistent.

---

### P2 - Port conflicts between module-scoped fixtures

**Problem**
The `local_orchestrator` fixture (§12.1, line 869) starts sandbox-host on `:8082` and orchestrator on `:8080`. The `fleet_orchestrator` fixture (line 886) also uses `:8080`, `:8082`, `:8083`. Both are module-scoped, so if two test modules using different fixtures run in the same pytest session, the second module will fail with port-already-in-use.

§3.5 defines a per-tier port allocation scheme (E2E-Fast uses 18080/18082, etc.) but the fixtures don't use these ports — they hardcode the default ports. The stubserver fixture (line 934) also hardcodes port 9090.

**Required fix**
Update fixtures to use the tier-specific port scheme from §3.5, or use dynamic port allocation. At minimum, each fixture should use unique ports that don't conflict with other fixtures.

---

### P2 - Open questions should be resolved before implementation

**Problem**
§16 lists 5 open questions that affect the core design:
1. **ConnectRPC streaming protocol** — directly affects how `send_message`, `continue_`, and `subscribe_events` work. This must be decided before the client can be implemented.
2. **Stubserver binary** — affects fixture design and CI workflow.
3. **Port allocation** — already causing the P2 above.
4. **EC2 mock** — affects E2E-Infra tier scope.
5. **Multi-runtime binaries** — affects whether MR tests are nightly-only.

Questions 1 and 3 are blocking for Phase 1 implementation.

**Required fix**
Resolve at least questions 1, 2, and 3 before moving to implementation beads. Add a "Decisions" subsection with chosen approaches. Questions 4 and 5 can remain open with a recommended default.

---

### P2 - Stubserver fixture uses private `_start` with incorrect args

**Problem**
The `stubserver_orchestrator` fixture (§12.2, line 933) calls `pm._start(["cmd/stubserver"], ...)` which would try to run `flexagent cmd/stubserver` — that's not a valid command. The stubserver is a separate binary built from `cmd/stubserver/main.go`. The fixture needs to either:
- Build and locate the stubserver binary separately, or
- Use `go run ./cmd/stubserver` as the command

Also, `_start` is a private method being called from outside the class, suggesting the `ProcessManager` API is incomplete.

**Required fix**
Add a `start_stubserver` method to `ProcessManager` that handles the stubserver binary path. The CI workflow should build both binaries (`flexagent` and `stubserver`).

---

### P3 - No remote ExecutionEnvironment provider coverage

**Problem**
The plan tests Native/ZFS sandbox paths but doesn't cover any remote `ExecutionEnvironment` providers (E2B, Daytona, Fly). These are now fully implemented (Plan 11.add01 complete). While the orchestrator delegates to the same interface regardless of provider, E2E tests that verify at least one cloud provider path would catch wiring issues not visible with Native alone (e.g., provider selection, environment factory, credential routing).

**Required fix**
Consider adding an optional test module `test_remote_providers.py` that exercises E2B or Fly in nightly runs (requires provider credentials). Not blocking.

---

### P3 - Test safety §3.3 cleanup_leaked_sessions calls undefined list_sessions

**Problem**
As noted in P1 above, but even beyond the missing client method — the test calls `local_orchestrator.list_sessions()` but `local_orchestrator` is a `FlexAgentClient`, not the orchestrator itself. The `ListSessions` RPC would need to return sessions for that orchestrator instance. This should work via the RPC but the fixture needs to handle the case where the orchestrator process has already been stopped (in module teardown, the orchestrator may be gone before the session sweep runs).

**Required fix**
Ensure the session sweep runs before process teardown (fixture ordering), and handle connection errors in the sweep.

---

## Summary

7 findings: 0 P0, 2 P1, 3 P2, 2 P3

**Verdict**: Not approved — P1s must be fixed

The plan is well-structured with excellent test safety design (§3 is thorough — multi-layer cleanup, timeouts at every level, idempotent runs). The architecture is sound: Python driver over ConnectRPC JSON protocol, pytest fixtures managing process lifecycle, credential management with graceful skipping. The CI tier design (Fast/Standard/Provider/Infra) with appropriate gating is good.

However, the two P1s are blocking:
1. **Incomplete client** — multiple RPC methods used by tests are missing from the client specification, including methods the cleanup fixture depends on
2. **Streaming mismatch** — the core `send_message` method uses the wrong protocol (unary instead of streaming), which means the primary test pattern won't work as written
