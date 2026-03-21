# Code Review: aiag-4av5 (R1, reviewer-sea)

- Bead: aiag-4av5
- Commit range: c468918..f784bdb
- Plan doc: docs/plans/22-e2e-external-tests.md §4.4, §5.2-5.3, §6-9
- Reviewer: reviewer-sea
- Review commit: f784bdb

## Findings

### P2 - ZFS-D5 large file test doesn't compare checksums

**Location:** `tests/e2e/test_zfs_durability.py:225-267`

**Problem**
`test_large_file_persistence` writes a 100MB file, computes its md5sum, snapshots, clones, and re-computes md5sum — but never actually compares the two checksums. Both assertions just check `_has_content_event(events)`. The test would pass even if the cloned file was corrupted, defeating the purpose of checksum-based verification.

**Suggested fix**
Extract the md5 hash from `events_md5` and from the clone's checksum events, then assert they match:
```python
original_md5 = _extract_text(events_md5)  # parse out the hash
clone_events = _collect_events(client, session2, "Run: md5sum /workspace/large-file.txt")
clone_md5 = _extract_text(clone_events)
assert original_md5.split()[0] == clone_md5.split()[0], "Checksums should match after clone"
```

---

### P2 - PA-5 auth failure test is a no-op

**Location:** `tests/e2e/test_provider_api.py:142-157`

**Problem**
`test_invalid_api_key` creates a session with valid config and then does nothing (`pass`). The docstring says it tests graceful auth failure, but the test body is a placeholder. This provides zero coverage for error handling of invalid credentials.

**Suggested fix**
Either implement the test (e.g., pass an invalid API key via session config and assert a clean error), or remove the test class and add a comment/bead tracking it as future work. A test that claims to verify auth failure but doesn't actually test it is misleading.

---

### P3 - Duplicated helper functions across all test files

**Location:** `tests/e2e/test_zfs_durability.py:25-40`, `tests/e2e/test_sandbox_identity.py:17-43`, `tests/e2e/test_multi_runtime.py:25-40`, `tests/e2e/test_provider_api.py:27-29`, `tests/e2e/test_deployment_modes.py:132-134`, `tests/e2e/test_sandbox_config.py:35-50`

**Problem**
`_collect_events`, `_has_content_event`, `_events_contain_text`, and `_extract_text` are copy-pasted across 6 test files. This violates DRY and makes future changes (e.g., adjusting content type detection) error-prone — you'd need to update every copy.

**Suggested fix**
Extract these into a shared module like `tests/e2e/helpers.py` and import from there.

---

## Summary

3 findings: 0 P0, 0 P1, 2 P2, 1 P3

**Verdict**: Approved with revisions

Phase 3 is a comprehensive addition covering ZFS durability, sandbox identity, multi-runtime, provider API, and real-tier deployment/sandbox config tests. The test structure is consistent, skip guards are properly applied, and the real-tier marker scheme works well. The two P2s should be addressed — ZFS-D5 needs actual checksum comparison to provide meaningful coverage, and PA-5 should either be implemented or removed as a placeholder.
