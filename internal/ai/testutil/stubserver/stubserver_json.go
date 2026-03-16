package stubserver

import (
	"fmt"
	"net/http"
	"time"
)

// WithJSONFixture configures the server to serve a canned JSON response.
// The fixture should be a valid JSON string.
func WithJSONFixture(fixture string) Option {
	return func(s *Server) {
		s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serveJSON(w, fixture, NoFault, Fault{})
		})
	}
}

// WithJSONFixtureFunc configures the server to call fn for each request to get
// the JSON fixture to serve. Useful for per-request fixture selection.
func WithJSONFixtureFunc(fn func(r *http.Request) string) Option {
	return func(s *Server) {
		s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serveJSON(w, fn(r), NoFault, Fault{})
		})
	}
}

// WithJSONFault configures the server to inject a fault while serving a JSON response.
// For TCPReset, AfterEvents is reinterpreted as byte count: the connection is closed
// after writing that many bytes of the response body.
func WithJSONFault(fixture string, fault Fault) Option {
	return func(s *Server) {
		s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch fault.Mode {
			case Throttle:
				serveThrottle(w, fault)
			case EmptyBody:
				serveEmptyBody(w, fault)
			default:
				serveJSON(w, fixture, fault.Mode, fault)
			}
		})
	}
}

// serveJSON writes a JSON response with optional fault injection.
func serveJSON(w http.ResponseWriter, fixture string, mode FaultMode, fault Fault) {
	switch mode {
	case TCPReset:
		serveJSONTCPReset(w, fixture, fault)
	case Malformed:
		serveJSONMalformed(w, fault)
	case Backpressure:
		if fault.EventDelay > 0 {
			time.Sleep(fault.EventDelay)
		}
		writeJSON(w, fixture)
	default:
		writeJSON(w, fixture)
	}
}

// writeJSON writes a normal JSON response.
func writeJSON(w http.ResponseWriter, fixture string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, fixture)
}

// serveJSONTCPReset writes a partial JSON response then closes the TCP connection.
// AfterEvents is reinterpreted as byte count.
func serveJSONTCPReset(w http.ResponseWriter, fixture string, fault Fault) {
	afterBytes := fault.AfterEvents
	if afterBytes <= 0 {
		// Close immediately before any data.
		hijacker, ok := w.(http.Hijacker)
		if ok {
			conn, _, err := hijacker.Hijack()
			if err == nil {
				conn.Close()
			}
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// Write partial data.
	data := fixture
	if afterBytes < len(data) {
		data = data[:afterBytes]
	}
	fmt.Fprint(w, data)

	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// Force close the TCP connection.
	hijacker, ok := w.(http.Hijacker)
	if ok {
		conn, _, err := hijacker.Hijack()
		if err == nil {
			conn.Close()
		}
	}
}

// serveJSONMalformed writes corrupt JSON data as the response body.
func serveJSONMalformed(w http.ResponseWriter, fault Fault) {
	data := fault.MalformedData
	if data == "" {
		data = `{{{INVALID JSON`
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, data)
}

// NewJSONServer creates a server that serves a canned JSON response.
func NewJSONServer(fixture string) *Server {
	return New(WithJSONFixture(fixture))
}

// NewJSONFaultServer creates a server that injects a fault while serving JSON.
func NewJSONFaultServer(fixture string, fault Fault) *Server {
	return New(WithJSONFault(fixture, fault))
}
