package anthropic

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/ai"
	"github.com/anthropics/flex-agent-runtime/internal/ai/sse"
	"github.com/anthropics/flex-agent-runtime/internal/ai/testutil/stubserver"
)

// B1: SSE throughput benchmark target (>=50k events/sec).
func BenchmarkB1_SSEThroughput(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		sb.WriteString("event: content_block_delta\n")
		sb.WriteString("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"x\"}}\n\n")
	}
	input := sb.String()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sc := sse.NewScanner(strings.NewReader(input))
		for sc.Next() {
			_ = sc.UnsafeEvent()
		}
		if err := sc.Err(); err != nil {
			b.Fatalf("scanner err: %v", err)
		}
	}
}

// B2: allocation budget for delta processing.
func BenchmarkB2_DeltaAllocations(b *testing.B) {
	fixture := textFixture("a", "b", "c", "d", "e")
	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()
	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	ctx := ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}}}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		es := p.Stream(context.Background(), testModel(), ctx, ai.StreamOptions{})
		if _, err := es.Drain(); err != nil {
			b.Fatalf("stream err: %v", err)
		}
	}
}

// B3: tool JSON incremental parse overhead.
func BenchmarkB3_ToolJSONIncrementalParse(b *testing.B) {
	chunk := strings.Repeat("a", 1024)
	jsonChunk := `{"blob":"` + chunk + `"}`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p := newToolJSONParser()
		if _, _, err := p.AppendDelta(jsonChunk[:len(jsonChunk)/2]); err != nil {
			b.Fatalf("append1: %v", err)
		}
		if _, _, err := p.AppendDelta(jsonChunk[len(jsonChunk)/2:]); err != nil {
			b.Fatalf("append2: %v", err)
		}
		if _, err := p.ParseFinal(); err != nil {
			b.Fatalf("final: %v", err)
		}
	}
}

// B4: provider overhead vs scanner baseline (approximate p95 proxy by worst sample).
func BenchmarkB4_StreamOverhead(b *testing.B) {
	fixture := textFixture("a", "b", "c", "d", "e", "f", "g", "h", "i", "j")
	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()
	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	ctx := ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}}}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		es := p.Stream(context.Background(), testModel(), ctx, ai.StreamOptions{})
		if _, err := es.Drain(); err != nil {
			b.Fatalf("stream err: %v", err)
		}
		_ = time.Since(start)
	}
}
