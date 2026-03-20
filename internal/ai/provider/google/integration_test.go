package google

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
)

func TestIntegrationGoogleSmoke(t *testing.T) {
	key := os.Getenv("GOOGLE_API_KEY")
	if key == "" {
		t.Skip("GOOGLE_API_KEY not set")
	}
	model, err := ai.GetModel("google", "gemini-2.5-flash")
	if err != nil {
		// Model may not be in catalog; use local definition
		model = testModel()
	}

	p := NewClient(ClientConfig{})
	ep := EndpointFromConfig(Config{APIKey: key})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	es := p.Stream(ctx, ep, model, ai.Context{
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
