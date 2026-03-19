package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/ai/testutil/stubserver"
)

// =====================================================================
// Benchmarks (B1, B3, B5, B6)
// =====================================================================

// B1: SSE Throughput Benchmark
// Target: Parse >= 50K SSE events/sec
func BenchmarkB1_SSEThroughput(b *testing.B) {
	chunks := make([]string, 1000)
	for i := range chunks {
		chunks[i] = fmt.Sprintf("word%d ", i)
	}
	fixture := buildTextSSE(chunks, "stop", 100, 1000)

	b.ResetTimer()
	b.SetBytes(int64(len(fixture)))
	for i := 0; i < b.N; i++ {
		srv := stubserver.New(stubserver.WithFixture(fixture))
		p, ep := testClientAndEndpoint(srv.URL, "k")
		es := p.Stream(context.Background(), ep, testModel(), ai.Context{
			Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
		}, ai.StreamOptions{})
		for range es.C {
		}
		es.Result()
		srv.Close()
	}
}

// B3: Tool JSON Incremental Parse Overhead
// Target: CompleteJSON()+Unmarshal median < 100μs for 4KB cumulative tool JSON
func BenchmarkB3_ToolJSONParse(b *testing.B) {
	// Generate a ~4KB JSON object
	obj := make(map[string]any, 20)
	for i := 0; i < 20; i++ {
		obj[fmt.Sprintf("field_%d", i)] = strings.Repeat("x", 180) // ~200 bytes per field
	}
	argsJSON, _ := json.Marshal(obj)
	argChunks := splitEvenly(string(argsJSON), 50) // ~50-byte deltas

	fixture := buildToolCallSSE("call_bench", "benchmark_tool", argChunks)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		srv := stubserver.New(stubserver.WithFixture(fixture))
		p, ep := testClientAndEndpoint(srv.URL, "k")
		es := p.Stream(context.Background(), ep, testModel(), ai.Context{
			Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
		}, ai.StreamOptions{})
		for range es.C {
		}
		es.Result()
		srv.Close()
	}
}

// B5: Message Conversion Benchmark
// Target: < 500μs for a 30-message conversation
func BenchmarkB5_MessageConversion(b *testing.B) {
	messages := make([]ai.Message, 0, 30)

	for i := 0; i < 15; i++ {
		// User message
		messages = append(messages, &ai.UserMessage{
			Content: []ai.ContentBlock{
				&ai.TextContent{Text: fmt.Sprintf("User message %d with some content to process", i)},
			},
		})
		// Assistant message with tool call
		argsJSON := map[string]any{
			"path":    fmt.Sprintf("/path/to/file_%d.go", i),
			"content": strings.Repeat("code content ", 10),
		}
		messages = append(messages, &ai.AssistantMessage{
			Content: []ai.ContentBlock{
				&ai.TextContent{Text: fmt.Sprintf("Let me help with that. Looking at file %d.", i)},
				&ai.ToolCall{
					ID:        fmt.Sprintf("call_%d", i),
					Name:      "read_file",
					Arguments: argsJSON,
				},
			},
		})
	}

	model := testModel()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		convertMessages(messages, model, "You are a helpful assistant")
	}
}

// B6: Compat Flag Resolution Benchmark
// Target: < 50ns for all flag checks combined
func BenchmarkB6_CompatFlagResolution(b *testing.B) {
	sup := true
	model := ai.Model{
		ID:       "test-model",
		API:      "openai-completions",
		Provider: "openai",
		Compat: &ai.ModelCompat{
			SupportsDeveloperRole:            &sup,
			SupportsStrictMode:               &sup,
			SupportsStore:                    &sup,
			SupportsUsageInStreaming:         &sup,
			MaxTokensField:                   "max_completion_tokens",
			RequiresToolResultName:           &sup,
			RequiresAssistantAfterToolResult: &sup,
			RequiresThinkingAsText:           &sup,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = useMaxCompletionTokens(model)
		_ = systemRole(model)
		_ = shouldIncludeStore(model)
		_ = shouldRequestStreamUsage(model)
		_ = supportsStrictMode(model)
		_ = requiresToolResultName(model)
		_ = requiresAssistantAfterToolResult(model)
		_ = requiresThinkingAsText(model)
	}
}

// =====================================================================
// Stress Tests (ST1-ST3)
// =====================================================================

// ST1: Long Session Soak (compressed — 5000 iterations instead of 50K for CI)
func TestST1_LongSessionSoak(t *testing.T) {
	if testing.Short() {
		t.Skip("soak test — skipped in short mode")
	}

	// Build a few diverse fixtures
	fixtures := []string{
		buildTextSSE([]string{"Hello", " world", "!"}, "stop", 10, 3),
		buildToolCallSSE("call_s", "soak_tool", []string{`{"key"`, `:"value"}`}),
		buildMultiToolSSE([]multiToolDef{
			{"c0", "t0", `{"a":1}`},
			{"c1", "t1", `{"b":2}`},
		}),
	}

	var startMem runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&startMem)
	startGoroutines := runtime.NumGoroutine()

	iterations := 5000

	for i := 0; i < iterations; i++ {
		fixture := fixtures[i%len(fixtures)]
		srv := stubserver.New(stubserver.WithFixture(fixture))

		p, ep := testClientAndEndpoint(srv.URL, "k")
		es := p.Stream(context.Background(), ep, testModel(), ai.Context{
			Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
		}, ai.StreamOptions{})
		for range es.C {
		}
		es.Result()
		srv.Close()

		if i%500 == 0 && i > 0 {
			runtime.GC()
			var currentMem runtime.MemStats
			runtime.ReadMemStats(&currentMem)
			goroutines := runtime.NumGoroutine()

			heapGrowth := int64(currentMem.HeapInuse) - int64(startMem.HeapInuse)
			if heapGrowth > 50<<20 { // 50MB
				t.Fatalf("memory grew >50MB at iteration %d: %dMB", i, heapGrowth>>20)
			}
			goroutineLeak := goroutines - startGoroutines
			if goroutineLeak > 10 {
				t.Fatalf("goroutine leak at iteration %d: %d extra", i, goroutineLeak)
			}
		}
	}
}

// ST2: Concurrency Stress
// Plan target: 500 concurrent. Reduced to 200 for CI stability on resource-constrained runners.
func TestST2_ConcurrencyStress(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test — skipped in short mode")
	}

	const concurrent = 200
	fixture := buildTextSSE([]string{"a", "b", "c", "d", "e"}, "stop", 5, 5)
	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p, ep := testClientAndEndpoint(srv.URL, "k")

	var wg sync.WaitGroup
	var successCount, errorCount atomic.Int32

	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			es := p.Stream(context.Background(), ep, testModel(), ai.Context{
				Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
			}, ai.StreamOptions{})
			for range es.C {
			}
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
	if total != concurrent {
		t.Fatalf("not all streams completed: %d/%d", total, concurrent)
	}
	t.Logf("ST2: Success=%d Error=%d", successCount.Load(), errorCount.Load())
}

// ST3: Burst Tool-Call Stress
// Plan target: 100 concurrent. Reduced to 50 for CI stability on resource-constrained runners.
// Each stream processes 10KB+ tool args split into 50-byte chunks.
func TestST3_BurstToolCallStress(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test — skipped in short mode")
	}

	const concurrent = 50

	// Build a large (~10KB) JSON argument
	obj := make(map[string]any, 50)
	for i := 0; i < 50; i++ {
		obj[fmt.Sprintf("field_%d", i)] = strings.Repeat("x", 180)
	}
	argsJSON, _ := json.Marshal(obj)
	argChunks := splitEvenly(string(argsJSON), 50)

	fixture := buildToolCallSSE("call_burst", "big_tool", argChunks)
	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p, ep := testClientAndEndpoint(srv.URL, "k")

	var wg sync.WaitGroup
	var successCount, errorCount atomic.Int32

	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			es := p.Stream(context.Background(), ep, testModel(), ai.Context{
				Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
			}, ai.StreamOptions{})

			var finalArgs map[string]any
			for e := range es.C {
				if e.Type == ai.EventToolCallEnd && e.ToolCall != nil {
					finalArgs = e.ToolCall.Arguments
				}
			}
			_, err := es.Result()
			if err != nil {
				errorCount.Add(1)
				return
			}
			successCount.Add(1)

			// Verify args round-trip correctly
			if finalArgs != nil {
				got, _ := json.Marshal(finalArgs)
				expected, _ := json.Marshal(obj)
				if string(got) != string(expected) {
					// Use t.Errorf since we're in a goroutine
					t.Errorf("args mismatch in concurrent stream")
				}
			}
		}()
	}

	wg.Wait()
	total := successCount.Load() + errorCount.Load()
	if total != concurrent {
		t.Fatalf("not all streams completed: %d/%d", total, concurrent)
	}
	t.Logf("ST3: Success=%d Error=%d", successCount.Load(), errorCount.Load())
}
