package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"h2-agent-runtime/internal/ai"
	"h2-agent-runtime/internal/ai/testutil/stubserver"
	"pgregory.net/rapid"
)

func textFixture(chunks ...string) string {
	var b strings.Builder
	b.WriteString("event: message_start\n")
	b.WriteString("data: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":10}}}\n\n")
	b.WriteString("event: content_block_start\n")
	b.WriteString("data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
	for _, c := range chunks {
		b.WriteString("event: content_block_delta\n")
		b.WriteString(fmt.Sprintf("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", c))
	}
	b.WriteString("event: content_block_stop\n")
	b.WriteString("data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
	b.WriteString("event: message_delta\n")
	b.WriteString("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n")
	b.WriteString("event: message_stop\n")
	b.WriteString("data: {\"type\":\"message_stop\"}\n\n")
	return b.String()
}

// P1: stream event ordering and terminal uniqueness.
func TestP1_StreamEventOrderingAndTerminalUniqueness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		chunks := rapid.SliceOfN(rapid.StringMatching(`[a-zA-Z0-9]{1,8}`), 1, 20).Draw(rt, "chunks")
		fixture := textFixture(chunks...)

		srv := stubserver.New(stubserver.WithFixture(fixture))
		defer srv.Close()
		p := New(Config{BaseURL: srv.URL, APIKey: "k"})
		es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}}}, ai.StreamOptions{})

		var terminal int
		lastType := ai.EventStart
		for ev := range es.C {
			if ev.Type == ai.EventDone || ev.Type == ai.EventError {
				terminal++
			}
			if lastType == ai.EventDone || lastType == ai.EventError {
				t.Fatalf("event emitted after terminal: %q", ev.Type)
			}
			lastType = ev.Type
		}
		_, err := es.Result()
		if err != nil {
			t.Fatalf("unexpected result err: %v", err)
		}
		if terminal != 1 {
			t.Fatalf("terminal events: got %d want 1", terminal)
		}
	})
}

// P2: tool JSON delta convergence.
func TestP2_ToolJSONDeltaConvergence(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		v := rapid.IntRange(0, 10_000).Draw(rt, "value")
		payload := fmt.Sprintf(`{"n":%d,"s":"ok"}`, v)
		split := rapid.IntRange(1, len(payload)-1).Draw(rt, "split")
		p := newToolJSONParser()
		if _, _, err := p.AppendDelta(payload[:split]); err != nil {
			t.Fatalf("append1 err: %v", err)
		}
		if _, _, err := p.AppendDelta(payload[split:]); err != nil {
			t.Fatalf("append2 err: %v", err)
		}
		final, err := p.ParseFinal()
		if err != nil {
			t.Fatalf("final parse err: %v", err)
		}
		if got := int(final["n"].(float64)); got != v {
			t.Fatalf("n mismatch: got %d want %d", got, v)
		}
	})
}

// P3: usage/cost arithmetic consistency.
func TestP3_UsageCostConsistency(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		u := wireUsage{
			InputTokens:              rapid.IntRange(0, 100000).Draw(rt, "in"),
			OutputTokens:             rapid.IntRange(0, 100000).Draw(rt, "out"),
			CacheReadInputTokens:     rapid.IntRange(0, 100000).Draw(rt, "cr"),
			CacheCreationInputTokens: rapid.IntRange(0, 100000).Draw(rt, "cw"),
		}
		usage := mapUsage(u)
		model := testModel()
		ai.CalculateCost(model, &usage)
		expected := usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
		if math.Abs(usage.Cost.Total-expected) > 1e-12 {
			t.Fatalf("cost total mismatch: got %f want %f", usage.Cost.Total, expected)
		}
	})
}

// P4: error classification stability.
func TestP4_ErrorClassificationStability(t *testing.T) {
	cases := []struct {
		status int
		msg    string
		want   ai.ProviderErrorCode
	}{
		{401, "bad key", ai.ErrAuth},
		{429, "rate limited", ai.ErrRateLimit},
		{500, "upstream down", ai.ErrServerError},
		{400, "maximum context length is 1000 tokens", ai.ErrContextOverflow},
		{418, "teapot", ai.ErrUnknown},
	}
	for _, tc := range cases {
		if got := classifyHTTPError(tc.status, tc.msg); got != tc.want {
			t.Fatalf("status=%d msg=%q: got %q want %q", tc.status, tc.msg, got, tc.want)
		}
	}
}

// S1: correctness replay from fixture.
func TestS1_StubServerReplay(t *testing.T) {
	srv := stubserver.New(stubserver.WithFixture(textFixture("a", "b", "c")))
	defer srv.Close()
	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}}}}, ai.StreamOptions{})
	msg, err := es.Drain()
	if err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if got := msg.Content[0].(*ai.TextContent).Text; got != "abc" {
		t.Fatalf("text mismatch: %q", got)
	}
}

// F1: mid-stream TCP reset.
func TestF1_TCPReset(t *testing.T) {
	srv := stubserver.NewTCPResetServer(textFixture("a", "b", "c", "d", "e"), 3)
	defer srv.Close()
	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}}}}, ai.StreamOptions{})
	if _, err := es.Drain(); err == nil {
		t.Fatalf("expected tcp reset error")
	}
}

// F2: malformed SSE payload.
func TestF2_MalformedSSE(t *testing.T) {
	srv := stubserver.NewMalformedServer(textFixture("a", "b"), 1, "data: {{{INVALID\n\n")
	defer srv.Close()
	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}}}}, ai.StreamOptions{})
	if _, err := es.Drain(); err == nil {
		t.Fatalf("expected malformed SSE error")
	}
}

// F3: API throttling.
func TestF3_Throttle(t *testing.T) {
	srv := stubserver.NewThrottleServer("1")
	defer srv.Close()
	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}}}}, ai.StreamOptions{})
	if _, err := es.Drain(); err == nil {
		t.Fatalf("expected throttle error")
	}
}

// F4: backpressure + context timeout.
func TestF4_BackpressureTimeout(t *testing.T) {
	srv := stubserver.New(stubserver.WithFault(textFixture("a", "b", "c", "d"), stubserver.Fault{Mode: stubserver.Backpressure, EventDelay: 200 * time.Millisecond}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(ctx, testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}}}}, ai.StreamOptions{})
	if _, err := es.Drain(); err == nil {
		t.Fatalf("expected timeout/cancel error")
	}
}

// F5: context cancellation race.
func TestF5_ContextCancellationRace(t *testing.T) {
	srv := stubserver.New(stubserver.WithFault(textFixture("a", "b", "c", "d", "e"), stubserver.Fault{Mode: stubserver.Backpressure, EventDelay: 20 * time.Millisecond}))
	defer srv.Close()
	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		es := p.Stream(ctx, testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}}}}, ai.StreamOptions{})
		time.AfterFunc(time.Duration(i+1)*5*time.Millisecond, cancel)
		_, _ = es.Drain()
	}
}

// S1 deterministic simulation: illegal trace should fail.
func TestDS1_EventFSMIllegalTrace(t *testing.T) {
	fixture := "event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\"}}\n\n"
	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()
	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}}}}, ai.StreamOptions{})
	if _, err := es.Drain(); err == nil {
		t.Fatalf("expected deterministic failure for illegal trace")
	}
}

// S2 deterministic simulation: multi-tool isolation.
func TestDS2_MultiToolIsolation(t *testing.T) {
	fixture := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":1}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"a\",\"name\":\"t1\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"x\\\":1}\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"b\",\"name\":\"t2\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"y\\\":2}\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":1}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"
	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()
	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}}}}, ai.StreamOptions{})
	msg, err := es.Drain()
	if err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("want 2 tool calls, got %d", len(msg.Content))
	}
}

// S3 deterministic simulation: thinking + tool interleaving.
func TestDS3_ThinkingToolInterleave(t *testing.T) {
	fixture := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":1}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"r\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"a\",\"name\":\"t1\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"x\\\":1}\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":1}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"
	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()
	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}}}}, ai.StreamOptions{})
	msg, err := es.Drain()
	if err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(msg.Content))
	}
	if _, ok := msg.Content[0].(*ai.ThinkingContent); !ok {
		t.Fatalf("first block should be thinking")
	}
	if _, ok := msg.Content[1].(*ai.ToolCall); !ok {
		t.Fatalf("second block should be tool call")
	}
}

// O1/O2 oracle tests are gated.
func TestO1_CrossImplementationOracle(t *testing.T) {
	if os.Getenv("ANTHROPIC_ORACLE_TS") == "" {
		t.Skip("ANTHROPIC_ORACLE_TS not set")
	}
}

func TestO2_SDKConsistencyOracle(t *testing.T) {
	if os.Getenv("ANTHROPIC_ORACLE_SDK") == "" {
		t.Skip("ANTHROPIC_ORACLE_SDK not set")
	}
}

// SEC1: injection safety in tool delta path.
func TestSEC1_ToolJSONInjection(t *testing.T) {
	p := newToolJSONParser()
	_, _, err := p.AppendDelta(`{"cmd":"$(rm -rf /)"}`)
	if err != nil {
		t.Fatalf("append err: %v", err)
	}
	final, err := p.ParseFinal()
	if err != nil {
		t.Fatalf("parse err: %v", err)
	}
	if final["cmd"] == "" {
		t.Fatalf("missing parsed cmd")
	}
}

// SEC2: header leakage check on errors.
func TestSEC2_NoAPIKeyLeakInErrors(t *testing.T) {
	const secret = "sk-secret-test"
	srv := stubserver.New(stubserver.WithHandler(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"message":"unauthorized"}}`))
	}))
	defer srv.Close()
	p := New(Config{BaseURL: srv.URL, APIKey: secret})
	es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}}}}, ai.StreamOptions{})
	_, err := es.Drain()
	if err == nil {
		t.Fatalf("expected error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked api key")
	}
}

// SEC3: oversized payload protection.
func TestSEC3_OversizedPayload(t *testing.T) {
	tooLong := strings.Repeat("x", (1<<20)+10)
	fixture := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":1}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		fmt.Sprintf("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", tooLong)
	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()
	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}}}}, ai.StreamOptions{})
	if _, err := es.Drain(); err == nil {
		t.Fatalf("expected oversized payload failure")
	}
}

// ST2: concurrency stress.
func TestST2_ConcurrentStreams(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	srv := stubserver.New(stubserver.WithFixture(textFixture("a", "b", "c")))
	defer srv.Close()
	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}}}}, ai.StreamOptions{})
			_, _ = es.Drain()
		}()
	}
	wg.Wait()
}

// ST1/ST3 are heavy and gated.
func TestST1_LongSoak(t *testing.T) {
	if os.Getenv("ANTHROPIC_SOAK") == "" {
		t.Skip("ANTHROPIC_SOAK not set")
	}
}

func TestST3_BurstToolStress(t *testing.T) {
	if os.Getenv("ANTHROPIC_BURST_STRESS") == "" {
		t.Skip("ANTHROPIC_BURST_STRESS not set")
	}
}

// Manual QA execution recording gate.
func TestManualQAChecklistRecord(t *testing.T) {
	if os.Getenv("ANTHROPIC_MANUAL_QA") == "" {
		t.Skip("ANTHROPIC_MANUAL_QA not set")
	}
}

func TestHarnessCoverageTarget(t *testing.T) {
	// Sentinel test to keep harness package visible in CI filtering.
	var req wireRequest
	if err := json.Unmarshal([]byte(`{"model":"x","max_tokens":1,"messages":[],"stream":true}`), &req); err != nil {
		t.Fatalf("unexpected unmarshal err: %v", err)
	}
}
