# 04: Google Provider (Gemini) — Test Harness

**Companion to:** [04-provider-google.md](./04-provider-google.md)
**Scope:** All testing beyond basic unit tests for the `internal/ai/provider/google` package.

---

## Harness Structure

1. Stub server tests (correctness replay + fault injection) in one server-backed test binary.
2. Property/fuzz tests (pure functions, no server).
3. Comparison oracles (cross-implementation, no server).
4. Live smoke tests (real API, weekly, gated).

The stub server is a shared utility (e.g., `internal/ai/testutil/stubserver/`) with Google fixture sets.
Recorded fixture interactions are captured from real APIs (initially via pi-mono clients), sanitized, and served over HTTP.

---

## 1. Property-Based Tests

### P1. Message Conversion Round-Trip Stability

**Invariant:** Converting runtime messages to Gemini wire format and back (if we had a reverse converter for testing) must preserve all semantically meaningful content. In practice: convert to wire format, serialize to JSON, deserialize, and verify all fields match.

```go
func TestMessageConversionStability(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        msgs := generateRandomConversation(t)
        model := drawGeminiModel(t)

        wire := convertContents(msgs, model)
        wireJSON, err := json.Marshal(wire)
        assert.NoError(t, err)

        var roundTrip []contentObj
        assert.NoError(t, json.Unmarshal(wireJSON, &roundTrip))
        assert.DeepEqual(t, wire, roundTrip)
    })
}
```

### P2. ThoughtSignature Byte-Exact Round-Trip

**Invariant:** A `thoughtSignature` from a Gemini response must survive the full round-trip: response part → `ai.ToolCall.ThoughtSignature` (base64 string) → next request part `ThoughtSignature` ([]byte). The original bytes must be recovered exactly.

```go
func TestThoughtSignatureRoundTrip(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        // Generate random signature bytes (typical: 32-256 bytes)
        sigLen := rapid.IntRange(16, 512).Draw(t, "sigLen")
        originalSig := make([]byte, sigLen)
        rand.Read(originalSig)

        // Simulate: response part → ToolCall.ThoughtSignature (base64)
        b64Sig := base64.StdEncoding.EncodeToString(originalSig)

        // Simulate: ToolCall.ThoughtSignature → request part.ThoughtSignature
        decoded, err := base64.StdEncoding.DecodeString(b64Sig)
        assert.NoError(t, err)
        assert.Equal(t, originalSig, decoded)
    })
}
```

### P3. Finish Reason Mapping Completeness

**Invariant:** Every known Gemini `finishReason` string maps to a valid `ai.StopReason`. No panics or empty strings.

```go
func TestFinishReasonMappingCompleteness(t *testing.T) {
    knownReasons := []string{
        "STOP", "MAX_TOKENS", "SAFETY", "RECITATION", "LANGUAGE",
        "OTHER", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII",
        "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL",
        "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_RECITATION",
        "NO_IMAGE", "FINISH_REASON_UNSPECIFIED",
    }

    for _, reason := range knownReasons {
        result := mapFinishReason(reason)
        assert.NotEmpty(t, result, "unmapped finish reason: %s", reason)
    }
}
```

### P4. Safety Settings Cover All Categories

**Invariant:** `defaultSafetySettings()` must produce a setting for every known harm category.

```go
func TestDefaultSafetySettingsCompleteness(t *testing.T) {
    settings := defaultSafetySettings()
    categories := make(map[string]bool)
    for _, s := range settings {
        categories[s.Category] = true
    }

    required := []string{
        "HARM_CATEGORY_HARASSMENT",
        "HARM_CATEGORY_HATE_SPEECH",
        "HARM_CATEGORY_SEXUALLY_EXPLICIT",
        "HARM_CATEGORY_DANGEROUS_CONTENT",
        "HARM_CATEGORY_CIVIC_INTEGRITY",
    }
    for _, cat := range required {
        assert.True(t, categories[cat], "missing safety category: %s", cat)
    }
}
```

### P5. Request JSON Uses camelCase

**Invariant:** Serialized `generateContentRequest` JSON must use camelCase field names, matching Gemini's wire format (not snake_case).

```go
func TestRequestJSONCamelCase(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        req := generateRandomRequest(t)
        data, err := json.Marshal(req)
        assert.NoError(t, err)

        jsonStr := string(data)
        // Must not contain snake_case versions of known fields
        snakeCaseFields := []string{
            "system_instruction", "generation_config", "safety_settings",
            "tool_config", "function_declarations", "function_calling_config",
            "max_output_tokens", "thinking_config", "thinking_budget",
            "inline_data", "mime_type", "function_call", "function_response",
            "finish_reason", "thought_signature", "cached_content",
        }
        for _, field := range snakeCaseFields {
            assert.NotContains(t, jsonStr, `"`+field+`"`,
                "found snake_case field %q in JSON", field)
        }
    })
}
```

### P6. Usage Cost Consistency

**Invariant:** `cost.Total == cost.Input + cost.Output + cost.CacheRead + cost.CacheWrite` for any model and usage combination (matching P3 from 01-ai-core-test-harness).

```go
func TestUsageCostConsistency(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        meta := &usageMetadata{
            PromptTokenCount:        rapid.IntRange(0, 1_000_000).Draw(t, "prompt"),
            CandidatesTokenCount:    rapid.IntRange(0, 100_000).Draw(t, "candidates"),
            TotalTokenCount:         rapid.IntRange(0, 2_000_000).Draw(t, "total"),
            CachedContentTokenCount: rapid.IntRange(0, 500_000).Draw(t, "cached"),
            ThoughtsTokenCount:      rapid.IntRange(0, 50_000).Draw(t, "thoughts"),
        }
        model := drawGeminiModel(t)

        usage := mapUsage(meta, model)

        expected := usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
        assert.InEpsilon(t, expected, usage.Cost.Total, 1e-10)
    })
}
```

---

## 2. Stub Server Tests (Correctness Replay + Fault Injection)

Use one shared stub server in two modes:
- Correctness mode: replay recorded SSE fixtures over HTTP to validate full client stack behavior (headers/auth/timeouts + stream handling).
- Fault mode: inject transport/protocol failures (TCP reset, malformed payloads, rate limiting, backpressure, partial streams, connection drops).

### S1. Correctness Replay Mode

- Serve text/thinking/tool/safety fixture streams from the stub server and assert expected event/result outputs.

### F1. Truncated SSE Stream

Simulate a network disconnection mid-stream by truncating the SSE data after a random number of events.

```go
func TestTruncatedSSEStream(t *testing.T) {
    fixtures := []string{
        fixtureTextStream,
        fixtureThinkingStream,
        fixtureToolCallStream,
    }

    for _, fixture := range fixtures {
        events := parseSSEFixture(fixture)
        for truncateAt := 1; truncateAt < len(events); truncateAt++ {
            truncated := events[:truncateAt]
            resp := buildMockSSEResponse(truncated)

            es := ai.NewEventStream()
            go func() {
                defer es.Close()
                provider.processStream(context.Background(), resp, testModel, es)
            }()

            result := es.Result()
            // Must not panic, must emit error or partial result
            assert.NotNil(t, result)
        }
    }
}
```

### F2. Malformed JSON in SSE Chunk

Inject malformed JSON in random positions of an SSE stream.

```go
func TestMalformedJSONChunk(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        events := parseSSEFixture(fixtureTextStream)
        injectIdx := rapid.IntRange(0, len(events)-1).Draw(t, "injectIdx")
        events[injectIdx] = "data: {{{INVALID JSON"

        resp := buildMockSSEResponse(events)
        es := ai.NewEventStream()
        go func() {
            defer es.Close()
            provider.processStream(context.Background(), resp, testModel, es)
        }()

        result := es.Result()
        assert.NotNil(t, result.Error)
    })
}
```

### F3. HTTP Error Bodies

Test all HTTP error codes with both valid Google API error JSON and garbage bodies.

```go
func TestHTTPErrorHandling(t *testing.T) {
    testCases := []struct {
        status int
        body   string
        want   string
    }{
        {403, `{"error":{"code":403,"message":"forbidden","status":"PERMISSION_DENIED"}}`, "auth"},
        {429, `{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED"}}`, "rate_limit"},
        {400, `{"error":{"code":400,"message":"token limit exceeded"}}`, "context_overflow"},
        {400, `{"error":{"code":400,"message":"invalid request"}}`, "bad_request"},
        {500, `{"error":{"code":500,"message":"internal"}}`, "server_error"},
        {503, ``, "server_error"},
        {403, `not json at all`, "auth"},
        {200, `completely unexpected`, "unknown"},
    }

    for _, tc := range testCases {
        err := classifyHTTPError(tc.status, []byte(tc.body))
        assert.Contains(t, err.Code, tc.want)
    }
}
```

### F4. Context Cancellation During Stream

Cancel the context at random points during stream processing. Verify no goroutine leaks.

```go
func TestContextCancellationDuringStream(t *testing.T) {
    for delay := time.Duration(0); delay < 100*time.Millisecond; delay += 10 * time.Millisecond {
        ctx, cancel := context.WithTimeout(context.Background(), delay)
        defer cancel()

        resp := buildSlowMockSSEResponse(fixtureTextStream, 5*time.Millisecond)
        es := ai.NewEventStream()
        go func() {
            defer es.Close()
            provider.processStream(ctx, resp, testModel, es)
        }()

        result := es.Result()
        assert.NotNil(t, result)
        // Either completed or aborted — no hang
    }
}
```

### F5. Prompt-Level Safety Block

Simulate a response where `promptFeedback.blockReason` is set (prompt itself was blocked before any generation).

```go
func TestPromptLevelBlock(t *testing.T) {
    fixture := `data: {"promptFeedback":{"blockReason":"SAFETY","safetyRatings":[{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","probability":"HIGH","blocked":true}]}}`

    resp := buildMockSSEResponse([]string{fixture})
    es := ai.NewEventStream()
    go func() {
        defer es.Close()
        provider.processStream(context.Background(), resp, testModel, es)
    }()

    result := es.Result()
    assert.NotNil(t, result.Error)
    assert.Contains(t, result.Error.ErrorMessage, "DANGEROUS_CONTENT")
}
```

---

## 3. Comparison / Oracle Tests (No Server)

### O1. Gemini Go SDK Comparison

For a set of input messages and options, compare our generated request JSON with what the official `github.com/googleapis/go-genai` SDK produces. This catches wire format drift.

```go
func TestRequestMatchesSDK(t *testing.T) {
    if testing.Short() {
        t.Skip("requires go-genai SDK for comparison")
    }

    testCases := []struct {
        name     string
        messages []ai.Message
        tools    []ai.Tool
        system   string
    }{
        {"simple text", simpleTextMessages(), nil, ""},
        {"with system", simpleTextMessages(), nil, "Be helpful"},
        {"with tools", toolCallMessages(), testTools(), ""},
        {"with thinking", thinkingMessages(), nil, ""},
    }

    for _, tc := range testCases {
        t.Run(tc.name, func(t *testing.T) {
            ourReq := buildRequest(testModel, ai.Context{
                Messages:     tc.messages,
                Tools:        tc.tools,
                SystemPrompt: tc.system,
            }, ai.StreamOptions{})

            sdkReq := buildSDKRequest(tc.messages, tc.tools, tc.system)

            // Compare structurally (not byte-exact, since field ordering may differ)
            assertStructurallyEqual(t, ourReq, sdkReq)
        })
    }
}
```

### O2. Cross-Provider Event Sequence Comparison

For the same conceptual scenario (text response, tool call, thinking), verify that the Google provider emits the same sequence of `AssistantMessageEvent` types as the Anthropic provider would. The event sequence semantics should be consistent across providers.

```go
func TestEventSequenceMatchesAnthropic(t *testing.T) {
    scenarios := []struct {
        name           string
        googleFixture  string
        expectedEvents []ai.EventType
    }{
        {
            "simple text",
            fixtureTextStream,
            []ai.EventType{ai.EventTextDelta, ai.EventTextDelta, ai.EventDone},
        },
        {
            "thinking + text",
            fixtureThinkingStream,
            []ai.EventType{ai.EventThinkingDelta, ai.EventTextDelta, ai.EventDone},
        },
        {
            "tool call",
            fixtureToolCallStream,
            []ai.EventType{ai.EventToolCallStart, ai.EventToolCallEnd, ai.EventDone},
        },
    }

    for _, s := range scenarios {
        t.Run(s.name, func(t *testing.T) {
            events := replayFixture(s.googleFixture)
            types := extractEventTypes(events)
            assert.Equal(t, s.expectedEvents, types)
        })
    }
}
```

---

## 4. Deterministic Simulation Tests

### S1. Stream State Machine Simulation

Generate random sequences of Gemini SSE chunks (text, thinking, functionCall, finishReason, error) and verify:
- The state machine transitions are always valid
- Every stream terminates (reaches Done state)
- No intermediate state is reachable after finishReason
- Error states always produce an error event

```go
func TestStreamStateMachineSimulation(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        chunks := generateRandomChunkSequence(t)
        resp := buildMockSSEResponse(chunks)

        es := ai.NewEventStream()
        go func() {
            defer es.Close()
            provider.processStream(context.Background(), resp, testModel, es)
        }()

        var events []ai.AssistantMessageEvent
        for e := range es.C {
            events = append(events, e)
        }

        // Must have at least one terminal event
        assert.NotEmpty(t, events)
        lastEvent := events[len(events)-1]
        assert.True(t, lastEvent.Type == ai.EventDone || lastEvent.Type == ai.EventError,
            "stream did not terminate properly, last event: %s", lastEvent.Type)

        // No events after terminal
        for i, e := range events {
            if e.Type == ai.EventDone || e.Type == ai.EventError {
                assert.Equal(t, len(events)-1, i,
                    "events after terminal event at index %d", i)
                break
            }
        }
    })
}
```

### S2. Concurrent Provider Usage

Simulate N concurrent streams through the same Provider instance. Verify no data races, no cross-contamination between streams.

```go
func TestConcurrentStreams(t *testing.T) {
    provider := newTestProvider()
    n := 20

    var wg sync.WaitGroup
    results := make([]*ai.AssistantMessage, n)

    for i := 0; i < n; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            fixture := fmt.Sprintf(fixtureTextStreamTemplate, idx)
            es := replayFixtureAsStream(provider, fixture)
            result := es.Result()
            if result.Message != nil {
                results[idx] = result.Message
            }
        }(i)
    }

    wg.Wait()

    // Each result should contain its unique index text
    for i, msg := range results {
        if msg != nil {
            text := extractText(msg)
            assert.Contains(t, text, fmt.Sprintf("%d", i))
        }
    }
}
```

---

## 5. Benchmarks and Performance Tests

### B1. SSE Parsing Throughput

**Target:** > 50,000 chunks/sec for typical Gemini text chunks.

```go
func BenchmarkSSEParsing(b *testing.B) {
    fixture := loadFixture("text_stream_100_chunks.sse")
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        resp := buildMockSSEResponse(fixture)
        es := ai.NewEventStream()
        go func() {
            defer es.Close()
            provider.processStream(context.Background(), resp, testModel, es)
        }()
        es.Result() // drain
    }
}
```

### B2. Message Conversion Throughput

**Target:** > 100,000 message conversions/sec.

```go
func BenchmarkMessageConversion(b *testing.B) {
    msgs := generateLargeConversation(50) // 50 turns
    model := testModel

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        convertContents(msgs, model)
    }
}
```

### B3. Request Serialization

**Target:** > 50,000 req/sec for typical request with tools and thinking config.

```go
func BenchmarkRequestSerialization(b *testing.B) {
    req := buildRequest(testModel, testContext, testOpts)
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        json.Marshal(req)
    }
}
```

### B4. ThoughtSignature Encoding

**Target:** > 1,000,000 encode+decode cycles/sec for 256-byte signatures.

```go
func BenchmarkThoughtSignatureEncoding(b *testing.B) {
    sig := make([]byte, 256)
    rand.Read(sig)

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        encoded := base64.StdEncoding.EncodeToString(sig)
        base64.StdEncoding.DecodeString(encoded)
    }
}
```

---

## 6. Stress / Soak Tests

### SK1. 10,000 Sequential SSE Replays (Leak Detection)

Replay 10,000 SSE fixtures sequentially through the provider. Check after each batch of 1,000:
- No goroutine leaks (`runtime.NumGoroutine()` stable)
- No memory growth (heap allocs stable within 10%)
- Every stream terminates with Done or Error

```go
func TestSoak_SequentialReplays(t *testing.T) {
    if testing.Short() {
        t.Skip("soak test")
    }

    provider := newTestProvider()
    baseGoroutines := runtime.NumGoroutine()

    for batch := 0; batch < 10; batch++ {
        for i := 0; i < 1000; i++ {
            es := replayFixtureAsStream(provider, fixtureTextStream)
            result := es.Result()
            assert.NotNil(t, result)
        }

        current := runtime.NumGoroutine()
        assert.InDelta(t, baseGoroutines, current, 5,
            "goroutine leak at batch %d: base=%d current=%d", batch, baseGoroutines, current)
    }
}
```

### SK2. Mixed Fixture Soak

Run a mix of fixture types (text, thinking, tool call, error, safety block) in random order for 10 minutes. Track success/failure rates and verify no degradation.

```go
func TestSoak_MixedFixtures(t *testing.T) {
    if testing.Short() {
        t.Skip("soak test")
    }

    fixtures := []string{
        fixtureTextStream,
        fixtureThinkingStream,
        fixtureToolCallStream,
        fixtureSafetyBlock,
        fixtureAPIError,
    }

    provider := newTestProvider()
    deadline := time.Now().Add(10 * time.Minute)
    var total, errors atomic.Int64

    for time.Now().Before(deadline) {
        fixture := fixtures[rand.Intn(len(fixtures))]
        es := replayFixtureAsStream(provider, fixture)
        result := es.Result()
        total.Add(1)
        if result.Error != nil && fixture != fixtureAPIError && fixture != fixtureSafetyBlock {
            errors.Add(1)
        }
    }

    errorRate := float64(errors.Load()) / float64(total.Load())
    t.Logf("Total: %d, Unexpected errors: %d, Rate: %.4f%%",
        total.Load(), errors.Load(), errorRate*100)
    assert.Less(t, errorRate, 0.001) // < 0.1% unexpected errors
}
```

---

## 7. Security Tests

### SEC1. API Key Not Leaked in Error Messages

Verify that ProviderError messages never contain the API key.

```go
func TestAPIKeyNotInErrors(t *testing.T) {
    apiKey := "test-api-key-secret-12345"
    provider := New(apiKey)

    errorBodies := []struct {
        status int
        body   string
    }{
        {403, `{"error":{"message":"` + apiKey + ` is invalid"}}`},
        {429, `{"error":{"message":"rate limit for key ` + apiKey + `"}}`},
        {500, `{"error":{"message":"internal error processing ` + apiKey + `"}}`},
    }

    for _, tc := range errorBodies {
        err := classifyHTTPError(tc.status, []byte(tc.body))
        // The error message should be from the API body, which might contain the key.
        // But our ProviderError wrapping should NOT add the key.
        // (If the API itself includes the key in the error, that's their leak, not ours.)
        // Key check: our provider code doesn't inject the key into errors.
        assert.NotContains(t, err.Error(), apiKey)
    }
}
```

### SEC2. ThoughtSignature Treated as Opaque

Verify that the provider never inspects, modifies, or logs the contents of `thoughtSignature`. It must be treated as an opaque blob.

```go
func TestThoughtSignatureOpaque(t *testing.T) {
    // Build a message with a thought signature
    sig := []byte{0x00, 0xFF, 0xFE, 0xFD} // arbitrary bytes
    msg := buildAssistantWithThoughtSig(sig)

    // Convert to wire format
    content := convertModelContent(msg, true)

    // Find the thoughtSignature part
    var foundSig []byte
    for _, p := range content.Parts {
        if p.ThoughtSignature != nil {
            foundSig = p.ThoughtSignature
        }
    }

    // Must be byte-exact — no modification
    assert.Equal(t, sig, foundSig)
}
```

---

## 8. Manual QA Plan

### QA1. Real Gemini Conversation

1. Configure provider with a real Gemini API key
2. Send a multi-turn conversation (3+ turns)
3. Verify: each turn produces coherent text, usage counts increase, cost accumulates correctly

### QA2. Function Calling End-to-End

1. Define a simple tool (e.g., `get_time`)
2. Send a prompt that should trigger the tool
3. Verify: function call is received with correct name/args
4. Send function response back
5. Verify: model produces final answer incorporating the tool result

### QA3. Thinking Mode Verification

1. Enable thinking on a Gemini 2.5 model with explicit budget
2. Send a complex reasoning prompt
3. Verify: thinking events appear in the stream, thinking content visible, `thoughtsTokenCount` > 0

### QA4. Thought Signature Multi-Turn

1. Enable thinking with a model that produces thought signatures
2. Complete turn 1 — note the thought signature in the response
3. Send turn 2 with the turn 1 response preserved (including signature)
4. Verify: turn 2 request includes the signature, model response is coherent and builds on prior thinking

### QA5. Safety Block Behavior

1. Send a prompt that would trigger safety filtering (on a model with default safety settings)
2. Verify: clear error message indicating safety block, specific category identified
3. Re-send with safety settings set to OFF
4. Verify: response completes without block

---

## 9. CI Tier Mapping

| Tier | Tests | Trigger | Environment |
|------|-------|---------|-------------|
| **Tier 1: Fast** | Unit tests (conversion, mapping, errors, safety, JSON casing) | Every commit | Any OS |
| **Tier 2: Component** | Stub-server replay + fault injection, property tests | Every commit | Any OS |
| **Tier 3: Integration** | Live Gemini API smoke tests | PR merge to main | Any OS + `GOOGLE_API_KEY` |
| **Tier 4: Oracle** | SDK comparison, cross-provider event comparison | Weekly | Any OS + go-genai SDK |
| **Tier 5: Soak** | Sequential replays, mixed fixture soak | Weekly | Any OS |
| **Tier 6: Benchmark** | Parse throughput, conversion, serialization | PR merge to main (regression check) | Consistent CI instance |

### CI Configuration Notes

- Tier 1-2 tests: `go test ./internal/ai/provider/google/...` (no env vars or build tags needed)
- Tier 3 tests: `go test -tags=integration ./internal/ai/provider/google/...` (requires `GOOGLE_API_KEY`)
- Soak tests: `-tags=soak -timeout=15m`
- Benchmarks stored and compared against baseline (alert on > 20% regression)

---

## 10. Exit Criteria

Implementation is considered complete when ALL of the following pass:

### Must Pass

- [ ] All Tier 1 unit tests pass on Linux, macOS, and Windows
- [ ] All Tier 2 component tests (stub-server replay + fault injection) pass
- [ ] All property-based tests pass with 1000+ iterations
- [ ] All 6 acceptance criteria from the plan doc are demonstrated
- [ ] SSE parsing benchmark > 50,000 chunks/sec
- [ ] Message conversion benchmark > 100,000 conversions/sec
- [ ] `thoughtSignature` round-trip property test passes with 10,000 iterations
- [ ] Request JSON uses camelCase (verified by P5)
- [ ] Unit + component tests pass under `-race`

### Should Pass

- [ ] Tier 3 integration tests pass with real Gemini API
- [ ] Oracle test O1 (SDK comparison) validates request format for all test cases
- [ ] Soak test SK1 (10,000 replays): zero goroutine leaks
- [ ] Manual QA1-QA5 performed and documented
- [ ] Coverage > 85% for unit-testable code

### Stretch

- [ ] Cross-provider event sequence comparison (O2) validates consistency with Anthropic provider
- [ ] Mixed fixture soak (SK2) runs for 10 minutes with < 0.1% unexpected error rate
- [ ] Benchmark regression check integrated into CI (Tier 6)
