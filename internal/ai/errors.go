package ai

import (
	"fmt"
	"time"
)

type ProviderErrorCode string

const (
	ErrContextOverflow ProviderErrorCode = "context_overflow"
	ErrRateLimit       ProviderErrorCode = "rate_limit"
	ErrAuth            ProviderErrorCode = "auth"
	ErrServerError     ProviderErrorCode = "server_error"
	ErrUnknown         ProviderErrorCode = "unknown"
)

// ProviderError is a typed provider error with optional transport metadata.
type ProviderError struct {
	Code       ProviderErrorCode
	Message    string
	StatusCode int
	Provider   string
	RetryAfter time.Duration
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("%s error from %s: %s", e.Code, e.Provider, e.Message)
}

func providerErrorFromMessage(msg *AssistantMessage) error {
	if msg == nil || msg.StopReason != StopReasonError {
		return nil
	}
	return &ProviderError{
		Code:     classifyErrorMessage(msg.ErrorMessage),
		Message:  msg.ErrorMessage,
		Provider: msg.Provider,
	}
}
