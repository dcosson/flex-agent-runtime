# Review: 11-sandbox-host-service.add02 R2 (reviewer-sea)

- Source doc: `docs/plans/11-sandbox-host-service.add02.md`
- Reviewed commit: f0dd2cd
- Reviewer: reviewer-sea

## Findings

### P3 - §7.3 claims capability negotiation on both connections but §7.2 defers agent-loop validation

**Problem**
§7.3 (line 515) says "Capability negotiation on both connections guards against drift" but §7.2 explicitly commits to deployment-time config consistency for the agent loop and defers runtime validation. The agent loop does not call `Create()` and therefore does not perform capability negotiation. The §7.3 statement is now incorrect given the §7.2 revision.

**Required fix**
Update §7.3 to say: "Capability negotiation on the orchestrator's connection guards against drift at session creation time. Operators must ensure config consistency for the agent loop at deployment time (see §7.2)."

---

## Summary

1 finding: 0 P0, 0 P1, 0 P2, 1 P3

**Verdict**: Approved
