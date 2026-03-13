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
	MaxTokens       *int
	APIKey          string
	SessionID       string
	CacheRetention  CacheRetention
	Headers         map[string]string
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
