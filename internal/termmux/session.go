package termmux

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"h2-agent-runtime/internal/termmux/monitor"
)

// Session represents a terminal multiplexer session with a PTY,
// an agent driver, and multi-client support.
type Session struct {
	ID     string
	Config SessionConfig
	VT     *VirtualTerminal

	monitor *monitor.AgentMonitor
	clients *ClientManager

	// Lifecycle
	mu         sync.RWMutex
	started    bool
	stopped    bool
	exitNotify chan struct{}
	stopCh     chan struct{}
	cancelFn   context.CancelFunc
	createdAt  time.Time
}

// SessionConfig configures a new session.
type SessionConfig struct {
	DriverType  string
	Command     string
	Args        []string
	CWD         string
	Env         map[string]string
	InitialRows int
	InitialCols int
}

// NewSession creates a new session but does not start it.
func NewSession(id string, cfg SessionConfig) *Session {
	if cfg.InitialRows <= 0 {
		cfg.InitialRows = 24
	}
	if cfg.InitialCols <= 0 {
		cfg.InitialCols = 80
	}

	return &Session{
		ID:         id,
		Config:     cfg,
		VT:         &VirtualTerminal{},
		monitor:    monitor.NewAgentMonitor(),
		clients:    NewClientManager(),
		exitNotify: make(chan struct{}),
		stopCh:     make(chan struct{}),
		createdAt:  time.Now(),
	}
}

// Start launches the child process in a PTY and begins piping output.
func (s *Session) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("session %s already started", s.ID)
	}
	if s.stopped {
		s.mu.Unlock()
		return fmt.Errorf("session %s already stopped", s.ID)
	}
	s.started = true
	ctx, s.cancelFn = context.WithCancel(ctx)
	s.mu.Unlock()

	// Start the PTY
	err := s.VT.StartPTY(
		s.Config.Command,
		s.Config.Args,
		s.Config.InitialRows,
		s.Config.InitialCols,
		s.Config.Env,
	)
	if err != nil {
		return fmt.Errorf("start pty: %w", err)
	}

	// Emit session started event
	s.monitor.Submit(monitor.AgentEvent{
		Type:      monitor.EventSessionStarted,
		Timestamp: time.Now(),
		Data: monitor.SessionStartedData{
			SessionID: s.ID,
		},
	})

	// Start output piping goroutine with panic recovery
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "panic recovered in session %s output goroutine: %v\n%s\n", s.ID, r, debug.Stack())
			}
		}()

		err := s.VT.PipeOutput(func(data []byte) {
			s.clients.FanOut(data)
		})

		// Child has exited
		s.mu.Lock()
		s.stopped = true
		s.mu.Unlock()

		reason := "exited"
		if err != nil {
			reason = fmt.Sprintf("error: %v", err)
		}
		if s.VT.ExitError != nil {
			reason = fmt.Sprintf("exit: %v", s.VT.ExitError)
		}

		s.monitor.Submit(monitor.AgentEvent{
			Type:      monitor.EventSessionEnded,
			Timestamp: time.Now(),
			Data: monitor.SessionEndedData{
				Reason: reason,
			},
		})

		close(s.exitNotify)
	}()

	// Start context cancellation watcher
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "panic recovered in session %s context watcher: %v\n%s\n", s.ID, r, debug.Stack())
			}
		}()

		select {
		case <-ctx.Done():
			s.Stop()
		case <-s.exitNotify:
			// Session already exited
		}
	}()

	return nil
}

// Stop gracefully stops the session.
func (s *Session) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	// Send SIGTERM first, give process time to clean up
	if s.VT.Cmd != nil && s.VT.Cmd.Process != nil {
		_ = s.VT.Cmd.Process.Signal(os.Interrupt)

		// Wait briefly for clean exit
		select {
		case <-s.exitNotify:
			goto cleanup
		case <-time.After(3 * time.Second):
			// Force kill
		}
	}

	// Force kill if still running
	s.VT.KillChild()

	// Wait for exit
	select {
	case <-s.exitNotify:
	case <-time.After(5 * time.Second):
		// Give up waiting
	}

cleanup:
	s.VT.Close()
	s.clients.CloseAll()
	s.monitor.Close()
	if s.cancelFn != nil {
		s.cancelFn()
	}
}

// Wait blocks until the session exits.
func (s *Session) Wait() {
	<-s.exitNotify
}

// ExitNotify returns a channel that is closed when the session exits.
func (s *Session) ExitNotify() <-chan struct{} {
	return s.exitNotify
}

// Attach adds a client to the session and returns it.
func (s *Session) Attach(clientID string) *Client {
	return s.clients.Attach(clientID)
}

// Detach removes a client from the session.
func (s *Session) Detach(clientID string) {
	s.clients.Detach(clientID)
}

// Monitor returns the session's agent monitor.
func (s *Session) Monitor() *monitor.AgentMonitor {
	return s.monitor
}

// WritePTY writes to the session's PTY with timeout.
func (s *Session) WritePTY(data []byte) (int, error) {
	s.mu.RLock()
	if s.stopped {
		s.mu.RUnlock()
		return 0, fmt.Errorf("session %s is stopped", s.ID)
	}
	s.mu.RUnlock()

	n, err := s.VT.WritePTY(data, 3*time.Second)
	if err == ErrPTYWriteTimeout {
		s.VT.Mu.Lock()
		s.VT.ChildHung = true
		s.VT.Mu.Unlock()
		s.VT.KillChild()
		return 0, fmt.Errorf("pty write timeout, child killed")
	}
	return n, err
}

// Resize changes the PTY size.
func (s *Session) Resize(rows, cols int) {
	s.VT.Resize(rows, cols, rows)
}

// IsRunning returns whether the session's child process is still running.
func (s *Session) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.started && !s.stopped
}

// CreatedAt returns the session creation time.
func (s *Session) CreatedAt() time.Time {
	return s.createdAt
}

// ClientCount returns the number of attached clients.
func (s *Session) ClientCount() int {
	return s.clients.Count()
}
