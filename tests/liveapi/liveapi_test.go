//go:build liveapi

package liveapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/ai"
	provideranthropic "github.com/dcosson/flex-agent-runtime/ai/provider/anthropic"
	providercohere "github.com/dcosson/flex-agent-runtime/ai/provider/cohere"
	providergoogle "github.com/dcosson/flex-agent-runtime/ai/provider/google"
	provideropenai "github.com/dcosson/flex-agent-runtime/ai/provider/openai"
)

func TestMain(m *testing.M) {
	setCredentialEnvVars()
	registerProviders()
	os.Exit(m.Run())
}

func registerProviders() {
	provideranthropic.Register(provideranthropic.ClientConfig{})
	provideropenai.Register(provideropenai.ClientConfig{})
	providergoogle.Register(providergoogle.ClientConfig{})

	// Embedding clients
	provideropenai.RegisterEmbeddingClient(provideropenai.ClientConfig{})
	providergoogle.RegisterEmbeddingClient(providergoogle.ClientConfig{})
	providercohere.RegisterEmbeddingClient(providercohere.ClientConfig{})
}

// ---------------------------------------------------------------------------
// Provider chat models for testing — use cheap/fast models.
// ---------------------------------------------------------------------------

type chatProvider struct {
	Name   string
	Model  string
	EnvVar string
}

var chatProviders = []chatProvider{
	{"anthropic", "claude-haiku-4-5-20251001", "ANTHROPIC_API_KEY"},
	{"openai", "gpt-4o-mini", "OPENAI_API_KEY"},
	{"google", "gemini-2.0-flash", "GOOGLE_API_KEY"},
}

func skipWithoutKey(t *testing.T, envVar string) {
	t.Helper()
	if strings.TrimSpace(os.Getenv(envVar)) == "" {
		t.Skipf("%s not set", envVar)
	}
}

func resolveModel(t *testing.T, provider, modelID string) ai.Model {
	t.Helper()
	m, err := ai.GetModel(provider, modelID)
	if err != nil {
		t.Fatalf("GetModel(%q, %q): %v", provider, modelID, err)
	}
	return m
}

// ---------------------------------------------------------------------------
// Test: Stream simple message from each provider
// ---------------------------------------------------------------------------

func TestStreamSimpleMessage(t *testing.T) {
	for _, p := range chatProviders {
		t.Run(p.Name, func(t *testing.T) {
			skipWithoutKey(t, p.EnvVar)
			model := resolveModel(t, p.Name, p.Model)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			llmCtx := ai.Context{
				Messages: []ai.Message{
					&ai.UserMessage{
						Content:   []ai.ContentBlock{&ai.TextContent{Text: "Say hello in one sentence."}},
						Timestamp: ai.TimeToMillis(time.Now()),
					},
				},
			}

			es := ai.StreamSimple(ctx, model, llmCtx, ai.SimpleStreamOptions{})
			var gotText bool
			for ev := range es.C {
				if ev.Type == ai.EventTextDelta && ev.Delta != "" {
					gotText = true
				}
			}
			msg, err := es.Result()
			if err != nil {
				t.Fatalf("Stream error: %v", err)
			}

			if !gotText {
				t.Error("Expected text delta events, got none")
			}

			// Verify the final message has text content
			text := extractAssistantText(msg)
			if strings.TrimSpace(text) == "" {
				t.Error("Expected non-empty text in assistant message")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test: Tool call round-trip (calculator)
// ---------------------------------------------------------------------------

func TestToolCallRoundTrip(t *testing.T) {
	for _, p := range chatProviders {
		t.Run(p.Name, func(t *testing.T) {
			skipWithoutKey(t, p.EnvVar)
			model := resolveModel(t, p.Name, p.Model)

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			calcTool := ai.Tool{
				Name:        "calculator",
				Description: "Evaluate an arithmetic expression. Returns the numeric result.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"expression":{"type":"string","description":"Arithmetic expression like 2+2"}},"required":["expression"]}`),
			}

			// Turn 1: Ask something that requires the calculator
			messages := []ai.Message{
				&ai.UserMessage{
					Content:   []ai.ContentBlock{&ai.TextContent{Text: "What is 7 * 13? Use the calculator tool."}},
					Timestamp: ai.TimeToMillis(time.Now()),
				},
			}

			llmCtx := ai.Context{
				Messages: ai.TransformMessages(messages, model, nil),
				Tools:    []ai.Tool{calcTool},
			}

			es := ai.StreamSimple(ctx, model, llmCtx, ai.SimpleStreamOptions{})
			for range es.C {
			}
			msg, err := es.Result()
			if err != nil {
				t.Fatalf("Turn 1 stream error: %v", err)
			}

			// Find tool call
			var toolCall *ai.ToolCall
			for _, block := range msg.Content {
				if tc, ok := block.(*ai.ToolCall); ok {
					toolCall = tc
					break
				}
			}
			if toolCall == nil {
				t.Fatalf("Expected tool_use block from %s, got: %v", p.Name, contentTypes(msg))
			}
			if toolCall.Name != "calculator" {
				t.Fatalf("Expected calculator tool call, got %q", toolCall.Name)
			}

			// Turn 2: Send tool result
			messages = append(messages, &msg)
			messages = append(messages, &ai.ToolResultMessage{
				ToolCallID: toolCall.ID,
				ToolName:   toolCall.Name,
				Content:    []ai.ContentBlock{&ai.TextContent{Text: "7 * 13 = 91"}},
				Timestamp:  ai.TimeToMillis(time.Now()),
			})

			llmCtx2 := ai.Context{
				Messages: ai.TransformMessages(messages, model, nil),
				Tools:    []ai.Tool{calcTool},
			}

			es2 := ai.StreamSimple(ctx, model, llmCtx2, ai.SimpleStreamOptions{})
			for range es2.C {
			}
			msg2, err := es2.Result()
			if err != nil {
				t.Fatalf("Turn 2 stream error: %v", err)
			}

			// The response should reference the result (91)
			text := extractAssistantText(msg2)
			if !strings.Contains(text, "91") {
				t.Errorf("Expected response to reference 91, got: %s", text)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test: Embeddings from each supporting provider
// ---------------------------------------------------------------------------

type embeddingProvider struct {
	Name    string
	Model   string
	EnvVar  string
	MinDims int
}

var embeddingProviders = []embeddingProvider{
	{"openai", "text-embedding-3-small", "OPENAI_API_KEY", 1536},
	{"google", "gemini-embedding-001", "GOOGLE_API_KEY", 768},
	{"cohere", "embed-v4.0", "COHERE_API_KEY", 256},
}

func TestEmbeddings(t *testing.T) {
	for _, ep := range embeddingProviders {
		t.Run(ep.Name, func(t *testing.T) {
			skipWithoutKey(t, ep.EnvVar)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			resp, err := ai.Embed(ctx, ep.Model, ai.EmbeddingRequest{
				Texts: []string{"hello world", "test document"},
			})
			if err != nil {
				t.Fatalf("Embed error: %v", err)
			}

			if len(resp.Embeddings) != 2 {
				t.Fatalf("Expected 2 embeddings, got %d", len(resp.Embeddings))
			}

			for i, emb := range resp.Embeddings {
				if len(emb.Values) < ep.MinDims {
					t.Errorf("Embedding %d: expected at least %d dimensions, got %d",
						i, ep.MinDims, len(emb.Values))
				}
				// Verify values are not all zero
				allZero := true
				for _, v := range emb.Values {
					if v != 0 {
						allZero = false
						break
					}
				}
				if allZero {
					t.Errorf("Embedding %d: all values are zero", i)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test: OpenRouter routing
// ---------------------------------------------------------------------------

func TestOpenRouterRouting(t *testing.T) {
	skipWithoutKey(t, "OPENROUTER_API_KEY")

	// OpenRouter uses the OpenAI API client — already registered.
	// Use deepseek/deepseek-chat which exists in the openrouter catalog section.
	model, err := ai.GetModel("openrouter", "deepseek/deepseek-chat")
	if err != nil {
		t.Fatalf("OpenRouter model not in catalog: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	llmCtx := ai.Context{
		Messages: []ai.Message{
			&ai.UserMessage{
				Content:   []ai.ContentBlock{&ai.TextContent{Text: "Say hello in one word."}},
				Timestamp: ai.TimeToMillis(time.Now()),
			},
		},
	}

	es := ai.StreamSimple(ctx, model, llmCtx, ai.SimpleStreamOptions{})
	for range es.C {
	}
	msg, err := es.Result()
	if err != nil {
		t.Fatalf("OpenRouter stream error: %v", err)
	}

	text := extractAssistantText(msg)
	if strings.TrimSpace(text) == "" {
		t.Error("Expected non-empty response from OpenRouter")
	}
}

// ---------------------------------------------------------------------------
// Test: Error cases
// ---------------------------------------------------------------------------

func TestBadModelName(t *testing.T) {
	skipWithoutKey(t, "ANTHROPIC_API_KEY")

	// Try to stream with a model that doesn't exist at the provider.
	model := ai.Model{
		ID:       "nonexistent-model-xyz-999",
		API:      "anthropic-messages",
		Provider: "anthropic",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	llmCtx := ai.Context{
		Messages: []ai.Message{
			&ai.UserMessage{
				Content:   []ai.ContentBlock{&ai.TextContent{Text: "hello"}},
				Timestamp: ai.TimeToMillis(time.Now()),
			},
		},
	}

	es := ai.StreamSimple(ctx, model, llmCtx, ai.SimpleStreamOptions{})
	for range es.C {
	}
	_, err := es.Result()
	if err == nil {
		t.Fatal("Expected error for nonexistent model, got nil")
	}
	// Should be a sensible error, not a panic
	t.Logf("Got expected error for bad model: %v", err)
}

func TestInvalidAPIKey(t *testing.T) {
	// Temporarily override the API key to test auth failure.
	// Use OpenAI since its auth errors are well-defined.
	skipWithoutKey(t, "OPENAI_API_KEY")

	// t.Setenv automatically restores the original value and marks
	// the test as incompatible with t.Parallel().
	t.Setenv("OPENAI_API_KEY", "sk-invalid-key-for-testing")

	// Re-register with the bad key so the provider picks it up.
	provideropenai.Register(provideropenai.ClientConfig{})
	defer func() {
		// Re-register with restored key (t.Setenv restores env on cleanup).
		provideropenai.Register(provideropenai.ClientConfig{})
	}()

	model := resolveModel(t, "openai", "gpt-4o-mini")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	llmCtx := ai.Context{
		Messages: []ai.Message{
			&ai.UserMessage{
				Content:   []ai.ContentBlock{&ai.TextContent{Text: "hello"}},
				Timestamp: ai.TimeToMillis(time.Now()),
			},
		},
	}

	es := ai.StreamSimple(ctx, model, llmCtx, ai.SimpleStreamOptions{})
	for range es.C {
	}
	_, err := es.Result()
	if err == nil {
		t.Fatal("Expected auth error for invalid API key, got nil")
	}
	t.Logf("Got expected auth error: %v", err)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func extractAssistantText(msg ai.AssistantMessage) string {
	var parts []string
	for _, block := range msg.Content {
		if t, ok := block.(*ai.TextContent); ok && strings.TrimSpace(t.Text) != "" {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func contentTypes(msg ai.AssistantMessage) string {
	var types []string
	for _, block := range msg.Content {
		types = append(types, fmt.Sprintf("%T", block))
	}
	return strings.Join(types, ", ")
}
