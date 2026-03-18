package agenttest

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc"
)

func CreateAgentSession(t testing.TB, svc agentapi.AgentService, cfg agentapi.SessionConfig) *agentapi.CreateAgentSessionResponse {
	t.Helper()
	resp, err := svc.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{SessionConfig: cfg})
	if err != nil {
		t.Fatalf("CreateSession(%q): %v", cfg.SessionID, err)
	}
	return resp
}

func CollectAllEvents(t testing.TB, recv agentapi.EventReceiver) []agentapi.AgentEvent {
	t.Helper()
	var out []agentapi.AgentEvent
	for {
		evt, err := recv.Recv()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("recv event: %v", err)
		}
		if evt != nil {
			out = append(out, *evt)
		}
	}
}

func DrainReceiver(t testing.TB, recv agentapi.EventReceiver) {
	t.Helper()
	for {
		_, err := recv.Recv()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatalf("drain receiver: %v", err)
		}
	}
}

func WaitForReceiverEOF(recv agentapi.EventReceiver, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		_, err := recv.Recv()
		done <- err
	}()
	select {
	case err := <-done:
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	case <-time.After(timeout):
		return context.DeadlineExceeded
	}
}

func AssertAgentRPCError(t testing.TB, err error, code rpc.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected rpc error %s, got nil", code)
	}
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected RPCError, got %T (%v)", err, err)
	}
	if rpcErr.Code != code {
		t.Fatalf("rpc code = %s, want %s (err=%v)", rpcErr.Code, code, err)
	}
}
