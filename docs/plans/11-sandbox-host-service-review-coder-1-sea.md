# Review: 11-sandbox-host-service (coder-1-sea)

- Source doc: `docs/plans/11-sandbox-host-service.md`
- Test harness: `docs/plans/11-sandbox-host-service-test-harness.md`
- Reviewed commit: 38b2e30
- Reviewer: coder-1-sea
- Round: R2

## Findings

### P1 - TurnComplete returns an undefined turn variable

**Problem**
In the per-turn snapshot pseudocode, `TurnComplete` returns `TurnNumber: turnNum`, but `turnNum` is not defined anywhere in the function (`docs/plans/11-sandbox-host-service.md:841`, `docs/plans/11-sandbox-host-service.md:876`). This is a direct contract bug in the canonical example for the core snapshot boundary path.

**Required fix**
Return the committed turn value (`prospectiveTurn` or `sess.turnCount` after commit) and keep that symbol name consistent throughout the section.

---

### P2 - Snapshot latency metric records space-used bytes

**Problem**
The code records `svc.metrics.snapshotLatency.Record(ctx, info.Used)` (`docs/plans/11-sandbox-host-service.md:872`). `info.Used` is snapshot space usage, not latency. This will corrupt latency telemetry and make SLOs in this plan and downstream runtime harness inaccurate.

**Required fix**
Add explicit timing around `CreateSnapshot` and record duration in `snapshotLatency`; record `info.Used` in a separate snapshot-space metric.

---

## Summary

2 findings: 0 P0, 1 P1, 1 P2, 0 P3

**Verdict**: Approved with revisions
