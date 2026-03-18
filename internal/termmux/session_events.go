package termmux

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/dcosson/flex-agent-runtime/internal/termmux/eventsrc/otelserver"
	"github.com/dcosson/flex-agent-runtime/internal/termmux/eventsrc/sessionlog"
	"github.com/dcosson/flex-agent-runtime/internal/termmux/monitor"
)

// EventSourceConfig configures the event sources for a session.
type EventSourceConfig struct {
	// OTEL callbacks — set by the driver's event handler
	OtelCallbacks otelserver.Callbacks

	// Session log path and callback — set by the driver
	SessionLogPath   string
	OnSessionLogLine func(line []byte)

	// Hook event handler — set by the driver
	OnHookEvent func(eventName string, payload []byte) []monitor.AgentEvent
}

// SessionEventSources holds the running event sources for a session.
type SessionEventSources struct {
	OtelServer    *otelserver.Server
	SessionTailer *sessionlog.Tailer
	cancelTailer  context.CancelFunc
}

// StartEventSources initializes and starts the event sources for a session.
// The OTEL server must be started before launching the child process so that
// its endpoint can be injected into the child's environment.
func (s *Session) StartEventSources(cfg EventSourceConfig) (*SessionEventSources, error) {
	sources := &SessionEventSources{}

	// Start OTEL server
	srv, err := otelserver.New(cfg.OtelCallbacks)
	if err != nil {
		return nil, fmt.Errorf("create OTEL server: %w", err)
	}
	if err := srv.Start(); err != nil {
		return nil, fmt.Errorf("start OTEL server: %w", err)
	}
	sources.OtelServer = srv

	// Start session log tailer if configured
	if cfg.SessionLogPath != "" && cfg.OnSessionLogLine != nil {
		tailer := sessionlog.New(cfg.SessionLogPath, cfg.OnSessionLogLine)
		ctx, cancel := context.WithCancel(context.Background())
		sources.cancelTailer = cancel

		go func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "panic recovered in session log tailer: %v\n%s\n", r, debug.Stack())
				}
			}()
			_ = tailer.Start(ctx)
		}()
		sources.SessionTailer = tailer
	}

	return sources, nil
}

// Stop shuts down all event sources.
func (ses *SessionEventSources) Stop() {
	if ses.OtelServer != nil {
		ses.OtelServer.Stop()
	}
	if ses.cancelTailer != nil {
		ses.cancelTailer()
	}
}

// OtelEndpoint returns the OTEL server endpoint URL for injection into
// the child process environment.
func (ses *SessionEventSources) OtelEndpoint() string {
	if ses.OtelServer != nil {
		return ses.OtelServer.Endpoint()
	}
	return ""
}
