# 09: Terminal Multiplexer Port — Review Findings (coder-2-sea, R1)

**Plan:** [09-h2-termmux-port.md](./09-h2-termmux-port.md)
**Test Harness:** None (missing — see F1)
**Reviewer:** coder-2-sea
**Round:** 1
**Date:** 2026-03-11

---

## Summary

The plan covers a substantial amount of complex functionality (PTY management, three-source event handling, state machine, panic recovery, multi-client attach/detach) but has a critical gap in test coverage planning and several interface naming/ownership conflicts with existing plans that need resolution before implementation begins.

## Findings

### [F1] Missing Test Harness Companion Doc — P0

**Section:** Section 11 (Testing Strategy) / General
**Issue:** Every other plan in this project has a companion test harness document (e.g., `05-agent-test-harness.md`, `07-code-interpreter-test-harness.md`). This plan has none. The testing section (Section 11) is abbreviated compared to other plans and does not provide sufficient depth for a port of this complexity. The termmux package involves PTY lifecycle management, a three-source event multiplexer, an agent state machine with panic recovery, and multi-client attach/detach — all of which have subtle failure modes that demand rigorous testing.
**Recommendation:** Write a full companion test harness doc (`09-h2-termmux-port-test-harness.md`) covering at minimum: property-based tests for the state machine (valid transitions, invariant preservation), fault injection for PTY failures and unexpected process exits, deterministic simulation of the event multiplexer (reproducible event ordering), benchmarks for event throughput and attach/detach latency, stress tests for multi-client scenarios, and security tests for PTY isolation. This is a blocking issue — implementation should not begin without the test harness doc.

### [F2] AgentDriver Interface Name Collision with Plan 05-agent — P1

**Section:** Section 3 (internal/termmux/driver)
**Issue:** Plan 05-agent defines an `AgentDriver` interface for `NativeDriver`/`ClaudeCodeDriver`/`CodexDriver` at the agent abstraction level. This plan defines a *different* `AgentDriver` interface in `internal/termmux/driver` that serves a related but distinct purpose (terminal multiplexer driver handling vs. agent-level driver abstraction). Having two interfaces with the same name in closely related packages will cause confusion during implementation and code review, and makes it unclear which `AgentDriver` is being referenced in cross-cutting discussions.
**Recommendation:** Rename the termmux driver interface to something like `CLIDriverAdapter` or `TermmuxDriverHandler` to clearly distinguish it from the agent-level `AgentDriver` defined in plan 05. Update all references within this plan accordingly.

### [F3] Event Type Definitions Duplicated Between Agent and Termmux — P1

**Section:** Section 3.5 (AgentEventType constants)
**Issue:** Section 3.5 defines `AgentEventType` constants (`EventSessionStarted`, `EventToolStarted`, etc.), but plan 05-agent also defines `AgentEvent` types. It is unclear which package owns the canonical event type definitions. The architecture doc (`00-architecture.md`) specifies that termmux imports agent (for `AgentEvent` types), which implies termmux should *use* agent's event types, not define its own. If termmux defines its own event types, an explicit mapping layer between termmux events and agent-level events must be specified. Currently, neither the mapping nor the ownership is clearly stated.
**Recommendation:** Clarify ownership. If the architecture doc's import direction is authoritative, remove the event type definitions from this plan and reference the ones from plan 05-agent. If termmux needs its own internal event representation, explicitly define the mapping functions and document why a separate type is needed.

### [F4] OTEL Server Lifecycle Management Unspecified — P2

**Section:** Section 5.1 (Per-session OTEL HTTP server)
**Issue:** The plan states a "per-session HTTP server on 127.0.0.1:0 (random port)" for OTEL collection but does not specify: (1) how and when the server is shut down when the session ends (graceful shutdown with timeout? immediate close?), (2) error handling if `Listen` fails for any reason, and (3) the mechanism by which the OTEL endpoint URL propagates from the server to the child process. The plan mentions env var injection but does not show this in the `BuildCommandEnvVars` specification.
**Recommendation:** Add lifecycle details: server should be shut down via context cancellation tied to session teardown with a drain timeout. Explicitly add the OTEL endpoint env var (e.g., `OTEL_EXPORTER_OTLP_ENDPOINT`) to the `BuildCommandEnvVars` interface or document where this injection occurs.

### [F5] Session Log Tailer Polling Interval Hardcoded — P2

**Section:** Section 5.3 (Session log tailer)
**Issue:** The plan specifies "Polls at 500ms intervals" with no configurability. A hardcoded interval is problematic for testing (where faster feedback loops are needed for CI speed) and for production tuning (where lower polling overhead may be desired for resource-constrained environments).
**Recommendation:** Make the polling interval configurable via the session or termmux configuration, with 500ms as the default. At minimum, document the rationale for choosing 500ms (e.g., tradeoff between latency and CPU overhead) and why it should not be configurable if that is the intentional decision.

### [F6] Codex Session Log Conversion Entirely Unspecified — P2

**Section:** Section 8.3 (Codex driver)
**Issue:** The plan states that Codex session log conversion "needs investigation." While this is acceptable for an early draft, it leaves the Codex driver implementation impossible to scope or estimate. If the Codex log format is unknown, it is unclear whether the driver can be implemented within the same timeline as the Claude Code and native drivers, or whether it requires a separate investigation phase.
**Recommendation:** Either (a) investigate and specify the Codex log format and conversion before finalizing this plan, or (b) explicitly mark Codex session log conversion as out of scope for V1 and create a follow-up task/bead for it. The latter is preferred if investigation would delay the rest of the port.

### [F7] Config Directory Manager Lacks Concrete Types — P3

**Section:** Section 7 (Config directory management)
**Issue:** This section explains the design philosophy well but is less actionable than other sections of the plan. It lacks a struct definition for the config directory manager, does not specify what makes a path "stable" (hash-based? deterministic naming? symlinked?), and does not show Go types or interfaces. Compare with how Sections 3-5 provide concrete Go type definitions and method signatures.
**Recommendation:** Add a `ConfigDirManager` struct definition with its key methods (`EnsureDir`, `StablePath`, `Cleanup`, etc.) and document the stable path scheme (e.g., `<base>/<session-id>/` or `<base>/<hash>/`).

### [F8] No Acceptance Criteria Section — P2

**Section:** General (between Section 11 and migration notes)
**Issue:** Every other plan in this project includes numbered acceptance criteria that define the "done" state for the implementation work. This plan jumps from the testing strategy section directly to migration notes without specifying acceptance criteria. Without these, it is ambiguous when the port is considered complete, which makes review and task closure subjective.
**Recommendation:** Add a numbered acceptance criteria section consistent with the format used in other plans (e.g., plans 05 through 08). Criteria should cover functional completeness (all drivers ported), test coverage thresholds, benchmark targets, and compatibility verification with the existing h2 implementation.

### [F9] Plan Number Collision: 09-sandbox-zfs and 09-h2-termmux-port — P3

**Section:** General (plan indexing)
**Issue:** Both `09-sandbox-zfs.md` and `09-h2-termmux-port.md` use the prefix `09`. This is inconsistent with the rest of the plan index where each number is unique. The collision makes it confusing to reference "plan 09" in discussions and review docs.
**Recommendation:** Renumber this plan to the next available number (e.g., `12-h2-termmux-port.md` — note that 12 appears to be unused) or document in `00-plan-index.md` why the collision is acceptable. Update all cross-references accordingly.

---

## Statistics

- Total findings: 9
- P0 (blocking): 1
- P1 (significant): 2
- P2 (moderate): 4
- P3 (minor): 2
