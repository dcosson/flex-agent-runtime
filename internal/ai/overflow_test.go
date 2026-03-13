package ai

import (
	"testing"
)

func TestIsContextOverflowPatterns(t *testing.T) {
	cases := []string{
		"prompt is too long",
		"maximum context length is 8192 tokens",
		"token limit exceeded",
		"400 status code (no body)",
	}
	for _, c := range cases {
		msg := &AssistantMessage{StopReason: StopReasonError, ErrorMessage: c}
		if !IsContextOverflow(msg, 0) {
			t.Fatalf("expected overflow for %q", c)
		}
	}
	msg := &AssistantMessage{StopReason: StopReasonStop, Usage: Usage{Input: 1000, CacheRead: 10}}
	if !IsContextOverflow(msg, 900) {
		t.Fatalf("expected silent overflow")
	}
	if IsContextOverflow(&AssistantMessage{StopReason: StopReasonStop, Usage: Usage{Input: 100}}, 1000) {
		t.Fatalf("unexpected overflow")
	}
}

// F4 overflow pattern fuzzing.
func FuzzIsContextOverflow(f *testing.F) {
	f.Add("token limit exceeded")
	f.Add("random error")
	f.Add("400 status code (no body)")
	f.Add("")

	f.Fuzz(func(t *testing.T, errMsg string) {
		msg := &AssistantMessage{StopReason: StopReasonError, ErrorMessage: errMsg}
		_ = IsContextOverflow(msg, 0)
		_ = classifyErrorMessage(errMsg)
	})
}

func TestProviderErrorFromMessage(t *testing.T) {
	msg := &AssistantMessage{StopReason: StopReasonError, ErrorMessage: "token limit exceeded", Provider: "openai"}
	err := providerErrorFromMessage(msg)
	pe, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected ProviderError, got %T", err)
	}
	if pe.Code != ErrContextOverflow {
		t.Fatalf("unexpected code: %s", pe.Code)
	}
}
