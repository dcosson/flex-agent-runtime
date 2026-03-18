package orchestrator

import (
	"fmt"
	"sort"
	"sync/atomic"
	"time"

	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
)

var generatedSessionIDCounter atomic.Uint64

type sessionState string

const (
	sessionCreating  sessionState = "creating"
	sessionActive    sessionState = "active"
	sessionPaused    sessionState = "paused"
	sessionUnhealthy sessionState = "unhealthy"
	sessionDestroyed sessionState = "destroyed"
)

// sessionEntry tracks an active proxied session.
type sessionEntry struct {
	sessionID       string
	remoteSessionID string
	placement       PlacementMode

	sandboxID       string
	toolSessionID   string // host-local session ID for ToolEnvironment (tools-sandbox only)
	processID       string
	sandboxControl  control.SandboxControl
	sandboxHostAddr string
	agentService    agentapi.AgentService

	state      sessionState
	createdAt  time.Time
	lastHealth time.Time
}

func generateSessionID() string {
	n := generatedSessionIDCounter.Add(1)
	return fmt.Sprintf("orch-%08x", n)
}

func (o *Orchestrator) addSession(entry *sessionEntry) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closing {
		return rpc.NewRPCError(rpc.CodeUnavailable, "orchestrator is shutting down", nil)
	}
	if o.config.MaxSessions > 0 && len(o.sessions) >= o.config.MaxSessions {
		return rpc.NewRPCError(rpc.CodeResourceExhausted, "max sessions reached", nil)
	}
	if _, exists := o.sessions[entry.sessionID]; exists {
		return rpc.NewRPCError(rpc.CodeAlreadyExists, fmt.Sprintf("session %q already exists", entry.sessionID), nil)
	}
	o.sessions[entry.sessionID] = entry
	return nil
}

func (o *Orchestrator) getSessionEntry(sessionID string) (*sessionEntry, error) {
	o.mu.RLock()
	entry, ok := o.sessions[sessionID]
	o.mu.RUnlock()
	if !ok {
		return nil, rpc.NewRPCError(rpc.CodeNotFound, fmt.Sprintf("session %q not found", sessionID), nil)
	}
	return entry, nil
}

func (o *Orchestrator) removeSessionEntry(sessionID string) {
	o.mu.Lock()
	delete(o.sessions, sessionID)
	o.mu.Unlock()
}

func (o *Orchestrator) listSessionEntries() []*sessionEntry {
	o.mu.RLock()
	entries := make([]*sessionEntry, 0, len(o.sessions))
	for _, entry := range o.sessions {
		entries = append(entries, entry)
	}
	o.mu.RUnlock()
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].createdAt.Equal(entries[j].createdAt) {
			return entries[i].sessionID < entries[j].sessionID
		}
		return entries[i].createdAt.Before(entries[j].createdAt)
	})
	return entries
}
