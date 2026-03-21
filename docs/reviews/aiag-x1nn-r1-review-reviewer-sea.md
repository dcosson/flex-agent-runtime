# Code Review: aiag-x1nn (R1, reviewer-sea)

- Bead: aiag-x1nn
- Commit range: 8c3823d..cf9d628
- Plan doc: docs/plans/22-e2e-external-tests.md §4.3, §5.1, §11
- Reviewer: reviewer-sea
- Review commit: cf9d628

## Findings

### P2 - Test assertions only check event count, never content

**Location:** `tests/e2e/test_deployment_modes.py:31-35`, `tests/e2e/test_sandbox_config.py:42-49`

**Problem**
Every test that sends a message asserts only `len(events) > 0` without inspecting event content. For example, `test_create_send_destroy` checks `event_types` is non-empty but never verifies any specific type (e.g., `"text"` or `"tool_use"`). The sandbox config tests ask the agent to write/read files but never verify the response mentions the expected content (`"hello e2e"`, `"turn1-data"`). These tests would pass even if the orchestrator returned a single error event or garbage data.

**Suggested fix**
At minimum, check that a text/content event exists in deployment mode tests. For sandbox config tests, check that the read-back response contains the expected string. Example:
```python
# test_sandbox_config.py - test_write_and_read_file
text_events = [e for e in events if e.get("type") == "text"]
assert any("hello e2e" in (e.get("text", "") or "") for e in text_events)
```

---

### P3 - Hardcoded URL in test_orchestrator_health_endpoint

**Location:** `tests/e2e/test_deployment_modes.py:101`

**Problem**
`test_orchestrator_health_endpoint` hardcodes `http://localhost:18080/health` instead of deriving the URL from the `local_orchestrator` fixture or a shared constant. If mock tier ports change, this test would silently hit the wrong endpoint.

**Suggested fix**
Use `local_orchestrator.base_url` or a shared port constant from conftest to construct the URL.

---

### P3 - Missing SC-T3 (grep/glob) and SC-T4 (error handling) tests

**Location:** `tests/e2e/test_sandbox_config.py`

**Problem**
Plan 22 §5.1 specifies SC-T1 through SC-T6. SC-T3 (grep/glob tools dispatch to sandbox-host) and SC-T4 (error handling for invalid tool calls) are not covered. The file docstring claims "Tests SC-T1 through SC-T6" but only covers SC-T1, SC-T2, SC-T5, SC-T6.

**Suggested fix**
Either add SC-T3 and SC-T4 tests, or update the docstring to accurately reflect coverage. If deferred, create a follow-up bead.

---

## Summary

3 findings: 0 P0, 0 P1, 1 P2, 2 P3

**Verdict**: Approved with revisions

The implementation is well-structured and the conftest/process.py changes correctly apply the R2 review fixes (explicit `env` parameter, `_sandbox_host_kwargs()` for CI-safe local-disk mode). The CI workflow is clean and correctly filters with `-m "not real"`. The P2 on weak assertions should be addressed to ensure tests provide meaningful coverage — currently they verify the orchestrator responds but not that it responds correctly.
