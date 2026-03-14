// Package otelserver provides a per-session HTTP server that receives
// OTEL log, metric, and trace data from agent CLIs.
package otelserver

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	defaultDrainTimeout = 2 * time.Second
	maxBodySize         = 10 * 1024 * 1024 // 10MB
)

// Callbacks define the handler functions for OTEL data.
type Callbacks struct {
	OnLogs    func(body []byte) // POST /v1/logs
	OnMetrics func(body []byte) // POST /v1/metrics
	OnTraces  func(body []byte) // POST /v1/traces
}

// Server is a per-session OTEL HTTP collector on localhost.
type Server struct {
	listener     net.Listener
	server       *http.Server
	callbacks    Callbacks
	port         int
	drainTimeout time.Duration

	mu      sync.Mutex
	started bool
	stopped bool
}

// Option configures the Server.
type Option func(*Server)

// WithDrainTimeout sets the graceful shutdown drain timeout.
func WithDrainTimeout(d time.Duration) Option {
	return func(s *Server) {
		s.drainTimeout = d
	}
}

// New creates a new OTEL server with the given callbacks.
// The server binds to 127.0.0.1:0 (random port).
func New(cb Callbacks, opts ...Option) (*Server, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bind OTEL server: %w", err)
	}

	s := &Server{
		listener:     listener,
		callbacks:    cb,
		port:         listener.Addr().(*net.TCPAddr).Port,
		drainTimeout: defaultDrainTimeout,
	}

	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/logs", s.handleLogs)
	mux.HandleFunc("POST /v1/metrics", s.handleMetrics)
	mux.HandleFunc("POST /v1/traces", s.handleTraces)

	s.server = &http.Server{
		Handler: mux,
	}

	return s, nil
}

// Start begins serving. Returns immediately.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return fmt.Errorf("OTEL server already started")
	}
	s.started = true

	go func() {
		if err := s.server.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			fmt.Printf("OTEL server error: %v\n", err)
		}
	}()

	return nil
}

// Endpoint returns the OTEL endpoint URL (e.g., "http://127.0.0.1:12345").
func (s *Server) Endpoint() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.port)
}

// Port returns the port the server is listening on.
func (s *Server) Port() int {
	return s.port
}

// Stop gracefully shuts down the server with drain timeout.
func (s *Server) Stop() error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), s.drainTimeout)
	defer cancel()

	return s.server.Shutdown(ctx)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.callbacks.OnLogs != nil {
		s.callbacks.OnLogs(body)
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.callbacks.OnMetrics != nil {
		s.callbacks.OnMetrics(body)
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleTraces(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.callbacks.OnTraces != nil {
		s.callbacks.OnTraces(body)
	}
	w.WriteHeader(http.StatusOK)
}

func readBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(r.Body, maxBodySize))
}
