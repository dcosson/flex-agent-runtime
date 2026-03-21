# Review: 22-e2e-external-tests (r2-review-coder-1-sea)

- Source doc: `docs/plans/22-e2e-external-tests.md`
- Reviewed commit: b549b0e
- Reviewer: coder-1-sea

## Findings

### P1 - Streaming protocol specified as NDJSON without Connect envelope contract

**Problem**
The plan treats server-streaming RPCs as newline-delimited JSON objects (`iter_lines()` + `json.loads(line)`), but does not define Connect streaming envelope semantics (message envelope vs end-stream envelope vs error envelope) and required headers/content-types. This appears in the client architecture and decisions text (`docs/plans/22-e2e-external-tests.md:79-80`, `:100-101`, `:143-152`, `:1102`). If the wire format is not plain NDJSON (or includes envelope metadata/error frames), the proposed parser can silently mis-handle stream completion and error propagation, producing false positives/negatives in E2E tests.

**Required fix**
Replace the NDJSON assumption with an explicit streaming protocol contract matching the runtime’s actual Connect behavior (headers, content-type, frame/envelope shape, end-of-stream and error semantics). Add one concrete golden example (raw response body sequence) and require a parser test that validates success, mid-stream error, and clean end-of-stream handling.

---

### P2 - Real-tier config coverage is internally inconsistent (C1-C6 vs C2-C6)

**Problem**
The Real tier section states configs `C2, C3, C4, C5, C6` are tested (`docs/plans/22-e2e-external-tests.md:480`), but acceptance criteria later require “Real tier configs covered | C1-C6” (`:1052`). These criteria cannot both be true without additional C1 real-tier tests.

**Required fix**
Choose one target and make all sections consistent: either add explicit C1 real-tier scenarios, or change acceptance criteria to `C2-C6`.

---

### P2 - Credential precedence text conflicts with the provided loader implementation

**Problem**
The “Lookup Order” section says later sources override earlier and lists: secrets file, environment, then `.env.test.local` (`docs/plans/22-e2e-external-tests.md:794-799`). But the sample implementation loads secrets file then `.env.test.local`, then applies environment-variable override (`:804-818`). The prose and code disagree on precedence, which will cause surprises in local runs.

**Required fix**
Make precedence unambiguous and consistent across prose and code. If environment must win (recommended), describe order as: shared secrets file -> local override file -> environment override.

---

## Summary

3 findings: 1 P1, 2 P2, 0 P3

**Verdict**: Approved with revisions
