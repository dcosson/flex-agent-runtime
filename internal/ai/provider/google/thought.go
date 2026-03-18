package google

import (
	"encoding/base64"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
)

// attachThoughtSignatures attaches collected thought signatures to the
// appropriate content blocks in the final AssistantMessage.
//
// Gemini places thoughtSignature parts after functionCall parts or at the
// end of the response. We attach them as ThoughtSignature on the last
// ToolCall in the message. If there are no tool calls, the signature is
// stored on the last ThinkingContent block.
func attachThoughtSignatures(msg *ai.AssistantMessage, sigs [][]byte) {
	if len(sigs) == 0 {
		return
	}

	encoded := base64.StdEncoding.EncodeToString(sigs[len(sigs)-1])

	// Find the last ToolCall and attach the signature
	for i := len(msg.Content) - 1; i >= 0; i-- {
		if tc, ok := msg.Content[i].(*ai.ToolCall); ok {
			tc.ThoughtSignature = encoded
			return
		}
	}

	// No tool calls — store on the last ThinkingContent if present
	for i := len(msg.Content) - 1; i >= 0; i-- {
		if tc, ok := msg.Content[i].(*ai.ThinkingContent); ok {
			tc.ThinkingSignature = encoded
			return
		}
	}

	// Fallback: create a synthetic ThinkingContent to hold the signature
	msg.Content = append(msg.Content, &ai.ThinkingContent{
		ThinkingSignature: encoded,
	})
}
