package openai

import "github.com/dcosson/flex-agent-runtime/internal/ai"

func mapUsage(u *chunkUsage) ai.Usage {
	if u == nil {
		return ai.Usage{}
	}
	usage := ai.Usage{
		Input:       u.PromptTokens,
		Output:      u.CompletionTokens,
		TotalTokens: u.TotalTokens,
	}
	if u.PromptTokensDetails != nil {
		usage.CacheRead = u.PromptTokensDetails.CachedTokens
	}
	return usage
}

func mapStopReason(reason *string) ai.StopReason {
	if reason == nil {
		return ""
	}
	switch *reason {
	case "stop":
		return ai.StopReasonStop
	case "tool_calls":
		return ai.StopReasonToolUse
	case "length":
		return ai.StopReasonLength
	case "content_filter":
		return ai.StopReasonStop
	default:
		return ai.StopReasonStop
	}
}
