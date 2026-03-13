# Code Review: aiag-qkh.3 (R1, reviewer-sea)

- Bead: aiag-qkh.3
- Commit range: efa83a8..e507a7f
- Plan doc: docs/plans/01-ai-core.md §9
- Test harness: docs/plans/01-ai-core-test-harness.md §1 (P4), §3 (F2, F3), §4 (S3), §5 (B4, B5), §7 (Sec1)
- Reviewer: reviewer-sea
- Review commit: e507a7f

## Findings

### P2 - unmarshalToAny silently ignores JSON parse errors

**Location:** `internal/ai/validation.go:82-86`

**Problem**
```go
func unmarshalToAny(raw json.RawMessage) any {
    var v any
    _ = json.Unmarshal(raw, &v)
    return v
}
```
If `raw` contains malformed JSON (non-empty but invalid), `Unmarshal` fails silently and `v` remains nil. This nil is then passed to `c.AddResource("tool.json", nil)`, which may produce a confusing error from the jsonschema compiler rather than a clear "invalid JSON schema" error. While `ValidateToolArguments` guards against empty Parameters, it doesn't guard against non-empty invalid JSON.

**Suggested fix**
Return an error from `unmarshalToAny` and propagate it through `compileSchema`:
```go
func unmarshalToAny(raw json.RawMessage) (any, error) {
    var v any
    if err := json.Unmarshal(raw, &v); err != nil {
        return nil, fmt.Errorf("invalid JSON in schema: %w", err)
    }
    return v, nil
}
```

---

### P3 - CoerceTypes returns same map reference, making copy-back loop redundant

**Location:** `internal/ai/validation.go:95-101`

**Problem**
The plan specifies CoerceTypes should "return a copy of the coerced arguments." The implementation returns the same map reference (not a copy). This makes the copy-back loop in `ValidateToolArguments` a no-op (`args[k] = args[k]`). The code works correctly since CoerceTypes modifies args in-place, but the redundant loop could confuse future maintainers.

**Suggested fix**
Either remove the copy-back loop (since coercion is already in-place) or add a comment explaining the intentional behavior. Not blocking.

---

### P3 - Custom contains/searchString functions instead of strings.Contains

**Location:** `internal/ai/validation_test.go:289-298`

**Problem**
Test file defines custom `contains` and `searchString` functions that replicate `strings.Contains`. The stdlib function is more idiomatic and well-tested.

**Suggested fix**
Replace with `strings.Contains(errMsg, "test")`. Import `strings` (already available in validation.go).

---

## Plan Compliance Check

| Deliverable | Status |
|---|---|
| CoerceTypes (string→int, string→float, string→bool, string→null, nested objects, arrays) | ✅ |
| Coercion failure: leaves original value | ✅ |
| ValidateToolArguments (coerce → compile → validate → copy back) | ✅ |
| ValidateToolCall (lookup + delegate) | ✅ |
| Schema compilation cache (xxhash key, sync.RWMutex, double-checked locking) | ✅ |
| Cache hit/miss metrics (URP §16.1) | ✅ (atomic counters + SchemaCacheStats) |
| formatValidationError with JSON paths | ✅ (BasicOutput from jsonschema v6) |
| External deps: jsonschema/v6, xxhash/v2 | ✅ |
| Correct v6 API usage (Compiler.AddResource + Compile vs v5 CompileString) | ✅ |

## Test Harness Coverage

| Test | Status | Notes |
|---|---|---|
| P4: CoerceTypes preserves valid types (rapid) | ✅ | string/int/float/bool all verified |
| F2: JSON Schema validation fuzz | ✅ | 6 seeds, no-panic guarantee |
| F3: CoerceTypes fuzz | ✅ | 5 seeds, no-panic guarantee |
| S3: Schema cache varied load | ✅ | cold (1000 unique), hot (999 repeated), mixed 80/20, concurrent (10×100) |
| B4: Validation benchmark (warm cache) | ✅ | BenchmarkValidateToolArguments |
| B5: Schema compilation (cold) benchmark | ✅ | BenchmarkSchemaCompilationCold |
| Sec1: Malicious schema injection | ✅ | recursive $ref, deep nesting (100), huge enum (10K), regex DoS — all complete within 5s |
| All tests pass with `-race` | ✅ | 0 race warnings |

## Implementation Quality Notes

- **Double-checked locking**: Correct RLock→miss→compile→Lock→double-check pattern. Prevents redundant compilations while minimizing lock contention.
- **Security**: Sec1 tests verify that Go's `regexp` engine (no backtracking) prevents regex DoS. Deep nesting and recursive refs handled gracefully by jsonschema library.
- **Coercion completeness**: All 6 coercion types implemented (integer, number, boolean, null, object recursion, array element coercion). Failure path correctly preserves original values.
- **Cache observability**: Atomic counters with `SchemaCacheStats()` and `ClearSchemaCache()` APIs enable testing and monitoring.

## Summary

3 findings: 0 P0, 0 P1, 1 P2, 2 P3

**Verdict**: Approved with revisions

Solid implementation with comprehensive test coverage including fuzz testing and security validation. The P2 finding (unmarshalToAny error swallowing) should be addressed to ensure clear error messages for malformed schemas. The P3 findings are minor style issues.
