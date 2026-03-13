package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"h2-agent-runtime/internal/ai"
	"h2-agent-runtime/internal/ai/testutil/stubserver"
)

func testModel() ai.Model {
	return ai.Model{
		ID:        "gpt-4o",
		API:       "openai-completions",
		Provider:  "openai",
		MaxTokens: 16384,
		Cost:      ai.ModelCost{Input: 2.5, Output: 10},
	}
}

func testModelWithCompat(compat *ai.ModelCompat) ai.Model {
	m := testModel()
	m.Compat = compat
	return m
}

func reasoningModel() ai.Model {
	sup := true
	return ai.Model{
		ID:        "o3",
		API:       "openai-completions",
		Provider:  "openai",
		Reasoning: true,
		MaxTokens: 100000,
		Cost:      ai.ModelCost{Input: 10, Output: 40},
		Compat: &ai.ModelCompat{
			SupportsReasoningEffort: &sup,
			ReasoningEffortMap:      map[string]string{"high": "high", "medium": "medium", "low": "low"},
			SupportsDeveloperRole:   &sup,
		},
	}
}

// --- Unit Tests ---

func TestMapStopReason(t *testing.T) {
	stop := "stop"
	toolCalls := "tool_calls"
	length := "length"
	filter := "content_filter"

	if got := mapStopReason(&stop); got != ai.StopReasonStop {
		t.Fatalf("stop: got %q", got)
	}
	if got := mapStopReason(&toolCalls); got != ai.StopReasonToolUse {
		t.Fatalf("tool_calls: got %q", got)
	}
	if got := mapStopReason(&length); got != ai.StopReasonLength {
		t.Fatalf("length: got %q", got)
	}
	if got := mapStopReason(&filter); got != ai.StopReasonStop {
		t.Fatalf("content_filter: got %q", got)
	}
	if got := mapStopReason(nil); got != "" {
		t.Fatalf("nil: got %q", got)
	}
}

func TestToolJSONParserStrictFinalParse(t *testing.T) {
	p := newToolJSONParser()
	if _, _, err := p.AppendDelta(`{"a":`); err != nil {
		t.Fatalf("append err: %v", err)
	}
	if _, err := p.ParseFinal(); err == nil {
		t.Fatalf("expected strict parse failure")
	}
}

func TestBuildRequestConvertsMessagesAndTools(t *testing.T) {
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
	req, err := buildRequest(testModel(), llmCtx, ai.StreamOptions{MaxTokens: &max}, requestParams{})
	if err != nil {
		t.Fatalf("buildRequest err: %v", err)
	}
	if req.Model != "gpt-4o" {
		t.Fatalf("model: %q", req.Model)
	}
	if req.MaxCompletionTokens == nil || *req.MaxCompletionTokens != 200 {
		t.Fatalf("max_completion_tokens mismatch")
	}
	if len(req.Tools) != 1 || req.Tools[0].Function.Name != "read_file" {
		t.Fatalf("tools mismatch")
	}
	// system + user + assistant + tool = 4 messages
	if len(req.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "system" {
		t.Fatalf("system message mismatch: %+v", req.Messages[0])
	}
	if req.Messages[2].Role != "assistant" {
		t.Fatalf("assistant role not converted")
	}
	if req.Messages[3].Role != "tool" || req.Messages[3].ToolCallID != "call_1" {
		t.Fatalf("tool result conversion mismatch: %+v", req.Messages[3])
	}
}

func TestBuildRequestDeveloperRole(t *testing.T) {
	llmCtx := ai.Context{
		SystemPrompt: "system",
		Messages:     []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}
	req, err := buildRequest(reasoningModel(), llmCtx, ai.StreamOptions{}, requestParams{})
	if err != nil {
		t.Fatalf("buildRequest err: %v", err)
	}
	if req.Messages[0].Role != "developer" {
		t.Fatalf("expected developer role, got %q", req.Messages[0].Role)
	}
}

func TestBuildRequestStrictMode(t *testing.T) {
	sup := true
	model := testModelWithCompat(&ai.ModelCompat{SupportsStrictMode: &sup})
	toolSchema := json.RawMessage(`{"type":"object"}`)
	llmCtx := ai.Context{
		Tools:    []ai.Tool{{Name: "t", Parameters: toolSchema}},
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}
	req, err := buildRequest(model, llmCtx, ai.StreamOptions{}, requestParams{})
	if err != nil {
		t.Fatalf("buildRequest err: %v", err)
	}
	if req.Tools[0].Function.Strict == nil || !*req.Tools[0].Function.Strict {
		t.Fatalf("strict mode not set")
	}
}

func TestBuildRequestMaxTokensFieldOldStyle(t *testing.T) {
	model := testModelWithCompat(&ai.ModelCompat{MaxTokensField: "max_tokens"})
	llmCtx := ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}
	max := 100
	req, err := buildRequest(model, llmCtx, ai.StreamOptions{MaxTokens: &max}, requestParams{})
	if err != nil {
		t.Fatalf("buildRequest err: %v", err)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 100 {
		t.Fatalf("max_tokens not set")
	}
	if req.MaxCompletionTokens != nil {
		t.Fatalf("max_completion_tokens should be nil")
	}
}

func TestBuildRequestReasoningEffort(t *testing.T) {
	llmCtx := ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}
	req, err := buildRequest(reasoningModel(), llmCtx, ai.StreamOptions{}, requestParams{reasoningEffort: "high"})
	if err != nil {
		t.Fatalf("buildRequest err: %v", err)
	}
	if req.ReasoningEffort != "high" {
		t.Fatalf("reasoning_effort: %q", req.ReasoningEffort)
	}
}

func TestBuildRequestTopP(t *testing.T) {
	llmCtx := ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}
	topP := 0.7
	req, err := buildRequest(testModel(), llmCtx, ai.StreamOptions{TopP: &topP}, requestParams{})
	if err != nil {
		t.Fatalf("buildRequest err: %v", err)
	}
	if req.TopP == nil || *req.TopP != topP {
		t.Fatalf("top_p mismatch: %+v", req.TopP)
	}
}

func TestBuildRequestStore(t *testing.T) {
	sup := true
	model := testModelWithCompat(&ai.ModelCompat{SupportsStore: &sup})
	llmCtx := ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}
	req, err := buildRequest(model, llmCtx, ai.StreamOptions{}, requestParams{})
	if err != nil {
		t.Fatalf("buildRequest err: %v", err)
	}
	if req.Store == nil || !*req.Store {
		t.Fatalf("store not set")
	}
}

func TestConvertToolResultWithName(t *testing.T) {
	sup := true
	model := testModelWithCompat(&ai.ModelCompat{RequiresToolResultName: &sup})
	msg := &ai.ToolResultMessage{ToolCallID: "c1", ToolName: "read_file", Content: []ai.ContentBlock{&ai.TextContent{Text: "data"}}}
	result := convertToolResult(msg, model, nil)
	if result[0].Name != "read_file" {
		t.Fatalf("expected tool name in result, got %q", result[0].Name)
	}
}

func TestConvertToolResultWithAssistantInjection(t *testing.T) {
	sup := true
	model := testModelWithCompat(&ai.ModelCompat{RequiresAssistantAfterToolResult: &sup})
	msg := &ai.ToolResultMessage{ToolCallID: "c1", ToolName: "read_file", Content: []ai.ContentBlock{&ai.TextContent{Text: "data"}}}
	result := convertToolResult(msg, model, nil)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (tool + assistant), got %d", len(result))
	}
	if result[1].Role != "assistant" {
		t.Fatalf("expected injected assistant message, got %q", result[1].Role)
	}
}

func TestUsageMapping(t *testing.T) {
	u := &chunkUsage{
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
		PromptTokensDetails: &promptTokensDetails{
			CachedTokens: 3,
		},
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
	if got := classifyHTTPError(401, "bad key"); got != ai.ErrAuth {
		t.Fatalf("401: got %q", got)
	}
	if got := classifyHTTPError(429, "rate limited"); got != ai.ErrRateLimit {
		t.Fatalf("429: got %q", got)
	}
	if got := classifyHTTPError(500, "internal"); got != ai.ErrServerError {
		t.Fatalf("500: got %q", got)
	}
}

// --- Fixture Streaming Tests ---

func TestStreamTextFixture(t *testing.T) {
	fixture := makeTextFixture("Hello", "stop", 10, 5)

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
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
	if len(msg.Content) != 1 {
		t.Fatalf("content count: %d", len(msg.Content))
	}
	text, ok := msg.Content[0].(*ai.TextContent)
	if !ok || text.Text != "Hello" {
		t.Fatalf("text mismatch: %#v", msg.Content[0])
	}
	if msg.Usage.Input != 10 || msg.Usage.Output != 5 || msg.Usage.Cost.Total <= 0 {
		t.Fatalf("usage/cost mismatch: %+v", msg.Usage)
	}
}

func TestStreamToolCallFixture(t *testing.T) {
	fixture := `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"calc","arguments":""}}]},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\":"}}]},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}` + "\n\n" +
		"data: [DONE]\n\n"

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(context.Background(), testModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{})
	msg, err := es.Drain()
	if err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if msg.StopReason != ai.StopReasonToolUse {
		t.Fatalf("stop reason: %q", msg.StopReason)
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

func TestStreamMultiToolCallFixture(t *testing.T) {
	fixture := `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":""}},{"index":1,"id":"call_2","type":"function","function":{"name":"write","arguments":""}}]},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"p\":\"a\"}"}}]},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"p\":\"b\"}"}}]},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		"data: [DONE]\n\n"

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(context.Background(), testModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{})
	msg, err := es.Drain()
	if err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if msg.StopReason != ai.StopReasonToolUse {
		t.Fatalf("stop reason: %q", msg.StopReason)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("content count: %d (expected 2 tool calls)", len(msg.Content))
	}
	tc0, ok := msg.Content[0].(*ai.ToolCall)
	if !ok || tc0.Name != "read" || tc0.ID != "call_1" {
		t.Fatalf("tool 0 mismatch: %+v", msg.Content[0])
	}
	tc1, ok := msg.Content[1].(*ai.ToolCall)
	if !ok || tc1.Name != "write" || tc1.ID != "call_2" {
		t.Fatalf("tool 1 mismatch: %+v", msg.Content[1])
	}
}

func TestStreamReasoningFixture(t *testing.T) {
	fixture := `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning":"Think "},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning":"hard"},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Answer"},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(context.Background(), reasoningModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{})
	msg, err := es.Drain()
	if err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("content count: %d (expected thinking + text)", len(msg.Content))
	}
	thinking, ok := msg.Content[0].(*ai.ThinkingContent)
	if !ok {
		t.Fatalf("expected thinking block, got %T", msg.Content[0])
	}
	if thinking.Thinking != "Think hard" {
		t.Fatalf("thinking mismatch: %q", thinking.Thinking)
	}
	text, ok := msg.Content[1].(*ai.TextContent)
	if !ok || text.Text != "Answer" {
		t.Fatalf("text mismatch: %#v", msg.Content[1])
	}
}

func TestHTTPErrorClassification(t *testing.T) {
	srv := stubserver.New(stubserver.WithHandler(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "bad"})
	es := p.Stream(context.Background(), testModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hello"}}}},
	}, ai.StreamOptions{})
	_, err := es.Drain()
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestStreamSimpleReasoningEffortMapping(t *testing.T) {
	fixture := makeTextFixture("ok", "stop", 1, 1)
	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.StreamSimple(context.Background(), reasoningModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.SimpleStreamOptions{
		Reasoning: ai.ThinkingHigh,
	})
	_, err := es.Drain()
	if err != nil {
		t.Fatalf("stream simple err: %v", err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	var payload chatRequest
	if err := json.Unmarshal(reqs[0].Body, &payload); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if payload.ReasoningEffort != "high" {
		t.Fatalf("reasoning_effort mismatch: %q", payload.ReasoningEffort)
	}
}

func TestStreamRequestCapture(t *testing.T) {
	fixture := makeTextFixture("ok", "stop", 1, 1)
	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "test-key"})
	es := p.Stream(context.Background(), testModel(), ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{})
	_, _ = es.Drain()

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if reqs[0].Header.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("auth header mismatch: %q", reqs[0].Header.Get("Authorization"))
	}
	if reqs[0].Header.Get("Content-Type") != "application/json" {
		t.Fatalf("content-type mismatch: %q", reqs[0].Header.Get("Content-Type"))
	}
}

// --- Helpers ---

func makeTextFixture(text, finishReason string, promptTokens, completionTokens int) string {
	return `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"` + text + `"},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"` + finishReason + `"}]}` + "\n\n" +
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":` + itoa(promptTokens) + `,"completion_tokens":` + itoa(completionTokens) + `,"total_tokens":` + itoa(promptTokens+completionTokens) + `}}` + "\n\n" +
		"data: [DONE]\n\n"
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
