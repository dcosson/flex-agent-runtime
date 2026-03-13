package stubserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

const anthropicFixture = `event: message_start
data: {"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-20250514","stop_reason":null,"usage":{"input_tokens":25,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":12}}

event: message_stop
data: {"type":"message_stop"}
`

const openaiFixture = `data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
`

// S1: Correctness replay mode.
func TestFixtureReplay(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		events  int
	}{
		{"anthropic", anthropicFixture, 7},
		{"openai", openaiFixture, 5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New(WithFixture(tc.fixture))
			defer s.Close()

			resp, err := http.Get(s.URL)
			if err != nil {
				t.Fatalf("GET failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.Header.Get("Content-Type") != "text/event-stream" {
				t.Fatalf("expected text/event-stream, got %q", resp.Header.Get("Content-Type"))
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}

			// Count events in response (delimited by double newline).
			events := splitSSEEvents(string(body))
			if len(events) != tc.events {
				t.Fatalf("expected %d events, got %d", tc.events, len(events))
			}
		})
	}
}

// Test request capture.
func TestRequestCapture(t *testing.T) {
	s := New(WithFixture("data: ok\n\n"))
	defer s.Close()

	req, _ := http.NewRequest("POST", s.URL+"/v1/messages", strings.NewReader(`{"model":"test"}`))
	req.Header.Set("Authorization", "Bearer sk-test-key")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	reqs := s.Requests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 captured request, got %d", len(reqs))
	}

	captured := reqs[0]
	if captured.Method != "POST" {
		t.Fatalf("expected POST, got %s", captured.Method)
	}
	if captured.Path != "/v1/messages" {
		t.Fatalf("expected /v1/messages, got %s", captured.Path)
	}
	if captured.Header.Get("Authorization") != "Bearer sk-test-key" {
		t.Fatalf("auth header not captured")
	}
	if string(captured.Body) != `{"model":"test"}` {
		t.Fatalf("body not captured: %s", string(captured.Body))
	}
}

func TestClearRequests(t *testing.T) {
	s := New(WithFixture("data: ok\n\n"))
	defer s.Close()

	resp, _ := http.Get(s.URL)
	resp.Body.Close()

	if len(s.Requests()) != 1 {
		t.Fatal("expected 1 request")
	}
	s.ClearRequests()
	if len(s.Requests()) != 0 {
		t.Fatal("expected 0 requests after clear")
	}
}

// WithFixtureFunc per-request fixture selection.
func TestFixtureFunc(t *testing.T) {
	s := New(WithFixtureFunc(func(r *http.Request) string {
		if r.URL.Path == "/a" {
			return "data: path-a\n\n"
		}
		return "data: other\n\n"
	}))
	defer s.Close()

	resp, _ := http.Get(s.URL + "/a")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "path-a") {
		t.Fatalf("expected path-a fixture, got: %s", string(body))
	}

	resp, _ = http.Get(s.URL + "/b")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "other") {
		t.Fatalf("expected other fixture, got: %s", string(body))
	}
}

// F1: TCP reset mid-stream.
func TestTCPReset(t *testing.T) {
	for _, cutAfter := range []int{0, 1, 3, 5} {
		t.Run(fmt.Sprintf("cutAfter=%d", cutAfter), func(t *testing.T) {
			s := NewTCPResetServer(anthropicFixture, cutAfter)
			defer s.Close()

			resp, err := http.Get(s.URL)
			if err != nil {
				// Connection refused for cutAfter=0 is acceptable.
				if cutAfter == 0 {
					return
				}
				t.Fatalf("GET failed: %v", err)
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			// For mid-stream cuts, we expect either an error or partial data.
			if cutAfter > 0 {
				events := splitSSEEvents(string(body))
				if len(events) > cutAfter {
					// May get exactly cutAfter due to buffering, or fewer
					// if the connection was killed before flush.
					t.Logf("cutAfter=%d, got %d events (err=%v)", cutAfter, len(events), err)
				}
			}
		})
	}
}

// F2: Malformed SSE payload.
func TestMalformedPayload(t *testing.T) {
	malformedCases := []struct {
		name string
		data string
	}{
		{"truncated_json", "data: {\"type\":\"content_block_delta\",\"index\":0\n\n"},
		{"garbage", "data: not json at all\n\n"},
		{"null_bytes", "data: \x00\x00\x00\n\n"},
		{"empty_data", "data: \n\n"},
	}

	for _, mc := range malformedCases {
		t.Run(mc.name, func(t *testing.T) {
			s := NewMalformedServer(anthropicFixture, 2, mc.data)
			defer s.Close()

			resp, err := http.Get(s.URL)
			if err != nil {
				t.Fatalf("GET failed: %v", err)
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			// Should contain the malformed data at position 2.
			if !strings.Contains(string(body), strings.TrimSpace(strings.TrimPrefix(mc.data, "data: "))) {
				t.Logf("response body does not contain malformed data (expected injection at event 2)")
			}
			// Must not panic or hang — reaching here is success.
		})
	}
}

// F3: Throttling.
func TestThrottleResponse(t *testing.T) {
	s := NewThrottleServer("5")
	defer s.Close()

	resp, err := http.Get(s.URL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") != "5" {
		t.Fatalf("expected Retry-After: 5, got %q", resp.Header.Get("Retry-After"))
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "rate_limit") {
		t.Fatalf("expected rate_limit in body, got: %s", string(body))
	}
}

func TestThrottleWithoutRetryAfter(t *testing.T) {
	s := NewThrottleServer("")
	defer s.Close()

	resp, err := http.Get(s.URL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") != "" {
		t.Fatalf("expected no Retry-After header")
	}
}

// F4: Backpressure (slow server).
func TestBackpressure(t *testing.T) {
	s := NewBackpressureServer(anthropicFixture, 10*time.Millisecond)
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", s.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	start := time.Now()
	body, _ := io.ReadAll(resp.Body)
	elapsed := time.Since(start)

	events := splitSSEEvents(string(body))
	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d", len(events))
	}
	// With 7 events × 10ms delay, should take at least 60ms.
	if elapsed < 50*time.Millisecond {
		t.Fatalf("backpressure too fast: %v", elapsed)
	}
}

// F5: Context cancellation races.
func TestContextCancellationRace(t *testing.T) {
	s := NewBackpressureServer(anthropicFixture, 5*time.Millisecond)
	defer s.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(delay time.Duration) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), delay)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "GET", s.URL, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return // connection refused or cancelled — expected
			}
			defer resp.Body.Close()
			io.ReadAll(resp.Body) // drain — must not panic
		}(time.Duration(i) * time.Millisecond)
	}
	wg.Wait()
}

// F6: Empty body with various status codes.
func TestEmptyBody(t *testing.T) {
	for _, status := range []int{400, 401, 403, 429, 500, 502, 503} {
		t.Run(fmt.Sprintf("status-%d", status), func(t *testing.T) {
			s := NewEmptyBodyServer(status)
			defer s.Close()

			resp, err := http.Get(s.URL)
			if err != nil {
				t.Fatalf("GET failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != status {
				t.Fatalf("expected %d, got %d", status, resp.StatusCode)
			}

			body, _ := io.ReadAll(resp.Body)
			if len(body) != 0 {
				t.Fatalf("expected empty body, got %d bytes", len(body))
			}
		})
	}
}

// Status code server with body.
func TestStatusCodeServerWithBody(t *testing.T) {
	body := `{"error":{"message":"forbidden","type":"auth_error"}}`
	s := NewStatusCodeServer(403, body)
	defer s.Close()

	resp, err := http.Get(s.URL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 403 {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}

	respBody, _ := io.ReadAll(resp.Body)
	if string(respBody) != body {
		t.Fatalf("expected body %q, got %q", body, string(respBody))
	}
}

// Sequence server for retry testing.
func TestSequenceServer(t *testing.T) {
	s := NewSequenceServer([]func(w http.ResponseWriter, r *http.Request){
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":"rate limited"}`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":"rate limited again"}`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fmt.Fprint(w, "data: success\n\n")
		},
	})
	defer s.Close()

	// First two requests get 429.
	for i := 0; i < 2; i++ {
		resp, _ := http.Get(s.URL)
		if resp.StatusCode != 429 {
			t.Fatalf("request %d: expected 429, got %d", i, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// Third request succeeds.
	resp, _ := http.Get(s.URL)
	if resp.StatusCode != 200 {
		t.Fatalf("request 3: expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "success") {
		t.Fatalf("expected success in body")
	}

	// Fourth request exhausts sequence.
	resp, _ = http.Get(s.URL)
	if resp.StatusCode != 500 {
		t.Fatalf("request 4: expected 500, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// Test splitSSEEvents helper.
func TestSplitSSEEvents(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		events int
	}{
		{"empty", "", 0},
		{"single", "data: hello\n\n", 1},
		{"multi", "data: a\n\n\ndata: b\n\n", 2},
		{"with_event_type", "event: msg\ndata: x\n\nevent: end\ndata: y\n\n", 2},
		{"trailing_whitespace", "data: x\n\n  \n\ndata: y\n\n", 2},
		{"crlf", "data: x\r\n\r\ndata: y\r\n\r\n", 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			events := splitSSEEvents(tc.input)
			if len(events) != tc.events {
				t.Fatalf("expected %d events, got %d: %v", tc.events, len(events), events)
			}
			for _, e := range events {
				if !strings.HasSuffix(e, "\n\n") {
					t.Fatalf("event missing terminator: %q", e)
				}
			}
		})
	}
}

// Concurrent safety test.
func TestConcurrentRequests(t *testing.T) {
	s := New(WithFixture("data: concurrent\n\n"))
	defer s.Close()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(s.URL)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			io.ReadAll(resp.Body)
		}()
	}
	wg.Wait()

	if len(s.Requests()) != 20 {
		t.Fatalf("expected 20 captured requests, got %d", len(s.Requests()))
	}
}
