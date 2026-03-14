package otelserver

import (
	"bytes"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestServer_StartAndEndpoint(t *testing.T) {
	s, err := New(Callbacks{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	endpoint := s.Endpoint()
	if endpoint == "" {
		t.Error("expected non-empty endpoint")
	}
	if s.Port() <= 0 {
		t.Errorf("expected positive port, got %d", s.Port())
	}
}

func TestServer_DoubleStart(t *testing.T) {
	s, err := New(Callbacks{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	if err := s.Start(); err == nil {
		t.Error("expected error on double start")
	}
}

func TestServer_LogsCallback(t *testing.T) {
	var received []byte
	var mu sync.Mutex

	s, err := New(Callbacks{
		OnLogs: func(body []byte) {
			mu.Lock()
			received = make([]byte, len(body))
			copy(received, body)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	// Send logs
	payload := []byte(`{"resource_logs": [{"scope_logs": []}]}`)
	resp, err := http.Post(s.Endpoint()+"/v1/logs", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	if !bytes.Equal(received, payload) {
		t.Errorf("callback received wrong data: %s", string(received))
	}
	mu.Unlock()
}

func TestServer_MetricsCallback(t *testing.T) {
	var called bool
	var mu sync.Mutex

	s, err := New(Callbacks{
		OnMetrics: func(body []byte) {
			mu.Lock()
			called = true
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	resp, err := http.Post(s.Endpoint()+"/v1/metrics", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	if !called {
		t.Error("metrics callback was not called")
	}
	mu.Unlock()
}

func TestServer_TracesCallback(t *testing.T) {
	var called bool
	var mu sync.Mutex

	s, err := New(Callbacks{
		OnTraces: func(body []byte) {
			mu.Lock()
			called = true
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	resp, err := http.Post(s.Endpoint()+"/v1/traces", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	if !called {
		t.Error("traces callback was not called")
	}
	mu.Unlock()
}

func TestServer_NilCallbacks(t *testing.T) {
	s, err := New(Callbacks{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	// Should not panic with nil callbacks
	resp, err := http.Post(s.Endpoint()+"/v1/logs", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with nil callback, got %d", resp.StatusCode)
	}
}

func TestServer_Stop(t *testing.T) {
	s, err := New(Callbacks{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Double stop should be safe
	if err := s.Stop(); err != nil {
		t.Fatalf("Double Stop: %v", err)
	}
}

func TestServer_DrainTimeout(t *testing.T) {
	s, err := New(Callbacks{}, WithDrainTimeout(100*time.Millisecond))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestServer_ConcurrentRequests(t *testing.T) {
	var count int
	var mu sync.Mutex

	s, err := New(Callbacks{
		OnLogs: func(body []byte) {
			mu.Lock()
			count++
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(s.Endpoint()+"/v1/logs", "application/json", bytes.NewReader([]byte("{}")))
			if err != nil {
				return
			}
			resp.Body.Close()
		}()
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if count != 50 {
		t.Errorf("expected 50 callbacks, got %d", count)
	}
	mu.Unlock()
}
