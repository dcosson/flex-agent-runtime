package rpc

import (
	"context"
	"errors"
	"testing"

	"github.com/anthropics/flex-agent-runtime/internal/agent"
	"github.com/anthropics/flex-agent-runtime/internal/sandbox"
	"github.com/anthropics/flex-agent-runtime/internal/sandbox/zfs"
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
		{name: "agent queue full", err: agent.ErrQueueFull, code: CodeResourceExhausted},
		{name: "agent busy", err: agent.BusyError{State: agent.StateStreaming}, code: CodeFailedPrecondition},
		{name: "service not found", err: &agent.ServiceError{Code: agent.CodeNotFound, Message: "missing"}, code: CodeNotFound},
		{name: "service duplicate", err: &agent.ServiceError{Code: agent.CodeAlreadyExists, Message: "duplicate"}, code: CodeAlreadyExists},
		{name: "service unavailable", err: &agent.ServiceError{Code: agent.CodeUnavailable, Message: "closing"}, code: CodeUnavailable},
		{name: "service max sessions", err: &agent.ServiceError{Code: agent.CodeResourceExhausted, Message: "max sessions reached"}, code: CodeResourceExhausted},
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
