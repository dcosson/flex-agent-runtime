package api

import (
	"errors"
	"io"
	"testing"
)

func TestNewExecuteToolStreamChannelWithErr(t *testing.T) {
	ch := make(chan *ExecuteToolStreamMessage)
	errCh := make(chan error, 1)
	errCh <- errors.New("boom")
	close(ch)
	close(errCh)
	stream := NewExecuteToolStreamChannelWithErr(ch, errCh, nil)
	_, err := stream.Recv()
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected terminal error, got %v", err)
	}
}

func TestAgentEventReceiverCloseAndEOF(t *testing.T) {
	ch := make(chan *AgentEventEnvelope)
	closed := false
	recv := NewAgentEventReceiver(ch, func() error {
		if closed {
			return nil
		}
		closed = true
		close(ch)
		return nil
	})
	if err := recv.Close(); err != nil {
		t.Fatalf("close error: %v", err)
	}
	if err := recv.Close(); err != nil {
		t.Fatalf("second close should be idempotent: %v", err)
	}
	_, err := recv.Recv()
	if !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("expected ErrStreamClosed after Close, got %v", err)
	}
}

func TestBufferedExecuteToolStreamEOF(t *testing.T) {
	stream := NewExecuteToolStream(&ExecuteToolStreamMessage{Progress: &ToolProgress{Content: "x"}})
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("first recv: %v", err)
	}
	_, err := stream.Recv()
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}
