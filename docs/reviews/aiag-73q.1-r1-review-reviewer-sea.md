# Code Review: aiag-73q.1 (R1, reviewer-sea)

- Bead: aiag-73q.1
- Commit range: 52483cc..9427201
- Plan doc: docs/plans/17-external-testing.md (Section 12)
- Reviewer: reviewer-sea
- Review commit: 9427201

## Findings

### P2 - NewHandler leaks captured requests with no way to access or clear them

**Location:** `internal/ai/testutil/stubserver/stubserver.go:106-109`

**Problem**
`NewHandler()` creates an internal `*Server` but only returns the `http.Handler`. The `configureHandler` closure captures every request (body read via `io.ReadAll`, headers cloned, appended to `s.requests`) but the `*Server` is never returned to the caller. This means:

1. Captured requests accumulate forever with no way to clear them (memory leak).
2. The capture work (body read, header clone, mutex lock, append) runs on every request but serves no purpose since the caller can't call `Requests()`.

For the standalone binary (`cmd/stubserver/main.go`), which runs as a long-lived Docker service, this is unbounded memory growth proportional to request count.

**Suggested fix**
Add a `skipCapture` flag or separate code path in `configureHandler` so that `NewHandler` skips request capture entirely. The simplest approach:

```go
func NewHandler(opts ...Option) http.Handler {
    s := &Server{}
    for _, opt := range opts {
        opt(s)
    }
    if s.handler == nil {
        s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            http.Error(w, "no handler configured", http.StatusInternalServerError)
        })
    }
    return s.handler // No capture wrapper — caller has no *Server to read from anyway
}
```

This also avoids the unnecessary `io.ReadAll(r.Body)` in `NewHandler` which consumes the request body before the actual handler sees it — though in the current code the inner handler doesn't read the body again, so it's not broken, just wasteful.

---

### P3 - Tool-call fixture uses single input_json_delta event

**Location:** `testdata/fixtures/anthropic-tool-call.sse:8`

**Problem**
The tool-call fixture delivers the entire JSON input in a single `input_json_delta` event:
```
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Seattle\"}"}}
```

Real Anthropic responses typically split tool input across multiple delta events (e.g., `"{"`, `"\"city\""`, `": "`, `"\"Seattle\""`, `"}"`) to exercise incremental JSON accumulation in the parser. A single-chunk fixture doesn't test the streaming JSON reassembly path that providers implement.

**Suggested fix**
Split the `input_json_delta` into 2-3 events to exercise the incremental accumulation path. For example:
```
event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"city\": \"Seattle\"}"}}
```

---

## Summary

2 findings: 0 P0, 0 P1, 1 P2, 1 P3

**Verdict**: Approved with revisions

The implementation is clean and well-structured. `NewHandler` correctly addresses the plan review finding about `Server` not implementing `http.Handler` — the refactoring to extract `configureHandler` and share it between `New` and `NewHandler` is the right approach. The standalone binary properly uses an `http.ServeMux` to route `/health` separately from the stubserver handler, reads the fixture directory from both flag and env var (addressing the plan review finding), and handles the default fixture fallback sensibly. The Dockerfile is minimal and correct (Go version matches `go.mod`, alpine matches the sandbox-host pattern, healthcheck is wired). Docker compose service is correctly added to all three profiles. The three fixture files are valid Anthropic SSE and cover the core scenarios (simple text, tool use, post-tool response). Tests cover default handler, fixture selection via X-Fixture header, and fault injection through `NewHandler`.

The P2 (memory leak via captured requests in `NewHandler`) should be fixed before the stubserver runs as a long-lived Docker service.
