# Review: 22-e2e-external-tests R2 (reviewer-sea)

- Source doc: `docs/plans/22-e2e-external-tests.md`
- Reviewed commit: b549b0e
- Reviewer: reviewer-sea

## Findings

### P2 - start_orchestrator passes env dict as CLI flag

**Problem**
`start_orchestrator` (§2.4, line 214-218) iterates `**kwargs` to build CLI args, then passes `kwargs.get("env", {})` to `_start` as environment variables. When called with `env={...}` (as in §12.2 line 1026-1033), the dict gets both:
1. Stringified as `--env "{'ANTHROPIC_BASE_URL': ...}"` — an invalid CLI flag
2. Passed correctly as process environment to `_start`

The `env` kwarg should be excluded from the CLI args loop.

**Required fix**
Pop `env` from kwargs before iterating, or use a separate parameter:
```python
def start_orchestrator(self, listen: str = ":8080", env: dict = None, **kwargs) -> subprocess.Popen:
    args = ["serve", "orchestrator", "--listen", listen]
    for k, v in kwargs.items():
        args.extend([f"--{k.replace('_', '-')}", str(v)])
    return self._start(args, env or {})
```

---

### P3 - §16 Decisions Q3 references old 4-tier port scheme

**Problem**
§16 Decision 3 (line 1106) still references the old tier names: "E2E-Fast: 18xxx, E2E-Standard: 28xxx, E2E-Provider: 38xxx, E2E-Infra: 48xxx". The plan now uses 2 tiers (Mock and Real) with only 2 port ranges in §3.5 (Mock: 18xxx, Real: 28xxx).

**Required fix**
Update §16 Q3 to reference Mock/Real tier names and 2 port ranges only.

---

### P3 - _start missing stdin=DEVNULL despite §3.1 requirement

**Problem**
§3.1 (line 259) says "All `flexagent` processes are started with `stdin=/dev/null` (or `subprocess.DEVNULL`)." But `_start` (line 220-228) and `start_stubserver` (line 230-240) don't pass `stdin=subprocess.DEVNULL` to `Popen`.

**Required fix**
Add `stdin=subprocess.DEVNULL` to both `_start` and `start_stubserver` Popen calls.

---

### P3 - stop_all inconsistency between §2.4 and §3.2

**Problem**
`stop_all` at line 242-246 (§2.4) uses simple `terminate(); wait(timeout=10)` without a kill fallback. The enhanced version at line 280-288 (§3.2) adds `except TimeoutExpired: proc.kill()`. These describe the same method but the §2.4 version would hang on stuck processes.

**Required fix**
Use the enhanced version (with kill fallback) in §2.4, or note that §3.2 supersedes it.

---

## Summary

4 findings: 0 P0, 0 P1, 1 P2, 3 P3

**Verdict**: Approved with revisions

The revision is a significant improvement over R1:
- **2-tier simplification** (Mock + Real) is much cleaner than the original 4-tier scheme. The Mock tier runs in CI on every PR; the Real tier runs locally with real infrastructure. Clear separation.
- **Deployment dimension matrix** (§4) is excellent — organizing tests by the 4 dimensions from the README (agent loop placement, tools placement, agent type, execution environment) makes the coverage explicit and traceable.
- **All R1 P1s resolved**: FlexAgentClient now has all RPC methods with correct streaming patterns. The `_stream` / `_call` split is clean.
- **All R1 P2s resolved**: tier-specific ports, decisions section, stubserver fixture with proper ProcessManager method.
- **Test safety** (§3) remains thorough with multi-layer cleanup, timeouts, and fixture ordering.

The single P2 (env dict as CLI flag) is a real bug in the ProcessManager API but is straightforward to fix. The P3s are minor inconsistencies.
