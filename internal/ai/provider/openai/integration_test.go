package openai

import (
	"context"
	"os"
	"testing"
	"time"

	"h2-agent-runtime/internal/ai"
)

func TestIntegrationOpenAISmoke(t *testing.T) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY not set")
	}
	model, err := ai.GetModel("openai", "gpt-4o")
	if err != nil {
		// Model may not be in catalog; use local definition
		model = testModel()
	}

	p := New(Config{APIKey: key})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	es := p.Stream(ctx, model, ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "Say hello in one short sentence."}}}},
	}, ai.StreamOptions{})
	msg, err := es.Drain()
	if err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if len(msg.Content) == 0 {
		t.Fatalf("empty response content")
	}
}
