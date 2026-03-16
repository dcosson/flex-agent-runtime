# h2-agent-runtime

Go runtime foundation for agent orchestration and provider execution.

Current implemented areas:
- `internal/ai`: core message/content types, options, event stream, provider + model registries, stream entry points, message transformation, overflow detection
- `internal/ai/sse`: shared SSE scanner utility
- `ai`: public re-export layer over `internal/ai`

## How it works

1. Providers implement `internal/ai.Provider` and register via `RegisterProvider`.
2. Models are loaded/registered in the model registry (`models/catalog.json` embedded at init).
3. Callers use `Stream`/`StreamSimple` or blocking `Complete`/`CompleteSimple`.
4. Provider streams emit `AssistantMessageEvent` values through `EventStream` with terminal-state guarantees.
5. `TransformMessages` adapts conversations across model/provider seams.
6. `IsContextOverflow` + `ProviderError` classify error/overflow behavior consistently.

## Test policy

There are NO known failures or flakes. Every test must pass, every time. If you discover a flaky or failing test — even one that predates your current work — create a bead to track it and fix it as soon as you finish your current task. Never dismiss a failure as "known" or "pre-existing." Zero tolerance.

## Before committing

Always run `make check` before committing. This runs gofmt, go vet, and staticcheck. All must pass clean — no warnings, no unused code.

Run `make test` to verify tests pass. Use `make test-race` for race detection.

## Make commands

- `make help` — list commands
- `make build` — build all packages
- `make check` — gofmt + vet + staticcheck
- `make test` — quick tests
- `make test-race` — race-enabled tests
- `make test-harness` — harness-focused property/determinism/concurrency tests
- `make test-bench` — benchmark suite
- `make test-fuzz` — short fuzz runs
- `make test-integration` — integration scenario suite
