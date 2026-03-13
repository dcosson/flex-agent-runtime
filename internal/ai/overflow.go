package ai

import "regexp"

var (
	overflowPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)prompt is too long`),
		regexp.MustCompile(`(?i)input is too long for requested model`),
		regexp.MustCompile(`(?i)exceeds the context window`),
		regexp.MustCompile(`(?i)input token count.*exceeds the maximum`),
		regexp.MustCompile(`(?i)maximum prompt length is \d+`),
		regexp.MustCompile(`(?i)reduce the length of the messages`),
		regexp.MustCompile(`(?i)maximum context length is \d+ tokens`),
		regexp.MustCompile(`(?i)exceeds the limit of \d+`),
		regexp.MustCompile(`(?i)exceeds the available context size`),
		regexp.MustCompile(`(?i)greater than the context length`),
		regexp.MustCompile(`(?i)context window exceeds limit`),
		regexp.MustCompile(`(?i)exceeded model token limit`),
		regexp.MustCompile(`(?i)context[_ ]length[_ ]exceeded`),
		regexp.MustCompile(`(?i)too many tokens`),
		regexp.MustCompile(`(?i)token limit exceeded`),
	}
	emptyBody4xxPattern = regexp.MustCompile(`^4(00|13)\s*(status code)?\s*\(no body\)`)
)

// IsContextOverflow detects overflow errors and silent overflow conditions.
func IsContextOverflow(msg *AssistantMessage, contextWindow int) bool {
	if msg == nil {
		return false
	}
	if msg.StopReason == StopReasonError && msg.ErrorMessage != "" {
		for _, p := range overflowPatterns {
			if p.MatchString(msg.ErrorMessage) {
				return true
			}
		}
		if emptyBody4xxPattern.MatchString(msg.ErrorMessage) {
			return true
		}
	}
	if contextWindow > 0 && msg.StopReason == StopReasonStop {
		inputTokens := msg.Usage.Input + msg.Usage.CacheRead
		if inputTokens > contextWindow {
			return true
		}
	}
	return false
}

func classifyErrorMessage(errMsg string) ProviderErrorCode {
	if errMsg == "" {
		return ErrUnknown
	}
	for _, p := range overflowPatterns {
		if p.MatchString(errMsg) {
			return ErrContextOverflow
		}
	}
	return ErrUnknown
}
