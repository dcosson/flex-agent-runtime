# Code Review: aiag-0dou.4 (R1, reviewer-sea)

- Bead: aiag-0dou.4
- Commit range: abe9249..7ce73e9
- Plan doc: docs/plans/11-sandbox-host-service.add01.md §5.5
- Reviewer: reviewer-sea
- Review commit: 7ce73e9

## Findings

### P1 - Heredoc injection in write_file command construction

**Location:** `internal/sandbox/environment/fly/fly.go:478-479`

**Problem**
`buildFileCommand` for `write_file` constructs:
```
mkdir -p '/dir' && cat > '/path' <<'__FLEX_EOF__'
{content}
__FLEX_EOF__
```
If `content` contains the literal string `__FLEX_EOF__` on a line by itself, the heredoc terminates early and subsequent lines are executed as shell commands. This is a command injection vector.

Example: content = `line1\n__FLEX_EOF__\nrm -rf /\n` would execute `rm -rf /` on the remote host.

**Suggested fix**
Use `printf '%s' ... | cat > path` with proper escaping instead of a heredoc, or use base64 encoding:
```go
encoded := base64.StdEncoding.EncodeToString([]byte(content))
return fmt.Sprintf("echo %s | base64 -d > %s", shQuote(encoded), shQuote(path)), nil
```
Alternatively, ensure the delimiter cannot appear in content by using a sufficiently unique/random delimiter.

---

### P1 - `edit_file` only reads — does not edit

**Location:** `internal/sandbox/environment/fly/fly.go:496-501`

**Problem**
The `edit_file` case in `buildFileCommand` just runs `cat {path}` — identical to `read_file`. It doesn't apply any edits. The `edit_file` tool presumably receives parameters like `old_string`/`new_string` for replacement, but these are completely ignored.

This means `edit_file` tool calls will silently return the file contents without making any changes, which is a correctness bug.

**Suggested fix**
Implement actual edit semantics. For example, using `sed`:
```go
case "edit_file":
    path, _ := params["path"].(string)
    oldStr, _ := params["old_string"].(string)
    newStr, _ := params["new_string"].(string)
    if path == "" { return "", fmt.Errorf("fly: edit_file requires path") }
    if oldStr == "" { return "", fmt.Errorf("fly: edit_file requires old_string") }
    // Use a temp file + replace approach for safety
```
Or delegate to a write-the-whole-file approach by reading server-side, doing the replacement, and writing back. This may be complex enough to warrant a follow-up.

---

### P2 - Volume leak if machine creation fails

**Location:** `internal/sandbox/environment/fly/fly.go:167-172`

**Problem**
`Create` calls `apiCreateVolume` first, then `apiCreateMachine`. If machine creation fails, the volume is orphaned — there's no cleanup. The Destroy method won't help since state never transitions to Active and `machineID` stays empty.

**Suggested fix**
Add cleanup logic: if `apiCreateMachine` fails, call delete on the volume:
```go
volID, err := e.apiCreateVolume(ctx, opts)
if err != nil {
    return fmt.Errorf("fly: create volume: %w", err)
}
machineID, ip, err := e.apiCreateMachine(ctx, image, opts, volID, config.SessionID)
if err != nil {
    _ = e.doJSON(ctx, http.MethodDelete, fmt.Sprintf("/apps/%s/volumes/%s", e.appName, volID), nil, nil)
    return fmt.Errorf("fly: create machine: %w", err)
}
```

---

### P2 - `doJSON` and `cloneLabels` duplicated (third copy)

**Location:** `internal/sandbox/environment/fly/fly.go:341-544`

**Problem**
Third copy of `doJSON`, `cloneLabels`. Same finding as aiag-0dou.3 P2. Should be extracted to shared package.

**Suggested fix**
Create follow-up bead to extract shared HTTP helper. Not blocking.

---

### P3 - `bash` command passed directly without wrapping

**Location:** `internal/sandbox/environment/fly/fly.go:509-514`

**Problem**
The `bash` tool case takes the user's command string and passes it directly to SSH `session.Run()`. This is intentional — the command should run as-is on the remote host. Just noting that `session.Run()` passes the command to the remote shell, so the command will be interpreted by the remote shell. This is expected behavior for a bash tool.

**Suggested fix**
No fix needed.

---

## Summary

5 findings: 0 P0, 2 P1, 2 P2, 1 P3

**Verdict**: Not approved — P1s must be fixed

The implementation has strong foundations:
- Clean SSH execution with pluggable executor for testing
- Proper pinned host key verification via `ssh.FixedHostKey`
- Correct write-lock pattern on lifecycle transitions
- Two-resource provisioning (volume + machine) with cleanup on Destroy
- Good test coverage with mocked HTTP + SSH executor

However, the two P1s are blocking:
1. **Heredoc injection** is a real security vulnerability — arbitrary commands can be executed on the remote host via crafted file content
2. **edit_file is non-functional** — it reads but doesn't edit, silently dropping edits
