package google

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"flex-agent-runtime/internal/ai"
	"flex-agent-runtime/internal/ai/testutil/stubserver"
)

func testModel() ai.Model {
	return ai.Model{
		ID:        "gemini-2.5-flash",
		API:       "google-genai",
		Provider:  "google",
		Reasoning: true,
		MaxTokens: 65536,
		Cost:      ai.ModelCost{Input: 0.15, Output: 0.6, CacheRead: 0.0375},
	}
}

// --- Unit Tests ---

func TestMapFinishReason(t *testing.T) {
	if got := mapFinishReason("STOP"); got != ai.StopReasonStop {
		t.Fatalf("STOP: got %q", got)
	}
	if got := mapFinishReason("MAX_TOKENS"); got != ai.StopReasonLength {
		t.Fatalf("MAX_TOKENS: got %q", got)
	}
	if got := mapFinishReason("SAFETY"); got != ai.StopReasonError {
		t.Fatalf("SAFETY: got %q", got)
	}
	if got := mapFinishReason("MALFORMED_FUNCTION_CALL"); got != ai.StopReasonError {
		t.Fatalf("MALFORMED_FUNCTION_CALL: got %q", got)
	}
}

func TestDefaultSafetySettings(t *testing.T) {
	settings := defaultSafetySettings()
	if len(settings) != 5 {
		t.Fatalf("expected 5 safety settings, got %d", len(settings))
	}
	for _, s := range settings {
		if s.Threshold != "OFF" {
			t.Fatalf("expected OFF threshold for %s, got %s", s.Category, s.Threshold)
		}
	}
}

func TestIsSafetyBlock(t *testing.T) {
	blocked := []string{"SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII",
		"IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_RECITATION"}
	for _, reason := range blocked {
		if !isSafetyBlock(reason) {
			t.Fatalf("%s should be blocked", reason)
		}
	}
	notBlocked := []string{"STOP", "MAX_TOKENS", "MALFORMED_FUNCTION_CALL"}
	for _, reason := range notBlocked {
		if isSafetyBlock(reason) {
			t.Fatalf("%s should not be blocked", reason)
		}
	}
}

func TestFormatSafetyRatings(t *testing.T) {
	ratings := []safetyRating{
		{Category: "HARM_CATEGORY_HARASSMENT", Probability: "HIGH", Blocked: true},
		{Category: "HARM_CATEGORY_HATE_SPEECH", Probability: "LOW", Blocked: false},
	}
	got := formatSafetyRatings(ratings)
	if got != "blocked by: HARM_CATEGORY_HARASSMENT (HIGH)" {
		t.Fatalf("format mismatch: %q", got)
	}
}

func TestFormatSafetyRatingsNoneBlocked(t *testing.T) {
	ratings := []safetyRating{
		{Category: "HARM_CATEGORY_HARASSMENT", Probability: "LOW", Blocked: false},
	}
	got := formatSafetyRatings(ratings)
	if got != "unknown safety block" {
		t.Fatalf("expected unknown, got: %q", got)
	}
}

func TestUsageMapping(t *testing.T) {
	u := &usageMetadata{
		PromptTokenCount:        10,
		CandidatesTokenCount:    5,
		TotalTokenCount:         15,
		CachedContentTokenCount: 3,
		ThoughtsTokenCount:      20,
	}
	usage := mapUsage(u)
	if usage.Input != 10 || usage.Output != 5 || usage.TotalTokens != 15 {
		t.Fatalf("usage mismatch: %+v", usage)
	}
	if usage.CacheRead != 3 {
		t.Fatalf("cache read mismatch: %d", usage.CacheRead)
	}
}

func TestUsageMappingNil(t *testing.T) {
	usage := mapUsage(nil)
	if usage.Input != 0 || usage.Output != 0 {
		t.Fatalf("nil usage should be zero: %+v", usage)
	}
}

func TestErrorClassification(t *testing.T) {
	if got := classifyHTTPError(401, "unauthenticated"); got != ai.ErrAuth {
		t.Fatalf("401: got %q", got)
	}
	if got := classifyHTTPError(403, "forbidden"); got != ai.ErrAuth {
		t.Fatalf("403: got %q", got)
	}
	if got := classifyHTTPError(429, "rate limited"); got != ai.ErrRateLimit {
		t.Fatalf("429: got %q", got)
	}
	if got := classifyHTTPError(500, "internal"); got != ai.ErrServerError {
		t.Fatalf("500: got %q", got)
	}
}

func TestBuildRequestConvertsMessages(t *testing.T) {
	toolSchema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
	llmCtx := ai.Context{
		SystemPrompt: "system",
		Tools:        []ai.Tool{{Name: "read_file", Description: "Read file", Parameters: toolSchema}},
		Messages: []ai.Message{
			&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hello"}}},
			&ai.AssistantMessage{Content: []ai.ContentBlock{&ai.ToolCall{ID: "call_1", Name: "read_file", Arguments: map[string]any{"path": "/tmp/x"}}}},
			&ai.ToolResultMessage{ToolCallID: "call_1", ToolName: "read_file", Content: []ai.ContentBlock{&ai.TextContent{Text: "ok"}}},
		},
	}
	max := 200
	req := buildRequest(testModel(), llmCtx, ai.StreamOptions{MaxTokens: &max}, requestParams{})

	// System instruction
	if req.SystemInstruction == nil || req.SystemInstruction.Parts[0].Text != "system" {
		t.Fatalf("system instruction mismatch")
	}
	// Contents: user + model + user (tool result)
	if len(req.Contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(req.Contents))
	}
	if req.Contents[0].Role != "user" {
		t.Fatalf("first content role: %q", req.Contents[0].Role)
	}
	if req.Contents[1].Role != "model" {
		t.Fatalf("second content role: %q", req.Contents[1].Role)
	}
	if req.Contents[1].Parts[0].FunctionCall == nil || req.Contents[1].Parts[0].FunctionCall.Name != "read_file" {
		t.Fatalf("function call conversion mismatch")
	}
	if req.Contents[2].Role != "user" || req.Contents[2].Parts[0].FunctionResponse == nil {
		t.Fatalf("tool result conversion mismatch")
	}
	if req.Contents[2].Parts[0].FunctionResponse.Name != "read_file" {
		t.Fatalf("function response name mismatch")
	}
	// Tools
	if len(req.Tools) != 1 || len(req.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools mismatch")
	}
	// Generation config
	if req.GenerationConfig == nil || req.GenerationConfig.MaxOutputTokens == nil || *req.GenerationConfig.MaxOutputTokens != 200 {
		t.Fatalf("max output tokens mismatch")
	}
}

func TestBuildRequestThinkingBudget(t *testing.T) {
	llmCtx := ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}
	budget := 4096
	req := buildRequest(testModel(), llmCtx, ai.StreamOptions{}, requestParams{thinkingBudget: &budget})
	if req.GenerationConfig.ThinkingConfig == nil {
		t.Fatal("thinking config not set")
	}
	if req.GenerationConfig.ThinkingConfig.ThinkingBudget == nil || *req.GenerationConfig.ThinkingConfig.ThinkingBudget != 4096 {
		t.Fatalf("thinking budget mismatch")
	}
	if req.GenerationConfig.ThinkingConfig.IncludeThoughts == nil || !*req.GenerationConfig.ThinkingConfig.IncludeThoughts {
		t.Fatal("includeThoughts not set")
	}
}

func TestBuildRequestTopPSampling(t *testing.T) {
	llmCtx := ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}
	topP := 0.8
	topK := 40
	req := buildRequest(testModel(), llmCtx, ai.StreamOptions{TopP: &topP, TopK: &topK}, requestParams{})
	if req.GenerationConfig == nil {
		t.Fatal("generation config not set")
	}
	if req.GenerationConfig.TopP == nil || *req.GenerationConfig.TopP != topP {
		t.Fatalf("topP mismatch: %+v", req.GenerationConfig.TopP)
	}
	if req.GenerationConfig.TopK == nil || *req.GenerationConfig.TopK != topK {
		t.Fatalf("topK mismatch: %+v", req.GenerationConfig.TopK)
	}
}

func TestConvertModelContentWithThinking(t *testing.T) {
	msg := &ai.AssistantMessage{Content: []ai.ContentBlock{
		&ai.ThinkingContent{Thinking: "reasoning trace"},
		&ai.TextContent{Text: "answer"},
	}}
	content := convertModelContent(msg)
	if len(content.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(content.Parts))
	}
	if content.Parts[0].Thought == nil || !*content.Parts[0].Thought {
		t.Fatal("thought flag not set")
	}
	if content.Parts[0].Text != "reasoning trace" {
		t.Fatalf("thinking text mismatch: %q", content.Parts[0].Text)
	}
}

func TestConvertModelContentWithThoughtSignature(t *testing.T) {
	sig := base64.StdEncoding.EncodeToString([]byte("encrypted-sig"))
	msg := &ai.AssistantMessage{Content: []ai.ContentBlock{
		&ai.ToolCall{ID: "c1", Name: "read_file", Arguments: map[string]any{"p": "a"}, ThoughtSignature: sig},
	}}
	content := convertModelContent(msg)
	if len(content.Parts) != 2 {
		t.Fatalf("expected 2 parts (functionCall + thoughtSignature), got %d", len(content.Parts))
	}
	if content.Parts[1].ThoughtSignature == nil {
		t.Fatal("thought signature not attached")
	}
}

func TestAttachThoughtSignaturesToToolCall(t *testing.T) {
	msg := &ai.AssistantMessage{Content: []ai.ContentBlock{
		&ai.ToolCall{ID: "c1", Name: "calc"},
	}}
	sigs := [][]byte{[]byte("sig-bytes")}
	attachThoughtSignatures(msg, sigs)

	tc := msg.Content[0].(*ai.ToolCall)
	if tc.ThoughtSignature == "" {
		t.Fatal("signature not attached to tool call")
	}
	decoded, _ := base64.StdEncoding.DecodeString(tc.ThoughtSignature)
	if string(decoded) != "sig-bytes" {
		t.Fatalf("signature mismatch: %q", string(decoded))
	}
}

func TestAttachThoughtSignaturesToThinking(t *testing.T) {
	msg := &ai.AssistantMessage{Content: []ai.ContentBlock{
		&ai.ThinkingContent{Thinking: "trace"},
		&ai.TextContent{Text: "answer"},
	}}
	sigs := [][]byte{[]byte("sig-bytes")}
	attachThoughtSignatures(msg, sigs)

	tc := msg.Content[0].(*ai.ThinkingContent)
	if tc.ThinkingSignature == "" {
		t.Fatal("signature not attached to thinking content")
	}
}

func TestAttachThoughtSignaturesFallback(t *testing.T) {
	msg := &ai.AssistantMessage{Content: []ai.ContentBlock{
		&ai.TextContent{Text: "answer"},
	}}
	sigs := [][]byte{[]byte("sig-bytes")}
	attachThoughtSignatures(msg, sigs)

	// Should create a synthetic ThinkingContent
	if len(msg.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(msg.Content))
	}
	tc, ok := msg.Content[1].(*ai.ThinkingContent)
	if !ok {
		t.Fatalf("expected ThinkingContent, got %T", msg.Content[1])
	}
	if tc.ThinkingSignature == "" {
		t.Fatal("signature not set on synthetic thinking content")
	}
}

func TestGenerateToolCallID(t *testing.T) {
	id := generateToolCallID("read_file", 0)
	if id != "call_read_file_0" {
		t.Fatalf("unexpected ID: %q", id)
	}
}

// --- Fixture Streaming Tests ---

func TestStreamTextFixture(t *testing.T) {
	fixture := `data: {"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"}}],"modelVersion":"gemini-2.5-flash"}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":" world"}],"role":"model"}}],"modelVersion":"gemini-2.5-flash"}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":"!"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15},"modelVersion":"gemini-2.5-flash"}` + "\n\n"

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
	// Override URL construction by setting baseURL to srv.URL root
	// The stub server handles any path, so this works
	es := p.Stream(context.Background(), testModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{})
	msg, err := es.Drain()
	if err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if msg.StopReason != ai.StopReasonStop {
		t.Fatalf("stop reason: %q", msg.StopReason)
	}
	if len(msg.Content) == 0 {
		t.Fatal("no content")
	}
	text, ok := msg.Content[0].(*ai.TextContent)
	if !ok || text.Text != "Hello world!" {
		t.Fatalf("text mismatch: %#v", msg.Content[0])
	}
	if msg.Usage.Input != 10 || msg.Usage.Output != 5 || msg.Usage.Cost.Total <= 0 {
		t.Fatalf("usage/cost mismatch: %+v", msg.Usage)
	}
}

func TestStreamThinkingFixture(t *testing.T) {
	fixture := `data: {"candidates":[{"content":{"parts":[{"text":"Thinking...","thought":true}],"role":"model"}}],"modelVersion":"gemini-2.5-flash"}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":"Answer"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8,"thoughtsTokenCount":10},"modelVersion":"gemini-2.5-flash"}` + "\n\n"

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
	es := p.Stream(context.Background(), testModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{})
	msg, err := es.Drain()
	if err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("expected 2 content blocks (thinking + text), got %d", len(msg.Content))
	}
	thinking, ok := msg.Content[0].(*ai.ThinkingContent)
	if !ok {
		t.Fatalf("expected ThinkingContent, got %T", msg.Content[0])
	}
	if thinking.Thinking != "Thinking..." {
		t.Fatalf("thinking mismatch: %q", thinking.Thinking)
	}
	text, ok := msg.Content[1].(*ai.TextContent)
	if !ok || text.Text != "Answer" {
		t.Fatalf("text mismatch: %#v", msg.Content[1])
	}
}

func TestStreamToolCallFixture(t *testing.T) {
	fixture := `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"calc","args":{"a":1},"id":"call_1"}}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13},"modelVersion":"gemini-2.5-flash"}` + "\n\n"

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
	es := p.Stream(context.Background(), testModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{})
	msg, err := es.Drain()
	if err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("content count: %d", len(msg.Content))
	}
	tc, ok := msg.Content[0].(*ai.ToolCall)
	if !ok {
		t.Fatalf("expected tool call, got %T", msg.Content[0])
	}
	if tc.Name != "calc" || tc.ID != "call_1" {
		t.Fatalf("tool identity mismatch: %+v", tc)
	}
	if got, ok := tc.Arguments["a"].(float64); !ok || got != 1 {
		t.Fatalf("tool args mismatch: %+v", tc.Arguments)
	}
}

func TestStreamToolCallNoIDFixture(t *testing.T) {
	// Gemini may not provide an ID for function calls
	fixture := `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"read_file","args":{"path":"/tmp"}}}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":7},"modelVersion":"gemini-2.5-flash"}` + "\n\n"

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
	es := p.Stream(context.Background(), testModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{})
	msg, err := es.Drain()
	if err != nil {
		t.Fatalf("stream err: %v", err)
	}
	tc := msg.Content[0].(*ai.ToolCall)
	if tc.ID != "call_read_file_0" {
		t.Fatalf("expected generated ID, got %q", tc.ID)
	}
}

func TestStreamThoughtSignatureFixture(t *testing.T) {
	sigBytes := []byte("encrypted-sig-data")
	sigB64 := base64.StdEncoding.EncodeToString(sigBytes)

	fixture := `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"calc","args":{"a":1},"id":"c1"}},{"thoughtSignature":"` + sigB64 + `"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":7},"modelVersion":"gemini-2.5-flash"}` + "\n\n"

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
	es := p.Stream(context.Background(), testModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{})
	msg, err := es.Drain()
	if err != nil {
		t.Fatalf("stream err: %v", err)
	}
	tc := msg.Content[0].(*ai.ToolCall)
	if tc.ThoughtSignature == "" {
		t.Fatal("thought signature not attached")
	}
	decoded, err := base64.StdEncoding.DecodeString(tc.ThoughtSignature)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if string(decoded) != "encrypted-sig-data" {
		t.Fatalf("signature mismatch: %q", string(decoded))
	}
}

func TestStreamSafetyBlockFixture(t *testing.T) {
	fixture := `data: {"candidates":[{"content":{"parts":[{"text":"partial"}],"role":"model"}}],"modelVersion":"gemini-2.5-flash"}` + "\n\n" +
		`data: {"candidates":[{"finishReason":"SAFETY","safetyRatings":[{"category":"HARM_CATEGORY_HARASSMENT","probability":"HIGH","blocked":true}]}],"modelVersion":"gemini-2.5-flash"}` + "\n\n"

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
	es := p.Stream(context.Background(), testModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{})

	// Collect all events to verify safety invariant: no EventDone with text content
	for ev := range es.C {
		// No EventDone should carry text content from a safety-blocked response
		if ev.Type == ai.EventDone && ev.Message != nil {
			for _, block := range ev.Message.Content {
				if tc, ok := block.(*ai.TextContent); ok && tc.Text != "" {
					t.Fatalf("safety invariant violated: EventDone leaked text content: %q", tc.Text)
				}
			}
		}
	}
	_, err := es.Result()
	if err == nil {
		t.Fatal("expected error for safety block")
	}
}

func TestStreamPromptBlockedFixture(t *testing.T) {
	fixture := `data: {"promptFeedback":{"blockReason":"SAFETY","safetyRatings":[{"category":"HARM_CATEGORY_HARASSMENT","probability":"HIGH","blocked":true}]},"modelVersion":"gemini-2.5-flash"}` + "\n\n"

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
	es := p.Stream(context.Background(), testModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{})
	_, err := es.Drain()
	if err == nil {
		t.Fatal("expected error for prompt block")
	}
}

func TestHTTPErrorClassification(t *testing.T) {
	srv := stubserver.New(stubserver.WithHandler(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"message":"permission denied","status":"PERMISSION_DENIED"}}`))
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "bad", Version: "v1beta"})
	es := p.Stream(context.Background(), testModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hello"}}}},
	}, ai.StreamOptions{})

	// Collect events and verify the error event carries the right message
	var errorMsg string
	for ev := range es.C {
		if ev.Type == ai.EventError && ev.Error != nil {
			errorMsg = ev.Error.ErrorMessage
		}
	}
	_, err := es.Result()
	if err == nil {
		t.Fatal("expected error")
	}
	if errorMsg != "permission denied" {
		t.Fatalf("expected 'permission denied' error, got %q", errorMsg)
	}
	// Verify unit-level classification directly
	if got := classifyHTTPError(403, "permission denied"); got != ai.ErrAuth {
		t.Fatalf("expected ErrAuth from classifyHTTPError, got %q", got)
	}
}

func TestStreamSimpleThinkingBudget(t *testing.T) {
	fixture := `data: {"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2},"modelVersion":"gemini-2.5-flash"}` + "\n\n"
	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
	high := 4096
	es := p.StreamSimple(context.Background(), testModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.SimpleStreamOptions{
		Reasoning:       ai.ThinkingHigh,
		ThinkingBudgets: &ai.ThinkingBudgets{High: &high},
	})
	_, err := es.Drain()
	if err != nil {
		t.Fatalf("stream simple err: %v", err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	var payload generateContentRequest
	if err := json.Unmarshal(reqs[0].Body, &payload); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if payload.GenerationConfig == nil || payload.GenerationConfig.ThinkingConfig == nil {
		t.Fatal("thinking config not set")
	}
	if payload.GenerationConfig.ThinkingConfig.ThinkingBudget == nil {
		t.Fatal("thinking budget not set")
	}
	if *payload.GenerationConfig.ThinkingConfig.ThinkingBudget != high {
		t.Fatalf("thinking budget mismatch: %d", *payload.GenerationConfig.ThinkingConfig.ThinkingBudget)
	}
	if payload.GenerationConfig.ThinkingConfig.ThinkingLevel != "THINKING_LEVEL_HIGH" {
		t.Fatalf("thinking level mismatch: %q", payload.GenerationConfig.ThinkingConfig.ThinkingLevel)
	}
}

func TestStreamSimpleThinkingLevelMapping(t *testing.T) {
	fixture := `data: {"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2},"modelVersion":"gemini-2.5-flash"}` + "\n\n"

	cases := []struct {
		level    ai.ThinkingLevel
		expected string
	}{
		{ai.ThinkingMinimal, "THINKING_LEVEL_LOW"},
		{ai.ThinkingLow, "THINKING_LEVEL_LOW"},
		{ai.ThinkingMedium, "THINKING_LEVEL_MEDIUM"},
		{ai.ThinkingHigh, "THINKING_LEVEL_HIGH"},
		{ai.ThinkingXHigh, "THINKING_LEVEL_HIGH"}, // xhigh clamped to high
	}

	for _, tc := range cases {
		t.Run(string(tc.level), func(t *testing.T) {
			srv := stubserver.New(stubserver.WithFixture(fixture))
			defer srv.Close()

			p := New(Config{BaseURL: srv.URL, APIKey: "k", Version: "v1beta"})
			es := p.StreamSimple(context.Background(), testModel(), ai.Context{
				Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
			}, ai.SimpleStreamOptions{Reasoning: tc.level})
			_, _ = es.Drain()

			reqs := srv.Requests()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request, got %d", len(reqs))
			}
			var payload generateContentRequest
			if err := json.Unmarshal(reqs[0].Body, &payload); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if payload.GenerationConfig == nil || payload.GenerationConfig.ThinkingConfig == nil {
				t.Fatal("thinking config not set")
			}
			if payload.GenerationConfig.ThinkingConfig.ThinkingLevel != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, payload.GenerationConfig.ThinkingConfig.ThinkingLevel)
			}
		})
	}
}

func TestStreamRequestCapture(t *testing.T) {
	fixture := `data: {"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2},"modelVersion":"gemini-2.5-flash"}` + "\n\n"
	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "test-key", Version: "v1beta"})
	es := p.Stream(context.Background(), testModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{})
	_, _ = es.Drain()

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if reqs[0].Header.Get("x-goog-api-key") != "test-key" {
		t.Fatalf("api key header mismatch: %q", reqs[0].Header.Get("x-goog-api-key"))
	}
	if reqs[0].Header.Get("Content-Type") != "application/json" {
		t.Fatalf("content-type mismatch: %q", reqs[0].Header.Get("Content-Type"))
	}
}
