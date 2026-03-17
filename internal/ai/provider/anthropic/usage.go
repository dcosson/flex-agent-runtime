package anthropic

import "github.com/anthropics/flex-agent-runtime/internal/ai"

func mapUsage(usage wireUsage) ai.Usage {
	return ai.Usage{
		Input:       usage.InputTokens,
		Output:      usage.OutputTokens,
		CacheRead:   usage.CacheReadInputTokens,
		CacheWrite:  usage.CacheCreationInputTokens,
		TotalTokens: usage.InputTokens + usage.OutputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens,
	}
}

func mergeUsage(dst *ai.Usage, usage wireUsage) {
	if usage.InputTokens > 0 {
		dst.Input = usage.InputTokens
	}
	if usage.OutputTokens > 0 {
		dst.Output = usage.OutputTokens
	}
	if usage.CacheReadInputTokens > 0 {
		dst.CacheRead = usage.CacheReadInputTokens
	}
	if usage.CacheCreationInputTokens > 0 {
		dst.CacheWrite = usage.CacheCreationInputTokens
	}
	dst.TotalTokens = dst.Input + dst.Output + dst.CacheRead + dst.CacheWrite
}

func mapStopReason(reason string) ai.StopReason {
	switch reason {
	case "end_turn", "stop_sequence":
		return ai.StopReasonStop
	case "max_tokens":
		return ai.StopReasonLength
	case "tool_use":
		return ai.StopReasonToolUse
	case "pause_turn":
		return ai.StopReasonStop
	case "":
		return ai.StopReasonStop
	default:
		return ai.StopReasonError
	}
}
