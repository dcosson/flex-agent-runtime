package ai

type ThinkingLevel string

const (
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXHigh   ThinkingLevel = "xhigh"
)

type CacheRetention string

const (
	CacheRetentionNone  CacheRetention = "none"
	CacheRetentionShort CacheRetention = "short"
	CacheRetentionLong  CacheRetention = "long"
)

// StreamOptions controls provider streaming behavior.
type StreamOptions struct {
	Temperature     *float64
	TopP            *float64
	TopK            *int
	MaxTokens       *int
	APIKey          string
	SessionID       string
	CacheRetention  CacheRetention
	Headers         map[string][]string
	MaxRetryDelayMs int
	Metadata        map[string]any
	OnPayload       func(payload any)
}

// SimpleStreamOptions layers high-level reasoning controls over StreamOptions.
type SimpleStreamOptions struct {
	StreamOptions
	Reasoning       ThinkingLevel
	ThinkingBudgets *ThinkingBudgets
}

// ThinkingBudgets stores per-thinking-level token budgets.
type ThinkingBudgets struct {
	Minimal *int
	Low     *int
	Medium  *int
	High    *int
}

// BuildBaseOptions converts SimpleStreamOptions into StreamOptions.
func BuildBaseOptions(model Model, opts *SimpleStreamOptions) StreamOptions {
	var so StreamOptions
	if opts != nil {
		so = opts.StreamOptions
	}
	if so.MaxTokens == nil {
		maxTok := minInt(model.MaxTokens, 32000)
		so.MaxTokens = &maxTok
	}
	return so
}

// ClampReasoning reduces xhigh to high for providers that do not support xhigh.
func ClampReasoning(level ThinkingLevel) ThinkingLevel {
	switch level {
	case ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh:
		return level
	case ThinkingXHigh:
		return ThinkingHigh
	default:
		return ThinkingMedium
	}
}

// AdjustMaxTokensForThinking computes output max tokens and thinking budget.
func AdjustMaxTokensForThinking(baseMaxTokens, modelMaxTokens int, level ThinkingLevel, budgets *ThinkingBudgets) (maxTokens int, thinkingBudget int) {
	defaults := map[ThinkingLevel]int{
		ThinkingMinimal: 1024,
		ThinkingLow:     2048,
		ThinkingMedium:  8192,
		ThinkingHigh:    16384,
	}

	clamped := ClampReasoning(level)
	thinkingBudget = defaults[clamped]

	if budgets != nil {
		switch clamped {
		case ThinkingMinimal:
			if budgets.Minimal != nil {
				thinkingBudget = *budgets.Minimal
			}
		case ThinkingLow:
			if budgets.Low != nil {
				thinkingBudget = *budgets.Low
			}
		case ThinkingMedium:
			if budgets.Medium != nil {
				thinkingBudget = *budgets.Medium
			}
		case ThinkingHigh:
			if budgets.High != nil {
				thinkingBudget = *budgets.High
			}
		}
	}

	maxTokens = minInt(baseMaxTokens+thinkingBudget, modelMaxTokens)
	const minOutputTokens = 1024
	if maxTokens <= thinkingBudget {
		thinkingBudget = maxInt(0, maxTokens-minOutputTokens)
	}
	return maxTokens, thinkingBudget
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
