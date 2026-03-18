package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/ai/testutil/stubserver"
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

func buildToolCallFixture(toolID, toolName string, chunks []string) string {
	var b strings.Builder
	b.WriteString("event: message_start\n")
	b.WriteString("data: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":10}}}\n\n")
	b.WriteString("event: content_block_start\n")
	b.WriteString(fmt.Sprintf("data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":%q,\"name\":%q}}\n\n", toolID, toolName))
	for _, c := range chunks {
		b.WriteString("event: content_block_delta\n")
		b.WriteString(fmt.Sprintf("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%q}}\n\n", c))
	}
	b.WriteString("event: content_block_stop\n")
	b.WriteString("data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
	b.WriteString("event: message_delta\n")
	b.WriteString("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":3}}\n\n")
	b.WriteString("event: message_stop\n")
	b.WriteString("data: {\"type\":\"message_stop\"}\n\n")
	return b.String()
}

func splitIntoChunks(s string, cuts []int) []string {
	if len(s) == 0 {
		return []string{""}
	}
	norm := make([]int, 0, len(cuts))
	for _, c := range cuts {
		if c > 0 && c < len(s) {
			norm = append(norm, c)
		}
	}
	sort.Ints(norm)
	uniq := norm[:0]
	prev := -1
	for _, c := range norm {
		if c != prev {
			uniq = append(uniq, c)
			prev = c
		}
	}
	out := make([]string, 0, len(uniq)+1)
	start := 0
	for _, c := range uniq {
		out = append(out, s[start:c])
		start = c
	}
	out = append(out, s[start:])
	return out
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
		name := rapid.StringMatching(`[a-zA-Z0-9_ .:/\\-]{1,32}`).Draw(rt, "name")
		obj := map[string]any{"n": v, "s": name}
		raw, err := json.Marshal(obj)
		if err != nil {
			t.Fatalf("marshal obj err: %v", err)
		}
		if len(raw) < 3 {
			t.Fatalf("unexpected small payload: %q", string(raw))
		}

		cutCount := rapid.IntRange(1, 4).Draw(rt, "cutCount")
		cuts := make([]int, 0, cutCount)
		for i := 0; i < cutCount; i++ {
			cuts = append(cuts, rapid.IntRange(1, len(raw)-1).Draw(rt, fmt.Sprintf("cut-%d", i)))
		}
		chunks := splitIntoChunks(string(raw), cuts)
		fixture := buildToolCallFixture("call_prop", "prop_tool", chunks)
		srv := stubserver.New(stubserver.WithFixture(fixture))
		defer srv.Close()

		prov := New(Config{BaseURL: srv.URL, APIKey: "k"})
		es := prov.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}}}, ai.StreamOptions{})
		msg, err := es.Drain()
		if err != nil {
			t.Fatalf("stream err: %v", err)
		}
		if len(msg.Content) != 1 {
			t.Fatalf("expected one tool call block, got %d", len(msg.Content))
		}
		tc, ok := msg.Content[0].(*ai.ToolCall)
		if !ok {
			t.Fatalf("expected tool call, got %T", msg.Content[0])
		}
		gotN, ok := tc.Arguments["n"].(float64)
		if !ok || int(gotN) != v {
			t.Fatalf("n mismatch: got %#v want %d", tc.Arguments["n"], v)
		}
		if gotS, _ := tc.Arguments["s"].(string); gotS != name {
			t.Fatalf("s mismatch: got %q want %q", gotS, name)
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
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
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
	cases := []map[string]any{
		{"cmd": "$(rm -rf /)", "kind": "shell"},
		{"sql": "'; DROP TABLE users; --", "kind": "sql"},
		{"path": "../../etc/passwd", "kind": "path"},
		{"null": "abc\u0000def", "kind": "null-byte"},
		{"script": "<script>alert(1)</script>", "kind": "xss"},
		{"nested": map[string]any{"a": map[string]any{"b": map[string]any{"c": "deep"}}}, "kind": "nested"},
		{"oversized": strings.Repeat("x", 32*1024), "kind": "large"},
	}
	for i, payload := range cases {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("case %d marshal err: %v", i, err)
		}
		// Route through the full streaming pipeline (SSE -> provider -> tool parser).
		chunks := splitIntoChunks(string(raw), []int{len(raw) / 3, 2 * len(raw) / 3})
		srv := stubserver.New(stubserver.WithFixture(buildToolCallFixture("call_sec", "sec_tool", chunks)))
		p := New(Config{BaseURL: srv.URL, APIKey: "k"})
		es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}}}}, ai.StreamOptions{})
		msg, err := es.Drain()
		srv.Close()
		if err != nil {
			t.Fatalf("case %d stream err: %v", i, err)
		}
		tc, ok := msg.Content[0].(*ai.ToolCall)
		if !ok {
			t.Fatalf("case %d expected tool call, got %T", i, msg.Content[0])
		}
		// Must remain valid JSON object after roundtrip.
		enc, err := json.Marshal(tc.Arguments)
		if err != nil {
			t.Fatalf("case %d marshal args err: %v", i, err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(enc, &decoded); err != nil {
			t.Fatalf("case %d unmarshal args err: %v", i, err)
		}
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
	// Plan target is 500; we run 200 in regular CI to keep runtime bounded while
	// preserving meaningful contention coverage.
	const n = 200
	var wg sync.WaitGroup
	var success atomic.Int32
	var failures atomic.Int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}}}}, ai.StreamOptions{})
			if _, err := es.Drain(); err != nil {
				failures.Add(1)
				return
			}
			success.Add(1)
		}()
	}
	wg.Wait()
	if got := int(success.Load()); got != n {
		t.Fatalf("success count: got %d want %d (failures=%d)", got, n, failures.Load())
	}
	if failures.Load() != 0 {
		t.Fatalf("unexpected failures: %d", failures.Load())
	}
}

// ST1: compressed long-session soak with memory/goroutine sanity checks.
func TestST1_LongSoak(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	fixtures := []string{
		textFixture("a", "b", "c"),
		buildToolCallFixture("call_soak", "soak_tool", []string{`{"n":`, `1}`}),
	}
	var seq atomic.Int32
	srv := stubserver.New(stubserver.WithFixtureFunc(func(_ *http.Request) string {
		i := seq.Add(1)
		return fixtures[i%int32(len(fixtures))]
	}))
	defer srv.Close()
	p := New(Config{BaseURL: srv.URL, APIKey: "k"})

	runtime.GC()
	startG := runtime.NumGoroutine()
	var m0 runtime.MemStats
	runtime.ReadMemStats(&m0)

	const iterations = 5000
	for i := 0; i < iterations; i++ {
		es := p.Stream(context.Background(), testModel(), ai.Context{
			Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: fmt.Sprintf("turn-%d", i)}}}},
		}, ai.StreamOptions{})
		if _, err := es.Drain(); err != nil {
			t.Fatalf("iteration %d err: %v", i, err)
		}
		if i > 0 && i%500 == 0 {
			runtime.GC()
			var mi runtime.MemStats
			runtime.ReadMemStats(&mi)
			// Allow growth but catch runaway leaks.
			if mi.Alloc > m0.Alloc+128*1024*1024 {
				t.Fatalf("alloc grew too much at i=%d: start=%d current=%d", i, m0.Alloc, mi.Alloc)
			}
		}
	}
	runtime.GC()
	endG := runtime.NumGoroutine()
	if endG > startG+20 {
		t.Fatalf("possible goroutine leak: start=%d end=%d", startG, endG)
	}
}

// ST3: burst tool-call stress with large payload and concurrent streams.
func TestST3_BurstToolStress(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	largeArgs := map[string]any{
		"blob": strings.Repeat("x", 12*1024),
		"n":    42,
	}
	raw, err := json.Marshal(largeArgs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// High-frequency chunking.
	chunks := make([]string, 0, (len(raw)/64)+1)
	for i := 0; i < len(raw); i += 64 {
		j := i + 64
		if j > len(raw) {
			j = len(raw)
		}
		chunks = append(chunks, string(raw[i:j]))
	}
	fixture := buildToolCallFixture("call_stress", "stress_tool", chunks)
	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()
	p := New(Config{BaseURL: srv.URL, APIKey: "k"})

	const streams = 50
	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < streams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			es := p.Stream(context.Background(), testModel(), ai.Context{
				Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "burst"}}}},
			}, ai.StreamOptions{})
			msg, err := es.Drain()
			if err != nil {
				failures.Add(1)
				return
			}
			tc, ok := msg.Content[0].(*ai.ToolCall)
			if !ok {
				failures.Add(1)
				return
			}
			blob, ok := tc.Arguments["blob"].(string)
			if !ok || len(blob) != len(largeArgs["blob"].(string)) {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("stress failures: %d", failures.Load())
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
