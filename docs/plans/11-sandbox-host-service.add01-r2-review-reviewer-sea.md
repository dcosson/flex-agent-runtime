# Review: 11-sandbox-host-service.add01 R2 (reviewer-sea)

- Source doc: `docs/plans/11-sandbox-host-service.add01.md`
- Test harness doc: `docs/plans/11-sandbox-host-service.add01-test-harness.md`
- Reviewed commit: 92feda3
- Reviewer: reviewer-sea
- Round: 2 (post-R1 incorporation of 14 findings)

## R1 Fix Verification

All R1 P1 and P2 fixes are clean:

- **Concurrency contract (§3.4)**: Comprehensive — RWMutex strategy, permitted concurrent pairs table, per-environment notes. Remote env structs show `mu sync.RWMutex` field. NativeSandbox delegation explained. LocalEnvironment `atomic.Bool` noted.
- **LocalCapabilities.Pause = true**: Correct with clear semantics (§4.2 lines 404-411). Capability contract documented in Pause() doc comment (§3.1 lines 212-214). Test harness P1 updated with both positive and negative branches.
- **State() SessionState**: Added to interface (§3.1 line 240). Per-environment behavior documented.
- **SSH host key verification**: Pinned key strategy documented after §5.5 (line 970). Clear and implementable.
- **IsFileOp()**: Defined in §7.5 with concrete tool name list and package location.
- **Acceptance criteria**: §10 added with 5 cross-boundary scenarios. Good coverage.
- **Orphaned environment cleanup (§13)**: Label-based GC sweep + provider TTLs. Well-specified.
- **LocalEnvironment destroyed state**: Tracks `destroyed` field, ExecuteTool returns ErrNotActive post-Destroy.

No regressions introduced by the changes.

## Findings

### P3 - Capabilities Pause comment lists Daytona alongside Pause:true environments

**Problem**
§4.1 Capabilities struct comment (lines 375-381) describes "The degree of preservation varies" and lists Daytona as having "partial" preservation alongside NativeSandbox, E2B, and Fly — all of which have `Pause: true`. But `DaytonaCapabilities.Pause = false` (line 439), meaning Daytona returns `ErrCapabilityNotSupported` on Pause(). Listing Daytona's partial preservation in the same comment as the Pause:true environments is misleading — it implies Daytona participates in Pause when it doesn't.

**Required fix**
Either remove Daytona from the Pause field comment (it's Pause:false, so it doesn't participate), or restructure the comment to separate "environments that support Pause" from "why Daytona doesn't" (e.g., "DaytonaSandboxEnvironment: Pause:false — auto-stop is lossy, not true pause").

---

### P3 - Section numbering mismatch in Testing Strategy

**Problem**
§11 is titled "Testing Strategy" but its subsections are numbered "10.1", "10.2", "10.3", "10.4" instead of "11.1", "11.2", etc. This happened because the Acceptance Criteria section was inserted as §10, pushing Testing Strategy from §10 to §11, but the subsection numbers weren't updated.

**Required fix**
Renumber subsections from "10.1" to "11.1", "10.2" to "11.2", etc.

---

### P3 - Test harness SEC2 inconsistent with required SessionID

**Problem**
Test harness SEC2 (line 358) says "Empty session ID handled gracefully (auto-generated or error)." The plan was updated in R1 to make SessionID required — §3.2 line 276 now says "Required — Create() returns an error if empty. The caller (RuntimeController) owns session identity; environments do not generate IDs." The test harness should reflect that empty SessionID is always an error, not "auto-generated or error."

**Required fix**
Update SEC2 line to: "Empty session ID returns an error (SessionID is required per §3.2)."

---

### P3 - LocalEnvironment destroyed field type contradicts concurrency contract

**Problem**
§3.4 concurrency contract (line 338) says LocalEnvironment should use "a simple `atomic.Bool`" for its `destroyed` field. But the LocalEnvironment code (§5.1 line 475) declares `destroyed bool` (plain bool, not atomic). This is a minor code/contract mismatch — an implementor following the code example would have a data race that the contract explicitly says to avoid.

**Required fix**
Change the code example at line 475 to `destroyed atomic.Bool` and update the Destroy/ExecuteTool methods to use `e.destroyed.Store(true)` and `e.destroyed.Load()`.

---

## Summary

4 findings: 0 P0, 0 P1, 0 P2, 4 P3

All R1 P1 and P2 fixes verified clean. No new correctness or functional gaps. Remaining findings are minor consistency/documentation issues.

**Verdict**: Approved
