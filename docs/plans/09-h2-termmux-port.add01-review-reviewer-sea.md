# Review: 09-h2-termmux-port.add01 (reviewer-sea)

- Source doc: `docs/plans/09-h2-termmux-port.add01.md`
- Reviewed commit: 50cc41c
- Reviewer: reviewer-sea

## Findings

### P2 - `sub.chunks` channel never closed — dead code in output pump

**Problem**
In §5.3, the output pump goroutine checks for channel closure:
```go
case chunk, ok := <-sub.chunks:
    if !ok {
        errCh <- nil
        return
    }
```
But `sub.chunks` is never closed anywhere in the design. `UnsubscribeTerminal` (§4.3) closes `sub.done` but not `sub.chunks`. The `!ok` path is dead code. The output pump correctly exits via `<-sub.done` or `<-ctx.Done()`, making the closure check misleading.

**Required fix**
Either: (a) have `UnsubscribeTerminal` close `sub.chunks` instead of (or in addition to) `sub.done`, and remove the separate `done` channel — simplifying the design to use channel closure as the cancellation signal; or (b) remove the `, ok` check from the output pump since the channel is never closed, and document that cancellation is exclusively via `sub.done`.

Option (a) is cleaner but requires that `fanOutTerminalOutput` handles send-on-closed-channel panics (e.g., check `sub.done` before sending). Option (b) preserves the current two-channel design.

---

### P2 - No acceptance criteria section

**Problem**
The parent plan (09-h2-termmux-port.md §11) includes explicit acceptance criteria that cross component boundaries. This addendum has a testing section (§9) but no acceptance criteria. Testing describes what to verify internally; acceptance criteria describe what behavior the user/system observes.

**Required fix**
Add an acceptance criteria section. Suggested criteria:
1. A browser client can attach to a running session via WebSocket and see the terminal output rendered in xterm.js with correct colors and cursor positioning.
2. Keystrokes typed in xterm.js are received by the child process with latency <30ms (local deployment).
3. A late-attaching client receives scrollback history and can see prior output without gaps.
4. Multiple browser clients attached simultaneously all receive identical output.
5. Disconnecting one client does not affect other clients or the underlying session.

---

### P2 - Scrollback snapshot size unbounded — could exceed RPC message limits

**Problem**
§4.4 sends the entire `ScrollbackSnapshot()` in the initial `TerminalAttached` message (§5.2). §12.1 notes the default scrollback is 50,000 lines, which "could be several MB." Plan 13 §13.1 sets a 16MB max RPC message size. A long-running session with verbose output could easily approach or exceed this limit.

No cap or streaming approach is proposed for scrollback delivery.

**Required fix**
Specify a maximum scrollback size for the initial attach message (e.g., cap at 1MB or last N lines). If the full scrollback exceeds the cap, deliver the most recent portion. Document whether the client can request additional history via a separate RPC call, or whether the truncated scrollback is sufficient for V1.

---

### P2 - PipeOutput signature change affects parent plan interface

**Problem**
§10.1 recommends Option B: changing `PipeOutput(onData func())` to `PipeOutput(onData func([]byte))`. This changes the `VirtualTerminal` interface defined in plan 09 §3.3, which is an approved plan. The addendum recommends this change but doesn't note it as a cross-plan modification.

**Required fix**
Explicitly note this as a required change to plan 09's VirtualTerminal interface. If both plans are being implemented by different agents, this interface change needs to be coordinated — the parent plan implementer needs to know that PipeOutput's callback signature will change.

---

### P3 - Resize handler passes rows for childRows

**Problem**
In §5.3, the resize handler calls:
```go
session.VT.Resize(int(p.Rows), int(p.Cols), int(p.Rows))
```
Plan 09 §3.3 defines `Resize(rows, cols, childRows int)` with separate `Rows` and `ChildRows` fields. Passing `p.Rows` for both parameters assumes the browser-visible rows always equal the child process rows. This may not be correct — in the existing h2 system, `ChildRows` can differ from `Rows` (e.g., reserving rows for a status bar).

**Required fix**
Clarify the intent. If remote terminal subscribers should always have childRows == rows (since there's no status bar in the browser view), add a comment explaining this. If ChildRows should be configurable independently, add a `child_rows` field to the `TerminalResize` message.

---

### P3 - StreamTerminal service location ambiguous

**Problem**
§5.1 says "Added to the SandboxService or a separate TerminalService" without deciding. This ambiguity could cause coordination issues between this addendum's implementer and plan 13's implementer.

**Required fix**
Pick one. A separate `TerminalService` is likely better since terminal streaming is orthogonal to sandbox tool dispatch. State the decision explicitly.

---

### P3 - Multi-client resize contention not addressed

**Problem**
§6.3 discusses multi-client input contention ("last writer wins") but doesn't address resize contention. Multiple browser tabs at different window sizes will send competing resize messages, causing rapid SIGWINCH storms that may confuse the child process.

**Required fix**
Add a note to §6.3 about resize contention. Options: (a) only allow the first-attached client to resize (primary client), (b) debounce resize messages with a small window (e.g., 100ms), or (c) accept "last writer wins" for resize too but document the consequence. Option (c) is likely fine for V1 with a note.

---

## Summary

7 findings: 0 P0, 0 P1, 4 P2, 3 P3

**Verdict**: Approved with revisions

Well-structured addendum that clearly defines the raw terminal streaming path as a debug/power-user feature. The architecture is clean — subscription ring buffers, bidirectional RPC pumps, and the WebSocket relay decision are all reasonable. The main gaps are: a dead code path in the output pump design, missing acceptance criteria, unbounded scrollback in the attach message, and an interface change to the parent plan that needs coordination. The P3 findings are clarifications and minor spec gaps.
