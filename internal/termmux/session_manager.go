package termmux

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/termmux/monitor"
)

// SessionManager manages the lifecycle of terminal multiplexer sessions.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session

	maxSessions int
	ctx         context.Context
	cancelFn    context.CancelFunc
}

// SessionInfo is a summary of a session's state for listing.
type SessionInfo struct {
	ID          string
	DriverType  string
	Command     string
	State       monitor.State
	SubState    monitor.SubState
	CreatedAt   time.Time
	ClientCount int
}

// ManagerOption configures the SessionManager.
type ManagerOption func(*SessionManager)

// WithMaxSessions sets the maximum number of concurrent sessions.
func WithMaxSessions(n int) ManagerOption {
	return func(sm *SessionManager) {
		sm.maxSessions = n
	}
}

// NewSessionManager creates a new session manager.
func NewSessionManager(opts ...ManagerOption) *SessionManager {
	ctx, cancel := context.WithCancel(context.Background())
	sm := &SessionManager{
		sessions:    make(map[string]*Session),
		maxSessions: 100, // default
		ctx:         ctx,
		cancelFn:    cancel,
	}
	for _, opt := range opts {
		opt(sm)
	}
	return sm
}

// Create creates a new session with the given configuration.
// The session is not started until Start is called on it.
func (sm *SessionManager) Create(id string, cfg SessionConfig) (*Session, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, exists := sm.sessions[id]; exists {
		return nil, fmt.Errorf("session %s already exists", id)
	}

	if len(sm.sessions) >= sm.maxSessions {
		return nil, fmt.Errorf("session limit reached (%d)", sm.maxSessions)
	}

	s := NewSession(id, cfg)
	sm.sessions[id] = s

	// Watch for session exit to clean up
	go func() {
		<-s.ExitNotify()
		// Don't remove from map automatically — let the caller do it
		// via Kill or explicit removal. This preserves session info for
		// post-mortem inspection.
	}()

	return s, nil
}

// Get retrieves a session by ID.
func (sm *SessionManager) Get(id string) (*Session, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	s, ok := sm.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %s not found", id)
	}
	return s, nil
}

// List returns info about all sessions, sorted by creation time.
func (sm *SessionManager) List() []SessionInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	infos := make([]SessionInfo, 0, len(sm.sessions))
	for _, s := range sm.sessions {
		state, subState := s.Monitor().State()
		infos = append(infos, SessionInfo{
			ID:          s.ID,
			DriverType:  s.Config.DriverType,
			Command:     s.Config.Command,
			State:       state,
			SubState:    subState,
			CreatedAt:   s.CreatedAt(),
			ClientCount: s.ClientCount(),
		})
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].CreatedAt.Before(infos[j].CreatedAt)
	})
	return infos
}

// Kill stops and removes a session.
func (sm *SessionManager) Kill(id string) error {
	sm.mu.Lock()
	s, ok := sm.sessions[id]
	if !ok {
		sm.mu.Unlock()
		return fmt.Errorf("session %s not found", id)
	}
	delete(sm.sessions, id)
	sm.mu.Unlock()

	s.Stop()
	return nil
}

// Count returns the number of sessions.
func (sm *SessionManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// Shutdown gracefully stops all sessions.
func (sm *SessionManager) Shutdown(timeout time.Duration) error {
	sm.cancelFn()

	sm.mu.Lock()
	sessions := make([]*Session, 0, len(sm.sessions))
	for _, s := range sm.sessions {
		sessions = append(sessions, s)
	}
	sm.sessions = make(map[string]*Session)
	sm.mu.Unlock()

	// Stop all sessions concurrently
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func(sess *Session) {
			defer wg.Done()
			sess.Stop()
		}(s)
	}

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("shutdown timed out after %v with %d sessions remaining", timeout, len(sessions))
	}
}
