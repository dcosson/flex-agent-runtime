# Code Review: aiag-0dou.1 (R1, reviewer-sea)

- Bead: aiag-0dou.1
- Commit range: 87a0fec..2ab9ec9
- Plan doc: docs/plans/11-sandbox-host-service.add01.md §4, §5.1
- Reviewer: reviewer-sea
- Review commit: 2ab9ec9

## Findings

### P2 - IsFileOp tool names may not match actual tool names in catalog

**Location:** `internal/sandbox/environment/classify.go:11-14`

**Problem**
`IsFileOp` classifies `"read"`, `"write"`, `"edit"`, `"grep"`, `"glob"` as file operations. However, looking at the tool catalog (via `NewEnvironmentTools` and the test at `envtools_test.go:780`), the actual tool names are `"read_file"`, `"write_file"`, `"edit_file"`, `"grep"`, `"glob"`. The `classify.go` names don't match: `"read"` vs `"read_file"`, `"write"` vs `"write_file"`, `"edit"` vs `"edit_file"`.

If remote providers call `IsFileOp(toolRequest.ToolName)` with the actual catalog tool names, `read_file`/`write_file`/`edit_file` will incorrectly return `false` and be routed as command ops instead of file ops.

**Suggested fix**
Update `IsFileOp` to use the actual tool catalog names:
```go
case "read_file", "write_file", "edit_file", "grep", "glob":
```
Or derive the list programmatically from the tool catalog to avoid future drift.

---

### P3 - EnvironmentTools callback invoked eagerly, not lazily

**Location:** `internal/agent/loop.go:28-30`

**Problem**
The doc comment on `DriverConfig` says "NativeDriver calls EnvironmentTools() once at start to populate the tool catalog." The callback is called in `NewNativeDriver` (constructor), not when the first turn starts. This is effectively eager, not lazy. The naming and docs imply laziness. This is fine in practice since the environment should already be initialized by the time the driver is constructed, but the terminology is slightly misleading.

**Suggested fix**
Update the doc comment from "lazily" to "at construction time" or "once during NewNativeDriver". No code change needed.

---

### P3 - envtools test checks hardcoded tool names that could drift

**Location:** `internal/tools/envtools/envtools_test.go:780`

**Problem**
The test checks for specific tool names (`read_file`, `bash`, `write_file`, `edit_file`, `grep`, `glob`). If the tool catalog changes, this test could break. This is actually a feature — it serves as a contract test ensuring the expected tools exist. Just noting for awareness.

**Suggested fix**
No fix needed. This is good defensive testing.

---

## Summary

3 findings: 0 P0, 0 P1, 1 P2, 2 P3

**Verdict**: Approved with revisions

The architecture is clean:
- The `EnvironmentTools` callback pattern elegantly solves the circular import problem (agent → environment → tools → agent)
- The `envtools` bridge package is minimal and focused
- Tests are thorough — tool resolution, precedence (Tools > EnvironmentTools), and full agent loop execution with environment tools
- `IsFileOp` is a simple, well-tested classifier

The P2 (tool name mismatch) should be fixed before the remote provider tasks (aiag-0dou.2/3/4) start using `IsFileOp`, as they will pass actual tool names from the catalog.
