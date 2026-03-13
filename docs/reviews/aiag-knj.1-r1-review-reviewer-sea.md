# Code Review: aiag-knj.1 (R1, reviewer-sea)

- Bead: aiag-knj.1
- Commit range: a808e59 (on branch batch3/agent-harness)
- Plan doc: docs/plans/06-built-in-tools.md §3-§5
- Reviewer: reviewer-sea
- Review commit: a808e59

## Findings

### P2 - buildAgentTools type-asserts to *LocalBackend, breaking ToolBackend abstraction

**Location:** `internal/tools/factory.go:37-41`

**Problem**
`buildAgentTools` takes a `ToolBackend` interface but immediately type-asserts to `*LocalBackend`:
```go
lb, ok := backend.(*LocalBackend)
if !ok {
    return nil
}
```
This defeats the purpose of the `ToolBackend` interface. If `SandboxBackend` is passed (plan 06 §5.2), the function silently returns nil instead of building tools. The factory should work with any `ToolBackend`, not just `LocalBackend`.

**Suggested fix**
Either: (a) change `buildAgentTools` to accept `*LocalBackend` directly (honest about its actual requirement), or (b) add a method to `ToolBackend` interface that lists available tool implementations (e.g., `ToolNames() []string`) so the factory doesn't need to reach into backend internals.

---

### P2 - LocalBackend.ExecuteTool ignores onProgress callback

**Location:** `internal/tools/local_backend.go:33`

**Problem**
The `onProgress` parameter is `_`-ignored. While this is acceptable for Tier 1 tools (per implementation guide §1.6: "Tier 1 tools are fast enough to skip progress callbacks"), the `LocalBackend` should propagate `onProgress` to `toolImpl.execute` so that individual tools could optionally use it. Currently the `toolImpl.execute` signature doesn't accept `onProgress` at all, so no tool can provide incremental output even if it wanted to.

This isn't blocking for the current three Tier 1 tools, but will need fixing when `bash` or other Tier 2 tools are added to `LocalBackend`.

**Suggested fix**
Low priority for this bead — `bash` tool is in aiag-knj.2. Note this as a known gap.

---

### P3 - errorResponse doesn't set IsError on ToolResponse

**Location:** `internal/tools/read.go:138-141`

**Problem**
`errorResponse()` creates a `ToolResponse` with error text but doesn't set any error indicator. The `ToolResponse` type doesn't have an `IsError` field (unlike `AgentToolResult`), so there's no way for the factory layer to distinguish error responses from success responses. Currently the factory in `factory.go` doesn't check for errors from `ExecuteTool` returning `nil` error with error content — it just passes the content through.

This works because the LLM reads the text and understands "file not found" is an error, but machine consumers can't distinguish success from failure without parsing the text.

**Suggested fix**
Consider adding `IsError bool` to `ToolResponse`, or accept that tool errors are communicated textually (which is the common pattern in agent frameworks). Low priority.

---

## Summary

3 findings: 0 P0, 2 P2, 1 P3

**Verdict**: Approved with revisions

**Review notes:**
- `ToolBackend` interface matches implementation guide §1.6 exactly: `ExecuteTool(ctx, req, onProgress)` signature
- `ToolRequest`, `ToolResponse`, `ToolProgress`, `ResourceSpec` types all match the plan
- `ClassifyTool` is correctly centralized and shared, with sensible defaults (unknown → Tier2)
- Path safety via `resolveSafePath` is well-implemented: EvalSymlinks for symlink-escape detection, isUnderRoot for traversal prevention, handles new-file creation by resolving parent dir
- `read_file`: line numbers, offset/limit, max-size guard (10MB), long-line truncation (2000 chars) all working
- `write_file`: atomic write (temp+fsync+rename) with proper cleanup on error, optional dir creation
- `edit_file`: exact string match, ambiguity rejection for non-unique matches, structured mismatch diagnostics with nearest partial match context — very nice UX for LLM consumers
- `NewLocalTools` factory correctly bridges `ToolBackend` → `[]agent.AgentTool` with proper `ToolProgress` → `AgentToolResult` mapping
- 31 tests cover all three tools comprehensively: happy paths, error cases, path traversal, symlink escape, ambiguity rejection, mismatch diagnostics, atomic write integrity, factory integration
- Race detector passes clean
