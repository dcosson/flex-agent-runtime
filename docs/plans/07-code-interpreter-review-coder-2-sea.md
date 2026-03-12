# 07: Code Interpreter — Review Findings (coder-2-sea, R1)

**Plan:** [07-code-interpreter.md](./07-code-interpreter.md)
**Test Harness:** [07-code-interpreter-test-harness.md](./07-code-interpreter-test-harness.md)
**Reviewer:** coder-2-sea
**Round:** 1
**Date:** 2026-03-11

---

## Summary

The plan is well-structured and covers the core architecture of the Starlark-based code interpreter comprehensively. However, there are several specification gaps — particularly around RLM determinism claims, DataStore semantics, provider configuration, and tier classification robustness — that need to be addressed before implementation to avoid ambiguity and rework.

## Findings

### [F1] RLM sub-calls break Starlark determinism guarantee — P1

**Section:** 1 (Overview), Architecture Doc AD7
**Issue:** The plan claims "Deterministic: Starlark is intentionally deterministic," but RLM sub-calls (`llm_call`, `llm_batch`) are inherently non-deterministic — different model responses will occur across runs even with identical inputs. The test harness (P1) attempts to address this by constraining tests to "same RLM responses," but the plan text itself is misleading. Architecture doc AD7 also claims determinism without qualification. This creates a false expectation for consumers of the API and makes replay-based debugging unreliable without additional mechanisms.
**Recommendation:** Explicitly carve out RLM calls from the determinism guarantee in the plan overview and AD7. Add a subsection explaining: (1) pure Starlark execution remains deterministic, (2) RLM calls introduce controlled non-determinism, (3) the mitigation strategy for testing and replay (mock provider injection for deterministic tests, content-hash caching for replay scenarios). Update the test harness doc to reference this carve-out rather than implicitly working around it.

### [F2] DataStore interface has ambiguous Search semantics — P1

**Section:** 4.5 (DataStore Interface)
**Issue:** `Search(key string, pattern string)` has unclear semantics. It is ambiguous whether `key` scopes the search to a single stored value or acts as a prefix/filter across all keys. The Starlark builtin `store_search(key, pattern)` naming suggests single-key search, but the `Match` struct includes a `Key` field, which implies results can span multiple keys. Additionally, `BlobDataStore` returns an error for `Search`, which silently breaks the interface contract — callers have no way to know at compile time that a given implementation does not support this operation.
**Recommendation:** (1) Clarify whether `Search` is single-key (search within one value) or multi-key (search across stored entries matching a key pattern). Update the `Match` struct and Starlark builtin signature accordingly. (2) Define explicit behavior for `BlobDataStore` — either return a typed `ErrNotSupported` sentinel error or remove `Search` from the base interface and use an optional `Searchable` interface. (3) Consider adding a `Capabilities() DataStoreCaps` method so scripts can introspect what operations are available on their backing store.

### [F3] Tier classification via identifier scan is fragile — P2

**Section:** 4.7 (Tier Classification)
**Issue:** The plan states "a simple identifier scan suffices since llm_call and llm_batch are unique identifiers." However, a Starlark string literal containing `"llm_call"` (e.g., `msg = "calling llm_call now"`) or a variable name like `my_llm_call_count` would trigger a false positive, promoting a TierLite script to TierFull unnecessarily. This increases overhead and reduces predictability of tier assignment.
**Recommendation:** Use proper Starlark AST parsing (`go.starlark.net` provides `syntax.Parse` and the AST types needed to walk call expressions) to detect actual function calls to `llm_call` and `llm_batch`. This eliminates false positives from string literals, comments, and similarly-named identifiers. If AST parsing is considered too heavy for V1, document the limitation explicitly and add it as a known issue with a follow-up task.

### [F4] Missing DataStore cleanup lifecycle — P2

**Section:** 4.5 (DataStore Interface), 5 (Lifecycle)
**Issue:** The plan states "DataStore persists, script state does not" but does not specify when or how DataStore data is cleaned up. `MemoryDataStore` data is implicitly garbage collected when the struct is unreferenced, but `FSDataStore` writes files to disk that persist indefinitely. `BlobDataStore` may create cloud objects with associated storage costs. There is no specified cleanup hook, no TTL mechanism, and no clarity on whether the script, the agent loop, or the sandbox host service is responsible for cleanup.
**Recommendation:** Define explicit cleanup semantics per DataStore implementation: (1) `MemoryDataStore` — document that it is GC'd with the interpreter instance. (2) `FSDataStore` — specify a cleanup method (e.g., `Close()` or `Cleanup()`) and who calls it (sandbox host on session destroy, or the agent loop on tool completion). (3) `BlobDataStore` — define whether objects are ephemeral (auto-deleted after session) or persistent (require explicit deletion). Add a `Close() error` method to the `DataStore` interface to formalize this lifecycle.

### [F5] RLM cost estimation is underspecified — P2

**Section:** 5.3 (Token Budget Management)
**Issue:** The plan says "Pre-check before each RLM call: if remaining budget < estimated minimum, return typed token_budget_exceeded error." However, the estimation strategy is not defined. Input tokens can be approximated from prompt length, but output tokens are unknown before the call completes. Without a specified estimation approach, implementations will make ad-hoc choices that may be overly conservative (wasting budget headroom) or too permissive (allowing budget overruns).
**Recommendation:** Specify the estimation strategy explicitly. Options include: (1) use the `max_tokens` parameter from the `llm_call` invocation as the output estimate (conservative but predictable), (2) use a configurable fixed estimate (e.g., 1024 tokens) when `max_tokens` is not set, (3) use historical average from previous calls in the same session. Document the chosen approach and its trade-offs. Also clarify whether the budget is hard (call is rejected) or soft (call proceeds but the session is flagged).

### [F6] invoke_safe and llm_call_safe introduced without full specification — P2

**Section:** 5.4 (Error Handling Extensions)
**Issue:** `invoke_safe` and `llm_call_safe` are listed as "Extension (available now)" but they do not appear in the approved builtins list (Section 3.1), are not included in the Starlark builtin signatures (Sections 4.2-4.3), and are not mentioned in the sandbox capability closure. Their behavior, return types, and error semantics are undefined.
**Recommendation:** If these are V1 builtins, add them to Section 3.1 with full signatures, document their return types (presumably a result-or-error tuple), and include them in the sandbox capability closure. If they are planned for a future version, label them explicitly as "Future / V2" in Section 5.4 to avoid confusion during implementation.

### [F7] store_read_range and store_delete missing from DataStore builtin wrappers section — P3

**Section:** 4.4 (Starlark Builtins), 6 (Package Structure)
**Issue:** `store_read_range` and `store_delete` are listed as Starlark builtins in Section 4.4, but Section 4.1 stats tracking does not account for range reads or deletes separately, and the package structure description in Section 6 only mentions "store_read/write/search/list" for `datastore.go`. This is a minor consistency issue but could cause implementers to overlook these operations.
**Recommendation:** Update Section 4.1 to include stats for range reads and deletes (or document that they roll up into existing counters). Update the Section 6 `datastore.go` description to include all six DataStore builtins.

### [F8] No specification of how RLM provider is configured — P1

**Section:** Architecture (CodeInterpreterTool struct), 4.2-4.3 (RLM Builtins)
**Issue:** The `CodeInterpreterTool` struct shows `provider ai.Provider` but the plan does not specify how this provider is supplied to the code interpreter or how it relates to the agent's own provider. The `llm_call` builtin accepts a `model` parameter, but the resolution logic is unspecified. If the agent uses an Anthropic provider but a script requests an OpenAI model via the `model` parameter, the behavior is undefined. It is also unclear whether scripts can override the provider or are constrained to the agent's configured provider.
**Recommendation:** Specify the provider resolution strategy: (1) Is the code interpreter's provider always the same as the agent's provider? (2) Does the `model` parameter in `llm_call` select a model within the same provider, or can it cross provider boundaries? (3) If cross-provider is supported, how are credentials and provider instances managed? A simple V1 approach would be to pin the code interpreter to the agent's provider and treat the `model` parameter as a model selector within that provider, with cross-provider support deferred to V2.

### [F9] Missing specification of sandbox host ↔ code interpreter relationship — P2

**Section:** 4.7 (Tier Classification), Architecture (Connected Components)
**Issue:** In TierFull mode, the plan states "Sandbox Starlark VM" is used, but the mechanism for transitioning from in-process to sandboxed execution is unspecified. It is unclear who decides whether to run the Starlark VM in-process vs. in a sandbox: does the agent loop instruct the code interpreter, does the code interpreter self-select based on tier classification, or does the sandbox host service have code-interpreter-specific awareness? The connected components table does not document this relationship.
**Recommendation:** Add a subsection specifying: (1) the decision point — who triggers sandbox mode (likely the code interpreter itself, based on tier classification result), (2) the interface between the code interpreter and the sandbox host (e.g., does it call a sandbox execution API, or does it spin up a sandboxed subprocess?), (3) how DataStore and RLM provider are plumbed into the sandboxed environment. Update the connected components table to include the codeinterp-to-sandbox relationship.

### [F10] MemoryDataStore benchmark target of 100K ops/sec is too low — P3

**Section:** Test Harness B6 (DataStore Benchmarks)
**Issue:** The benchmark target of 100K ops/sec for `MemoryDataStore` implies ~10us per operation. For an in-memory `map` with a `sync.RWMutex`, individual operations should complete in ~150ns (map lookup ~100ns + RWMutex acquire/release ~50ns), yielding a realistic throughput of 1M+ ops/sec on a single goroutine and potentially higher with read-heavy workloads using `RLock`. The current target would pass even a severely degraded implementation.
**Recommendation:** Raise the `MemoryDataStore` benchmark target to at least 1M ops/sec for single-goroutine sequential operations, and define separate targets for concurrent read-heavy workloads. If the 100K target was chosen to account for a specific overhead (e.g., stats tracking, deep copy on read), document that rationale explicitly.

---

## Statistics

- Total findings: 10
- P0 (blocking): 0
- P1 (significant): 3
- P2 (moderate): 5
- P3 (minor): 2
