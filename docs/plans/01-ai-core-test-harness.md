# 01: AI Core — Test Harness

**Companion to:** [01-ai-core.md](./01-ai-core.md)
**Scope:** All testing beyond basic unit tests for the `ai` core package.

---

## 1. Property-Based Tests

### P1. EventStream Ordering Guarantee

**Invariant:** Events received on `EventStream.C` must arrive in exactly the order they were sent via `Send()`.

```go
func TestEventStreamOrdering(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        n := rapid.IntRange(1, 500).Draw(t, "eventCount")
        es := NewEventStream()

        // Generate n events with sequential indices
        events := make([]AssistantMessageEvent, n)
        for i := range events {
            events[i] = AssistantMessageEvent{
                Type:         EventTextDelta,
                ContentIndex: i,
                Delta:        fmt.Sprintf("chunk-%d", i),
            }
        }
        // Final event
        events = append(events, AssistantMessageEvent{
            Type:    EventDone,
            Reason:  StopReasonStop,
            Message: &AssistantMessage{},
        })

        go func() {
            defer es.Close()
            for _, e := range events {
                es.Send(e)
            }
        }()

        idx := 0
        for e := range es.C {
            assert.Equal(t, events[idx].ContentIndex, e.ContentIndex)
            idx++
        }
        assert.Equal(t, len(events), idx)
    })
}
```

### P2. TransformMessages Idempotency

**Invariant:** `TransformMessages(TransformMessages(msgs, model, nil), model, nil)` must equal `TransformMessages(msgs, model, nil)`.

Applying transformation twice yields the same result as once. Catches bugs where transformation produces states that would be further modified on re-transformation.

```go
func TestTransformIdempotency(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        msgs := generateRandomConversation(t)
        model := drawRandomModel(t)

        once := TransformMessages(msgs, model, nil)
        twice := TransformMessages(once, model, nil)

        assert.DeepEqual(t, once, twice)
    })
}
```

### P3. CalculateCost Consistency

**Invariant:** `cost.Total == cost.Input + cost.Output + cost.CacheRead + cost.CacheWrite` for any model and usage combination. No floating-point drift outside epsilon.

```go
func TestCostConsistency(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        usage := &Usage{
            Input:      rapid.IntRange(0, 1_000_000).Draw(t, "input"),
            Output:     rapid.IntRange(0, 100_000).Draw(t, "output"),
            CacheRead:  rapid.IntRange(0, 1_000_000).Draw(t, "cacheRead"),
            CacheWrite: rapid.IntRange(0, 1_000_000).Draw(t, "cacheWrite"),
        }
        model := Model{Cost: ModelCost{
            Input:      rapid.Float64Range(0, 100).Draw(t, "inputCost"),
            Output:     rapid.Float64Range(0, 100).Draw(t, "outputCost"),
            CacheRead:  rapid.Float64Range(0, 100).Draw(t, "cacheReadCost"),
            CacheWrite: rapid.Float64Range(0, 100).Draw(t, "cacheWriteCost"),
        }}

        CalculateCost(model, usage)

        expected := usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
        assert.InEpsilon(t, expected, usage.Cost.Total, 1e-10)
    })
}
```

### P4. CoerceTypes Preserves Valid Types

**Invariant:** If an argument already matches its schema type, `CoerceTypes` must not modify it.

### P5. EventStream Terminal-State Guarantee

**Invariant:** `Result()` always unblocks, regardless of how the stream ends (done event, error event, close-without-terminal, panic in provider goroutine, double terminal event). Exactly one result is ever published.

```go
func TestEventStreamAlwaysTerminates(t *testing.T) {
    cases := []struct{
        name string
        run  func(es *EventStream)
    }{
        {"done_event", func(es *EventStream) {
            es.Send(AssistantMessageEvent{Type: EventDone, Message: &AssistantMessage{}})
        }},
        {"error_event", func(es *EventStream) {
            es.Send(AssistantMessageEvent{Type: EventError, Error: &AssistantMessage{StopReason: StopReasonError}})
        }},
        {"close_without_terminal", func(es *EventStream) {
            // Provider goroutine exits without sending done/error
        }},
        {"panic_in_provider", func(es *EventStream) {
            panic("simulated provider panic")
        }},
        {"double_done", func(es *EventStream) {
            es.Send(AssistantMessageEvent{Type: EventDone, Message: &AssistantMessage{Model: "first"}})
            es.Send(AssistantMessageEvent{Type: EventDone, Message: &AssistantMessage{Model: "second"}})
        }},
        {"done_then_close", func(es *EventStream) {
            es.Send(AssistantMessageEvent{Type: EventDone, Message: &AssistantMessage{}})
            // Close() follows via defer — should not publish competing result
        }},
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            es := NewEventStream()
            go func() {
                defer func() { recover() }() // catch panics
                defer es.Close()
                tc.run(es)
            }()

            // Must not hang — use timeout
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            defer cancel()

            done := make(chan struct{})
            go func() {
                for range es.C {} // drain events
                _, _ = es.Result()
                close(done)
            }()

            select {
            case <-done:
                // success
            case <-ctx.Done():
                t.Fatal("Result() did not unblock")
            }
        })
    }
}
```

### P6. TransformMessages Preserves Message Count Bound

**Invariant:** `len(TransformMessages(msgs, model, nil)) <= len(msgs) + toolCallCount(msgs)`. Transformation can add synthetic tool results but never more than one per tool call, and can remove messages (skipped errors) but never duplicates.

### P7. Model Registry Mutation Isolation

**Invariant:** Mutating a Model returned by `GetModel`/`GetModels` must not affect subsequent calls to the registry.

```go
func TestModelRegistryMutationIsolation(t *testing.T) {
    ClearModels()
    RegisterModel(Model{
        ID:       "test-model",
        Provider: "test",
        Headers:  map[string]string{"X-Key": "original"},
        Input:    []string{"text"},
        Compat:   &ModelCompat{ReasoningEffortMap: map[string]string{"high": "h"}},
    })

    // Get model and mutate everything
    m, err := GetModel("test", "test-model")
    require.NoError(t, err)
    m.Headers["X-Key"] = "mutated"
    m.Headers["X-New"] = "injected"
    m.Input[0] = "corrupted"
    m.Compat.ReasoningEffortMap["high"] = "corrupted"

    // Original must be unaffected
    m2, err := GetModel("test", "test-model")
    require.NoError(t, err)
    assert.Equal(t, "original", m2.Headers["X-Key"])
    assert.NotContains(t, m2.Headers, "X-New")
    assert.Equal(t, "text", m2.Input[0])
    assert.Equal(t, "h", m2.Compat.ReasoningEffortMap["high"])
}
```

### P8. TransformMessages Deterministic Output

**Invariant:** Running `TransformMessages` on the same input N times produces byte-for-byte identical output every time. Tests the sorted map iteration fix for synthetic tool results.

```go
func TestTransformDeterministic(t *testing.T) {
    // Conversation with multiple orphaned tool calls (will generate synthetic results)
    msgs := []Message{
        &AssistantMessage{
            Content: []ContentBlock{
                &ToolCall{ID: "z-call", Name: "toolZ", Arguments: map[string]any{}},
                &ToolCall{ID: "a-call", Name: "toolA", Arguments: map[string]any{}},
                &ToolCall{ID: "m-call", Name: "toolM", Arguments: map[string]any{}},
            },
            StopReason: StopReasonToolUse,
        },
        // No tool results — all three are orphaned
        &UserMessage{Content: []ContentBlock{&TextContent{Text: "continue"}}},
    }
    model := Model{ID: "target", Provider: "test", API: "test"}

    // Run 100 times, compare output
    first := TransformMessages(msgs, model, nil)
    for i := 0; i < 100; i++ {
        result := TransformMessages(msgs, model, nil)
        assert.DeepEqual(t, first, result,
            "iteration %d produced different output — map ordering is non-deterministic", i)
    }
}
```

### P9. Registry Thread-Safety

**Invariant:** Concurrent Register/Get/Unregister operations never panic, never return corrupt data, and always pass `-race`.

```go
func TestRegistryConcurrentAccess(t *testing.T) {
    const goroutines = 50
    const ops = 1000
    var wg sync.WaitGroup

    for i := 0; i < goroutines; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            for j := 0; j < ops; j++ {
                api := fmt.Sprintf("api-%d", id%10)
                switch j % 4 {
                case 0:
                    RegisterProvider(&mockProvider{api: api}, "test")
                case 1:
                    GetProvider(api)
                case 2:
                    GetProviders()
                case 3:
                    UnregisterProviders("test")
                }
            }
        }(i)
    }
    wg.Wait()
}
```

---

## 2. Comparison Oracle Tests

### O1. TransformMessages vs TypeScript Reference

Run the TypeScript `transformMessages` function and the Go `TransformMessages` function on identical inputs, compare outputs.

**Setup:**
1. Create a Node.js test harness that imports `@mariozechner/pi-ai` and exposes `transformMessages` via stdin/stdout JSON
2. Generate a corpus of 100+ diverse conversations (varying tool calls, thinking blocks, cross-model switches, errors)
3. For each conversation: run through both implementations, compare serialized output

```go
func TestTransformMessagesVsTypeScript(t *testing.T) {
    if testing.Short() {
        t.Skip("comparison oracle requires Node.js")
    }

    corpus := loadTestCorpus(t, "testdata/transform_corpus.json")
    for _, tc := range corpus {
        goResult := TransformMessages(tc.Messages, tc.TargetModel, nil)
        tsResult := runTSTransform(t, tc.Messages, tc.TargetModel)
        assert.DeepEqual(t, tsResult, goResult, cmpopts.IgnoreFields(ToolResultMessage{}, "Timestamp"))
    }
}
```

**Corpus generation:** A script that creates conversations covering:
- Simple text-only conversations
- Tool call with result
- Multiple tool calls in one turn
- Cross-model switch (Anthropic → OpenAI)
- Thinking blocks (regular, redacted, empty)
- Error/aborted messages
- Orphaned tool calls
- Mixed multi-turn with all content types

### O2. CalculateCost vs TypeScript Reference

Same pattern — run TS `calculateCost` and Go `CalculateCost` on identical model/usage pairs, verify identical results.

### O3. IsContextOverflow vs TypeScript Reference

Test identical error strings against both implementations, verify same boolean result.

---

## 3. Fuzz Testing

### F1. SSE Parser Fuzzing

```go
func FuzzSSEScanner(f *testing.F) {
    // Seed with well-formed events
    f.Add([]byte("event: message\ndata: hello\n\n"))
    f.Add([]byte("data: line1\ndata: line2\n\n"))
    f.Add([]byte(": comment\ndata: test\n\n"))
    f.Add([]byte("\xEF\xBB\xBFdata: bom\n\n"))

    f.Fuzz(func(t *testing.T, input []byte) {
        scanner := sse.NewScanner(bytes.NewReader(input))
        for scanner.Next() {
            e := scanner.Event()
            // Must not panic
            _ = e.Type
            _ = e.Data
            _ = e.ID
        }
        // Err should be nil or a well-formed error
        if err := scanner.Err(); err != nil {
            _ = err.Error()
        }
    })
}
```

### F2. JSON Schema Validation Fuzzing

```go
func FuzzValidateToolArguments(f *testing.F) {
    schema := `{"type":"object","properties":{"name":{"type":"string"},"count":{"type":"integer"}},"required":["name"]}`

    f.Add(`{"name":"test","count":5}`)
    f.Add(`{"name":"test"}`)
    f.Add(`{}`)
    f.Add(`{"name":123}`)

    f.Fuzz(func(t *testing.T, argsJSON string) {
        var args map[string]any
        if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
            return // skip non-JSON inputs
        }
        tool := Tool{
            Name:       "test",
            Parameters: json.RawMessage(schema),
        }
        // Must not panic
        _ = ValidateToolArguments(tool, args)
    })
}
```

### F3. CoerceTypes Fuzzing

Fuzz the type coercion logic with random schema+args combinations. Must never panic, must never produce values that violate the schema worse than the input.

### F4. Overflow Pattern Fuzzing

Fuzz `IsContextOverflow` with random error strings. Must never panic. Verify no false positives on strings that don't match any known pattern.

---

## 4. Stress Tests

### S1. EventStream High-Throughput

Producer sends 1M events as fast as possible. Consumer reads and counts. Verify:
- All events received
- Ordering preserved
- No deadlocks
- Memory usage stays bounded (not accumulating in buffer)

```go
func TestEventStreamHighThroughput(t *testing.T) {
    const eventCount = 1_000_000
    es := NewEventStream()

    go func() {
        defer es.Close()
        for i := 0; i < eventCount; i++ {
            es.Send(AssistantMessageEvent{
                Type:         EventTextDelta,
                ContentIndex: i,
                Delta:        "x",
            })
        }
        es.Send(AssistantMessageEvent{
            Type:    EventDone,
            Message: &AssistantMessage{},
        })
    }()

    count := 0
    for range es.C {
        count++
    }
    assert.Equal(t, eventCount+1, count)
}
```

### S2. Registry Stress Under Contention

100 goroutines, each doing 10K register/get/unregister cycles. Measure:
- No data races (must pass `-race`)
- No panics
- All registered providers are retrievable
- Unregistered providers are not retrievable

### S3. Schema Cache Under Varied Load

Validate that the schema cache performs well with:
- 1000 unique schemas (cold cache)
- Same schema repeated 1000 times (hot cache)
- Mixed (80% repeated, 20% unique)
- Measure allocations and verify cache hit rate via metrics

---

## 5. Benchmarks

### B1. EventStream Throughput

```go
func BenchmarkEventStreamSendReceive(b *testing.B) {
    es := NewEventStream()
    event := AssistantMessageEvent{Type: EventTextDelta, Delta: "hello"}

    go func() {
        defer es.Close()
        for i := 0; i < b.N; i++ {
            es.Send(event)
        }
        es.Send(AssistantMessageEvent{Type: EventDone, Message: &AssistantMessage{}})
    }()

    for range es.C {}
}
```

**Target:** >1M events/second on modern hardware.

### B2. SSE Parsing

```go
func BenchmarkSSEParsing(b *testing.B) {
    // Pre-generate a realistic SSE stream (Anthropic-style content_block_delta events)
    var buf bytes.Buffer
    for i := 0; i < 100; i++ {
        fmt.Fprintf(&buf, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"word%d \"}}\n\n", i)
    }
    buf.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
    input := buf.Bytes()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        scanner := sse.NewScanner(bytes.NewReader(input))
        for scanner.Next() {}
    }
}
```

**Target:** Parse a 100-event stream in <100μs (SSE parsing should never be a bottleneck vs network latency).

### B3. TransformMessages

```go
func BenchmarkTransformMessages(b *testing.B) {
    // 50-message conversation with tool calls, thinking, cross-model
    msgs := loadBenchmarkConversation()
    model := anthropicSonnetModel()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        TransformMessages(msgs, model, nil)
    }
}
```

**Target:** <1ms for a 50-message conversation. Message transformation happens once per LLM call.

### B4. JSON Schema Validation

```go
func BenchmarkValidateToolArguments(b *testing.B) {
    tool := Tool{
        Name: "search",
        Parameters: json.RawMessage(`{
            "type":"object",
            "properties":{
                "query":{"type":"string"},
                "limit":{"type":"integer"},
                "filters":{"type":"object","properties":{"category":{"type":"string"}}}
            },
            "required":["query"]
        }`),
    }
    args := map[string]any{"query": "test", "limit": 10, "filters": map[string]any{"category": "books"}}

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        ValidateToolArguments(tool, args)
    }
}
```

**Target:** <50μs per validation (with schema cache warm). Validation happens once per tool call.

### B5. Schema Compilation (Cold Cache)

```go
func BenchmarkSchemaCompilationCold(b *testing.B) {
    schemas := generateUniqueSchemas(b.N)
    ClearSchemaCache()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        compileSchema(schemas[i])
    }
}
```

**Target:** <500μs per schema compilation. This is the cold path — hot path uses cache.

### B6. CalculateCost

Benchmark to ensure cost calculation is trivially fast (should be <100ns — just multiplications).

### B7. Model Catalog Lookup

```go
func BenchmarkGetModel(b *testing.B) {
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        GetModel("anthropic", "claude-sonnet-4-20250514")
    }
}
```

**Target:** <200ns per lookup (map access under RLock).

---

## 6. Deterministic Simulation Tests

### D1. EventStream with Simulated Slow Consumer

Simulate a consumer that processes events at varying speeds (fast bursts, then pauses). Verify:
- Buffer fills during pauses (backpressure)
- Events are not lost
- Producer blocks when buffer is full (doesn't drop events)
- Resume after pause drains correctly

### D2. EventStream Context Cancellation Timing

Use `context.WithCancel` at specific points during streaming:
- Cancel before any events sent
- Cancel mid-stream (after N events)
- Cancel after done event but before Result() called
- Verify clean shutdown in all cases (no goroutine leaks)

```go
func TestEventStreamCancelMidStream(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    es := NewEventStream()

    go func() {
        defer es.Close()
        for i := 0; i < 1000; i++ {
            select {
            case <-ctx.Done():
                es.Send(AssistantMessageEvent{
                    Type:   EventError,
                    Reason: StopReasonAborted,
                    Error:  &AssistantMessage{StopReason: StopReasonAborted},
                })
                return
            default:
                es.Send(AssistantMessageEvent{Type: EventTextDelta, Delta: "x"})
            }
        }
    }()

    count := 0
    for e := range es.C {
        count++
        if count == 50 {
            cancel()
        }
    }

    _, err := es.Result()
    assert.Error(t, err)
}
```

---

## 7. Security Tests

### Sec1. JSON Schema Injection

Validate that malicious JSON Schema documents don't cause:
- Infinite loops (recursive $ref)
- Excessive memory allocation (deeply nested schemas)
- Code execution (no eval/exec in Go JSON schema libs, but verify)

```go
func TestMaliciousSchemas(t *testing.T) {
    cases := []struct {
        name   string
        schema string
    }{
        {"recursive_ref", `{"$ref": "#"}`},
        {"deep_nesting", generateDeeplyNestedSchema(100)},
        {"huge_enum", `{"type":"string","enum":[` + generateLargeEnum(10000) + `]}`},
        {"regex_dos", `{"type":"string","pattern":"(a+)+$"}`},
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            defer cancel()

            done := make(chan error, 1)
            go func() {
                tool := Tool{Name: "test", Parameters: json.RawMessage(tc.schema)}
                done <- ValidateToolArguments(tool, map[string]any{"x": "y"})
            }()

            select {
            case <-ctx.Done():
                t.Fatal("schema validation timed out — possible DoS")
            case err := <-done:
                // Error is fine, timeout/panic is not
                _ = err
            }
        })
    }
}
```

### Sec2. SSE Parser Resource Limits

Verify the SSE parser handles adversarial input without excessive memory:
- Extremely long lines (>1MB single line)
- No event delimiters (never-ending event)
- Millions of empty lines

---

## 8. Manual QA Plan

### MQ1. Model Catalog Completeness

Before each release, compare the Go `catalog.json` against the latest TS `models.generated.ts`:
- All V1 providers (Anthropic, OpenAI, Google) have all current models
- Cost data matches
- Context window and max tokens match
- New models added since last sync are present

### MQ2. Overflow Pattern Coverage

When a new provider is added or an existing provider updates its API:
1. Intentionally send a prompt exceeding the context window
2. Capture the exact error message
3. Verify `IsContextOverflow` detects it
4. Add the error message as a test fixture

### MQ3. Cross-Model Transformation Spot Check

Manually create a multi-turn conversation with tool calls, switch the target model, and inspect the transformed output:
- Thinking blocks correctly converted/dropped
- Tool call IDs normalized
- Signatures stripped
- Orphaned tool calls get synthetic results

---

## 9. CI Tier Mapping

| Tier | Tests | Trigger | Timeout |
|------|-------|---------|---------|
| **T1: Fast** | Unit tests, property tests (limited iterations), benchmarks (verify no regression) | Every commit | 2 min |
| **T2: Thorough** | Full property tests (10K iterations), fuzz tests (30s each), stress tests, comparison oracle | PR merge, nightly | 10 min |
| **T3: Extended** | Extended fuzz (5 min each), soak tests (1M events), security tests, manual QA checklist | Release candidate | 30 min |

All tiers run with `-race` flag.

---

## 10. Exit Criteria

Before `01-ai-core` implementation is considered complete:

1. **All P-tests pass** (property-based tests with 10K+ iterations each)
2. **All F-tests pass** (fuzz tests with 30s minimum each, no panics found)
3. **All S-tests pass** (stress tests demonstrate stability under load)
4. **All B-tests pass** (benchmarks meet stated targets)
5. **All D-tests pass** (deterministic simulations show correct behavior)
6. **Comparison oracle passes** (Go matches TS output for 100+ corpus entries)
7. **Security tests pass** (no DoS vectors in schema validation or SSE parsing)
8. **`go test -race ./internal/ai/...`** passes with zero race conditions
9. **`go vet ./internal/ai/...`** and **`staticcheck ./internal/ai/...`** report no issues
10. **Test coverage ≥ 90%** for `internal/ai/` (excluding `models/catalog.json`)
11. **Benchmark baselines recorded** for regression tracking in CI

---

## 11. Test Dependencies

| Dependency | Purpose |
|-----------|---------|
| `pgregory.net/rapid` | Property-based testing (Go's best rapid-check library) |
| `github.com/stretchr/testify` | Assertions (assert/require) |
| `github.com/google/go-cmp` | Deep comparison with options (for oracle tests) |
| Go stdlib `testing` | Fuzz testing (native since Go 1.18) |
| Node.js (optional) | TypeScript comparison oracle |

---

## Completion Signoff

- **Status**: Partial
- **Date**: 2026-03-12
- **Branch**: main
- **Verified by**: coder-1-sea
- **Completed items**:
  - Implemented test categories present for property (P1-P9), oracle (O1-O3), fuzz (F1-F4), stress (S1-S3), benchmark (B1-B7), deterministic simulation (D1-D2), and security (Sec1-Sec2) in `internal/ai/*_test.go` and `internal/ai/sse/sse_test.go`.
  - Verification commands passed: `go test -race ./internal/ai/... -count=1`, `make test-harness`, `make test-fuzz`, `go test ./internal/ai/... -run '^$' -bench 'Benchmark(EventStreamSendReceive|Scanner100Events|TransformMessages|ValidateToolArguments|SchemaCompilationCold|CalculateCost|GetModel)$' -benchmem`, `go test ./internal/ai -run 'TestO[123]_.*|TestCorpusSize' -count=1`.
- **Deviations**:
  - [Missing] Exit criterion #1 requires full P-test runs at 10K+ iterations each; current property tests use default rapid iteration counts and no 10K configuration.
  - [Missing] Exit criterion #2 requires fuzz runs with 30s minimum each; current automated harness runs 5s fuzz windows.
  - [Contractual] Benchmark target B1 (>1M events/sec) is not met on current benchmark run (`BenchmarkEventStreamSendReceive`: 1347 ns/op, ~742K events/sec).
  - [Missing] Exit criterion #10 coverage threshold is not met for `./internal/ai/...` aggregate coverage (`total: 89.7%`, below 90%).
  - [Missing] Exit criterion #11 requires benchmark baselines recorded for CI regression tracking; no baseline artifact/checkpoint is currently recorded.
- **Outstanding gaps**:
  - Gap 1: Add deterministic high-iteration property-test mode (10K+) and CI wiring; suggested follow-up bead: `aiag-qkh.followup-harness-pbt-10k`.
  - Gap 2: Add 30s fuzz tier and CI trigger separation; suggested follow-up bead: `aiag-qkh.followup-harness-fuzz-30s`.
  - Gap 3: Improve EventStream benchmark throughput or adjust plan target with measured rationale; suggested follow-up bead: `aiag-qkh.followup-benchmark-b1-target`.
  - Gap 4: Raise aggregate `internal/ai/...` coverage to >=90% (notably `internal/ai/sse`); suggested follow-up bead: `aiag-qkh.followup-coverage-90`.
  - Gap 5: Record and persist benchmark baselines in CI workflow; suggested follow-up bead: `aiag-qkh.followup-benchmark-baselines`.
