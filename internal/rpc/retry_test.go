package rpc

import "testing"

func TestIsRetryableMethod(t *testing.T) {
	if !IsRetryableMethod("GetSession") {
		t.Fatalf("expected GetSession retryable")
	}
	if IsRetryableMethod("ExecuteTool") {
		t.Fatalf("expected ExecuteTool non-retryable")
	}
}

func TestIsRetryableError(t *testing.T) {
	if !IsRetryableError(&RPCError{Code: CodeUnavailable}) {
		t.Fatalf("expected unavailable retryable")
	}
	if IsRetryableError(&RPCError{Code: CodeInternal}) {
		t.Fatalf("expected internal non-retryable")
	}
}

func TestBackoffDelayMs(t *testing.T) {
	p := RetryPolicy{BaseDelayMs: 100, MaxDelayMs: 500}
	if got := BackoffDelayMs(p, 1); got != 100 {
		t.Fatalf("attempt1 got=%d", got)
	}
	if got := BackoffDelayMs(p, 2); got != 200 {
		t.Fatalf("attempt2 got=%d", got)
	}
	if got := BackoffDelayMs(p, 4); got != 500 {
		t.Fatalf("attempt4 got=%d", got)
	}
}
