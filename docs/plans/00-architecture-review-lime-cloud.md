# Review: 00-architecture (lime-cloud)

- Source doc: `docs/plans/00-architecture.md`
- Reviewed commit: cc2664e
- Reviewer: lime-cloud

## Findings

### P1 - Provider/model global registries are unsynchronized mutable maps

**Problem**
The architecture specifies package-level mutable registries (`var providerRegistry = map[string]Provider{}` with register/get/unregister APIs) but does not define synchronization or immutability guarantees ([docs/plans/00-architecture.md:238](docs/plans/00-architecture.md:238), [docs/plans/00-architecture.md:241](docs/plans/00-architecture.md:241), [docs/plans/00-architecture.md:245](docs/plans/00-architecture.md:245), [docs/plans/00-architecture.md:645](docs/plans/00-architecture.md:645)). In Go, concurrent reads+writes on maps panic/race. This will be hit by parallel tests, runtime provider registration, or dynamic model updates.

**Required fix**
Specify an explicit concurrency contract for registries (e.g., `sync.RWMutex`-guarded maps or copy-on-write immutable snapshots with atomic pointer swap), including whether registration is startup-only or runtime-safe. Require race-detector coverage for register/get/unregister under concurrent access.

---

### P1 - Agent state mutation and loop execution concurrency model is undefined

**Problem**
`AgentState` is mutable and includes streaming lifecycle fields (`IsStreaming`, `StreamMessage`, `PendingToolCalls`) while the public API exposes mutators and queue operations (`Prompt`, `Continue`, `Abort`, `SetModel`, `SetTools`, `ReplaceMessages`, `Steer`, `FollowUp`) with no thread-safety/serialization rules ([docs/plans/00-architecture.md:381](docs/plans/00-architecture.md:381), [docs/plans/00-architecture.md:387](docs/plans/00-architecture.md:387), [docs/plans/00-architecture.md:501](docs/plans/00-architecture.md:501), [docs/plans/00-architecture.md:512](docs/plans/00-architecture.md:512), [docs/plans/00-architecture.md:521](docs/plans/00-architecture.md:521)). Without a defined execution model, concurrent callers can race with the running loop and violate invariants (duplicate turns, missed abort, stale state snapshots).

**Required fix**
Define one concrete concurrency architecture for `Agent`: either a single-threaded internal event loop (command mailbox) or lock-based state machine with strict lock ordering. Document allowed concurrent method calls, linearization guarantees, and behavior of mutators while streaming. Add stress tests (`-race`) for concurrent `Prompt/Abort/Steer/Set*` interleavings.

---

### P1 - `AgentMessage`/`piai.Message` boundary is underspecified and risks lossy conversions

**Problem**
The doc introduces a second message interface (`AgentMessage`) and states standard `piai` messages satisfy it "through a wrapper or direct method," while conversion is delegated to `ConvertToLLM` ([docs/plans/00-architecture.md:355](docs/plans/00-architecture.md:355), [docs/plans/00-architecture.md:361](docs/plans/00-architecture.md:361), [docs/plans/00-architecture.md:489](docs/plans/00-architecture.md:489)). This is not concrete enough to implement safely: custom message types, unknown roles, metadata retention, and round-trip behavior are unspecified. Different implementations could silently drop data or produce incompatible context sent to providers.

**Required fix**
Pin a canonical interoperability contract: exact built-in `AgentMessage` variants, adapter rules to/from `piai.Message`, and required handling for unknown/custom messages (error vs passthrough vs projection). Specify round-trip invariants and add golden tests for conversion fidelity across tool-call turns and provider switches.

---

### P2 - Context overflow handling via string pattern matching is too brittle

**Problem**
The error strategy says context overflow is detected by matching provider error strings ([docs/plans/00-architecture.md:728](docs/plans/00-architecture.md:728)). This is fragile across provider API versions, localization, and compatible backends (Groq/Mistral-style OpenAI APIs), creating false negatives and inconsistent compaction behavior.

**Required fix**
Define provider-normalized typed error codes (`ErrContextOverflow`, etc.) mapped from structured status/code fields where available, with string fallback only as last resort and covered by fixtures. Include conformance tests that replay representative provider error payloads.

---

## Summary

4 findings: 0 P0, 3 P1, 1 P2, 0 P3

**Verdict**: Approved with revisions
