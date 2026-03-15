package environment

import "errors"

var (
	ErrCapabilityNotSupported = errors.New("environment: capability not supported")
	ErrSessionNotFound        = errors.New("environment: session not found")
	ErrNotActive              = errors.New("environment: not active")
	ErrProviderUnavailable    = errors.New("environment: provider unavailable")
	ErrSessionLimitReached    = errors.New("environment: session limit reached")
)
