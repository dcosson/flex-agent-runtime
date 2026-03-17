package anthropic

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/ai"
)

func TestIntegrationAnthropicSmoke(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	model, err := ai.GetModel("anthropic", "claude-sonnet-4-20250514")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
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
