package environment

import "errors"

var (
	ErrCapabilityNotSupported = errors.New("capability not supported by environment")
	ErrSessionNotFound        = errors.New("environment: session not found")
	ErrNotActive              = errors.New("environment not in active state")
	ErrUnavailable            = errors.New("environment unavailable")
	ErrSessionLimitReached    = errors.New("session limit reached")
)
