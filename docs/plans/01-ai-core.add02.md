# 01-ai-core Addendum 02: Demo CLIs for Chat + Embeddings

**Status:** Complete
**Parent plan:** [01-ai-core](./01-ai-core.md)
**Depends on:** [01-ai-core.add01](./01-ai-core.add01.md), provider implementations in 02/03/04
**Implements:** `demos/llm-demo`, `demos/embedding-demo`, demo build targets in `Makefile`

---

## 1. Overview

This addendum introduces two intentionally small CLI demos that exercise real provider paths end-to-end without the full agent/runtime stack:

1. `llm-demo`: interactive stdin chat loop that supports LLM tool use with a calculator tool.
2. `embedding-demo`: embeds a query and multiple texts, then ranks texts by cosine similarity.

These demos are for manual validation and developer smoke testing, not product UX.

---

## 2. Goals

- Provide a direct, low-friction path to test chat + tool-use flow against real APIs.
- Provide a direct path to test embedding provider registration, batching, and normalized response handling.
- Keep implementation minimal and readable so future adapters can follow the same wiring pattern.

---

## 3. `llm-demo` Design (`demos/llm-demo`)

### 3.1 Runtime Flow

1. Parse flags/env (`provider`, `model`, `timeout`).
2. Register chat providers (Anthropic/OpenAI/Google) from env keys/base URLs.
3. Resolve model from catalog (with small fallback defaults for providers not yet cataloged).
4. Read user prompt from stdin.
5. Build `ai.Context` with transformed history + calculator tool schema.
6. Call `ai.StreamSimple(...)`.
7. If assistant emits tool calls:
   - execute calculator tool locally,
   - append `ToolResultMessage`,
   - call model again.
8. Print assistant text response, keep conversation in-memory.

### 3.2 Tool Contract

Tool name: `calculator`

Input schema:
- `expression` (string)

Supported syntax:
- numbers, parentheses, `+ - * /`, unary `+/-`

Error behavior:
- malformed expression and divide-by-zero return tool error text back into conversation.

---

## 4. `embedding-demo` Design (`demos/embedding-demo`)

### 4.1 Runtime Flow

1. Parse flags/env (`-query`, repeatable `-text`, `-model`, `-timeout`).
2. If `-text` is omitted and stdin is piped, read newline-delimited texts from stdin.
3. Register embedding providers (OpenAI/Google/Cohere) from env keys/base URLs.
4. Call `ai.Embed(...)` with `texts + query` in one request.
5. Split response vectors into document vectors and query vector.
6. Compute cosine similarity for each document vs query.
7. Sort descending and print ranked results with scores.

### 4.2 Similarity Metric

- Cosine similarity over float vectors returned by normalized embedding response.
- Stable descending sort to preserve deterministic ordering for equal scores.

---

## 5. Build Surface

`Makefile` adds:

- `build-llm-demo`: `go build -o bin/llm-demo ./demos/llm-demo`
- `build-embedding-demo`: `go build -o bin/embedding-demo ./demos/embedding-demo`

Help output includes both targets.

---

## 6. Acceptance Criteria

- `demos/llm-demo` compiles and can complete a multi-turn conversation with calculator tool-use against configured provider.
- `demos/embedding-demo` compiles and ranks input texts by similarity to query using configured embedding model.
- Demo code includes focused unit tests for arithmetic evaluation and cosine/ranking helpers.
- `Makefile help` documents both new build targets.
