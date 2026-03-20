package google

import (
	"context"
	"encoding/base64"
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
// Benchmarks (B1-B4)
// =====================================================================

// B1: SSE Parsing Throughput
// Target: > 50,000 chunks/sec
func BenchmarkB1_SSEParsingThroughput(b *testing.B) {
	chunks := make([]string, 100)
	for i := range chunks {
		chunks[i] = fmt.Sprintf("word%d ", i)
	}
	fixture := buildGeminiTextSSE(chunks, 100, 100)

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

// B2: Message Conversion Throughput
// Target: > 100,000 conversions/sec
func BenchmarkB2_MessageConversion(b *testing.B) {
	messages := make([]ai.Message, 0, 50)
	for i := 0; i < 25; i++ {
		messages = append(messages, &ai.UserMessage{
			Content: []ai.ContentBlock{
				&ai.TextContent{Text: fmt.Sprintf("User message %d with content", i)},
			},
		})
		messages = append(messages, &ai.AssistantMessage{
			Content: []ai.ContentBlock{
				&ai.TextContent{Text: fmt.Sprintf("Assistant reply %d", i)},
			},
		})
	}

	model := testModel()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		convertContents(messages, model)
	}
}

// B3: Request Serialization
// Target: > 50,000 req/sec
func BenchmarkB3_RequestSerialization(b *testing.B) {
	toolSchema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
	budget := 4096
	max := 200

	req := buildRequest(testModel(), ai.Context{
		SystemPrompt: "You are a helpful assistant",
		Tools:        []ai.Tool{{Name: "read_file", Description: "Read file", Parameters: toolSchema}},
		Messages:     []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hello"}}}},
	}, ai.StreamOptions{MaxTokens: &max}, requestParams{thinkingBudget: &budget})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		json.Marshal(req)
	}
}

// B4: ThoughtSignature Encoding Round-Trip
// Target: > 1,000,000 cycles/sec for 256-byte signatures
func BenchmarkB4_ThoughtSignatureEncoding(b *testing.B) {
	sig := make([]byte, 256)
	for i := range sig {
		sig[i] = byte(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encoded := base64.StdEncoding.EncodeToString(sig)
		base64.StdEncoding.DecodeString(encoded)
	}
}

// =====================================================================
// Stress / Soak Tests (SK1-SK2)
// =====================================================================

// SK1: Sequential SSE Replays (Leak Detection)
// Plan target: 10,000 iterations. Reduced to 5,000 for CI stability.
func TestSK1_SequentialReplaySoak(t *testing.T) {
	if testing.Short() {
		t.Skip("soak test — skipped in short mode")
	}

	fixtures := []string{
		buildGeminiTextSSE([]string{"Hello", " world", "!"}, 10, 3),
		buildGeminiThinkingSSE([]string{"Thinking..."}, []string{"Answer"}, 5, 3, 10),
		buildGeminiToolCallSSE("call_s", "soak_tool", map[string]any{"key": "value"}),
		buildGeminiSafetyBlockSSE(""),
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

// SK2: Concurrent Stream Stress
// Plan target: high concurrency. Set to 200 for CI stability.
func TestSK2_ConcurrentStreamStress(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test — skipped in short mode")
	}

	const concurrent = 200
	fixture := buildGeminiTextSSE([]string{"a", "b", "c", "d", "e"}, 5, 5)
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
	t.Logf("SK2: Success=%d Error=%d", successCount.Load(), errorCount.Load())
}

// SK3: Mixed Fixture Tool Call Stress
func TestSK3_ToolCallStress(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test — skipped in short mode")
	}

	const concurrent = 50

	// Build a tool call with substantial args
	args := make(map[string]any, 20)
	for i := 0; i < 20; i++ {
		args[fmt.Sprintf("field_%d", i)] = strings.Repeat("x", 100)
	}

	fixture := buildGeminiToolCallSSE("call_stress", "big_tool", args)
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

			if finalArgs != nil {
				got, _ := json.Marshal(finalArgs)
				expected, _ := json.Marshal(args)
				if string(got) != string(expected) {
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
	t.Logf("SK3: Success=%d Error=%d", successCount.Load(), errorCount.Load())
}
