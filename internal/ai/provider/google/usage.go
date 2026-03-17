package google

import "github.com/anthropics/flex-agent-runtime/internal/ai"

func mapUsage(u *usageMetadata) ai.Usage {
	if u == nil {
		return ai.Usage{}
	}
	// Note: ThoughtsTokenCount is intentionally unmapped — ai.Usage has no
	// reasoning/thoughts token field yet. When one is added, map it here.
	return ai.Usage{
		Input:       u.PromptTokenCount,
		Output:      u.CandidatesTokenCount,
		CacheRead:   u.CachedContentTokenCount,
		TotalTokens: u.TotalTokenCount,
	}
}

func mapFinishReason(reason string) ai.StopReason {
	switch reason {
	case "STOP":
		return ai.StopReasonStop
	case "MAX_TOKENS":
		return ai.StopReasonLength
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII",
		"IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_RECITATION":
		// Safety blocks are handled separately; map to error for the stop reason
		return ai.StopReasonError
	case "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL":
		return ai.StopReasonError
	default:
		return ai.StopReasonStop
	}
}
