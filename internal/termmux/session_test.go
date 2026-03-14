package termmux

import (
	"context"
	"testing"
	"time"

	"h2-agent-runtime/internal/termmux/monitor"
)

func TestSession_StartAndWait(t *testing.T) {
	s := NewSession("test-1", SessionConfig{
		Command:     "/bin/echo",
		Args:        []string{"hello from session"},
		InitialRows: 24,
		InitialCols: 80,
	})

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for session to exit
	done := make(chan struct{})
	go func() {
		s.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Expected
	case <-time.After(5 * time.Second):
		t.Fatal("session did not exit within timeout")
	}

	if s.IsRunning() {
		t.Error("session should not be running after exit")
	}
}

func TestSession_DoubleStart(t *testing.T) {
	s := NewSession("test-1", SessionConfig{
		Command: "/bin/echo",
		Args:    []string{"hello"},
	})

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Second start should fail
	if err := s.Start(ctx); err == nil {
		t.Error("expected error on double start")
	}

	s.Wait()
}

func TestSession_Stop(t *testing.T) {
	s := NewSession("test-1", SessionConfig{
		Command: "/bin/sleep",
		Args:    []string{"300"},
	})

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Give process a moment to start
	time.Sleep(100 * time.Millisecond)

	if !s.IsRunning() {
		t.Fatal("session should be running")
	}

	s.Stop()

	// Wait for exit notification
	select {
	case <-s.ExitNotify():
		// Expected
	case <-time.After(10 * time.Second):
		t.Fatal("session did not stop within timeout")
	}
}

func TestSession_ContextCancellation(t *testing.T) {
	s := NewSession("test-1", SessionConfig{
		Command: "/bin/sleep",
		Args:    []string{"300"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Cancel context should stop the session
	cancel()

	select {
	case <-s.ExitNotify():
		// Expected
	case <-time.After(10 * time.Second):
		t.Fatal("session did not stop after context cancel")
	}
}

func TestSession_AttachDetach(t *testing.T) {
	s := NewSession("test-1", SessionConfig{
		Command: "/bin/sh",
		Args:    []string{"-c", "echo hello; sleep 1"},
	})

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	c1 := s.Attach("viewer-1")
	c2 := s.Attach("viewer-2")

	if s.ClientCount() != 2 {
		t.Errorf("expected 2 clients, got %d", s.ClientCount())
	}

	// Both clients should receive output
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()

	var got1, got2 bool
	for !got1 || !got2 {
		select {
		case data := <-c1.Output():
			if len(data) > 0 {
				got1 = true
			}
		case data := <-c2.Output():
			if len(data) > 0 {
				got2 = true
			}
		case <-timer.C:
			if !got1 {
				t.Error("client 1 received no data")
			}
			if !got2 {
				t.Error("client 2 received no data")
			}
			goto done
		}
	}
done:

	s.Detach("viewer-1")
	if s.ClientCount() != 1 {
		t.Errorf("expected 1 client after detach, got %d", s.ClientCount())
	}

	s.Stop()
}

func TestSession_WritePTY(t *testing.T) {
	s := NewSession("test-1", SessionConfig{
		Command: "/bin/cat",
	})

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	c := s.Attach("viewer")

	// Write to PTY
	n, err := s.WritePTY([]byte("test input\n"))
	if err != nil {
		t.Fatalf("WritePTY: %v", err)
	}
	if n != 11 {
		t.Errorf("expected 11 bytes written, got %d", n)
	}

	// Should receive echoed data
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()

	select {
	case data := <-c.Output():
		if len(data) == 0 {
			t.Error("expected non-empty data")
		}
	case <-timer.C:
		t.Error("timed out waiting for echoed data")
	}

	s.Stop()
}

func TestSession_Monitor(t *testing.T) {
	s := NewSession("test-1", SessionConfig{
		Command: "/bin/echo",
		Args:    []string{"hi"},
	})

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	m := s.Monitor()
	if m == nil {
		t.Fatal("Monitor should not be nil")
	}

	// Wait for session to finish
	s.Wait()

	// Monitor should eventually reach Exited
	deadline := time.Now().Add(2 * time.Second)
	for {
		state, _ := m.State()
		if state == monitor.StateExited {
			break
		}
		if time.Now().After(deadline) {
			state, sub := m.State()
			t.Fatalf("monitor did not reach Exited (state=%s, sub=%s)", state, sub)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSession_DefaultSize(t *testing.T) {
	s := NewSession("test-1", SessionConfig{
		Command:     "/bin/echo",
		Args:        []string{"hi"},
		InitialRows: 0, // Should default to 24
		InitialCols: 0, // Should default to 80
	})

	if s.Config.InitialRows != 24 {
		t.Errorf("expected default rows 24, got %d", s.Config.InitialRows)
	}
	if s.Config.InitialCols != 80 {
		t.Errorf("expected default cols 80, got %d", s.Config.InitialCols)
	}
}
