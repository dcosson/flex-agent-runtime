# Review: 17-external-testing (reviewer-sea)

- Source doc: `docs/plans/17-external-testing.md`
- Reviewed commit: ff422ab
- Reviewer: reviewer-sea
- Scope: Section 12 only (Stubserver-Based E2E Testing). Sections 1-11 were previously reviewed and signed off.

## Findings

### P2 - Standalone binary sketch won't compile: Server does not implement http.Handler

**Problem**
§12.3 (lines 1158-1180) shows the standalone `cmd/stubserver/main.go` calling `http.ListenAndServe(*addr, srv)` where `srv` is a `*stubserver.Server`. However, `stubserver.Server` does not implement `http.Handler` — it has no `ServeHTTP` method. The `Server` struct wraps an `httptest.Server` internally (started immediately by `New()`) and stores the handler in an unexported `handler` field (`stubserver.go:87`). This code would fail to compile.

**Required fix**
Either:
(a) Add a `Handler() http.Handler` method to `Server` that returns the capture-wrapping handler without starting an `httptest.Server`, or
(b) Add a `NewHandler(opts ...Option) http.Handler` constructor for standalone use (returns just the handler, caller owns the listener), or
(c) Restructure the standalone binary to use `New()` with a `WithListener` option that replaces `httptest.NewServer` with a custom `net.Listener`.

Option (b) is cleanest — it separates test-use (`New()` with auto-start) from production-use (`NewHandler()` with explicit `http.ListenAndServe`). Update the sketch in §12.3 to match.

---

### P2 - Oracle cross-verification workflow assumes SSE parsing capability the oracle doesn't have

**Problem**
§12.5 (lines 1264-1273) describes a workflow where "the same raw SSE" is fed through both the Go provider's SSE parser and the TypeScript oracle to compare parsed output. However, `testdata/oracle/harness.mjs` only supports three commands: `transformMessages`, `calculateCost`, and `isContextOverflow` — none of which parse raw SSE streams. The `@mariozechner/pi-ai` library may have SSE parsing internally, but the oracle wrapper has no `parseSSE` command, so the described workflow cannot be executed today.

**Required fix**
Either:
(a) Add a concrete specification for a new oracle command (e.g., `parseSSEStream`) — what it takes as input (raw SSE text), what it returns (parsed message events, tool calls, usage, stop reason), and which `pi-ai` API it calls internally, or
(b) Scope §12.5 to what the oracle can already do: verify that hand-crafted fixtures produce correct `transformMessages` / `isContextOverflow` output when the Go-parsed results are fed through it, rather than claiming raw SSE parsing. This is still valuable — it verifies fixture content fidelity at the message level, even if not at the SSE transport level.

---

### P2 - Completion Signoff says "Complete" but Section 12 adds unimplemented scope

**Problem**
The doc ends with a `## Completion Signoff` section (lines 1309-1352) with `Status: Complete`. But Section 12 describes entirely new, unimplemented work (standalone binary, Dockerfile.stubserver, compose service, fixture files, fault injection test variants). The signoff's deliverables table and acceptance criteria only cover Sections 1-11. A reader seeing "Status: Complete" would incorrectly conclude the entire plan is implemented.

**Required fix**
Either:
(a) Update the signoff to `Status: Partial` with Section 12 listed as an outstanding gap, or
(b) Add a clear scope note to the signoff header stating it covers Sections 1-11 only, and that Section 12 is a planned extension not yet implemented, or
(c) Move Section 12 to a separate addendum doc (e.g., `17-external-testing.add01.md`) with its own lifecycle. This keeps the signoff clean and the scope boundaries clear.

---

### P3 - Health endpoint not specified but referenced in Docker config

**Problem**
§12.3's Dockerfile (line 1199) specifies `HEALTHCHECK CMD wget -qO- http://localhost:9090/health || exit 1` and the compose healthcheck (lines 1216-1220) uses the same `/health` endpoint. Neither the stubserver library nor the standalone binary sketch defines a `/health` handler. The binary would start but the healthcheck would fail, causing Docker to mark the container as unhealthy.

**Suggested fix**
Add a `/health` route to the standalone binary sketch that returns 200 OK (e.g., via `http.HandleFunc("/health", ...)` wrapping the stubserver handler in a mux), or document that the stubserver library should gain a built-in `/health` endpoint.

---

### P3 - Environment variable vs flag inconsistency for fixture directory

**Problem**
The compose service (line 1214) sets `STUBSERVER_FIXTURE_DIR: "/fixtures"` as an environment variable. But the standalone binary sketch (line 1160) only reads `-fixtures` as a flag. The fixture directory is specified in two places using two different mechanisms, and the binary doesn't check the env var.

**Suggested fix**
Either use the flag only (change compose to pass `-fixtures=/fixtures` via `command:`) or have the binary check the env var as a fallback when the flag isn't set (standard pattern: `flag.String("fixtures", os.Getenv("STUBSERVER_FIXTURE_DIR"), ...)`). Pick one and be consistent.

---

## Summary

5 findings: 0 P0, 0 P1, 3 P2, 2 P3

**Verdict**: Approved with revisions

Section 12 is a well-motivated addition that fills a real gap — ScriptedProvider bypasses the entire HTTP/SSE/JSON client stack, and the stubserver is already built and proven in provider-level harness tests. The tiered integration strategy (in-process httptest for Tier 1, Docker service for Tier 2/3) is sound and avoids adding Docker as a Tier 1 dependency. The fault injection mapping to existing stubserver capabilities is accurate and complete. The coverage comparison table (§12.4) is precise and useful.

The P2s are all addressable with minor plan text updates: fix the compile error in the standalone binary sketch, scope the oracle workflow to what's actually available, and clarify the signoff scope. None require rethinking the approach.
