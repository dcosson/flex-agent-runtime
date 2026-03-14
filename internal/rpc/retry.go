package rpc

import "math"

type RetryPolicy struct {
	MaxAttempts int
	BaseDelayMs int
	MaxDelayMs  int
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 3, BaseDelayMs: 100, MaxDelayMs: 2000}
}

func IsRetryableMethod(method string) bool {
	switch method {
	case "GetSession", "ListSnapshots", "RollbackSession", "DestroySession", "ExecuteTool":
		return true
	case "CreateSession", "PauseSession", "ResumeSession", "CreateSnapshot", "TurnComplete":
		return false
	default:
		return false
	}
}

func IsRetryableError(err error) bool {
	rpcErr, ok := err.(*RPCError)
	if !ok {
		return false
	}
	switch rpcErr.Code {
	case CodeUnavailable, CodeDeadlineExceeded:
		return true
	default:
		return false
	}
}

func BackoffDelayMs(policy RetryPolicy, attempt int) int {
	if attempt <= 0 {
		attempt = 1
	}
	if policy.BaseDelayMs <= 0 {
		policy.BaseDelayMs = 100
	}
	if policy.MaxDelayMs <= 0 {
		policy.MaxDelayMs = 2000
	}
	pow := math.Pow(2, float64(attempt-1))
	delay := int(float64(policy.BaseDelayMs) * pow)
	if delay > policy.MaxDelayMs {
		return policy.MaxDelayMs
	}
	return delay
}
