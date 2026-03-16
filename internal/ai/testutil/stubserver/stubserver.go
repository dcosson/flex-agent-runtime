// Package stubserver provides a configurable HTTP test server for provider
// testing. It supports SSE fixture replay and fault injection over real HTTP
// connections, exercising the full client stack (headers, auth, timeouts).
//
// Provider-agnostic: the fixture format is raw SSE text, so it works for
// Anthropic, OpenAI, and Google streaming formats without modification.
package stubserver

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

// CapturedRequest stores an incoming request for later verification.
type CapturedRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

// Fault defines fault injection behavior for a stub server.
type Fault struct {
	// Mode selects the fault type.
	Mode FaultMode

	// AfterEvents specifies the fault injection point in the event stream.
	// For TCPReset: disconnects after this many events have been sent.
	// For Malformed: replaces the event at this index with corrupt data.
	// For Backpressure: not used (delay applies to all events).
	AfterEvents int

	// StatusCode for non-2xx faults (Throttle, EmptyBody).
	StatusCode int

	// RetryAfter sets the Retry-After header for Throttle faults.
	RetryAfter string

	// MalformedData is the corrupt payload to inject for Malformed faults.
	MalformedData string

	// EventDelay adds per-event delay for Backpressure faults.
	EventDelay time.Duration
}

// FaultMode enumerates the supported fault injection modes.
type FaultMode int

const (
	// NoFault serves the fixture normally.
	NoFault FaultMode = iota

	// TCPReset (F1) — close the TCP connection after AfterEvents SSE events.
	TCPReset

	// Malformed (F2) — inject MalformedData as a corrupt SSE event after
	// AfterEvents events, then continue or close.
	Malformed

	// Throttle (F3) — return StatusCode (default 429) with optional
	// Retry-After header. Does not serve SSE data.
	Throttle

	// Backpressure (F4) — serve events with EventDelay between each,
	// simulating a slow server to test client timeout behavior.
	Backpressure

	// EmptyBody (F6) — return StatusCode with an empty response body.
	// Tests error classification from status code alone.
	EmptyBody
)

// Server is a configurable HTTP test server for provider testing.
type Server struct {
	// URL is the base URL of the running test server.
	URL string

	httpServer *httptest.Server
	mu         sync.Mutex
	requests   []CapturedRequest
	handler    http.Handler
}

// Option configures a Server.
type Option func(*Server)

// New creates a stub server with the given handler options.
// The server starts immediately and must be closed with Close().
func New(opts ...Option) *Server {
	s := &Server{}
	handler := s.configureHandler(opts...)

	s.httpServer = httptest.NewServer(handler)
	s.URL = s.httpServer.URL
	return s
}

// NewHandler creates an http.Handler using the same option behavior as New,
// but without starting an httptest.Server. The caller owns listener lifecycle.
func NewHandler(opts ...Option) http.Handler {
	s := &Server{}
	return s.configureHandler(opts...)
}

func (s *Server) configureHandler(opts ...Option) http.Handler {
	for _, opt := range opts {
		opt(s)
	}
	if s.handler == nil {
		s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "no handler configured", http.StatusInternalServerError)
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture request.
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.requests = append(s.requests, CapturedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Header: r.Header.Clone(),
			Body:   body,
		})
		s.mu.Unlock()

		s.handler.ServeHTTP(w, r)
	})
}

// Close shuts down the test server.
func (s *Server) Close() {
	s.httpServer.Close()
}

// Requests returns a copy of all captured requests.
func (s *Server) Requests() []CapturedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CapturedRequest, len(s.requests))
	copy(out, s.requests)
	return out
}

// ClearRequests resets the captured request log.
func (s *Server) ClearRequests() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = s.requests[:0]
}

// WithFixture configures the server to replay an SSE fixture string.
// The fixture should be raw SSE text (e.g., "event: message_start\ndata: {...}\n\n").
func WithFixture(fixture string) Option {
	return func(s *Server) {
		s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serveSSE(w, fixture, NoFault, Fault{})
		})
	}
}

// WithFixtureFunc configures the server to call fn for each request to get
// the fixture to serve. Useful for per-request fixture selection.
func WithFixtureFunc(fn func(r *http.Request) string) Option {
	return func(s *Server) {
		s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serveSSE(w, fn(r), NoFault, Fault{})
		})
	}
}

// WithFault configures the server to inject a fault while serving the fixture.
func WithFault(fixture string, fault Fault) Option {
	return func(s *Server) {
		s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch fault.Mode {
			case Throttle:
				serveThrottle(w, fault)
			case EmptyBody:
				serveEmptyBody(w, fault)
			case TCPReset:
				serveSSE(w, fixture, TCPReset, fault)
			default:
				serveSSE(w, fixture, fault.Mode, fault)
			}
		})
	}
}

// WithHandler configures a fully custom handler.
func WithHandler(h http.HandlerFunc) Option {
	return func(s *Server) {
		s.handler = h
	}
}

// serveSSE writes SSE data with optional fault injection.
func serveSSE(w http.ResponseWriter, fixture string, mode FaultMode, fault Fault) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	events := splitSSEEvents(fixture)
	eventsSent := 0

	for _, event := range events {
		// Check fault injection point.
		if mode == TCPReset && eventsSent >= fault.AfterEvents {
			// Force close the TCP connection.
			hijacker, ok := w.(http.Hijacker)
			if ok {
				conn, _, err := hijacker.Hijack()
				if err == nil {
					conn.Close()
				}
			}
			return
		}

		if mode == Malformed && eventsSent == fault.AfterEvents {
			// Inject malformed data.
			data := fault.MalformedData
			if data == "" {
				data = "data: {{{INVALID JSON\n\n"
			}
			fmt.Fprint(w, data)
			flusher.Flush()
			eventsSent++
			continue
		}

		if mode == Backpressure && fault.EventDelay > 0 {
			time.Sleep(fault.EventDelay)
		}

		fmt.Fprint(w, event)
		flusher.Flush()
		eventsSent++
	}
}

// serveThrottle returns an HTTP 429 (or custom status) with optional Retry-After.
func serveThrottle(w http.ResponseWriter, fault Fault) {
	status := fault.StatusCode
	if status == 0 {
		status = http.StatusTooManyRequests
	}
	if fault.RetryAfter != "" {
		w.Header().Set("Retry-After", fault.RetryAfter)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":{"message":"rate limited","type":"rate_limit_error"}}`)
}

// serveEmptyBody returns a non-2xx status with an empty body.
func serveEmptyBody(w http.ResponseWriter, fault Fault) {
	status := fault.StatusCode
	if status == 0 {
		status = http.StatusInternalServerError
	}
	w.WriteHeader(status)
}

// splitSSEEvents splits raw SSE text into individual events.
// Each event is delimited by a blank line ("\n\n").
func splitSSEEvents(fixture string) []string {
	if fixture == "" {
		return nil
	}

	// Normalize line endings.
	fixture = strings.ReplaceAll(fixture, "\r\n", "\n")

	// Split on double newline (event boundary).
	raw := strings.Split(fixture, "\n\n")
	events := make([]string, 0, len(raw))
	for _, e := range raw {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		// Re-add the double newline terminator.
		events = append(events, e+"\n\n")
	}
	return events
}

// NewTCPResetServer creates a server that sends AfterEvents SSE events from
// fixture then kills the TCP connection.
func NewTCPResetServer(fixture string, afterEvents int) *Server {
	return New(WithFault(fixture, Fault{
		Mode:        TCPReset,
		AfterEvents: afterEvents,
	}))
}

// NewMalformedServer creates a server that injects a malformed SSE event
// after afterEvents normal events.
func NewMalformedServer(fixture string, afterEvents int, malformedData string) *Server {
	return New(WithFault(fixture, Fault{
		Mode:          Malformed,
		AfterEvents:   afterEvents,
		MalformedData: malformedData,
	}))
}

// NewThrottleServer creates a server that returns HTTP 429 with optional
// Retry-After header.
func NewThrottleServer(retryAfter string) *Server {
	return New(WithFault("", Fault{
		Mode:       Throttle,
		StatusCode: http.StatusTooManyRequests,
		RetryAfter: retryAfter,
	}))
}

// NewBackpressureServer creates a server that sends events with a delay
// between each to simulate slow server responses.
func NewBackpressureServer(fixture string, eventDelay time.Duration) *Server {
	return New(WithFault(fixture, Fault{
		Mode:       Backpressure,
		EventDelay: eventDelay,
	}))
}

// NewStatusCodeServer creates a server that returns the given status code
// with an optional body.
func NewStatusCodeServer(statusCode int, body string) *Server {
	return New(WithHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if body != "" {
			fmt.Fprint(w, body)
		}
	}))
}

// NewEmptyBodyServer creates a server that returns a non-2xx status with
// an empty response body.
func NewEmptyBodyServer(statusCode int) *Server {
	return New(WithFault("", Fault{
		Mode:       EmptyBody,
		StatusCode: statusCode,
	}))
}

// NewSequenceServer creates a server that serves different responses for
// sequential requests. Useful for testing retry behavior.
func NewSequenceServer(responses []func(w http.ResponseWriter, r *http.Request)) *Server {
	var mu sync.Mutex
	idx := 0
	return New(WithHandler(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		i := idx
		idx++
		mu.Unlock()

		if i < len(responses) {
			responses[i](w, r)
		} else {
			// After exhausting sequence, return 500.
			http.Error(w, "sequence exhausted", http.StatusInternalServerError)
		}
	}))
}

// Listener returns the underlying net.Listener for low-level control.
func (s *Server) Listener() net.Listener {
	return s.httpServer.Listener
}
