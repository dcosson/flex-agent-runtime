package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"h2-agent-runtime/internal/ai"
	"h2-agent-runtime/internal/ai/testutil/stubserver"
	"pgregory.net/rapid"
)

// --- Pointer helpers ---

func boolPtr(b bool) *bool { return &b }

// --- SSE Fixture Builders ---
// These build proper SSE data lines using json.Marshal for safe escaping.

func buildTextSSE(chunks []string, finish string, prompt, comp int) string {
	var buf strings.Builder
	// Role chunk
	c := chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
		Delta: chunkDelta{Role: "assistant"},
	}}}
	b, _ := json.Marshal(c)
	fmt.Fprintf(&buf, "data: %s\n\n", b)

	// Text delta chunks
	for _, text := range chunks {
		t := text
		c = chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
			Delta: chunkDelta{Content: &t},
		}}}
		b, _ = json.Marshal(c)
		fmt.Fprintf(&buf, "data: %s\n\n", b)
	}

	// Finish
	c = chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
		Delta: chunkDelta{}, FinishReason: &finish,
	}}}
	b, _ = json.Marshal(c)
	fmt.Fprintf(&buf, "data: %s\n\n", b)

	// Usage
	c = chatChunk{ID: "t", Object: "chat.completion.chunk",
		Usage: &chunkUsage{PromptTokens: prompt, CompletionTokens: comp, TotalTokens: prompt + comp}}
	b, _ = json.Marshal(c)
	fmt.Fprintf(&buf, "data: %s\n\n", b)

	buf.WriteString("data: [DONE]\n\n")
	return buf.String()
}

func buildToolCallSSE(callID, name string, argChunks []string) string {
	var buf strings.Builder
	idx := 0

	// Init chunk
	c := chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
		Delta: chunkDelta{
			Role:      "assistant",
			ToolCalls: []toolCall{{Index: &idx, ID: callID, Type: "function", Function: functionCall{Name: name}}},
		},
	}}}
	b, _ := json.Marshal(c)
	fmt.Fprintf(&buf, "data: %s\n\n", b)

	// Argument deltas
	for _, chunk := range argChunks {
		c = chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
			Delta: chunkDelta{
				ToolCalls: []toolCall{{Index: &idx, Function: functionCall{Arguments: chunk}}},
			},
		}}}
		b, _ = json.Marshal(c)
		fmt.Fprintf(&buf, "data: %s\n\n", b)
	}

	// Finish
	finish := "tool_calls"
	c = chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
		Delta: chunkDelta{}, FinishReason: &finish,
	}}}
	b, _ = json.Marshal(c)
	fmt.Fprintf(&buf, "data: %s\n\n", b)

	// Usage
	c = chatChunk{ID: "t", Object: "chat.completion.chunk",
		Usage: &chunkUsage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8}}
	b, _ = json.Marshal(c)
	fmt.Fprintf(&buf, "data: %s\n\n", b)

	buf.WriteString("data: [DONE]\n\n")
	return buf.String()
}

type multiToolDef struct {
	callID string
	name   string
	args   string
}

func buildMultiToolSSE(tools []multiToolDef) string {
	var buf strings.Builder

	// Init chunk with all tool calls
	var tcs []toolCall
	for i, t := range tools {
		idx := i
		tcs = append(tcs, toolCall{Index: &idx, ID: t.callID, Type: "function", Function: functionCall{Name: t.name}})
	}
	c := chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
		Delta: chunkDelta{Role: "assistant", ToolCalls: tcs},
	}}}
	b, _ := json.Marshal(c)
	fmt.Fprintf(&buf, "data: %s\n\n", b)

	// Split each tool's args into chunks and interleave round-robin
	type argChunk struct {
		idx  int
		data string
	}
	chunksPerTool := make([][]string, len(tools))
	maxRounds := 0
	for i, t := range tools {
		chunksPerTool[i] = splitEvenly(t.args, 10)
		if len(chunksPerTool[i]) > maxRounds {
			maxRounds = len(chunksPerTool[i])
		}
	}
	var interleaved []argChunk
	for round := 0; round < maxRounds; round++ {
		for i := range tools {
			if round < len(chunksPerTool[i]) {
				interleaved = append(interleaved, argChunk{idx: i, data: chunksPerTool[i][round]})
			}
		}
	}

	for _, ac := range interleaved {
		idx := ac.idx
		c = chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
			Delta: chunkDelta{
				ToolCalls: []toolCall{{Index: &idx, Function: functionCall{Arguments: ac.data}}},
			},
		}}}
		b, _ = json.Marshal(c)
		fmt.Fprintf(&buf, "data: %s\n\n", b)
	}

	// Finish
	finish := "tool_calls"
	c = chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
		Delta: chunkDelta{}, FinishReason: &finish,
	}}}
	b, _ = json.Marshal(c)
	fmt.Fprintf(&buf, "data: %s\n\n", b)

	// Usage
	c = chatChunk{ID: "t", Object: "chat.completion.chunk",
		Usage: &chunkUsage{PromptTokens: 5, CompletionTokens: 5, TotalTokens: 10}}
	b, _ = json.Marshal(c)
	fmt.Fprintf(&buf, "data: %s\n\n", b)

	buf.WriteString("data: [DONE]\n\n")
	return buf.String()
}

func splitEvenly(s string, size int) []string {
	if size <= 0 || len(s) == 0 {
		return []string{s}
	}
	var out []string
	for i := 0; i < len(s); i += size {
		end := i + size
		if end > len(s) {
			end = len(s)
		}
		out = append(out, s[i:end])
	}
	return out
}

// streamAndCollect runs a stream and collects all events plus the final result.
func streamAndCollect(p *Provider, model ai.Model, ctxOpt ...context.Context) ([]ai.AssistantMessageEvent, ai.AssistantMessage, error) {
	ctx := context.Background()
	if len(ctxOpt) > 0 {
		ctx = ctxOpt[0]
	}
	es := p.Stream(ctx, model, ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{})

	var events []ai.AssistantMessageEvent
	for ev := range es.C {
		events = append(events, ev)
	}
	msg, err := es.Result()
	return events, msg, err
}

func buildDeeplyNestedJSON(depth int) string {
	var buf strings.Builder
	for i := 0; i < depth; i++ {
		buf.WriteString(`{"n":`)
	}
	buf.WriteString(`1`)
	for i := 0; i < depth; i++ {
		buf.WriteString(`}`)
	}
	return buf.String()
}

// =====================================================================
// Property Tests (P1-P6)
// =====================================================================

// P1: Stream Event Ordering and Terminal Uniqueness
func TestP1_StreamEventOrdering(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		numChunks := rapid.IntRange(1, 20).Draw(t, "numChunks")
		chunks := make([]string, numChunks)
		for i := range chunks {
			chunks[i] = fmt.Sprintf("word%d ", i)
		}

		fixture := buildTextSSE(chunks, "stop", 10, numChunks)
		srv := stubserver.New(stubserver.WithFixture(fixture))
		defer srv.Close()

		p := New(Config{BaseURL: srv.URL, APIKey: "k"})
		events, _, err := streamAndCollect(p, testModel())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Exactly one terminal event
		terminalCount := 0
		for _, e := range events {
			if e.Type == ai.EventDone || e.Type == ai.EventError {
				terminalCount++
			}
		}
		if terminalCount != 1 {
			t.Fatalf("expected exactly 1 terminal event, got %d", terminalCount)
		}

		// Terminal event is last
		last := events[len(events)-1]
		if last.Type != ai.EventDone && last.Type != ai.EventError {
			t.Fatalf("last event should be terminal, got %q", last.Type)
		}

		// Start event is first
		if events[0].Type != ai.EventStart {
			t.Fatalf("first event should be start, got %q", events[0].Type)
		}
	})
}

// P2: Tool JSON Delta Convergence
func TestP2_ToolJSONDeltaConvergence(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		numFields := rapid.IntRange(1, 8).Draw(t, "numFields")
		obj := make(map[string]any, numFields)
		for i := 0; i < numFields; i++ {
			key := fmt.Sprintf("f_%d", i)
			switch rapid.IntRange(0, 2).Draw(t, fmt.Sprintf("type_%d", i)) {
			case 0:
				obj[key] = fmt.Sprintf("val_%d", rapid.IntRange(0, 999).Draw(t, fmt.Sprintf("sval_%d", i)))
			case 1:
				obj[key] = float64(rapid.IntRange(-1000, 1000).Draw(t, fmt.Sprintf("nval_%d", i)))
			case 2:
				obj[key] = rapid.IntRange(0, 1).Draw(t, fmt.Sprintf("bval_%d", i)) == 1
			}
		}

		objBytes, _ := json.Marshal(obj)
		objStr := string(objBytes)

		chunkSize := rapid.IntRange(1, 20).Draw(t, "chunkSize")
		argChunks := splitEvenly(objStr, chunkSize)

		fixture := buildToolCallSSE("call_test", "test_tool", argChunks)
		srv := stubserver.New(stubserver.WithFixture(fixture))
		defer srv.Close()

		p := New(Config{BaseURL: srv.URL, APIKey: "k"})
		events, _, err := streamAndCollect(p, testModel())
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}

		var finalTC *ai.ToolCall
		for _, e := range events {
			if e.Type == ai.EventToolCallEnd && e.ToolCall != nil {
				finalTC = e.ToolCall
			}
		}
		if finalTC == nil {
			t.Fatal("no tool call end event found")
			return
		}

		got, _ := json.Marshal(finalTC.Arguments)
		expected, _ := json.Marshal(obj)
		if string(got) != string(expected) {
			t.Fatalf("args mismatch:\ngot:    %s\nexpect: %s", got, expected)
		}
	})
}

// P3: Usage/Cost Arithmetic Consistency
func TestP3_UsageCostConsistency(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		prompt := rapid.IntRange(0, 100000).Draw(t, "prompt")
		comp := rapid.IntRange(0, 50000).Draw(t, "comp")
		cached := rapid.IntRange(0, prompt).Draw(t, "cached")

		inputCost := rapid.Float64Range(0, 100).Draw(t, "inputCost")
		outputCost := rapid.Float64Range(0, 100).Draw(t, "outputCost")
		cacheReadCost := rapid.Float64Range(0, 100).Draw(t, "cacheReadCost")

		model := ai.Model{
			ID: "test", API: "openai-completions", Provider: "openai",
			Cost: ai.ModelCost{Input: inputCost, Output: outputCost, CacheRead: cacheReadCost},
		}

		usage := ai.Usage{
			Input:       prompt,
			Output:      comp,
			CacheRead:   cached,
			TotalTokens: prompt + comp,
		}

		ai.CalculateCost(model, &usage)

		if usage.Cost.Input < 0 || usage.Cost.Output < 0 || usage.Cost.CacheRead < 0 || usage.Cost.Total < 0 {
			t.Fatalf("negative cost: %+v", usage.Cost)
		}

		expected := usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
		if math.Abs(usage.Cost.Total-expected) > 1e-10 {
			t.Fatalf("total %.15f != sum %.15f", usage.Cost.Total, expected)
		}
	})
}

// P4: Multi-Tool Index Isolation
func TestP4_MultiToolIndexIsolation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		numTools := rapid.IntRange(2, 4).Draw(t, "numTools")

		tools := make([]multiToolDef, numTools)
		expectedArgs := make([]map[string]any, numTools)

		for i := range tools {
			args := map[string]any{
				"tool_idx": float64(i),
				"value":    fmt.Sprintf("arg_%d", rapid.IntRange(0, 999).Draw(t, fmt.Sprintf("val_%d", i))),
			}
			argsJSON, _ := json.Marshal(args)
			tools[i] = multiToolDef{
				callID: fmt.Sprintf("call_%d", i),
				name:   fmt.Sprintf("tool_%d", i),
				args:   string(argsJSON),
			}
			expectedArgs[i] = args
		}

		fixture := buildMultiToolSSE(tools)
		srv := stubserver.New(stubserver.WithFixture(fixture))
		defer srv.Close()

		p := New(Config{BaseURL: srv.URL, APIKey: "k"})
		events, _, err := streamAndCollect(p, testModel())
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}

		endEvents := make(map[int]*ai.ToolCall)
		for _, e := range events {
			if e.Type == ai.EventToolCallEnd && e.ToolCall != nil {
				endEvents[e.ContentIndex] = e.ToolCall
			}
		}

		if len(endEvents) != numTools {
			t.Fatalf("expected %d tool calls, got %d", numTools, len(endEvents))
		}

		for i, def := range tools {
			tc, ok := endEvents[i]
			if !ok {
				t.Fatalf("missing tool call at index %d", i)
			}
			if tc.ID != def.callID {
				t.Fatalf("tool %d: ID %q != %q", i, tc.ID, def.callID)
			}
			if tc.Name != def.name {
				t.Fatalf("tool %d: name %q != %q", i, tc.Name, def.name)
			}
			got, _ := json.Marshal(tc.Arguments)
			exp, _ := json.Marshal(expectedArgs[i])
			if string(got) != string(exp) {
				t.Fatalf("tool %d args mismatch:\ngot:    %s\nexpect: %s", i, got, exp)
			}
		}
	})
}

// P5: ModelCompat Flag Independence
func TestP5_ModelCompatFlagIndependence(t *testing.T) {
	fixture := makeTextFixture("ok", "stop", 1, 1)

	cases := []struct {
		name   string
		compat *ai.ModelCompat
		check  func(t *testing.T, req chatRequest)
	}{
		{
			name:   "developer_role",
			compat: &ai.ModelCompat{SupportsDeveloperRole: boolPtr(true)},
			check: func(t *testing.T, req chatRequest) {
				if req.Messages[0].Role != "developer" {
					t.Fatalf("expected developer role, got %q", req.Messages[0].Role)
				}
			},
		},
		{
			name:   "store",
			compat: &ai.ModelCompat{SupportsStore: boolPtr(true)},
			check: func(t *testing.T, req chatRequest) {
				if req.Store == nil || !*req.Store {
					t.Fatal("store should be true")
				}
			},
		},
		{
			name:   "max_tokens_field",
			compat: &ai.ModelCompat{MaxTokensField: "max_tokens"},
			check: func(t *testing.T, req chatRequest) {
				if req.MaxTokens == nil {
					t.Fatal("max_tokens should be set")
				}
				if req.MaxCompletionTokens != nil {
					t.Fatal("max_completion_tokens should be nil")
				}
			},
		},
		{
			name:   "no_usage_streaming",
			compat: &ai.ModelCompat{SupportsUsageInStreaming: boolPtr(false)},
			check: func(t *testing.T, req chatRequest) {
				if req.StreamOptions != nil {
					t.Fatal("stream_options should be nil when usage streaming not supported")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := stubserver.New(stubserver.WithFixture(fixture))
			defer srv.Close()

			model := testModelWithCompat(tc.compat)
			max := 100
			p := New(Config{BaseURL: srv.URL, APIKey: "k"})
			es := p.Stream(context.Background(), model, ai.Context{
				SystemPrompt: "system",
				Messages:     []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
			}, ai.StreamOptions{MaxTokens: &max})
			_, _ = es.Drain()

			reqs := srv.Requests()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request, got %d", len(reqs))
			}
			var payload chatRequest
			if err := json.Unmarshal(reqs[0].Body, &payload); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			tc.check(t, payload)
		})
	}
}

// P6: Error Classification Stability
func TestP6_ErrorClassificationStability(t *testing.T) {
	known := []struct {
		status int
		expect ai.ProviderErrorCode
	}{
		{401, ai.ErrAuth},
		{403, ai.ErrAuth},
		{429, ai.ErrRateLimit},
		{500, ai.ErrServerError},
		{502, ai.ErrServerError},
		{503, ai.ErrServerError},
	}
	for _, tc := range known {
		got := classifyHTTPError(tc.status, "test error")
		if got != tc.expect {
			t.Errorf("status %d: got %q, want %q", tc.status, got, tc.expect)
		}
	}

	unknowns := []int{400, 404, 405, 406, 410, 418, 422, 450, 499}
	for _, status := range unknowns {
		got := classifyHTTPError(status, "generic error")
		if got == ai.ErrAuth || got == ai.ErrRateLimit || got == ai.ErrServerError {
			t.Errorf("status %d: misclassified as %q", status, got)
		}
	}
}

// =====================================================================
// Fault Injection Tests (F1-F6)
// =====================================================================

// F1: Mid-Stream TCP Reset
func TestF1_MidStreamTCPReset(t *testing.T) {
	fixture := buildTextSSE([]string{"Hello", " world", "!", " More", " text"}, "stop", 10, 5)

	for _, cutAfter := range []int{0, 1, 3, 5} {
		t.Run(fmt.Sprintf("cutAfter=%d", cutAfter), func(t *testing.T) {
			srv := stubserver.NewTCPResetServer(fixture, cutAfter)
			defer srv.Close()

			p := New(Config{BaseURL: srv.URL, APIKey: "k"})
			_, _, err := streamAndCollect(p, testModel())
			if err == nil {
				t.Fatal("expected error after TCP reset")
			}
		})
	}
}

// F2: Malformed SSE Payload
func TestF2_MalformedSSEPayload(t *testing.T) {
	fixture := buildTextSSE([]string{"ok"}, "stop", 1, 1)

	malformed := []struct {
		name string
		data string
	}{
		{"truncated_json", `data: {"choices": [{"delta": {"content": "ok"` + "\n\n"},
		{"not_json", "data: not json at all\n\n"},
		{"null_choices", "data: {\"choices\": null}\n\n"},
		{"empty_data", "data: \n\n"},
	}

	for _, tc := range malformed {
		t.Run(tc.name, func(t *testing.T) {
			srv := stubserver.NewMalformedServer(fixture, 0, tc.data)
			defer srv.Close()

			p := New(Config{BaseURL: srv.URL, APIKey: "k"})
			// Must not panic
			events, _, _ := streamAndCollect(p, testModel())
			_ = events
		})
	}
}

// F3: API Throttling Storm
func TestF3_APIThrottlingStorm(t *testing.T) {
	srv := stubserver.NewThrottleServer("1")
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	_, _, err := streamAndCollect(p, testModel())
	if err == nil {
		t.Fatal("expected error from throttle")
	}
}

// F4: Slow-Consumer Backpressure
func TestF4_SlowConsumerBackpressure(t *testing.T) {
	fixture := buildTextSSE([]string{"a", "b", "c", "d", "e"}, "stop", 5, 5)
	srv := stubserver.NewBackpressureServer(fixture, 10*time.Millisecond)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(ctx, testModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{})

	for e := range es.C {
		_ = e
		time.Sleep(5 * time.Millisecond)
	}
	// Must complete without hanging
}

// F5: Context Cancellation Races
func TestF5_ContextCancellationRaces(t *testing.T) {
	chunks := make([]string, 50)
	for i := range chunks {
		chunks[i] = fmt.Sprintf("chunk%d ", i)
	}
	fixture := buildTextSSE(chunks, "stop", 10, 50)

	for i := 0; i < 50; i++ {
		t.Run(fmt.Sprintf("iter_%d", i), func(t *testing.T) {
			t.Parallel()
			srv := stubserver.New(stubserver.WithFixture(fixture))
			defer srv.Close()

			ctx, cancel := context.WithCancel(context.Background())
			p := New(Config{BaseURL: srv.URL, APIKey: "k"})
			es := p.Stream(ctx, testModel(), ai.Context{
				Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
			}, ai.StreamOptions{})

			go func() {
				time.Sleep(time.Duration(rand.Intn(20)) * time.Millisecond)
				cancel()
			}()

			for range es.C {
			} // must not panic
			es.Result() // must not block
		})
	}
}

// F6: Non-2xx Response Without Body
func TestF6_EmptyBodyResponse(t *testing.T) {
	for _, status := range []int{400, 401, 429, 500, 502, 503} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			srv := stubserver.NewEmptyBodyServer(status)
			defer srv.Close()

			p := New(Config{BaseURL: srv.URL, APIKey: "k"})
			_, _, err := streamAndCollect(p, testModel())
			if err == nil {
				t.Fatalf("expected error for status %d", status)
			}
		})
	}
}

// =====================================================================
// Deterministic Simulation Tests (S2-S4)
// =====================================================================

// S2: Multi-Tool Interleaved Deltas
func TestS2_MultiToolInterleavedDeltas(t *testing.T) {
	tools := []multiToolDef{
		{"call_0", "read_file", `{"path":"main.go"}`},
		{"call_1", "write_file", `{"path":"out.txt","content":"hello"}`},
		{"call_2", "bash", `{"command":"go build ./..."}`},
	}

	fixture := buildMultiToolSSE(tools)
	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	events, _, err := streamAndCollect(p, testModel())
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}

	finalCalls := make(map[string]*ai.ToolCall)
	for _, e := range events {
		if e.Type == ai.EventToolCallEnd && e.ToolCall != nil {
			finalCalls[e.ToolCall.ID] = e.ToolCall
		}
	}

	if len(finalCalls) != 3 {
		t.Fatalf("expected 3 tool calls, got %d", len(finalCalls))
	}
	for _, tool := range tools {
		tc, ok := finalCalls[tool.callID]
		if !ok {
			t.Fatalf("missing tool call %q", tool.callID)
		}
		if tc.Name != tool.name {
			t.Fatalf("tool %q: name %q != %q", tool.callID, tc.Name, tool.name)
		}
		var expected map[string]any
		json.Unmarshal([]byte(tool.args), &expected)
		got, _ := json.Marshal(tc.Arguments)
		exp, _ := json.Marshal(expected)
		if string(got) != string(exp) {
			t.Fatalf("tool %q args mismatch:\ngot:    %s\nexpect: %s", tool.callID, got, exp)
		}
	}
}

// S3: Reasoning + Text + Tool Interleaving
func TestS3_ReasoningTextToolInterleaving(t *testing.T) {
	var buf strings.Builder

	// Role chunk
	c := chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
		Delta: chunkDelta{Role: "assistant"},
	}}}
	b, _ := json.Marshal(c)
	fmt.Fprintf(&buf, "data: %s\n\n", b)

	// Reasoning deltas
	for _, r := range []string{"Let me ", "think..."} {
		rs := r
		c = chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
			Delta: chunkDelta{Reasoning: &rs},
		}}}
		b, _ = json.Marshal(c)
		fmt.Fprintf(&buf, "data: %s\n\n", b)
	}

	// Text deltas
	for _, txt := range []string{"Here's ", "the answer"} {
		ts := txt
		c = chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
			Delta: chunkDelta{Content: &ts},
		}}}
		b, _ = json.Marshal(c)
		fmt.Fprintf(&buf, "data: %s\n\n", b)
	}

	// Tool call
	idx := 0
	c = chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
		Delta: chunkDelta{
			ToolCalls: []toolCall{{Index: &idx, ID: "call_1", Type: "function", Function: functionCall{Name: "calc"}}},
		},
	}}}
	b, _ = json.Marshal(c)
	fmt.Fprintf(&buf, "data: %s\n\n", b)

	argChunk := `{"x":1}`
	c = chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
		Delta: chunkDelta{
			ToolCalls: []toolCall{{Index: &idx, Function: functionCall{Arguments: argChunk}}},
		},
	}}}
	b, _ = json.Marshal(c)
	fmt.Fprintf(&buf, "data: %s\n\n", b)

	// Finish
	finish := "tool_calls"
	c = chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
		Delta: chunkDelta{}, FinishReason: &finish,
	}}}
	b, _ = json.Marshal(c)
	fmt.Fprintf(&buf, "data: %s\n\n", b)

	// Usage
	c = chatChunk{ID: "t", Object: "chat.completion.chunk",
		Usage: &chunkUsage{PromptTokens: 10, CompletionTokens: 8, TotalTokens: 18}}
	b, _ = json.Marshal(c)
	fmt.Fprintf(&buf, "data: %s\n\n", b)

	buf.WriteString("data: [DONE]\n\n")

	srv := stubserver.New(stubserver.WithFixture(buf.String()))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	_, msg, err := streamAndCollect(p, reasoningModel())
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}

	// Content should be: thinking, text, tool call (in that order)
	if len(msg.Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(msg.Content))
	}

	thinking, ok := msg.Content[0].(*ai.ThinkingContent)
	if !ok {
		t.Fatalf("content[0]: expected ThinkingContent, got %T", msg.Content[0])
	}
	if thinking.Thinking != "Let me think..." {
		t.Fatalf("thinking mismatch: %q", thinking.Thinking)
	}

	text, ok := msg.Content[1].(*ai.TextContent)
	if !ok {
		t.Fatalf("content[1]: expected TextContent, got %T", msg.Content[1])
	}
	if text.Text != "Here's the answer" {
		t.Fatalf("text mismatch: %q", text.Text)
	}

	tc, ok := msg.Content[2].(*ai.ToolCall)
	if !ok {
		t.Fatalf("content[2]: expected ToolCall, got %T", msg.Content[2])
	}
	if tc.Name != "calc" || tc.ID != "call_1" {
		t.Fatalf("tool call mismatch: %+v", tc)
	}
}

// S4: Usage in Various Positions
func TestS4_UsageInVariousPositions(t *testing.T) {
	cases := []struct {
		name   string
		build  func() string
		input  int
		output int
		cache  int
	}{
		{
			name: "separate_final_chunk",
			build: func() string {
				return buildTextSSE([]string{"ok"}, "stop", 10, 5)
			},
			input: 10, output: 5,
		},
		{
			name: "usage_with_cache",
			build: func() string {
				var buf strings.Builder
				txt := "ok"
				c := chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
					Delta: chunkDelta{Content: &txt},
				}}}
				b, _ := json.Marshal(c)
				fmt.Fprintf(&buf, "data: %s\n\n", b)
				finish := "stop"
				c = chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
					Delta: chunkDelta{}, FinishReason: &finish,
				}}}
				b, _ = json.Marshal(c)
				fmt.Fprintf(&buf, "data: %s\n\n", b)
				c = chatChunk{ID: "t", Object: "chat.completion.chunk",
					Usage: &chunkUsage{
						PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15,
						PromptTokensDetails: &promptTokensDetails{CachedTokens: 3},
					}}
				b, _ = json.Marshal(c)
				fmt.Fprintf(&buf, "data: %s\n\n", b)
				buf.WriteString("data: [DONE]\n\n")
				return buf.String()
			},
			input: 10, output: 5, cache: 3,
		},
		{
			name: "no_usage",
			build: func() string {
				var buf strings.Builder
				txt := "ok"
				c := chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
					Delta: chunkDelta{Content: &txt},
				}}}
				b, _ := json.Marshal(c)
				fmt.Fprintf(&buf, "data: %s\n\n", b)
				finish := "stop"
				c = chatChunk{ID: "t", Object: "chat.completion.chunk", Choices: []chunkChoice{{
					Delta: chunkDelta{}, FinishReason: &finish,
				}}}
				b, _ = json.Marshal(c)
				fmt.Fprintf(&buf, "data: %s\n\n", b)
				buf.WriteString("data: [DONE]\n\n")
				return buf.String()
			},
			input: 0, output: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := stubserver.New(stubserver.WithFixture(tc.build()))
			defer srv.Close()

			p := New(Config{BaseURL: srv.URL, APIKey: "k"})
			_, msg, err := streamAndCollect(p, testModel())
			if err != nil {
				t.Fatalf("stream error: %v", err)
			}
			if msg.Usage.Input != tc.input {
				t.Fatalf("input: %d != %d", msg.Usage.Input, tc.input)
			}
			if msg.Usage.Output != tc.output {
				t.Fatalf("output: %d != %d", msg.Usage.Output, tc.output)
			}
			if msg.Usage.CacheRead != tc.cache {
				t.Fatalf("cache: %d != %d", msg.Usage.CacheRead, tc.cache)
			}
		})
	}
}

// =====================================================================
// Security Tests (SEC1-SEC3)
// =====================================================================

// SEC1: Malicious Tool JSON
func TestSEC1_MaliciousToolJSON(t *testing.T) {
	malicious := []struct {
		name string
		args string
	}{
		{"sql_injection", `{"cmd":"'; DROP TABLE users; --"}`},
		{"path_traversal", `{"path":"../../../etc/passwd"}`},
		{"null_bytes", `{"data":"test\u0000test"}`},
		{"deeply_nested", buildDeeplyNestedJSON(30)},
		{"large_string", `{"key":"` + strings.Repeat("a", 1<<16) + `"}`},
		{"unicode_escape", `{"key":"\u003cscript\u003ealert(1)\u003c/script\u003e"}`},
		{"empty_object", `{}`},
	}

	for _, tc := range malicious {
		t.Run(tc.name, func(t *testing.T) {
			argChunks := splitEvenly(tc.args, 50)
			fixture := buildToolCallSSE("call_1", "test", argChunks)
			srv := stubserver.New(stubserver.WithFixture(fixture))
			defer srv.Close()

			p := New(Config{BaseURL: srv.URL, APIKey: "k"})
			events, _, _ := streamAndCollect(p, testModel()) // must not panic

			for _, e := range events {
				if e.Type == ai.EventToolCallEnd && e.ToolCall != nil {
					_, err := json.Marshal(e.ToolCall.Arguments)
					if err != nil {
						t.Fatalf("tool args not serializable: %v", err)
					}
				}
			}
		})
	}
}

// SEC2: No API Key Leakage
func TestSEC2_NoAPIKeyLeakage(t *testing.T) {
	secretKey := "sk-test-super-secret-key-12345"
	srv := stubserver.NewStatusCodeServer(500, `{"error":{"message":"internal error"}}`)
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: secretKey})
	_, _, err := streamAndCollect(p, testModel())
	if err == nil {
		t.Fatal("expected error")
	}

	errStr := err.Error()
	if strings.Contains(errStr, secretKey) {
		t.Fatal("API key leaked in error message")
	}
	if strings.Contains(errStr, "sk-test") {
		t.Fatal("API key prefix leaked in error message")
	}
}

// SEC3: Oversized Payload Protection
func TestSEC3_OversizedPayloadProtection(t *testing.T) {
	bigContent := strings.Repeat("x", 1<<20) // 1MB (keep reasonable for test speed)
	fixture := buildTextSSE([]string{bigContent}, "stop", 1, 1)

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(ctx, testModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{})

	for range es.C {
	}
	es.Result() // must complete without OOM
}

// =====================================================================
// Oracle Tests (O3)
// =====================================================================

// O3: Compat Endpoint Request Format Verification
func TestO3_CompatEndpointRequestFormat(t *testing.T) {
	fixture := makeTextFixture("ok", "stop", 1, 1)

	cases := []struct {
		name   string
		model  ai.Model
		system string
		check  func(t *testing.T, req chatRequest)
	}{
		{
			name: "default_openai", model: testModel(), system: "test system",
			check: func(t *testing.T, req chatRequest) {
				if req.Messages[0].Role != "system" {
					t.Fatalf("expected system role, got %q", req.Messages[0].Role)
				}
				if req.MaxCompletionTokens == nil {
					t.Fatal("expected max_completion_tokens")
				}
				if req.MaxTokens != nil {
					t.Fatal("max_tokens should be nil")
				}
			},
		},
		{
			name: "reasoning_model", model: reasoningModel(), system: "test system",
			check: func(t *testing.T, req chatRequest) {
				if req.Messages[0].Role != "developer" {
					t.Fatalf("expected developer role, got %q", req.Messages[0].Role)
				}
			},
		},
		{
			name: "old_max_tokens", model: testModelWithCompat(&ai.ModelCompat{MaxTokensField: "max_tokens"}), system: "",
			check: func(t *testing.T, req chatRequest) {
				if req.MaxTokens == nil {
					t.Fatal("max_tokens should be set")
				}
				if req.MaxCompletionTokens != nil {
					t.Fatal("max_completion_tokens should be nil")
				}
			},
		},
		{
			name: "store_enabled", model: testModelWithCompat(&ai.ModelCompat{SupportsStore: boolPtr(true)}), system: "",
			check: func(t *testing.T, req chatRequest) {
				if req.Store == nil || !*req.Store {
					t.Fatal("expected store: true")
				}
			},
		},
		{
			name: "no_stream_usage", model: testModelWithCompat(&ai.ModelCompat{SupportsUsageInStreaming: boolPtr(false)}), system: "",
			check: func(t *testing.T, req chatRequest) {
				if req.StreamOptions != nil {
					t.Fatal("expected no stream_options")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := stubserver.New(stubserver.WithFixture(fixture))
			defer srv.Close()

			max := 100
			p := New(Config{BaseURL: srv.URL, APIKey: "k"})
			es := p.Stream(context.Background(), tc.model, ai.Context{
				SystemPrompt: tc.system,
				Messages:     []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
			}, ai.StreamOptions{MaxTokens: &max})
			_, _ = es.Drain()

			reqs := srv.Requests()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request, got %d", len(reqs))
			}
			var payload chatRequest
			if err := json.Unmarshal(reqs[0].Body, &payload); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			tc.check(t, payload)
		})
	}
}
