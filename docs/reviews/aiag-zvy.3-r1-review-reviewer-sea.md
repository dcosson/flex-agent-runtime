# Code Review: aiag-zvy.3 + aiag-glo.3 (R1, reviewer-sea)

- Beads: aiag-zvy.3 (Sandbox Host wiring), aiag-glo.3 (ConnectRPC transport + policy interceptors)
- Commit range: 2ca2434..93172a6
- Plan docs: docs/plans/11-sandbox-host-service.md, docs/plans/13-rpc-layer.md
- Reviewer: reviewer-sea
- Review commit: 93172a6

## Findings

### P1 - Race condition: concurrent stream.Send in streamTerminal goroutine vs connection teardown

**Location:** `internal/rpc/transport/server.go:206-231` (goroutine) vs `server.go:233-264` (main loop)

**Problem**
In `streamTerminal`, a goroutine is spawned that calls `stream.Send` to push output chunks and detached messages. Meanwhile, the main goroutine runs the receive loop. When the main goroutine exits (e.g., client sends EOF on line 237, causing `return nil` on line 238), the function returns, triggering `defer session.UnsubscribeTerminal(subID)`. The spawned goroutine may still be executing a `stream.Send` call at that point. ConnectRPC's `BidiStream` does not guarantee that `Send` is safe to call after the handler function returns (the underlying HTTP connection is being torn down).

Additionally, there is no synchronization to ensure the goroutine has exited before the function returns. If the main loop receives EOF and returns, the goroutine is leaked until the context is cancelled or `sub.Done` closes.

**Suggested fix**
Add a `cancel` context derived from the handler's context. When the main receive loop exits, cancel it, then wait for the goroutine to finish via a `sync.WaitGroup` or by reading from the `done` channel before returning. For example:

```go
ctx, cancel := context.WithCancel(ctx)
defer cancel()
// ... spawn goroutine ...
defer func() { <-done }() // wait for goroutine exit
```

This ensures the goroutine stops before the handler returns and the stream is invalidated.

---

### P2 - connectrpc.com/connect listed as indirect dependency in go.mod

**Location:** `go.mod:14`

**Problem**
The commit adds `connectrpc.com/connect v1.19.1 // indirect` to the `require` block, but `internal/rpc/transport` directly imports it in multiple files (`server.go`, `client.go`, `errors.go`, `interceptor.go`). A `go mod tidy` would move it to the direct requirements block. The `// indirect` annotation is incorrect and may confuse tooling or developers inspecting the dependency graph.

**Suggested fix**
Run `go mod tidy` to correctly classify the dependency as direct, or manually move it to the direct `require` block.

---

### P2 - Auth token read from environment on every request in sandbox-host main.go

**Location:** `cmd/sandbox-host/main.go:72-81`

**Problem**
The `authHook` closure calls `os.Getenv("SANDBOX_HOST_AUTH_TOKEN")` on every incoming RPC request. This is functionally correct but has two issues:

1. **Performance:** `os.Getenv` acquires a lock internally on every call. Under high concurrency, this adds unnecessary contention on the env lock for every single RPC.
2. **Surprising behavior:** The auth token can change at runtime by modifying the environment variable, which is either an unintended side-effect or an undocumented feature. Config should be loaded once at startup for predictability.

**Suggested fix**
Read `SANDBOX_HOST_AUTH_TOKEN` once at startup (e.g., as a field on `Config`) and capture the value in the closure:

```go
authToken := cfg.AuthToken // loaded once at startup
authHook := func(_ context.Context, _ string, headers http.Header) error {
    if authToken == "" {
        return nil
    }
    ...
}
```

---

### P2 - Client-side AuthHook repurposed for setting headers, but semantics are misleading

**Location:** `internal/rpc/transport/client.go:86-94` (HeaderTokenAuth), `client.go:121-129` (apply)

**Problem**
The `AuthHook` type is defined as `func(ctx context.Context, procedure string, headers http.Header) error` and is used on the server side to *validate* incoming auth headers. On the client side, the same `AuthHook` type is repurposed by `HeaderTokenAuth` to *set* outgoing auth headers. This dual use of the same type for opposite operations (validation vs. injection) is confusing. The client `apply` method calls `AuthHook` to mutate request headers, but the type name and server-side usage imply verification/rejection.

Furthermore, `WrapStreamingClient` silently discards the error from `i.apply` (line 113: `_ = i.apply(...)`) for streaming clients, which means auth hook errors on streaming connections are silently swallowed on the client side.

**Suggested fix**
Either:
- Define a separate `ClientAuthHook` type for header injection that does not return an error (since the client is constructing the request, not validating it), or
- At minimum, do not discard the error in `WrapStreamingClient` -- log it or propagate it.

---

### P2 - versionLessThan uses fragile hand-rolled parser

**Location:** `internal/rpc/transport/interceptor.go:85-98`

**Problem**
The `versionLessThan` function parses version strings by manually iterating characters. It only handles the leading integer after an optional "v" prefix. This means:
- `"v1.2"` parses as `1` (silently ignores `.2`)
- `"v10a"` parses as `10` (silently ignores `a`)
- `""` parses as `0`
- Non-numeric versions like `"beta"` parse as `0`

While the current use is limited to simple `"v1"`, `"v2"` strings, the plan doc (Section 4.3.1) describes versions as "monotonically increasing integer" but uses the `"v1"` format with a prefix. If any multi-part version is ever used, this parser will silently produce wrong results rather than failing.

**Suggested fix**
Use `strconv.Atoi` after trimming the `"v"` prefix, and return an error or treat parse failures as version `0` with a log warning. This is more robust and clearer about what it supports:

```go
func versionLessThan(a, b string) bool {
    parse := func(v string) int {
        n, _ := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(v), "v"))
        return n
    }
    return parse(a) < parse(b)
}
```

---

### P2 - Rate-limited input silently dropped without client notification

**Location:** `internal/rpc/transport/server.go:246-248`

**Problem**
When the input rate limiter denies a write (`!limiter.Allow(len(msg.Input.Data))`), the input is silently discarded with `continue`. The client has no way to know their input was dropped. For a terminal session, this could lead to confusing behavior where keystrokes or pasted text is silently lost without any feedback.

**Suggested fix**
Consider sending an error or warning message back to the client via `stream.Send` with a rate-limit notification, or at minimum add a structured log entry so operators can diagnose dropped input issues. The plan mentions "fast-fail overload signaling" (Section 13, Extreme Optimization point 3), so notifying the client aligns with the plan intent.

---

### P3 - Terminal subscription ID not guaranteed unique

**Location:** `internal/rpc/transport/server.go:195`

**Problem**
The subscriber ID is constructed as `"rpc-" + first.Attach.SessionID + "-" + time.Now().Format("150405.000000000")`. While `time.Now()` has nanosecond precision in the format, two rapid concurrent attach calls could theoretically produce the same timestamp. The `termmux.SubscribeTerminal` API uses this as a map key, so a collision would overwrite the previous subscription.

**Suggested fix**
Add an atomic counter or random suffix to guarantee uniqueness:

```go
var subCounter atomic.Int64
subID := fmt.Sprintf("rpc-%s-%d", first.Attach.SessionID, subCounter.Add(1))
```

---

### P3 - Missing test for auth rejection path

**Location:** `internal/rpc/transport/server_test.go`

**Problem**
`TestTransportUnaryVersionAndAuth` tests the happy path (valid auth + valid version), and `TestTransportVersionRejected` tests version rejection. There is no test for auth rejection -- i.e., sending a request with invalid or missing auth credentials and verifying the server returns `CodeUnauthenticated`. The auth hook is configured in the first test but only the success path is exercised.

**Suggested fix**
Add a test case that sends a request without the auth token (or with a wrong token) and verifies:
- The response error code is `connect.CodeUnauthenticated`
- The response body does not contain any service data

---

### P3 - No test coverage for ExecuteToolStream or AgentEvent stream through transport

**Location:** `internal/rpc/transport/server_test.go`

**Problem**
The test file covers unary RPCs (HealthCheck) and the terminal bidi stream, but does not test the `ExecuteToolStream` server-streaming handler or the `AgentEvent` server-streaming handler through the transport layer. These are important paths that bridge the in-process stream receivers to ConnectRPC server streams.

The stream bridging logic in `server.go:122-145` (ExecuteToolStream) and `server.go:146-168` (EventsStream) has its own error handling (EOF detection, `toConnectError` wrapping) that is only tested indirectly via the in-process RPC server tests.

**Suggested fix**
Add transport-level tests for:
1. `ExecuteToolStream`: create a session, execute a tool, receive streamed progress + response
2. `AgentEvent stream`: publish events, verify they arrive via the ConnectRPC stream

---

### P3 - Server.Handler() creates new mux on every call

**Location:** `internal/rpc/transport/server.go:43`

**Problem**
`Handler()` constructs a new `http.ServeMux` and registers all handlers every time it is called. While this is only called once in `main.go`, there is no documentation or enforcement that it should only be called once. Repeated calls waste allocations and could lead to subtle bugs if different `Handler()` return values are used in different contexts.

**Suggested fix**
Either cache the handler on first call (lazy init with `sync.Once`), or document that `Handler()` should only be called once and rename it to something like `BuildHandler()` to signal it constructs rather than returns.

---

### P3 - No config validation in LoadConfig or main

**Location:** `cmd/sandbox-host/config.go:32-55`, `cmd/sandbox-host/main.go:20-116`

**Problem**
`LoadConfig` parses flags and environment variables but performs no validation. Critical fields like `PoolName`, `BasesDataset`, and `SessionsDataset` default to empty strings. If the operator forgets to set them, the binary will start and then fail with confusing errors deep in the ZFS or sandbox service code. The plan doc (Section 11) emphasizes robust binary wiring.

**Suggested fix**
Add a `Validate() error` method on `Config` that checks required fields are set and returns descriptive error messages. Call it immediately after `LoadConfig()` in `main()`.

---

### P3 - TerminalService nil passed in main.go but no SessionManager is instantiated

**Location:** `cmd/sandbox-host/main.go:82`

**Problem**
The `transport.NewServer` call passes `nil` for the `terms *termmux.SessionManager` parameter. While the server correctly handles this by returning `CodeUnimplemented` for terminal stream requests, the sandbox-host binary wiring appears incomplete -- there is no code path to create and pass a `SessionManager`. This means the terminal service is effectively disabled in the production binary with no way to enable it via config.

This may be intentional if terminal support is coming in a later bead, but it should be documented.

**Suggested fix**
Either:
- Add a config flag to enable terminal support and create a `SessionManager` when enabled, or
- Add a comment in `main.go` noting that terminal service wiring is deferred to a future bead

---

## Overall Assessment

This commit implements the ConnectRPC transport layer (`internal/rpc/transport`) and wires the `cmd/sandbox-host` binary to use real service implementations. The implementation is well-structured and covers the major pieces specified in the plan:

**Plan compliance (13-rpc-layer.md):**
- ConnectRPC transport wiring with JSON codec: Implemented
- API version header enforcement (x-api-version): Implemented via `policyInterceptor`
- Auth hook interceptor: Implemented for both server and client
- Max message size enforcement: Implemented via `connect.WithReadMaxBytes`/`WithSendMaxBytes`
- Server-streaming for ExecuteToolStream and AgentEvents: Implemented
- Bidi streaming for TerminalService: Implemented with rate limiting and chunk splitting
- Error mapping from internal RPC errors to Connect codes: Implemented
- Client-side transport adapters: Implemented (`SandboxClient`, `EventClient`, `TerminalClient`)

**Plan compliance (11-sandbox-host-service.md):**
- Config loading from flags + env: Implemented
- ZFS + gVisor manager instantiation: Implemented
- SandboxHostService + RPC server wiring: Implemented
- Graceful SIGTERM shutdown with HTTP drain: Implemented
- Auth token support: Implemented

**Code quality:**
- Clean separation of concerns across files (procedures, codec, errors, interceptor, ratelimit, server, client)
- Tests pass with `-race` (3/3 pass)
- staticcheck clean on changed packages
- Proper use of ConnectRPC generics API

**Areas for improvement:**
- The goroutine lifecycle in `streamTerminal` needs synchronization (P1)
- Test coverage should be expanded to cover streaming RPCs and error paths
- Config validation is missing
- The `AuthHook` dual-use pattern is confusing

## Summary

12 findings: 0 P0, 1 P1, 5 P2, 6 P3

**Verdict**: Approved with required changes (P1 must be addressed before merge)
