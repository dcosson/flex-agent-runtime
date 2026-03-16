package cohere

import (
	"encoding/json"
	"fmt"
	"strings"

	"flex-agent-runtime/internal/ai"
)

func classifyHTTPError(status int) ai.ProviderErrorCode {
	switch {
	case status == 401 || status == 403:
		return ai.ErrAuth
	case status == 429:
		return ai.ErrRateLimit
	case status >= 500:
		return ai.ErrServerError
	default:
		return ai.ErrUnknown
	}
}

func providerError(status int, msg string) *ai.ProviderError {
	code := classifyHTTPError(status)
	return &ai.ProviderError{
		Code:       code,
		Message:    msg,
		StatusCode: status,
		Provider:   "cohere",
	}
}

func decodeErrorMessage(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "empty response body"
	}
	var resp cohereErrorResponse
	if err := json.Unmarshal(body, &resp); err == nil && resp.Message != "" {
		return resp.Message
	}
	return fmt.Sprintf("http error: %s", trimmed)
}
