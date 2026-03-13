# AGENTS Guide

## Project Overview

This repository is the implementation workspace for `h2-agent-runtime`.

Primary package layout:
- `internal/ai`: core runtime contracts and helpers
- `internal/ai/sse`: shared SSE parsing package
- `ai`: public aliases/re-exports for consumers
- `docs/plans`: source planning and harness specs

## Engineering Expectations

- Follow plan-driven development from `docs/plans`.
- Keep interfaces stable and deterministic; avoid implicit behavior drift.
- Preserve concurrency safety (`-race` must stay green).
- Prefer table/property/fuzz tests for cross-provider invariants.

## Build/Test Workflow

- `make check` before commits (fmt, vet, staticcheck)
- `make test` for quick verification
- `make test-race` for concurrency safety
- `make test-harness` for targeted harness properties
- `make test-fuzz` for short fuzz sweeps
- `make test-bench` to monitor regressions

## Current Status

Batch 1 core implementation has landed for:
- `.1` core types + EventStream
- `.2` provider infra + model registry + SSE parser
- `.4` transform + overflow + public API aliases

Future work can extend from these foundations without changing the existing public contracts unless explicitly planned.
