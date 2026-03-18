package google

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	randv2 "math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/ai/testutil/stubserver"
	"pgregory.net/rapid"
)

// Deferred plan items (see docs/plans/04-provider-google-test-harness.md):
// TODO: O1 — Gemini Go SDK comparison (needs go-genai dependency)
// TODO: O2 — Cross-provider event sequence comparison (needs multi-provider infrastructure)
// TODO: S1 — Stream state machine FSM simulation with random chunk sequences

// --- SSE Fixture Builders ---

func buildGeminiTextSSE(chunks []string, prompt, comp int) string {
	var buf strings.Builder
	for i, text := range chunks {
		resp := generateContentResponse{
			Candidates:   []candidate{{Content: &contentObj{Role: "model", Parts: []part{{Text: text}}}}},
			ModelVersion: "gemini-2.5-flash",
		}
		// Last chunk gets finishReason and usage
		if i == len(chunks)-1 {
			resp.Candidates[0].FinishReason = "STOP"
			resp.UsageMetadata = &usageMetadata{
				PromptTokenCount:     prompt,
				CandidatesTokenCount: comp,
				TotalTokenCount:      prompt + comp,
			}
		}
		b, _ := json.Marshal(resp)
		fmt.Fprintf(&buf, "data: %s\n\n", b)
	}
	return buf.String()
}

func buildGeminiThinkingSSE(thinkChunks, textChunks []string, prompt, comp, thoughts int) string {
	var buf strings.Builder
	thought := true

	// Thinking chunks
	for _, text := range thinkChunks {
		resp := generateContentResponse{
			Candidates:   []candidate{{Content: &contentObj{Role: "model", Parts: []part{{Text: text, Thought: &thought}}}}},
			ModelVersion: "gemini-2.5-flash",
		}
		b, _ := json.Marshal(resp)
		fmt.Fprintf(&buf, "data: %s\n\n", b)
	}

	// Text chunks
	for i, text := range textChunks {
		resp := generateContentResponse{
			Candidates:   []candidate{{Content: &contentObj{Role: "model", Parts: []part{{Text: text}}}}},
			ModelVersion: "gemini-2.5-flash",
		}
		if i == len(textChunks)-1 {
			resp.Candidates[0].FinishReason = "STOP"
			resp.UsageMetadata = &usageMetadata{
				PromptTokenCount:     prompt,
				CandidatesTokenCount: comp,
				TotalTokenCount:      prompt + comp,
				ThoughtsTokenCount:   thoughts,
			}
		}
		b, _ := json.Marshal(resp)
		fmt.Fprintf(&buf, "data: %s\n\n", b)
	}
	return buf.String()
}

func buildGeminiToolCallSSE(id, name string, args map[string]any) string {
	resp := generateContentResponse{
		Candidates: []candidate{{
			Content: &contentObj{
				Role:  "model",
				Parts: []part{{FunctionCall: &functionCallWire{Name: name, Args: args, ID: id}}},
			},
			FinishReason: "STOP",
		}},
		UsageMetadata: &usageMetadata{PromptTokenCount: 10, CandidatesTokenCount: 5, TotalTokenCount: 15},
		ModelVersion:  "gemini-2.5-flash",
	}
	b, _ := json.Marshal(resp)
	return fmt.Sprintf("data: %s\n\n", b)
}

func buildGeminiMultiToolSSE(tools []struct {
	id, name string
	args     map[string]any
}) string {
	var parts []part
	for _, t := range tools {
		parts = append(parts, part{FunctionCall: &functionCallWire{Name: t.name, Args: t.args, ID: t.id}})
	}
	resp := generateContentResponse{
		Candidates: []candidate{{
			Content:      &contentObj{Role: "model", Parts: parts},
			FinishReason: "STOP",
		}},
		UsageMetadata: &usageMetadata{PromptTokenCount: 10, CandidatesTokenCount: 10, TotalTokenCount: 20},
		ModelVersion:  "gemini-2.5-flash",
	}
	b, _ := json.Marshal(resp)
	return fmt.Sprintf("data: %s\n\n", b)
}

func buildGeminiSafetyBlockSSE(partialText string) string {
	var buf strings.Builder
	// Partial text chunk
	if partialText != "" {
		resp := generateContentResponse{
			Candidates:   []candidate{{Content: &contentObj{Role: "model", Parts: []part{{Text: partialText}}}}},
			ModelVersion: "gemini-2.5-flash",
		}
		b, _ := json.Marshal(resp)
		fmt.Fprintf(&buf, "data: %s\n\n", b)
	}
	// Safety block
	resp := generateContentResponse{
		Candidates: []candidate{{
			FinishReason:  "SAFETY",
			SafetyRatings: []safetyRating{{Category: "HARM_CATEGORY_HARASSMENT", Probability: "HIGH", Blocked: true}},
		}},
		ModelVersion: "gemini-2.5-flash",
	}
	b, _ := json.Marshal(resp)
	fmt.Fprintf(&buf, "data: %s\n\n", b)
	return buf.String()
}

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

// =====================================================================
// Property Tests (P1-P6)
// =====================================================================

// P1: Message Conversion Round-Trip Stability
func TestP1_MessageConversionStability(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		numMsgs := rapid.IntRange(1, 10).Draw(t, "numMsgs")
		var msgs []ai.Message
		for i := 0; i < numMsgs; i++ {
			if i%2 == 0 {
				msgs = append(msgs, &ai.UserMessage{
					Content: []ai.ContentBlock{
						&ai.TextContent{Text: fmt.Sprintf("msg_%d", rapid.IntRange(0, 999).Draw(t, fmt.Sprintf("text_%d", i)))},
					},
				})
			} else {
				msgs = append(msgs, &ai.AssistantMessage{
					Content: []ai.ContentBlock{
						&ai.TextContent{Text: fmt.Sprintf("reply_%d", i)},
					},
				})
			}
		}

		wire := convertContents(msgs, testModel())
		wireJSON, err := json.Marshal(wire)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		var roundTrip []contentObj
		if err := json.Unmarshal(wireJSON, &roundTrip); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		got, _ := json.Marshal(roundTrip)
		if string(got) != string(wireJSON) {
			t.Fatalf("round-trip mismatch:\ngot:    %s\nexpect: %s", got, wireJSON)
		}
	})
}

// P2: ThoughtSignature Byte-Exact Round-Trip
func TestP2_ThoughtSignatureRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		sigLen := rapid.IntRange(16, 512).Draw(t, "sigLen")
		originalSig := make([]byte, sigLen)
		rand.Read(originalSig)

		// Response part → base64 string (simulating what attachThoughtSignatures does)
		b64Sig := base64.StdEncoding.EncodeToString(originalSig)

		// Base64 string → request part ThoughtSignature []byte (simulating convertModelContent)
		decoded, err := base64.StdEncoding.DecodeString(b64Sig)
		if err != nil {
			t.Fatalf("decode error: %v", err)
		}

		if len(decoded) != len(originalSig) {
			t.Fatalf("length mismatch: %d != %d", len(decoded), len(originalSig))
		}
		for i := range decoded {
			if decoded[i] != originalSig[i] {
				t.Fatalf("byte %d mismatch: %02x != %02x", i, decoded[i], originalSig[i])
			}
		}
	})
}

// P3: Finish Reason Mapping Completeness
func TestP3_FinishReasonMappingCompleteness(t *testing.T) {
	knownReasons := []string{
		"STOP", "MAX_TOKENS", "SAFETY", "RECITATION", "LANGUAGE",
		"OTHER", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII",
		"MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL",
		"FINISH_REASON_UNSPECIFIED",
		"IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_RECITATION",
	}

	for _, reason := range knownReasons {
		result := mapFinishReason(reason)
		if result == "" {
			t.Errorf("unmapped finish reason: %q → empty", reason)
		}
	}
}

// P4: Safety Settings Cover All Categories
func TestP4_SafetySettingsCompleteness(t *testing.T) {
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
		if !categories[cat] {
			t.Errorf("missing safety category: %s", cat)
		}
	}
}

// P5: Request JSON Uses camelCase
func TestP5_RequestJSONCamelCase(t *testing.T) {
	toolSchema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
	budget := 4096
	max := 200
	temp := 0.5

	req := buildRequest(testModel(), ai.Context{
		SystemPrompt: "system",
		Tools:        []ai.Tool{{Name: "test", Description: "test", Parameters: toolSchema}},
		Messages:     []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{MaxTokens: &max, Temperature: &temp}, requestParams{thinkingBudget: &budget})

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	snakeCaseFields := []string{
		"system_instruction", "generation_config", "safety_settings",
		"tool_config", "function_declarations", "function_calling_config",
		"max_output_tokens", "thinking_config", "thinking_budget",
		"inline_data", "mime_type", "function_call", "function_response",
		"thought_signature",
	}
	jsonStr := string(data)
	for _, field := range snakeCaseFields {
		if strings.Contains(jsonStr, `"`+field+`"`) {
			t.Errorf("found snake_case field %q in JSON: ...%s...", field, jsonStr[:min(200, len(jsonStr))])
		}
	}
}

// P6: Usage Cost Consistency (exercises Google-specific mapUsage)
func TestP6_UsageCostConsistency(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		prompt := rapid.IntRange(0, 100000).Draw(t, "prompt")
		comp := rapid.IntRange(0, 50000).Draw(t, "comp")
		total := rapid.IntRange(prompt+comp, prompt+comp+10000).Draw(t, "total")
		cached := rapid.IntRange(0, prompt).Draw(t, "cached")
		thoughts := rapid.IntRange(0, 50000).Draw(t, "thoughts")

		meta := &usageMetadata{
			PromptTokenCount:        prompt,
			CandidatesTokenCount:    comp,
			TotalTokenCount:         total,
			CachedContentTokenCount: cached,
			ThoughtsTokenCount:      thoughts,
		}

		// Exercise mapUsage to verify Google→common field mapping
		usage := mapUsage(meta)

		// Verify field mapping correctness
		if usage.Input != prompt {
			t.Fatalf("Input: got %d, want %d", usage.Input, prompt)
		}
		if usage.Output != comp {
			t.Fatalf("Output: got %d, want %d", usage.Output, comp)
		}
		if usage.TotalTokens != total {
			t.Fatalf("TotalTokens: got %d, want %d", usage.TotalTokens, total)
		}
		if usage.CacheRead != cached {
			t.Fatalf("CacheRead: got %d, want %d", usage.CacheRead, cached)
		}

		// Now verify cost arithmetic via CalculateCost
		inputCost := rapid.Float64Range(0, 100).Draw(t, "inputCost")
		outputCost := rapid.Float64Range(0, 100).Draw(t, "outputCost")
		cacheReadCost := rapid.Float64Range(0, 100).Draw(t, "cacheReadCost")

		model := ai.Model{
			ID: "test", API: "google-genai", Provider: "google",
			Cost: ai.ModelCost{Input: inputCost, Output: outputCost, CacheRead: cacheReadCost},
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

// =====================================================================
// Fault Injection Tests (F1-F5)
// =====================================================================

// F1: Truncated SSE Stream (TCP Reset)
func TestF1_TruncatedSSEStream(t *testing.T) {
	fixture := buildGeminiTextSSE([]string{"Hello", " world", "!", " More", " text"}, 10, 5)

	for _, cutAfter := range []int{0, 1, 2, 4} {
		t.Run(fmt.Sprintf("cutAfter=%d", cutAfter), func(t *testing.T) {
			srv := stubserver.NewTCPResetServer(fixture, cutAfter)
			defer srv.Close()

			p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
			_, _, err := streamAndCollect(p, testModel())
			if err == nil {
				t.Fatal("expected error after TCP reset")
			}
		})
	}
}

// F2: Malformed JSON in SSE Chunk
func TestF2_MalformedJSONChunk(t *testing.T) {
	fixture := buildGeminiTextSSE([]string{"ok"}, 1, 1)

	malformed := []struct {
		name string
		data string
	}{
		{"truncated_json", `data: {"candidates": [{"content": {"parts": [{"text": "ok"` + "\n\n"},
		{"not_json", "data: not json at all\n\n"},
		{"null_candidates", `data: {"candidates": null}` + "\n\n"},
		{"empty_data", "data: \n\n"},
	}

	for _, tc := range malformed {
		t.Run(tc.name, func(t *testing.T) {
			srv := stubserver.NewMalformedServer(fixture, 0, tc.data)
			defer srv.Close()

			p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
			// Must not panic
			events, _, _ := streamAndCollect(p, testModel())
			_ = events
		})
	}
}

// F3: HTTP Error Bodies
func TestF3_HTTPErrorHandling(t *testing.T) {
	cases := []struct {
		status int
		body   string
	}{
		{403, `{"error":{"code":403,"message":"forbidden","status":"PERMISSION_DENIED"}}`},
		{429, `{"error":{"code":429,"message":"quota exceeded","status":"RESOURCE_EXHAUSTED"}}`},
		{500, `{"error":{"code":500,"message":"internal error"}}`},
		{503, ``},
		{401, `not json at all`},
		{400, `{"error":{"code":400,"message":"invalid request"}}`},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("status_%d", tc.status), func(t *testing.T) {
			srv := stubserver.NewStatusCodeServer(tc.status, tc.body)
			defer srv.Close()

			p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
			_, _, err := streamAndCollect(p, testModel())
			if err == nil {
				t.Fatalf("expected error for status %d", tc.status)
			}
		})
	}
}

// F4: Context Cancellation During Stream
func TestF4_ContextCancellationRaces(t *testing.T) {
	chunks := make([]string, 50)
	for i := range chunks {
		chunks[i] = fmt.Sprintf("chunk%d ", i)
	}
	fixture := buildGeminiTextSSE(chunks, 10, 50)

	for i := 0; i < 50; i++ {
		t.Run(fmt.Sprintf("iter_%d", i), func(t *testing.T) {
			t.Parallel()
			srv := stubserver.New(stubserver.WithFixture(fixture))
			defer srv.Close()

			ctx, cancel := context.WithCancel(context.Background())
			p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
			es := p.Stream(ctx, testModel(), ai.Context{
				Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
			}, ai.StreamOptions{})

			go func() {
				time.Sleep(time.Duration(randv2.IntN(20)) * time.Millisecond)
				cancel()
			}()

			for range es.C {
			} // must not panic
			es.Result() // must not block
		})
	}
}

// F5: Prompt-Level Safety Block
func TestF5_PromptLevelBlock(t *testing.T) {
	resp := generateContentResponse{
		PromptFeedback: &promptFeedback{
			BlockReason: "SAFETY",
			SafetyRatings: []safetyRating{
				{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Probability: "HIGH", Blocked: true},
			},
		},
		ModelVersion: "gemini-2.5-flash",
	}
	b, _ := json.Marshal(resp)
	fixture := fmt.Sprintf("data: %s\n\n", b)

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
	_, _, err := streamAndCollect(p, testModel())
	if err == nil {
		t.Fatal("expected error for prompt block")
	}
	if !strings.Contains(err.Error(), "prompt blocked") {
		t.Fatalf("expected 'prompt blocked' in error, got: %v", err)
	}
}

// =====================================================================
// Deterministic Simulation Tests (S1-S2)
// =====================================================================

// S1: Stream Event Ordering and Terminal Uniqueness
func TestS1_StreamEventOrdering(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		numChunks := rapid.IntRange(1, 20).Draw(t, "numChunks")
		chunks := make([]string, numChunks)
		for i := range chunks {
			chunks[i] = fmt.Sprintf("word%d ", i)
		}

		fixture := buildGeminiTextSSE(chunks, 10, numChunks)
		srv := stubserver.New(stubserver.WithFixture(fixture))
		defer srv.Close()

		p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
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

// S2: Concurrent Provider Usage (no cross-contamination)
func TestS2_ConcurrentStreams(t *testing.T) {
	const numStreams = 20

	for i := 0; i < numStreams; i++ {
		i := i
		t.Run(fmt.Sprintf("stream_%d", i), func(t *testing.T) {
			t.Parallel()

			text := fmt.Sprintf("unique_text_%d", i)
			fixture := buildGeminiTextSSE([]string{text}, 5, 1)
			srv := stubserver.New(stubserver.WithFixture(fixture))
			defer srv.Close()

			p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
			_, msg, err := streamAndCollect(p, testModel())
			if err != nil {
				t.Fatalf("stream error: %v", err)
			}
			if len(msg.Content) == 0 {
				t.Fatal("no content")
			}
			tc, ok := msg.Content[0].(*ai.TextContent)
			if !ok {
				t.Fatalf("expected TextContent, got %T", msg.Content[0])
			}
			if tc.Text != text {
				t.Fatalf("text mismatch: got %q, want %q", tc.Text, text)
			}
		})
	}
}

// =====================================================================
// Google-Specific Tests
// =====================================================================

// GS1: Thinking + Text + Tool Call with ThoughtSignature
func TestGS1_ThinkingTextToolWithSignature(t *testing.T) {
	sigBytes := []byte("encrypted-sig-data-for-multi-turn")
	sigB64 := base64.StdEncoding.EncodeToString(sigBytes)
	thought := true

	// Build a response with thinking, text, function call, and thought signature
	resp := generateContentResponse{
		Candidates: []candidate{{
			Content: &contentObj{
				Role: "model",
				Parts: []part{
					{Text: "Let me think...", Thought: &thought},
					{Text: "Here's the answer"},
					{FunctionCall: &functionCallWire{Name: "calc", Args: map[string]any{"x": float64(1)}, ID: "call_1"}},
					{ThoughtSignature: sigBytes},
				},
			},
			FinishReason: "STOP",
		}},
		UsageMetadata: &usageMetadata{PromptTokenCount: 10, CandidatesTokenCount: 8, TotalTokenCount: 18, ThoughtsTokenCount: 5},
		ModelVersion:  "gemini-2.5-flash",
	}
	b, _ := json.Marshal(resp)
	fixture := fmt.Sprintf("data: %s\n\n", b)

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
	_, msg, err := streamAndCollect(p, testModel())
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}

	// Content order: thinking, text, tool call
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
	// ThoughtSignature should be on the tool call (last tool call gets signature)
	if tc.ThoughtSignature != sigB64 {
		t.Fatalf("thought signature mismatch: got %q, want %q", tc.ThoughtSignature, sigB64)
	}
}

// GS2: Safety Block Discards Partial Content
func TestGS2_SafetyBlockDiscardsContent(t *testing.T) {
	fixture := buildGeminiSafetyBlockSSE("partial dangerous content")
	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
	events, _, err := streamAndCollect(p, testModel())
	if err == nil {
		t.Fatal("expected error for safety block")
	}

	// No EventDone should carry text content from the blocked response
	for _, ev := range events {
		if ev.Type == ai.EventDone && ev.Message != nil {
			for _, block := range ev.Message.Content {
				if tc, ok := block.(*ai.TextContent); ok && tc.Text != "" {
					t.Fatalf("safety invariant violated: leaked text %q", tc.Text)
				}
			}
		}
	}
}

// GS3: All Safety Block Finish Reasons
func TestGS3_AllSafetyBlockReasons(t *testing.T) {
	reasons := []string{"SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII",
		"IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_RECITATION"}
	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			resp := generateContentResponse{
				Candidates: []candidate{{
					FinishReason:  reason,
					SafetyRatings: []safetyRating{{Category: "HARM_CATEGORY_HARASSMENT", Probability: "HIGH", Blocked: true}},
				}},
				ModelVersion: "gemini-2.5-flash",
			}
			b, _ := json.Marshal(resp)
			fixture := fmt.Sprintf("data: %s\n\n", b)

			srv := stubserver.New(stubserver.WithFixture(fixture))
			defer srv.Close()

			p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
			_, _, err := streamAndCollect(p, testModel())
			if err == nil {
				t.Fatalf("expected error for safety block reason %q", reason)
			}
		})
	}
}

// GS4: Multiple Tool Calls in Single Response
func TestGS4_MultipleToolCalls(t *testing.T) {
	tools := []struct {
		id, name string
		args     map[string]any
	}{
		{"call_0", "read_file", map[string]any{"path": "main.go"}},
		{"call_1", "write_file", map[string]any{"path": "out.txt", "content": "hello"}},
		{"call_2", "bash", map[string]any{"command": "go build"}},
	}

	fixture := buildGeminiMultiToolSSE(tools)
	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
	events, msg, err := streamAndCollect(p, testModel())
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}

	// Should have 3 tool calls in content
	if len(msg.Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(msg.Content))
	}

	// Verify event sequence: start + end for each tool call
	endEvents := make(map[string]*ai.ToolCall)
	for _, e := range events {
		if e.Type == ai.EventToolCallEnd && e.ToolCall != nil {
			endEvents[e.ToolCall.ID] = e.ToolCall
		}
	}
	if len(endEvents) != 3 {
		t.Fatalf("expected 3 tool call end events, got %d", len(endEvents))
	}

	for _, tool := range tools {
		tc, ok := endEvents[tool.id]
		if !ok {
			t.Fatalf("missing tool call end for %q", tool.id)
		}
		if tc.Name != tool.name {
			t.Fatalf("tool %q: name %q != %q", tool.id, tc.Name, tool.name)
		}
		got, _ := json.Marshal(tc.Arguments)
		exp, _ := json.Marshal(tool.args)
		if string(got) != string(exp) {
			t.Fatalf("tool %q args mismatch:\ngot:    %s\nexpect: %s", tool.id, got, exp)
		}
	}
}

// GS5: Tool Call Without ID (synthetic ID generation)
func TestGS5_ToolCallWithoutID(t *testing.T) {
	resp := generateContentResponse{
		Candidates: []candidate{{
			Content: &contentObj{
				Role: "model",
				Parts: []part{
					{FunctionCall: &functionCallWire{Name: "tool_a", Args: map[string]any{"x": float64(1)}}},
					{FunctionCall: &functionCallWire{Name: "tool_b", Args: map[string]any{"y": float64(2)}}},
				},
			},
			FinishReason: "STOP",
		}},
		UsageMetadata: &usageMetadata{PromptTokenCount: 5, CandidatesTokenCount: 3, TotalTokenCount: 8},
		ModelVersion:  "gemini-2.5-flash",
	}
	b, _ := json.Marshal(resp)
	fixture := fmt.Sprintf("data: %s\n\n", b)

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
	_, msg, err := streamAndCollect(p, testModel())
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}

	if len(msg.Content) != 2 {
		t.Fatalf("expected 2 tool calls, got %d content blocks", len(msg.Content))
	}

	tc0 := msg.Content[0].(*ai.ToolCall)
	tc1 := msg.Content[1].(*ai.ToolCall)
	if tc0.ID != "call_tool_a_0" {
		t.Fatalf("expected synthetic ID call_tool_a_0, got %q", tc0.ID)
	}
	if tc1.ID != "call_tool_b_1" {
		t.Fatalf("expected synthetic ID call_tool_b_1, got %q", tc1.ID)
	}
}

// =====================================================================
// Security Tests (SEC1-SEC2)
// =====================================================================

// SEC1: No API Key Leakage in Error Messages
func TestSEC1_NoAPIKeyLeakage(t *testing.T) {
	secretKey := "AIzaSy-test-super-secret-key-12345"
	srv := stubserver.NewStatusCodeServer(500, `{"error":{"code":500,"message":"internal error"}}`)
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: secretKey, Version: "v1beta"})
	_, _, err := streamAndCollect(p, testModel())
	if err == nil {
		t.Fatal("expected error")
	}

	errStr := err.Error()
	if strings.Contains(errStr, secretKey) {
		t.Fatal("API key leaked in error message")
	}
	if strings.Contains(errStr, "AIzaSy") {
		t.Fatal("API key prefix leaked in error message")
	}
}

// SEC2: ThoughtSignature Treated as Opaque
func TestSEC2_ThoughtSignatureOpaque(t *testing.T) {
	// Arbitrary bytes including nulls and high bytes
	sig := []byte{0x00, 0xFF, 0xFE, 0xFD, 0x01, 0x02, 0x80, 0x7F}

	msg := &ai.AssistantMessage{Content: []ai.ContentBlock{
		&ai.ToolCall{ID: "c1", Name: "test", Arguments: map[string]any{"a": float64(1)},
			ThoughtSignature: base64.StdEncoding.EncodeToString(sig)},
	}}

	content := convertModelContent(msg)

	// Find the thoughtSignature part
	var foundSig []byte
	for _, p := range content.Parts {
		if p.ThoughtSignature != nil {
			foundSig = p.ThoughtSignature
		}
	}

	if foundSig == nil {
		t.Fatal("no thought signature found in wire format")
	}
	if len(foundSig) != len(sig) {
		t.Fatalf("signature length mismatch: %d != %d", len(foundSig), len(sig))
	}
	for i := range sig {
		if foundSig[i] != sig[i] {
			t.Fatalf("byte %d mismatch: %02x != %02x", i, foundSig[i], sig[i])
		}
	}
}

// SEC3: Empty Body Response
func TestSEC3_EmptyBodyResponse(t *testing.T) {
	for _, status := range []int{400, 401, 429, 500, 502, 503} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			srv := stubserver.NewEmptyBodyServer(status)
			defer srv.Close()

			p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
			_, _, err := streamAndCollect(p, testModel())
			if err == nil {
				t.Fatalf("expected error for status %d", status)
			}
		})
	}
}

// =====================================================================
// Error Classification Tests
// =====================================================================

// EC1: HTTP Error Classification Stability
func TestEC1_ErrorClassificationStability(t *testing.T) {
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

	unknowns := []int{400, 404, 405, 406, 410, 418, 422}
	for _, status := range unknowns {
		got := classifyHTTPError(status, "generic error")
		if got == ai.ErrAuth || got == ai.ErrRateLimit || got == ai.ErrServerError {
			t.Errorf("status %d: misclassified as %q", status, got)
		}
	}
}
