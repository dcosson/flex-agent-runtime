package rpc

import (
	"errors"
	"testing"
)

func TestWrapRPCErrorAddsContext(t *testing.T) {
	err := wrapRPCError(&RPCError{Code: CodeInternal, Message: "boom"}, "s1", "bash")
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
