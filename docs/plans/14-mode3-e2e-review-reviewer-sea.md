# 14: Mode 3 E2E — Review Findings (reviewer-sea, R1)

**Plan:** [14-mode3-e2e.md](./14-mode3-e2e.md)
**Test Harness:** [14-mode3-e2e-test-harness.md](./14-mode3-e2e-test-harness.md)
**Reviewer:** reviewer-sea
**Round:** 1
**Date:** 2026-03-12

---

## Summary

The plan provides good coverage of the Mode 3 E2E scenarios and has a well-structured test matrix. The acceptance criteria are clear and testable. Main gaps are around the fake sandbox host specification (critical for the primary PR lane), missing network-level fault injection, and an unclear relationship between the Mode 3 E2E plan and the RPC layer's event streaming behavior.

## Findings

### [F1] Fake sandbox host implementation is unspecified — P1

**Section:** Plan §7.2 (Environment Modes)
**Issue:** The primary PR lane (`mode3-fake-host`) uses an in-process fake sandbox service, but the plan doesn't specify what the fake implements. Key questions: Does the fake support real snapshot semantics (maintaining filesystem state per snapshot)? Does it support rollback (reverting to earlier state)? Does it support concurrent sessions? If the fake is too simple (e.g., just echoing back fixed responses), the deterministic tests won't catch real integration issues like snapshot ordering, rollback correctness, or tier routing. If too complex, it duplicates the sandbox host service and becomes its own maintenance burden.
**Recommendation:** Specify the fake's behavior contract explicitly. At minimum, the fake should: (a) maintain in-memory filesystem state per session, (b) support snapshot-as-copy and rollback-as-restore, (c) track session state machine transitions, (d) classify tools into tiers correctly. This is essentially a `MemorySandboxService` implementation of the same interface as `SandboxHostService`. Define it in the plan and share between Mode 3 E2E (plan 14) and RPC layer tests (plan 13).

### [F2] No network-level fault injection — P2

**Section:** Test Harness §2 (Fault Injection)
**Issue:** The fault injection tests cover RPC-level failures (F1: transient unavailable/timeout, F2: host restart) but don't cover network-level faults: TCP connection resets mid-transfer, DNS resolution failures, TLS handshake failures, partial response delivery (connection drops after headers but before body). For a distributed E2E test suite validating Mode 3 over real network boundaries, network-level faults are how real outages manifest.
**Recommendation:** Add fault injection scenarios for: (a) connection reset during ExecuteTool response transfer, (b) TLS certificate rotation/expiry during active session, (c) network partition between tool request send and response receive. These can use a TCP proxy (like `toxiproxy`) in the test harness.

### [F3] Event streaming E2E test lacks specification of expected event sequence — P2

**Section:** Plan §4.6 (Event Stream Remote Visibility), Test Harness P5
**Issue:** Scenario 4.6 says "Assert key milestones and order consistency per stream" and P5 says events correlate 1:1 with ExecuteTool RPC spans. But neither defines what the expected event sequence is for a concrete scenario. For example, for a happy-path multi-turn run: is it `session_created → tool_started → tool_completed → turn_completed → tool_started → tool_completed → turn_completed → session_completed`? Without a concrete expected sequence, the test can't make meaningful assertions.
**Recommendation:** Define the canonical event sequence for each scenario (at minimum the happy path). Reference the event types from plan 05 (agent) and specify which events are guaranteed to appear and in what order. This also forces alignment with the RPC layer (plan 13) on event stream semantics.

### [F4] Mode 1 vs Mode 3 oracle comparison scope is unclear — P2

**Section:** Test Harness §3, O1
**Issue:** Oracle test O1 compares Mode 1 (local) and Mode 3 (remote) outcomes for "semantic parity." But Mode 1 uses LocalBackend with no snapshots, while Mode 3 uses SandboxBackend with snapshots and ZFS-backed filesystem. The two modes may legitimately differ in: timing-dependent behavior, filesystem metadata (permissions, timestamps), snapshot-related metadata in tool responses. Without specifying what constitutes "semantic equivalence" vs "expected divergence," the oracle will produce false positives or require constant exception tracking.
**Recommendation:** Define semantic equivalence precisely: file content changes must match, tool output content must match (ignoring timing/path differences), exit codes must match. Allow-list expected divergences: snapshot_id presence in Mode 3 responses, filesystem path differences, timing metadata.

### [F5] Cross-reference to plan 08 deterministic fixtures is missing — P3

**Section:** Plan §7.1 (Deterministic First)
**Issue:** The plan says "Default PR suite uses deterministic provider traces and reproducible fixture repos" but doesn't reference how these are defined. Plan 08 (agent-tools-e2e) presumably defines the deterministic provider/fixture infrastructure, but there's no explicit cross-reference. An implementor won't know what interfaces to use or what fixtures are available.
**Recommendation:** Add an explicit cross-reference: "Deterministic provider traces and fixture repos are defined in plan 08 (agent-tools-e2e). Mode 3 tests reuse the same fixture infrastructure, adding remote session setup/teardown around the existing test scenarios."

### [F6] Benchmark B3 rollback target unclear for cross-system comparison — P3

**Section:** Test Harness §5, B3
**Issue:** Benchmark B3 targets "rollback-to-ready p95 <= 500ms for small fixture datasets" but "small fixture datasets" is not defined. Is it 10MB? 100MB? 1GB? The benchmark target is meaningless without a defined dataset size, since ZFS rollback time scales with the amount of data changed (not dataset size, but snapshot diff size).
**Recommendation:** Define "small fixture dataset" precisely: e.g., "base snapshot ~50MB, with ~5MB of changes between snapshots." Also specify what "ready" means — is it just the ZFS rollback, or does it include session state reset and re-availability for tool calls?

---

## Statistics

- Total findings: 6
- P0 (blocking): 0
- P1 (significant): 1
- P2 (moderate): 3
- P3 (minor): 2
