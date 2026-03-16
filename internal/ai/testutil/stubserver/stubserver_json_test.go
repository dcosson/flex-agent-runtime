package stubserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const openaiEmbeddingFixture = `{"object":"list","data":[{"object":"embedding","embedding":[0.1,0.2,0.3],"index":0}],"model":"text-embedding-3-small","usage":{"prompt_tokens":5,"total_tokens":5}}`

const googleEmbeddingFixture = `{"embeddings":[{"values":[0.1,0.2,0.3]}]}`

const cohereEmbeddingFixture = `{"id":"emb_01","embeddings":{"float":[[0.1,0.2,0.3]]},"texts":["hello"],"meta":{"billed_units":{"input_tokens":1}}}`

// JSON fixture replay — all 3 provider formats.
func TestJSONFixtureReplay(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		checkFn func(t *testing.T, body []byte)
	}{
		{
			name:    "openai",
			fixture: openaiEmbeddingFixture,
			checkFn: func(t *testing.T, body []byte) {
				var resp map[string]any
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				if resp["object"] != "list" {
					t.Fatalf("expected object=list, got %v", resp["object"])
				}
			},
		},
		{
			name:    "google",
			fixture: googleEmbeddingFixture,
			checkFn: func(t *testing.T, body []byte) {
				var resp map[string]any
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				embeddings, ok := resp["embeddings"].([]any)
				if !ok || len(embeddings) == 0 {
					t.Fatal("expected non-empty embeddings array")
				}
			},
		},
		{
			name:    "cohere",
			fixture: cohereEmbeddingFixture,
			checkFn: func(t *testing.T, body []byte) {
				var resp map[string]any
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				if resp["id"] == nil {
					t.Fatal("expected id field")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewJSONServer(tc.fixture)
			defer s.Close()

			resp, err := http.Post(s.URL+"/embeddings", "application/json", strings.NewReader(`{"input":["test"]}`))
			if err != nil {
				t.Fatalf("POST failed: %v", err)
			}
			defer resp.Body.Close()

			if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
				t.Fatalf("expected application/json, got %q", ct)
			}
			if resp.StatusCode != 200 {
				t.Fatalf("expected 200, got %d", resp.StatusCode)
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			tc.checkFn(t, body)
		})
	}
}

// JSON fixture func — per-request selection.
func TestJSONFixtureFunc(t *testing.T) {
	s := New(WithJSONFixtureFunc(func(r *http.Request) string {
		if strings.Contains(r.URL.Path, "embed") {
			return openaiEmbeddingFixture
		}
		return `{"error":"not found"}`
	}))
	defer s.Close()

	resp, err := http.Post(s.URL+"/v1/embeddings", "application/json", nil)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "text-embedding-3-small") {
		t.Fatalf("expected openai fixture, got: %s", string(body))
	}
}

// JSON request capture.
func TestJSONRequestCapture(t *testing.T) {
	s := NewJSONServer(openaiEmbeddingFixture)
	defer s.Close()

	reqBody := `{"model":"text-embedding-3-small","input":["hello"]}`
	req, _ := http.NewRequest("POST", s.URL+"/embeddings", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer sk-test")
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
	if reqs[0].Header.Get("Authorization") != "Bearer sk-test" {
		t.Fatalf("auth header not captured: %q", reqs[0].Header.Get("Authorization"))
	}
	if string(reqs[0].Body) != reqBody {
		t.Fatalf("body not captured: %s", string(reqs[0].Body))
	}
}

// JSON TCP reset mid-response.
func TestJSONTCPReset(t *testing.T) {
	tests := []struct {
		name       string
		afterBytes int
	}{
		{"immediate", 0},
		{"partial_10bytes", 10},
		{"partial_50bytes", 50},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewJSONFaultServer(openaiEmbeddingFixture, Fault{
				Mode:        TCPReset,
				AfterEvents: tc.afterBytes,
			})
			defer s.Close()

			resp, err := http.Post(s.URL, "application/json", nil)
			if err != nil {
				if tc.afterBytes == 0 {
					return // Connection closed before response — acceptable
				}
				t.Fatalf("POST failed: %v", err)
			}
			defer resp.Body.Close()

			body, readErr := io.ReadAll(resp.Body)
			if tc.afterBytes > 0 && tc.afterBytes < len(openaiEmbeddingFixture) {
				// Should get partial data or a read error.
				if readErr == nil && len(body) >= len(openaiEmbeddingFixture) {
					t.Fatalf("expected partial data, got %d bytes (full=%d)", len(body), len(openaiEmbeddingFixture))
				}
				// Verify JSON is invalid (truncated).
				var parsed map[string]any
				if json.Unmarshal(body, &parsed) == nil {
					t.Log("warning: truncated body was still valid JSON")
				}
			}
		})
	}
}

// JSON malformed payload.
func TestJSONMalformed(t *testing.T) {
	malformedCases := []struct {
		name string
		data string
	}{
		{"default", ""},
		{"truncated_json", `{"data":[{"embedding":`},
		{"garbage", "not json at all"},
		{"null_bytes", "\x00\x00\x00"},
	}

	for _, mc := range malformedCases {
		t.Run(mc.name, func(t *testing.T) {
			s := NewJSONFaultServer(openaiEmbeddingFixture, Fault{
				Mode:          Malformed,
				MalformedData: mc.data,
			})
			defer s.Close()

			resp, err := http.Post(s.URL, "application/json", nil)
			if err != nil {
				t.Fatalf("POST failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				t.Fatalf("expected 200, got %d", resp.StatusCode)
			}

			body, _ := io.ReadAll(resp.Body)
			var parsed map[string]any
			if json.Unmarshal(body, &parsed) == nil {
				t.Fatal("expected invalid JSON, but unmarshalling succeeded")
			}
		})
	}
}

// JSON backpressure — delayed response.
func TestJSONBackpressure(t *testing.T) {
	s := NewJSONFaultServer(openaiEmbeddingFixture, Fault{
		Mode:       Backpressure,
		EventDelay: 50 * time.Millisecond,
	})
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", s.URL, nil)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	elapsed := time.Since(start)

	// Verify response is valid JSON.
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Verify delay was applied.
	if elapsed < 40*time.Millisecond {
		t.Fatalf("backpressure too fast: %v", elapsed)
	}
}

// JSON throttle — HTTP 429.
func TestJSONThrottle(t *testing.T) {
	s := NewJSONFaultServer("", Fault{
		Mode:       Throttle,
		StatusCode: 429,
		RetryAfter: "1",
	})
	defer s.Close()

	resp, err := http.Post(s.URL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 429 {
		t.Fatalf("expected 429, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") != "1" {
		t.Fatalf("expected Retry-After=1, got %q", resp.Header.Get("Retry-After"))
	}
}

// JSON empty body — HTTP error with no content.
func TestJSONEmptyBody(t *testing.T) {
	s := NewJSONFaultServer("", Fault{
		Mode:       EmptyBody,
		StatusCode: 500,
	})
	defer s.Close()

	resp, err := http.Post(s.URL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 500 {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 0 {
		t.Fatalf("expected empty body, got %d bytes", len(body))
	}
}

// Sequence server with JSON responses for retry testing.
func TestJSONSequenceServer(t *testing.T) {
	s := NewSequenceServer([]func(w http.ResponseWriter, r *http.Request){
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(429)
			io.WriteString(w, `{"error":{"message":"rate limited"}}`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			io.WriteString(w, openaiEmbeddingFixture)
		},
	})
	defer s.Close()

	// First request: 429.
	resp, _ := http.Post(s.URL, "application/json", nil)
	if resp.StatusCode != 429 {
		t.Fatalf("request 1: expected 429, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Second request: success.
	resp, _ = http.Post(s.URL, "application/json", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("request 2: expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON in success response: %v", err)
	}
}
