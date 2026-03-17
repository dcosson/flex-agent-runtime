package provider_test

import (
	"testing"

	provideranthropic "github.com/anthropics/flex-agent-runtime/ai/provider/anthropic"
	providercohere "github.com/anthropics/flex-agent-runtime/ai/provider/cohere"
	providergoogle "github.com/anthropics/flex-agent-runtime/ai/provider/google"
	provideropenai "github.com/anthropics/flex-agent-runtime/ai/provider/openai"
)

func TestCapabilities(t *testing.T) {
	a := provideranthropic.Capabilities()
	if !a.Chat || a.Embeddings {
		t.Fatalf("anthropic capabilities mismatch: %+v", a)
	}

	o := provideropenai.Capabilities()
	if !o.Chat || !o.Embeddings {
		t.Fatalf("openai capabilities mismatch: %+v", o)
	}

	g := providergoogle.Capabilities()
	if !g.Chat || !g.Embeddings {
		t.Fatalf("google capabilities mismatch: %+v", g)
	}

	c := providercohere.Capabilities()
	if c.Chat || !c.Embeddings {
		t.Fatalf("cohere capabilities mismatch: %+v", c)
	}
}
