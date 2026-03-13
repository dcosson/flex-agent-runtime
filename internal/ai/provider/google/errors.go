package google

import (
	"encoding/json"
	"fmt"
	"strings"

	"h2-agent-runtime/internal/ai"
)

func classifyHTTPError(status int, msg string) ai.ProviderErrorCode {
	switch {
	case status == 401 || status == 403:
		return ai.ErrAuth
	case status == 429:
		return ai.ErrRateLimit
	case status >= 500:
		return ai.ErrServerError
	}
	// Check for context overflow in 400 responses
	probe := &ai.AssistantMessage{StopReason: ai.StopReasonError, ErrorMessage: msg}
	if ai.IsContextOverflow(probe, 0) {
		return ai.ErrContextOverflow
	}
	return ai.ErrUnknown
}

func providerError(status int, msg string) *ai.ProviderError {
	code := classifyHTTPError(status, msg)
	return &ai.ProviderError{
		Code:       code,
		Message:    msg,
		StatusCode: status,
		Provider:   "google",
	}
}

func decodeErrorMessage(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "empty response body"
	}
	var resp googleErrorResponse
	if err := json.Unmarshal(body, &resp); err == nil && resp.Error.Message != "" {
		return resp.Error.Message
	}
	return fmt.Sprintf("http error: %s", trimmed)
}
