package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/ai/testutil/stubserver"
)

func testModel() ai.Model {
	return ai.Model{
		ID:        "claude-sonnet-4-20250514",
		API:       "anthropic-messages",
		Provider:  "anthropic",
		Reasoning: true,
		MaxTokens: 16384,
		Cost:      ai.ModelCost{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
	}
}

func TestMapStopReason(t *testing.T) {
	if got := mapStopReason("end_turn"); got != ai.StopReasonStop {
		t.Fatalf("end_turn: got %q", got)
	}
	if got := mapStopReason("max_tokens"); got != ai.StopReasonLength {
		t.Fatalf("max_tokens: got %q", got)
	}
	if got := mapStopReason("tool_use"); got != ai.StopReasonToolUse {
		t.Fatalf("tool_use: got %q", got)
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
	ctx := ai.Context{
		SystemPrompt: "system",
		Tools:        []ai.Tool{{Name: "read_file", Description: "Read file", Parameters: toolSchema}},
		Messages: []ai.Message{
			&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hello"}}},
			&ai.AssistantMessage{Content: []ai.ContentBlock{&ai.ToolCall{ID: "call_1", Name: "read_file", Arguments: map[string]any{"path": "/tmp/x"}}}},
			&ai.ToolResultMessage{ToolCallID: "call_1", ToolName: "read_file", Content: []ai.ContentBlock{&ai.TextContent{Text: "ok"}}},
		},
	}
	max := 200
	req, err := buildRequest(testModel(), ctx, ai.StreamOptions{MaxTokens: &max}, nil)
	if err != nil {
		t.Fatalf("buildRequest err: %v", err)
	}
	if req.System != "system" || req.MaxTokens != 200 || len(req.Tools) != 1 {
		t.Fatalf("bad request fields: %+v", req)
	}
	if req.Messages[1].Role != "assistant" {
		t.Fatalf("assistant role not converted")
	}
	if req.Messages[2].Role != "user" || req.Messages[2].Content[0].Type != "tool_result" {
		t.Fatalf("tool result conversion mismatch: %+v", req.Messages[2])
	}
}

func TestBuildRequestTopPSampling(t *testing.T) {
	ctx := ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}},
	}
	topP := 0.85
	topK := 16
	req, err := buildRequest(testModel(), ctx, ai.StreamOptions{TopP: &topP, TopK: &topK}, nil)
	if err != nil {
		t.Fatalf("buildRequest err: %v", err)
	}
	if req.TopP == nil || *req.TopP != topP {
		t.Fatalf("top_p mismatch: %+v", req.TopP)
	}
	if req.TopK == nil || *req.TopK != topK {
		t.Fatalf("top_k mismatch: %+v", req.TopK)
	}
}

func TestStreamTextFixture(t *testing.T) {
	fixture := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":10}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hel\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"lo\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}}}, ai.StreamOptions{})
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
	fixture := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":10}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"calc\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"a\\\":\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"1}\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":3}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}}}, ai.StreamOptions{})
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

func TestStreamThinkingFixture(t *testing.T) {
	fixture := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":10}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"Reasoning \"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"trace\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}}}, ai.StreamOptions{})
	msg, err := es.Drain()
	if err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("content count: %d", len(msg.Content))
	}
	tc, ok := msg.Content[0].(*ai.ThinkingContent)
	if !ok {
		t.Fatalf("expected thinking block, got %T", msg.Content[0])
	}
	if tc.Thinking != "Reasoning trace" {
		t.Fatalf("thinking mismatch: %q", tc.Thinking)
	}
}

func TestStreamThinkingSignatureDeltaCarriesToNextTurn(t *testing.T) {
	fixture := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":10}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"Reasoning trace\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig-abc123\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}}}, ai.StreamOptions{})
	msg, err := es.Drain()
	if err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("content count: %d", len(msg.Content))
	}
	tc, ok := msg.Content[0].(*ai.ThinkingContent)
	if !ok {
		t.Fatalf("expected thinking block, got %T", msg.Content[0])
	}
	if tc.ThinkingSignature != "sig-abc123" {
		t.Fatalf("thinking signature mismatch: %q", tc.ThinkingSignature)
	}

	wire, err := toWireMessage(&msg)
	if err != nil {
		t.Fatalf("toWireMessage error: %v", err)
	}
	if len(wire.Content) != 1 {
		t.Fatalf("wire content count: %d", len(wire.Content))
	}
	if wire.Content[0].Signature != "sig-abc123" {
		t.Fatalf("wire signature mismatch: %q", wire.Content[0].Signature)
	}
}

func TestStreamAPIErrorFixture(t *testing.T) {
	fixture := "event: error\n" +
		"data: {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\",\"message\":\"rate limited\"}}\n\n"

	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}}}, ai.StreamOptions{})
	_, err := es.Drain()
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestHTTPErrorClassification(t *testing.T) {
	srv := stubserver.New(stubserver.WithHandler(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"message":"bad key"}}`))
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "bad"})
	es := p.Stream(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hello"}}}}}, ai.StreamOptions{})
	_, err := es.Drain()
	if err == nil {
		t.Fatalf("expected error")
	}
	if got := classifyHTTPError(http.StatusUnauthorized, "bad key"); got != ai.ErrAuth {
		t.Fatalf("expected auth classification, got %q", got)
	}
}

func TestStreamSimpleThinkingMapping(t *testing.T) {
	fixture := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":1}}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"
	srv := stubserver.New(stubserver.WithFixture(fixture))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "k"})
	high := 4096
	es := p.StreamSimple(context.Background(), testModel(), ai.Context{Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "hi"}}}}}, ai.SimpleStreamOptions{
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
	var payload wireRequest
	if err := json.Unmarshal(reqs[0].Body, &payload); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if payload.Thinking == nil || payload.Thinking.BudgetTokens != high {
		t.Fatalf("thinking mapping mismatch: %+v", payload.Thinking)
	}
}
