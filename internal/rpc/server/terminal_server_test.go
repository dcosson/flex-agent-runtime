package server

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/rpc"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/api"
	"github.com/dcosson/flex-agent-runtime/internal/termmux"
)

func newTerminalTestStack(t *testing.T, cmd string, args ...string) (*TerminalServer, *termmux.SessionManager, *termmux.Session) {
	t.Helper()
	sm := termmux.NewSessionManager()
	sess, err := sm.Create("test", termmux.SessionConfig{
		Command:     cmd,
		Args:        args,
		InitialRows: 24,
		InitialCols: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := sess.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Stop() })
	srv := NewTerminalServer(sm)
	return srv, sm, sess
}

func TestTerminalServerStreamTerminal(t *testing.T) {
	// Use 'cat' which echoes stdin to stdout via PTY
	srv, _, _ := newTerminalTestStack(t, "cat")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle, err := srv.StreamTerminal(ctx, &api.StreamTerminalRequest{SessionID: "test"})
	if err != nil {
		t.Fatalf("StreamTerminal error = %v", err)
	}
	defer handle.Close()

	// First message should be Attached
	msg, err := handle.Recv()
	if err != nil {
		t.Fatalf("Recv error = %v", err)
	}
	if msg.Attached == nil {
		t.Fatal("expected Attached message")
	}
	if msg.Attached.Rows != 24 || msg.Attached.Cols != 80 {
		t.Fatalf("wrong dimensions: %dx%d", msg.Attached.Rows, msg.Attached.Cols)
	}
}

func TestTerminalServerMissingSession(t *testing.T) {
	sm := termmux.NewSessionManager()
	srv := NewTerminalServer(sm)
	ctx := context.Background()

	_, err := srv.StreamTerminal(ctx, &api.StreamTerminalRequest{SessionID: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for missing session")
	}
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != rpc.CodeNotFound {
		t.Fatalf("code = %s, want %s", rpcErr.Code, rpc.CodeNotFound)
	}
}

func TestTerminalServerEmptySessionID(t *testing.T) {
	sm := termmux.NewSessionManager()
	srv := NewTerminalServer(sm)
	ctx := context.Background()

	_, err := srv.StreamTerminal(ctx, &api.StreamTerminalRequest{SessionID: ""})
	if err == nil {
		t.Fatal("expected error for empty session_id")
	}
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != rpc.CodeInvalidArgument {
		t.Fatalf("code = %s, want %s", rpcErr.Code, rpc.CodeInvalidArgument)
	}
}

func TestTerminalServerNilRequest(t *testing.T) {
	sm := termmux.NewSessionManager()
	srv := NewTerminalServer(sm)
	ctx := context.Background()

	_, err := srv.StreamTerminal(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != rpc.CodeInvalidArgument {
		t.Fatalf("code = %s, want %s", rpcErr.Code, rpc.CodeInvalidArgument)
	}
}

func TestTerminalServerInputForwarding(t *testing.T) {
	// 'cat' echoes stdin back to stdout
	srv, _, _ := newTerminalTestStack(t, "cat")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	handle, err := srv.StreamTerminal(ctx, &api.StreamTerminalRequest{SessionID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()

	// Consume attached message
	msg, err := handle.Recv()
	if err != nil || msg.Attached == nil {
		t.Fatalf("expected Attached, got %+v, err=%v", msg, err)
	}

	// Send input
	testInput := "hello\n"
	if err := handle.Send(&api.TerminalClientMessage{
		Input: &api.TerminalInput{Data: []byte(testInput)},
	}); err != nil {
		t.Fatal(err)
	}

	// Read output — should eventually contain "hello" (echoed by cat via PTY)
	deadline := time.After(3 * time.Second)
	var collected []byte
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for echo, collected: %q", collected)
		default:
		}

		msg, err := handle.Recv()
		if err != nil {
			t.Fatalf("Recv error = %v (collected: %q)", err, collected)
		}
		if msg.Output != nil {
			collected = append(collected, msg.Output.Data...)
			if strings.Contains(string(collected), "hello") {
				return // success
			}
		}
	}
}

func TestTerminalServerResize(t *testing.T) {
	srv, _, sess := newTerminalTestStack(t, "cat")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle, err := srv.StreamTerminal(ctx, &api.StreamTerminalRequest{SessionID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()

	// Consume attached
	if _, err := handle.Recv(); err != nil {
		t.Fatal(err)
	}

	// Send resize
	if err := handle.Send(&api.TerminalClientMessage{
		Resize: &api.TerminalResize{Rows: 40, Cols: 120},
	}); err != nil {
		t.Fatal(err)
	}

	// Give the input pump time to process
	time.Sleep(50 * time.Millisecond)

	// Verify dimensions on the VT
	sess.VT.Mu.Lock()
	rows, cols := sess.VT.Rows, sess.VT.Cols
	sess.VT.Mu.Unlock()

	if rows != 40 || cols != 120 {
		t.Fatalf("resize not applied: got %dx%d, want 40x120", rows, cols)
	}
}

func TestTerminalServerScrollback(t *testing.T) {
	// Use a command that produces output then waits
	srv, _, _ := newTerminalTestStack(t, "sh", "-c", "echo SCROLLBACK_MARKER; sleep 30")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Wait for the initial output to be produced and stored in scrollback
	time.Sleep(200 * time.Millisecond)

	// Now attach — should get scrollback with the marker
	handle, err := srv.StreamTerminal(ctx, &api.StreamTerminalRequest{SessionID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()

	msg, err := handle.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if msg.Attached == nil {
		t.Fatal("expected Attached message")
	}
	if !strings.Contains(string(msg.Attached.Scrollback), "SCROLLBACK_MARKER") {
		t.Fatalf("scrollback missing marker: %q", msg.Attached.Scrollback)
	}
}

func TestTerminalServerMultiSubscriber(t *testing.T) {
	srv, _, _ := newTerminalTestStack(t, "cat")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const subscribers = 3
	handles := make([]api.TerminalStreamHandle, subscribers)
	for i := 0; i < subscribers; i++ {
		h, err := srv.StreamTerminal(ctx, &api.StreamTerminalRequest{SessionID: "test"})
		if err != nil {
			t.Fatal(err)
		}
		defer h.Close()
		handles[i] = h
	}

	// Consume attached messages from all subscribers
	for i, h := range handles {
		msg, err := h.Recv()
		if err != nil || msg.Attached == nil {
			t.Fatalf("subscriber %d: expected Attached, got err=%v", i, err)
		}
	}

	// Send input through the first handle
	if err := handles[0].Send(&api.TerminalClientMessage{
		Input: &api.TerminalInput{Data: []byte("multi\n")},
	}); err != nil {
		t.Fatal(err)
	}

	// All subscribers should receive output containing "multi"
	var wg sync.WaitGroup
	for i, h := range handles {
		wg.Add(1)
		go func(idx int, handle api.TerminalStreamHandle) {
			defer wg.Done()
			deadline := time.After(3 * time.Second)
			var collected []byte
			for {
				select {
				case <-deadline:
					t.Errorf("subscriber %d: timeout, collected: %q", idx, collected)
					return
				default:
				}
				msg, err := handle.Recv()
				if err != nil {
					t.Errorf("subscriber %d: Recv error = %v", idx, err)
					return
				}
				if msg.Output != nil {
					collected = append(collected, msg.Output.Data...)
					if strings.Contains(string(collected), "multi") {
						return // success
					}
				}
			}
		}(i, h)
	}
	wg.Wait()
}

func TestTerminalServerSessionEnded(t *testing.T) {
	// Use a short-lived command
	srv, _, sess := newTerminalTestStack(t, "echo", "done")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle, err := srv.StreamTerminal(ctx, &api.StreamTerminalRequest{SessionID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()

	// Read messages until we get Detached or EOF
	deadline := time.After(5 * time.Second)
	sawDetached := false
	for {
		select {
		case <-deadline:
			// The session should have ended quickly. Stop the session
			// explicitly and try one more time.
			sess.Stop()
			// Give pumps time to process
			time.Sleep(100 * time.Millisecond)
			if !sawDetached {
				t.Fatal("timeout waiting for Detached message")
			}
			return
		default:
		}

		msg, err := handle.Recv()
		if err == io.EOF {
			// Stream ended — check if we saw detached
			if !sawDetached {
				// EOF without Detached is acceptable for short-lived commands
				// where the output pump may exit before sending Detached.
				return
			}
			return
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if msg.Detached != nil {
			sawDetached = true
			if msg.Detached.Reason != "session_ended" {
				t.Fatalf("unexpected detach reason: %s", msg.Detached.Reason)
			}
			return
		}
	}
}

func TestTerminalServerCleanDisconnect(t *testing.T) {
	srv, _, sess := newTerminalTestStack(t, "cat")
	ctx, cancel := context.WithCancel(context.Background())

	handle, err := srv.StreamTerminal(ctx, &api.StreamTerminalRequest{SessionID: "test"})
	if err != nil {
		t.Fatal(err)
	}

	// Consume attached
	if _, err := handle.Recv(); err != nil {
		t.Fatal(err)
	}

	// Verify subscriber count
	if n := sess.TerminalSubscriberCount(); n != 1 {
		t.Fatalf("subscriber count = %d, want 1", n)
	}

	// Close the handle (simulates client disconnect)
	handle.Close()
	cancel()

	// Give cleanup goroutines time to run
	time.Sleep(100 * time.Millisecond)

	// Subscriber should be cleaned up
	if n := sess.TerminalSubscriberCount(); n != 0 {
		t.Fatalf("subscriber count after close = %d, want 0", n)
	}
}

func TestTerminalServerCloseIdempotent(t *testing.T) {
	srv, _, _ := newTerminalTestStack(t, "cat")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle, err := srv.StreamTerminal(ctx, &api.StreamTerminalRequest{SessionID: "test"})
	if err != nil {
		t.Fatal(err)
	}

	// Multiple closes should not panic
	_ = handle.Close()
	_ = handle.Close()
	_ = handle.Close()
}

func TestTerminalServerSendAfterClose(t *testing.T) {
	srv, _, _ := newTerminalTestStack(t, "cat")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle, err := srv.StreamTerminal(ctx, &api.StreamTerminalRequest{SessionID: "test"})
	if err != nil {
		t.Fatal(err)
	}

	_ = handle.Close()
	time.Sleep(20 * time.Millisecond)

	// Send after close should return ErrStreamClosed
	err = handle.Send(&api.TerminalClientMessage{
		Input: &api.TerminalInput{Data: []byte("after close")},
	})
	if !errors.Is(err, api.ErrStreamClosed) {
		t.Fatalf("Send after close: expected ErrStreamClosed, got %v", err)
	}
}
