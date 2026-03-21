# Code Review: aiag-xrd7.1 (R1, reviewer-sea)

- Bead: aiag-xrd7.1
- Commit range: bde4231..8c3823d
- Plan doc: docs/plans/22-e2e-external-tests.md §2-3, §10, §12
- Reviewer: reviewer-sea
- Review commit: 8c3823d

## Findings

No findings. This is a clean, well-structured implementation.

## Summary

0 findings: 0 P0, 0 P1, 0 P2, 0 P3

**Verdict**: Approved

The implementation matches the plan specification closely and incorporates all R1+R2 review fixes:

- **client.py** (175 lines): `FlexAgentClient` with proper unary/streaming split (`_call` vs `_stream`). Connect envelope parser (`_parse_connect_stream`) correctly handles the binary wire format (5-byte header: flags + big-endian length + payload). `ConnectStreamError` captures error code, message, and details. All 11 RPC methods present with correct streaming/unary classification.
- **process.py** (97 lines): `ProcessManager` with `stdin=subprocess.DEVNULL` (§3.1), `atexit`+signal handler cleanup (§3.3 Layer 2), kill fallback on timeout (§3.2), `env` as separate parameter in `start_orchestrator` (R2 fix), `start_stubserver` with configurable binary path.
- **credentials.py** (44 lines): Clean env-file parser with correct lookup order (file → env override). Guards empty key/value pairs.
- **conftest.py** (162 lines): Tier-specific ports (Mock: 18xxx, Real: 28xxx), module-scoped fixtures with proper teardown, `cleanup_leaked_sessions` dynamically finds active orchestrator client via `request.fixturenames` and handles connection errors.
- **test_connect_parser.py** (136 lines): 12 tests covering all envelope parser paths — multi-message streams, mid-stream errors, empty streams, clean EOF, truncated header/payload, error with details, no end-of-stream sentinel, metadata-only end-of-stream, and `_read_exact` edge cases.
- **requirements.txt**: Minimal deps (pytest, pytest-timeout, requests).
