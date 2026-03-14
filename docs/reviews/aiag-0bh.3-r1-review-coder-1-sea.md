# Code Review: aiag-0bh.3 (R1, coder-1-sea)

- Bead: aiag-0bh.3
- Commit range: 8989834^..8989834
- Plan doc: docs/plans/09-h2-termmux-port.add01.md, docs/plans/09-h2-termmux-port-test-harness.md
- Reviewer: coder-1-sea
- Review commit: 898983453f92e21b8292467cbcf5ce4991a2be73

## Findings

### P2 - Duplicate terminal subscriber IDs leak old subscriptions

**Location:** `internal/termmux/terminal_subscription.go:122`

**Problem**
`Subscribe()` overwrites an existing entry in `ts.subs` for the same subscriber ID without closing the previous subscription's `Done` channel first. If a client reconnects/re-attaches with the same ID, the prior subscription is orphaned and never receives a detach signal. This can leak reader goroutines waiting on `Done` and loses backpressure/accounting continuity for that client identity.

`ClientManager.Attach()` already handles this case by closing/replacing existing clients, so `terminalSubscribers.Subscribe()` is inconsistent with existing attach semantics.

**Suggested fix**
Before storing the new subscription, check `ts.subs[id]` and close the prior subscription's `Done` channel (and optionally drain/close its chunk channel if that becomes part of the contract), then replace it.

---

### P2 - Harness includes placeholder tests that do not exercise required scenarios

**Location:** `internal/termmux/termmux_harness_test.go:283`, `internal/termmux/termmux_harness_test.go:473`, plus other comment-only sections

**Problem**
The addendum harness suites are marked as implemented (P1-P5, F1-F5, S1-S4, etc.), but several named tests are stubs/comments and do not actually validate the scenario:

- `TestFault_OtelStartupFailure` has an empty body.
- `S4` is comment-only (no executable simulation test in this file).
- Multiple sections explicitly defer verification to other packages without asserting the expected end-to-end invariants for this bead.

This creates an audit mismatch with the bead deliverable wording and test-harness exit criteria, where each suite is expected to have concrete, executable coverage.

**Suggested fix**
Either:
1. Implement concrete tests for each declared suite scenario in this harness file, or
2. Rename these to explicit delegation wrappers that assert delegated coverage exists (e.g., subtest calls into referenced package tests or checks shared helper behavior), and update suite accounting to avoid claiming unimplemented cases.

---

## Summary

2 findings: 0 P0, 0 P1, 2 P2, 0 P3

**Verdict**: Approved with revisions
