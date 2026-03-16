package rpc

import (
	"context"
	"errors"
	"testing"

	"flex-agent-runtime/internal/sandbox"
	"flex-agent-runtime/internal/sandbox/zfs"
)

func TestWrapRPCErrorAddsContext(t *testing.T) {
	err := WrapRPCError(&RPCError{Code: CodeInternal, Message: "boom"}, "s1", "bash")
	rpcErr, ok := err.(*RPCError)
	if !ok {
		t.Fatalf("expected RPCError")
	}
	if rpcErr.Details["session_id"] != "s1" || rpcErr.Details["tool_name"] != "bash" {
		t.Fatalf("missing details: %+v", rpcErr.Details)
	}
}

func TestRPCErrorUnwrap(t *testing.T) {
	base := errors.New("base")
	err := &RPCError{Code: CodeInternal, Cause: base}
	if !errors.Is(err, base) {
		t.Fatalf("unwrap failed")
	}
}

func TestMapError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code Code
	}{
		{name: "not found", err: sandbox.ErrSessionNotFound, code: CodeNotFound},
		{name: "already exists", err: sandbox.ErrSessionExists, code: CodeAlreadyExists},
		{name: "failed precondition", err: sandbox.ErrSessionPaused, code: CodeFailedPrecondition},
		{name: "resource exhausted", err: zfs.ErrPoolFull, code: CodeResourceExhausted},
		{name: "canceled", err: context.Canceled, code: CodeCanceled},
		{name: "deadline", err: context.DeadlineExceeded, code: CodeDeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapped := MapError(tc.err)
			rpcErr, ok := mapped.(*RPCError)
			if !ok {
				t.Fatalf("expected RPCError, got %T", mapped)
			}
			if rpcErr.Code != tc.code {
				t.Fatalf("code = %s, want %s", rpcErr.Code, tc.code)
			}
		})
	}
}
