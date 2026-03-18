# Code Review: aiag-7ya.3 (R1, reviewer-sea)

- Bead: aiag-7ya.3
- Commit range: a32cf55 (single commit)
- Plan doc: docs/plans/19-*.md §3.2, §3.3, §9
- Reviewer: reviewer-sea
- Review commit: a32cf55

## Findings

### P2-1 - UserData SandboxID is always "direct:pending"

**Location:** `internal/sandbox/control/direct/create.go:42-46`

**Problem**
`renderUserData` is called before `LaunchInstance`, so the instance ID is unknown. The code passes `encodeSandboxID("pending")` which produces `"direct:pending"`. The `UserDataTemplateData.SandboxID` field exists and is exported, inviting template authors to use `{{.SandboxID}}` — but it always has a placeholder value. This is a chicken-and-egg problem inherent to EC2 UserData (it's provided at launch time), but having a field that is always incorrect is a code smell that can lead to subtle bugs if a template author uses it without realizing.

**Suggested fix**
Either: (a) remove `SandboxID` from `UserDataTemplateData` since it can never be correct at render time, replacing it with a comment explaining that the instance can discover its own ID via the EC2 metadata service, or (b) add a doc comment on the field: `// SandboxID is a placeholder ("direct:pending") — the actual ID is not known until after launch. Use the EC2 metadata service (169.254.169.254) for self-identification.`

---

### P2-2 - UserData template renders untrusted labels without shell escaping

**Location:** `internal/sandbox/control/direct/create.go:42-49, 232-245`

**Problem**
`renderUserData` uses `text/template` to render the UserData script. The template data includes `Labels` from the `CreateSandboxRequest`, which may contain values from upstream callers. If the operator's UserData template interpolates labels directly (e.g., `echo {{index .Labels "env"}}`), and a label value contains shell metacharacters (e.g., `$(malicious-command)`), the rendered UserData becomes a shell injection vector.

The codebase already has `shellQuote()` in `ssm_command.go` for exactly this purpose, but it's not available as a template function.

**Suggested fix**
Register `shellQuote` as a template function when parsing the UserData template:
```go
template.New("direct-user-data").Funcs(template.FuncMap{
    "shellQuote": shellQuote,
}).Option("missingkey=zero").Parse(tpl)
```
Template authors can then write `echo {{shellQuote (index .Labels "env")}}` for safe interpolation. This is defense-in-depth — labels currently originate from trusted internal code, but the template API should be safe by default.

---

### P2-3 - No test coverage for closing guard paths

**Location:** `internal/sandbox/control/direct/create.go:28-34, 90-95`

**Problem**
`CreateSandbox` checks `d.closing` at two points: (a) at entry (lines 28-34) before launching, and (b) after polling completes (lines 90-95) before storing state. Both paths return `ErrDirectClosed` and the post-poll path also terminates the orphan instance. Neither path is tested. These are important correctness paths — the post-poll closing check with orphan cleanup is the primary defense against leaked instances during shutdown.

**Suggested fix**
Add two tests:
1. `TestCreateSandbox_ClosedAtEntry` — set `d.closing = true` before calling `CreateSandbox`, verify `ErrDirectClosed` returned and no `LaunchInstance` call.
2. `TestCreateSandbox_ClosedDuringPolling` — set `d.closing = true` after `LaunchInstance` but before the post-poll check (e.g., via a mock that sets `closing` during `DescribeInstance`), verify `ErrDirectClosed` returned and `TerminateInstance` called for orphan cleanup.

---

### P3-1 - DestroySandbox concurrent double-call terminates instance twice

**Location:** `internal/sandbox/control/direct/destroy.go:7-32`

**Problem**
`DestroySandbox` releases the lock between the instance lookup (line 15) and `TerminateInstance` (line 23). Two concurrent `DestroySandbox` calls for the same sandbox ID both get the instance state, both call `TerminateInstance`, and both `delete` from the map. AWS `TerminateInstances` is idempotent for already-terminated instances, so this is safe in practice but the double API call is wasteful. The second `delete` is a no-op.

**Suggested fix**
No change strictly needed — AWS idempotency makes this benign. Could defensively set `st.status = statusStopped` before releasing the lock, and check the status at the start to skip already-terminating instances. Low priority.

---

### P3-2 - `math/rand` global source used instead of `math/rand/v2`

**Location:** `internal/sandbox/control/direct/create.go:8, 228`

**Problem**
`jitterDelay` uses `rand.Int63n` from the `math/rand` package (not `math/rand/v2`). In Go 1.20+, the global `math/rand` functions are concurrent-safe, so there's no correctness issue. But `math/rand/v2` is the recommended replacement in modern Go.

**Suggested fix**
Replace `"math/rand"` with `"math/rand/v2"` and use `rand.IntN` or `rand.Int64N`. Minor modernization nit.

---

### P3-3 - Instance type mapping gives fewer actual CPUs than requested

**Location:** `internal/sandbox/control/direct/instance_type.go:29-35, 47-52`

**Problem**
The mapping thresholds are ranges, not guarantees. Requesting 7 CPUs maps to `t3.xlarge` (actual: 4 vCPUs) because 7 <= maxCPUs(8). The `actualCPUs` and `actualMem` fields exist but are never used for validation or logging. A caller requesting 7 CPUs might expect to get at least 7 vCPUs. This is a design choice documented in the mapping table comments, but it's surprising.

**Suggested fix**
No code change needed — this is intentional resource binning. Consider adding a log line or a warning in the `CreateSandboxResponse` when the actual instance specs are significantly below the requested resources (e.g., `actual < requested * 0.8`). Low priority.

---

## Summary

6 findings: 0 P0, 0 P1, 3 P2, 3 P3

**Verdict**: Approved with revisions

The implementation is clean and well-structured. The closing guard with `inflightWg` tracking is correct, the orphan cleanup on polling failure/cancellation is thorough, and the lock-only-for-map-access concurrency pattern matches the plan's §9 design. Test coverage hits all 8 cases from the bead scope.

The P2 findings are non-blocking but worth addressing: the "direct:pending" SandboxID is a footgun for template authors, the lack of `shellQuote` in templates is a defense-in-depth gap, and the closing guard paths deserve test coverage given they protect against instance leaks.
