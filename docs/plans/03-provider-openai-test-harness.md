# 03: OpenAI Provider — Test Harness

**Companion to:** [03-provider-openai.md](./03-provider-openai.md)
**Scope:** High-assurance validation beyond ordinary unit tests for OpenAI provider behavior.

---

## Harness Structure

1. Stub server tests (correctness replay + fault injection) in one server-backed test binary.
2. Property/fuzz tests (pure functions, no server).
3. Comparison oracles (cross-implementation, no server).
4. Live smoke tests (real API, weekly, gated).

The stub server is a shared utility (e.g., `internal/ai/testutil/stubserver/`) with provider-specific fixture sets.
Recorded fixture interactions are captured from real APIs (initially via pi-mono clients), sanitized, and served over HTTP for end-to-end client validation.

---

## 1. Property-Based Tests

### P1. Stream Event Ordering and Terminal Uniqueness

**Invariant:**
- Event order emitted by provider matches OpenAI SSE order constraints.
- Exactly one terminal event (`done` or `error`) appears.
- `EventStream.Result()` returns exactly once and never blocks.

**Generator:**
- Produce valid synthetic OpenAI chunk sequences including text deltas, tool call deltas (with varying index values), reasoning deltas, usage chunks, and `[DONE]` sentinel.

**Checks:**
- No out-of-order content index updates.
- All tool call indices seen in deltas appear in final tool calls.
- Terminal event fires after `[DONE]` or error.

```go
func TestStreamEventOrdering(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        // Generate a valid OpenAI chunk sequence
        chunks := generateValidChunkSequence(t)
        server := newMockSSEServer(chunks)
        defer server.Close()

        p := openai.New("test-key", openai.WithBaseURL(server.URL))
        es := p.Stream(context.Background(), testModel(), ai.Context{}, ai.StreamOptions{})

        var events []ai.AssistantMessageEvent
        for e := range es.C {
            events = append(events, e)
        }
        msg, err := es.Result()

        // Exactly one terminal event
        terminalCount := 0
        for _, e := range events {
            if e.Type == ai.EventDone || e.Type == ai.EventError {
                terminalCount++
            }
        }
        assert.Equal(t, 1, terminalCount)

        if err == nil {
            assert.NotNil(t, msg)
        }
    })
}
```

### P2. Tool JSON Delta Convergence

**Invariant:**
- For any JSON object `J`, splitting `J` into arbitrary chunks and feeding them via `delta.tool_calls[0].function.arguments` yields final parsed args equal to `J`.

**Generator:**
- Random JSON objects with depth/array/string edge cases.
- Random chunk boundaries (including 1-byte chunks).

**Checks:**
- Zero panics.
- Final `toolcall_end` args deep-equal original JSON object.

```go
func TestToolJSONDeltaConvergence(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        // Generate random JSON object
        obj := generateRandomJSONObject(t)
        objBytes, _ := json.Marshal(obj)
        objStr := string(objBytes)

        // Split into random chunks
        chunks := splitIntoRandomChunks(t, objStr)

        // Build SSE sequence: initial chunk with id+name, then argument deltas
        sseChunks := buildToolCallSSESequence("call_123", "test_tool", chunks)
        server := newMockSSEServer(sseChunks)
        defer server.Close()

        p := openai.New("test-key", openai.WithBaseURL(server.URL))
        es := p.Stream(context.Background(), testModel(), ai.Context{}, ai.StreamOptions{})

        var finalToolCall *ai.ToolCall
        for e := range es.C {
            if e.Type == ai.EventToolCallEnd {
                finalToolCall = e.ToolCall
            }
        }

        require.NotNil(t, finalToolCall)
        assert.Equal(t, obj, finalToolCall.Arguments)
    })
}
```

### P3. Usage/Cost Arithmetic Consistency

**Invariant:**
- `usage.Cost.Total == Input + Output + CacheRead + CacheWrite` within epsilon.

**Generator:**
- Random valid token counts, randomized model prices.

**Checks:**
- Deterministic output.
- Non-negative costs.
- Total equals sum of components.

### P4. Multi-Tool Index Isolation

**Invariant:**
- When multiple tool calls are streamed with different indices, each tool call's arguments are accumulated independently. No cross-contamination between indices.

**Generator:**
- Random number of tool calls (2-5), each with random JSON arguments, interleaved in random order.

**Checks:**
- Each finalized tool call has the correct ID, name, and arguments.
- Order of `toolcall_end` events matches index order.

```go
func TestMultiToolIndexIsolation(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        numTools := rapid.IntRange(2, 5).Draw(t, "numTools")
        toolDefs := make([]struct {
            id   string
            name string
            args map[string]any
        }, numTools)

        for i := range toolDefs {
            toolDefs[i].id = fmt.Sprintf("call_%d", i)
            toolDefs[i].name = fmt.Sprintf("tool_%d", i)
            toolDefs[i].args = generateRandomJSONObject(t)
        }

        // Build interleaved SSE chunks
        chunks := buildInterleavedToolCallSequence(t, toolDefs)
        server := newMockSSEServer(chunks)
        defer server.Close()

        p := openai.New("test-key", openai.WithBaseURL(server.URL))
        es := p.Stream(context.Background(), testModel(), ai.Context{}, ai.StreamOptions{})

        finalCalls := make(map[int]*ai.ToolCall)
        for e := range es.C {
            if e.Type == ai.EventToolCallEnd {
                finalCalls[e.ContentIndex] = e.ToolCall
            }
        }

        assert.Equal(t, numTools, len(finalCalls))
        for i, def := range toolDefs {
            tc := finalCalls[i]
            require.NotNil(t, tc, "missing tool call at index %d", i)
            assert.Equal(t, def.id, tc.ID)
            assert.Equal(t, def.name, tc.Name)
            assert.Equal(t, def.args, tc.Arguments)
        }
    })
}
```

### P5. ModelCompat Flag Independence

**Invariant:**
- Each `ModelCompat` flag affects only its specified behavior. Toggling one flag does not change behavior controlled by another flag.

**Generator:**
- Random boolean combinations of all `ModelCompat` flags.
- Fixed test prompt and messages.

**Checks:**
- Request body field presence/absence matches flag state.
- Message role names match flag state.
- No unexpected interactions between flags.

### P6. Error Classification Stability

**Invariant:**
- Known OpenAI error forms always map to expected `ProviderErrorCode`.
- Unknown errors never misclassify as known typed codes.
- Compat endpoint error formats (Cerebras 400, Mistral 413) are correctly classified.

---

## 2. Stub Server Tests (Correctness Replay + Fault Injection)

All server-backed provider tests use one stub server with two modes:
- Correctness mode: deterministic fixture replay over HTTP.
- Fault mode: transport/protocol failures (TCP reset, malformed payloads, rate limiting, backpressure, partial streams, connection drops).

### S1. Correctness Replay Mode

- Serve OpenAI/compat SSE fixture transcripts over HTTP from the stub server.
- Assert emitted event sequence/final result against expected outputs while exercising full client stack (headers/auth/timeouts).

### F1. Mid-Stream TCP Reset

- Inject network disconnect after N SSE events.
- Expect terminal `error` event and no goroutine leaks.

```go
func TestMidStreamDisconnect(t *testing.T) {
    for _, cutAfter := range []int{0, 1, 5, 50} {
        t.Run(fmt.Sprintf("cutAfter=%d", cutAfter), func(t *testing.T) {
            server := newDisconnectingServer(cutAfter)
            defer server.Close()

            p := openai.New("test-key", openai.WithBaseURL(server.URL))
            es := p.Stream(context.Background(), testModel(), ai.Context{}, ai.StreamOptions{})

            for range es.C {} // drain
            _, err := es.Result()
            assert.Error(t, err)
        })
    }
}
```

### F2. Malformed SSE Payload

- Corrupt random event JSON payload (truncate, inject garbage, null bytes).
- Expect typed parse failure path; stream closes safely.

```go
func TestMalformedPayload(t *testing.T) {
    malformed := []string{
        `{"choices": [{"delta": {"content": "ok"`,        // truncated JSON
        `not json at all`,                                  // garbage
        `{"choices": null}`,                                // null choices
        `{"choices": [{"delta": null}]}`,                   // null delta
        `{"choices": [{"index": -1}]}`,                     // negative index
        "",                                                  // empty
    }

    for _, data := range malformed {
        t.Run(data[:min(20, len(data))], func(t *testing.T) {
            server := newMockSSEServer([]string{data})
            defer server.Close()

            p := openai.New("test-key", openai.WithBaseURL(server.URL))
            es := p.Stream(context.Background(), testModel(), ai.Context{}, ai.StreamOptions{})

            for range es.C {} // drain — must not panic
            es.Result()       // must not block
        })
    }
}
```

### F3. API Throttling Storm

- Return bursts of HTTP 429 with `Retry-After` header.
- Validate stable `rate_limit` error mapping.
- Provider does not retry (caller/orchestrator responsibility).

### F4. Slow-Consumer Backpressure

- Consumer intentionally sleeps between reads.
- Verify provider does not deadlock; closes cleanly when context expires.

```go
func TestSlowConsumer(t *testing.T) {
    server := newLargeStreamServer(1000) // 1000 events
    defer server.Close()

    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()

    p := openai.New("test-key", openai.WithBaseURL(server.URL))
    es := p.Stream(ctx, testModel(), ai.Context{}, ai.StreamOptions{})

    for e := range es.C {
        _ = e
        time.Sleep(10 * time.Millisecond) // deliberately slow
    }
    // Must complete (context cancel or drain) — not hang
}
```

### F5. Context Cancellation Races

- Cancel context at random points in stream lifecycle.
- Ensure no double-close and no send-on-closed-channel panic.

```go
func TestContextCancellationRaces(t *testing.T) {
    for i := 0; i < 100; i++ {
        t.Run(fmt.Sprintf("iter-%d", i), func(t *testing.T) {
            t.Parallel()
            server := newLargeStreamServer(100)
            defer server.Close()

            ctx, cancel := context.WithCancel(context.Background())
            p := openai.New("test-key", openai.WithBaseURL(server.URL))
            es := p.Stream(ctx, testModel(), ai.Context{}, ai.StreamOptions{})

            // Cancel at random point
            go func() {
                time.Sleep(time.Duration(rand.Intn(50)) * time.Millisecond)
                cancel()
            }()

            for range es.C {} // must not panic
            es.Result()       // must not block
        })
    }
}
```

### F6. Non-2xx Response Without Body

- OpenAI-compatible endpoints sometimes return error status codes with empty body.
- Verify error classification still works (falls back to status code).

```go
func TestEmptyErrorBody(t *testing.T) {
    for _, status := range []int{400, 401, 429, 500, 502, 503} {
        t.Run(fmt.Sprintf("status-%d", status), func(t *testing.T) {
            server := newStatusCodeServer(status, "")
            defer server.Close()

            p := openai.New("test-key", openai.WithBaseURL(server.URL))
            es := p.Stream(context.Background(), testModel(), ai.Context{}, ai.StreamOptions{})

            for range es.C {}
            _, err := es.Result()
            assert.Error(t, err)
        })
    }
}
```

---

## 3. Comparison/Oracle Tests (No Server)

### O1. Cross-Implementation Oracle (pi-ai TS)

- Run the same canonical prompt corpus through this Go provider and pi-ai TypeScript OpenAI provider.
- Compare semantic invariants (event type sequence shape, stop-reason category, tool-call structure, usage envelope), not exact text.

### O2. Cross-Provider Semantic Consistency (nightly)

- For a canonical prompt set, run the same prompt through both Anthropic and OpenAI providers.
- Compare semantic structure (not content): stop reason category, tool call count, usage shape.
- Purpose: detect drift in our event mapping between providers.

### O3. Compat Endpoint Request Format Verification

- For each compat endpoint (Groq, Mistral, Together), capture the request body sent by the provider and verify it matches the endpoint's expected format.
- Use `httptest.Server` to inspect request bodies.

```go
func TestCompatRequestFormat(t *testing.T) {
    cases := []struct {
        name     string
        model    ai.Model
        checkReq func(t *testing.T, req chatRequest)
    }{
        {
            name:  "groq_no_store",
            model: groqModel(),
            checkReq: func(t *testing.T, req chatRequest) {
                assert.Nil(t, req.Store, "Groq should not include store")
                assert.Nil(t, req.ReasoningEffort, "Groq has no reasoning")
            },
        },
        {
            name:  "mistral_tool_ids",
            model: mistralModel(),
            checkReq: func(t *testing.T, req chatRequest) {
                // Verify Mistral tool call ID format in assistant messages
                for _, msg := range req.Messages {
                    for _, tc := range msg.ToolCalls {
                        assert.Len(t, tc.ID, 9, "Mistral requires 9-char IDs")
                    }
                }
            },
        },
        {
            name:  "openai_developer_role",
            model: o3Model(),
            checkReq: func(t *testing.T, req chatRequest) {
                assert.Equal(t, "developer", req.Messages[0].Role)
                assert.NotEmpty(t, req.ReasoningEffort)
            },
        },
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            var capturedReq chatRequest
            server := newRequestCapturingServer(&capturedReq)
            defer server.Close()

            p := openai.New("test-key", openai.WithBaseURL(server.URL))
            es := p.Stream(context.Background(), tc.model,
                ai.Context{System: "test system"},
                ai.StreamOptions{})
            for range es.C {}

            tc.checkReq(t, capturedReq)
        })
    }
}
```

---

## 4. Deterministic Simulation Tests

### S1. Event Transition Validator

- Model OpenAI chunk transitions as a finite-state automaton.
- Legal traces: initial role delta → content deltas → finish_reason → usage → [DONE].
- Illegal traces: tool call delta before role, duplicate [DONE], finish_reason before content starts.
- Legal traces must parse to completion. Illegal traces must fail deterministically.

### S2. Multi-Tool Interleaved Deltas

- Simulate responses with 3+ tool calls whose argument deltas arrive interleaved (tool 0 chunk, tool 2 chunk, tool 1 chunk, tool 0 chunk, ...).
- Validate per-index parser isolation and correct final arguments.

```go
func TestInterleavedToolDeltas(t *testing.T) {
    // Define 3 tool calls with known arguments
    tools := []struct {
        id   string
        name string
        args string
    }{
        {"call_0", "read_file", `{"path":"main.go"}`},
        {"call_1", "write_file", `{"path":"out.txt","content":"hello"}`},
        {"call_2", "bash", `{"command":"go build ./..."}`},
    }

    // Build interleaved chunks: [0-init, 1-init, 2-init, 0-arg, 2-arg, 1-arg, 0-arg, 1-arg, 2-arg]
    chunks := buildInterleavedChunks(tools)
    server := newMockSSEServer(chunks)
    defer server.Close()

    p := openai.New("test-key", openai.WithBaseURL(server.URL))
    es := p.Stream(context.Background(), testModel(), ai.Context{}, ai.StreamOptions{})

    finalCalls := make(map[string]*ai.ToolCall)
    for e := range es.C {
        if e.Type == ai.EventToolCallEnd {
            finalCalls[e.ToolCall.ID] = e.ToolCall
        }
    }

    assert.Equal(t, 3, len(finalCalls))
    for _, tool := range tools {
        tc := finalCalls[tool.id]
        require.NotNil(t, tc)
        assert.Equal(t, tool.name, tc.Name)

        var expected map[string]any
        json.Unmarshal([]byte(tool.args), &expected)
        assert.Equal(t, expected, tc.Arguments)
    }
}
```

### S3. Reasoning + Text + Tool Interleaving

- Simulate a response that includes reasoning content, text content, and tool calls in a single response (some o-series models do this).
- Validate all three content types are accumulated independently and appear in the correct order in the final message.

### S4. Usage in Various Positions

- Simulate usage appearing in different positions: as a separate final chunk, embedded in the last choice chunk, or missing entirely.
- Verify correct extraction in all cases.

```go
func TestUsagePositions(t *testing.T) {
    cases := []struct {
        name   string
        chunks []string
        expect ai.Usage
    }{
        {
            name:   "separate_final_chunk",
            chunks: appendUsageChunk(textStreamChunks(), 10, 5),
            expect: ai.Usage{Input: 10, Output: 5},
        },
        {
            name:   "missing_usage",
            chunks: textStreamChunksNoUsage(),
            expect: ai.Usage{}, // zero
        },
        {
            name:   "usage_with_cache_details",
            chunks: appendUsageWithCacheChunk(textStreamChunks(), 10, 5, 3),
            expect: ai.Usage{Input: 10, Output: 5, CacheRead: 3},
        },
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            server := newMockSSEServer(tc.chunks)
            defer server.Close()

            p := openai.New("test-key", openai.WithBaseURL(server.URL))
            es := p.Stream(context.Background(), testModel(), ai.Context{}, ai.StreamOptions{})

            for range es.C {}
            msg, _ := es.Result()
            assert.Equal(t, tc.expect.Input, msg.Usage.Input)
            assert.Equal(t, tc.expect.Output, msg.Usage.Output)
            assert.Equal(t, tc.expect.CacheRead, msg.Usage.CacheRead)
        })
    }
}
```

---

## 5. Benchmarks and Performance Targets

### B1. SSE Throughput Benchmark

```go
func BenchmarkSSEThroughput(b *testing.B) {
    // Pre-generate a stream of 1000 text delta chunks
    chunks := generateTextDeltaChunks(1000)
    ssePayload := chunksToSSEBytes(chunks)

    b.ResetTimer()
    b.SetBytes(int64(len(ssePayload)))
    for i := 0; i < b.N; i++ {
        server := newRawSSEServer(ssePayload)
        p := openai.New("test-key", openai.WithBaseURL(server.URL))
        es := p.Stream(context.Background(), testModel(), ai.Context{}, ai.StreamOptions{})
        for range es.C {}
        es.Result()
        server.Close()
    }
}
```

**Target:** Parse >= 50K SSE events/sec on developer workstation baseline. (Matching Anthropic provider target.)

### B2. Allocation Budget

**Target:** <= 3 allocations per content delta event on steady-state text stream. Measured via `testing.AllocsPerRun`.

```go
func TestAllocationBudget(t *testing.T) {
    chunks := generateTextDeltaChunks(100)
    server := newMockSSEServer(chunks)
    defer server.Close()

    p := openai.New("test-key", openai.WithBaseURL(server.URL))

    allocs := testing.AllocsPerRun(10, func() {
        es := p.Stream(context.Background(), testModel(), ai.Context{}, ai.StreamOptions{})
        for range es.C {}
        es.Result()
    })

    allocsPerEvent := allocs / 100
    assert.LessOrEqual(t, allocsPerEvent, 3.0,
        "too many allocations per event: %.1f", allocsPerEvent)
}
```

### B3. Tool JSON Incremental Parse Overhead

**Target:** `CompleteJSON()+Unmarshal` median < 100μs for 4KB cumulative tool JSON. (Same as Anthropic.)

### B4. End-to-End Stream Latency Overhead

**Target:** Provider processing overhead < 5ms p95 per 1K events vs raw SSE scanner baseline. (Same as Anthropic.)

### B5. Message Conversion Benchmark

```go
func BenchmarkMessageConversion(b *testing.B) {
    // 30-message conversation with tool calls, system prompt, images
    messages := generateBenchmarkConversation(30)
    model := gpt4oModel()
    system := "You are a helpful assistant"

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        convertMessages(messages, model, system)
    }
}
```

**Target:** < 500μs for a 30-message conversation. Conversion happens once per API call.

### B6. Compat Flag Resolution

```go
func BenchmarkCompatFlagResolution(b *testing.B) {
    model := fullyFlaggedModel() // all compat flags set
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _ = maxTokensFieldName(model)
        _ = systemRole(model)
        _ = shouldIncludeStore(model)
        _ = shouldRequestStreamUsage(model)
    }
}
```

**Target:** < 50ns for all flag checks combined. These are just pointer dereferences.

---

## 6. Stress and Soak Tests

### ST1. Long Session Soak

- 8-hour replay loop with mixed text/reasoning/tool traces from recorded fixtures.
- Assertions: no memory growth trend, no goroutine leak, stable event emission.

```go
func TestLongSessionSoak(t *testing.T) {
    if testing.Short() {
        t.Skip("soak test")
    }

    fixtures := loadAllFixtures(t)
    startMem := readMemStats()
    startGoroutines := runtime.NumGoroutine()

    for i := 0; i < 50_000; i++ { // ~8 hours compressed
        fixture := fixtures[i%len(fixtures)]
        server := newRawSSEServer(fixture)

        p := openai.New("test-key", openai.WithBaseURL(server.URL))
        es := p.Stream(context.Background(), testModel(), ai.Context{}, ai.StreamOptions{})
        for range es.C {}
        es.Result()
        server.Close()

        if i%1000 == 0 {
            currentMem := readMemStats()
            goroutines := runtime.NumGoroutine()
            assert.Less(t, currentMem.HeapInuse-startMem.HeapInuse, uint64(50<<20),
                "memory grew >50MB at iteration %d", i)
            assert.Less(t, goroutines-startGoroutines, 10,
                "goroutine leak at iteration %d: %d extra", i, goroutines-startGoroutines)
        }
    }
}
```

### ST2. Concurrency Stress

- 500 concurrent provider streams against mock server.
- Assertions: no races under `-race`, stable completion ratio, bounded CPU.

```go
func TestConcurrencyStress(t *testing.T) {
    if testing.Short() {
        t.Skip("stress test")
    }

    const concurrent = 500
    server := newMockSSEServer(generateTextDeltaChunks(50))
    defer server.Close()

    p := openai.New("test-key", openai.WithBaseURL(server.URL))

    var wg sync.WaitGroup
    var successCount, errorCount atomic.Int32

    for i := 0; i < concurrent; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            es := p.Stream(context.Background(), testModel(), ai.Context{}, ai.StreamOptions{})
            for range es.C {}
            _, err := es.Result()
            if err == nil {
                successCount.Add(1)
            } else {
                errorCount.Add(1)
            }
        }()
    }

    wg.Wait()
    total := successCount.Load() + errorCount.Load()
    assert.Equal(t, int32(concurrent), total, "not all streams completed")
    t.Logf("Success: %d, Error: %d", successCount.Load(), errorCount.Load())
}
```

### ST3. Burst Tool-Call Stress

- High-frequency tool call argument deltas (simulating large tool args like file content).
- 100 concurrent streams, each with 10KB+ tool arguments split into 50-byte chunks.
- Assertions: parser stability, no progressive slowdown, correct final args.

---

## 7. Security Tests

### SEC1. Prompt/Data Injection in Tool JSON

- Feed malicious strings through `delta.tool_calls[].function.arguments`:
  - JSON with embedded `</script>` tags
  - Unicode escape sequences (`\u0000`, null bytes)
  - Extremely deeply nested objects
  - Circular-reference-like patterns
- Validate strict JSON object parsing and no code execution paths.

```go
func TestMaliciousToolJSON(t *testing.T) {
    malicious := []string{
        `{"cmd": "'; DROP TABLE users; --"}`,
        `{"path": "../../../etc/passwd"}`,
        `{"data": "\u0000\u0000\u0000"}`,
        `{"nested": ` + generateDeeplyNested(50) + `}`,
        `{"key": "` + strings.Repeat("a", 1<<20) + `"}`, // 1MB string
    }

    for _, args := range malicious {
        t.Run(args[:min(30, len(args))], func(t *testing.T) {
            chunks := buildToolCallSSESequence("call_1", "test", splitEvenly(args, 100))
            server := newMockSSEServer(chunks)
            defer server.Close()

            p := openai.New("test-key", openai.WithBaseURL(server.URL))
            es := p.Stream(context.Background(), testModel(), ai.Context{}, ai.StreamOptions{})

            for range es.C {} // must not panic
            msg, _ := es.Result()
            if msg != nil {
                // If parsed, verify args are valid JSON (not executable)
                for _, block := range msg.Content {
                    if tc, ok := block.(*ai.ToolCall); ok {
                        _, err := json.Marshal(tc.Arguments)
                        assert.NoError(t, err, "tool args must be valid JSON")
                    }
                }
            }
        })
    }
}
```

### SEC2. Header/Config Leakage

- Ensure request logging and error surfaces never expose API keys.
- Verify `Authorization` header is not included in `ZFSError`/`ProviderError` messages.

```go
func TestNoAPIKeyLeakage(t *testing.T) {
    secretKey := "sk-test-super-secret-key-12345"
    server := newStatusCodeServer(500, `{"error":{"message":"internal error"}}`)
    defer server.Close()

    p := openai.New(secretKey, openai.WithBaseURL(server.URL))
    es := p.Stream(context.Background(), testModel(), ai.Context{}, ai.StreamOptions{})

    for range es.C {}
    _, err := es.Result()
    require.Error(t, err)

    errStr := err.Error()
    assert.NotContains(t, errStr, secretKey, "API key leaked in error message")
    assert.NotContains(t, errStr, "sk-test", "API key prefix leaked in error message")
}
```

### SEC3. Oversized Payload Protection

- Send chunks with >10MB payloads.
- Verify graceful failure (error event, not OOM).

---

## 8. Manual QA Plan

### MQ1. Live Stream Visual Inspection

Run live OpenAI stream (GPT-4o) with visible incremental output. Verify:
- Text appears smoothly without stuttering
- Event progression feels natural
- No dropped characters or duplicate content
- Final message matches streamed content

### MQ2. Tool Call Prompt Inspection

Execute a known tool-call prompt and inspect emitted tool args snapshots:
- Partial args show progressive completion (not garbage)
- Final args are valid and complete
- Tool call ID is stable across all events

### MQ3. Reasoning Model Output

Run prompt against o3 with `reasoning_effort: "high"`:
- Verify reasoning content appears as thinking events (if model emits it)
- Verify final message structure
- Verify usage includes reasoning tokens

### MQ4. Compat Endpoint Spot Check

For each configured compat endpoint (Groq, Mistral):
1. Submit a simple text prompt — verify streaming works
2. Submit a tool call prompt — verify tool call parsing
3. Check that error messages from the endpoint are correctly classified

---

## 9. CI Tier Mapping

| Tier | Runs | Contents |
|------|------|----------|
| **PR-fast** | Every PR | Core unit tests, stub-server correctness replay lane, error mapping, compat flag tests |
| **PR-standard** | Every PR | Property tests (bounded iterations), race tests for provider package, message conversion tests |
| **Nightly** | Nightly | Long property runs (10K iterations), stub-server fault mode (chaos), benchmark trend capture |
| **Weekly** | Weekly | 8-hour soak, high-concurrency stress, live API integration suite (OPENAI_API_KEY required) |

Credentialed live tests are gated by env vars and skipped in fork PRs.

All tiers run with `-race` flag.

---

## 10. Exit Criteria

Implementation for OpenAI provider is complete only when all are true:

1. **Property tests P1-P6** pass consistently across repeated runs (10K+ iterations).
2. **Fault injection tests F1-F6** pass with no goroutine leaks.
3. **Oracle tests O1-O3** pass for cross-implementation and cross-provider semantic invariants.
4. **Deterministic simulations S1-S4** pass; illegal traces fail with expected categories.
5. **Benchmark targets B1-B6** are met or documented with approved regression rationale.
6. **Stress/soak tests ST1-ST3** pass on scheduled CI.
7. **Security tests SEC1-SEC3** pass; no sensitive-data leakage in logs.
8. **Manual QA checklist** (MQ1-MQ4) executed and recorded for at least one release-candidate commit.
9. **CI tier matrix** is wired and green.
10. **Unit + component tests pass under `-race`** with zero race conditions.
11. **Test coverage >= 85%** for `internal/ai/provider/openai/`.

---

## 11. Test Dependencies

| Dependency | Purpose |
|-----------|---------|
| `pgregory.net/rapid` | Property-based testing |
| `github.com/stretchr/testify` | Assertions (assert/require) |
| `net/http/httptest` | Mock SSE servers |
| Go stdlib `testing` | Benchmarks, fuzz tests |
| `github.com/google/go-cmp` | Deep comparison for oracle tests |
| OpenAI API key (optional) | Live integration tests |
